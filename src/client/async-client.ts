import type {
  DeepSeekClientCompletionResult,
  DeepSeekClientFetch,
  DeepSeekClientOptions,
  DeepSeekClientRequestOptions,
  DeepSeekMessageId,
  OpenAIChatCompletionResponse,
  OpenAIStreamChunk,
} from "../types.js";

const DEFAULT_BASE_URL = "http://127.0.0.1:9123/v1";
const DEFAULT_MODEL = "deepseek-chat";

function normalizeBaseURL(baseURL?: string) {
  const trimmed = (baseURL ?? DEFAULT_BASE_URL).replace(/\/+$/, "");

  if (trimmed.endsWith("/chat/completions")) {
    return trimmed.slice(0, -"/chat/completions".length);
  }

  if (trimmed.endsWith("/v1")) {
    return trimmed;
  }

  return `${trimmed}/v1`;
}

function resolveFetch(fetchImpl?: DeepSeekClientFetch) {
  if (fetchImpl) {
    return fetchImpl;
  }

  if (typeof globalThis.fetch !== "function") {
    throw new Error("A fetch implementation is required");
  }

  return globalThis.fetch.bind(globalThis) as DeepSeekClientFetch;
}

function readTextFromResponse(response: OpenAIChatCompletionResponse) {
  return response.choices
    .map((choice) => choice.message?.content ?? choice.delta?.content ?? "")
    .join("");
}

function parseChunk(line: string) {
  if (!line.startsWith("data: ")) {
    return null;
  }

  const payload = line.slice(6).trim();
  if (!payload || payload === "[DONE]") {
    return null;
  }

  return JSON.parse(payload) as OpenAIStreamChunk;
}

export class AsyncDeepSeekClient {
  protected readonly baseURL: string;
  protected readonly apiKey?: string;
  protected readonly defaultModel: string;
  protected readonly defaultThinkingEnabled: boolean;
  protected readonly defaultSearchEnabled: boolean;
  protected readonly fetchImpl: DeepSeekClientFetch;
  protected currentSessionId?: string;
  protected currentParentMessageId?: DeepSeekMessageId;

  constructor(options: DeepSeekClientOptions = {}) {
    this.baseURL = normalizeBaseURL(options.baseURL);
    this.apiKey = options.apiKey;
    this.defaultModel = options.model ?? DEFAULT_MODEL;
    this.defaultThinkingEnabled = options.thinkingEnabled ?? false;
    this.defaultSearchEnabled = options.searchEnabled ?? false;
    this.fetchImpl = resolveFetch(options.fetch);
    this.currentSessionId = options.sessionId;
    this.currentParentMessageId = options.parentMessageId ?? undefined;
  }

  get sessionId() {
    return this.currentSessionId;
  }

  get parentMessageId() {
    return this.currentParentMessageId;
  }

  newChat() {
    this.currentSessionId = undefined;
    this.currentParentMessageId = undefined;
  }

  async *chat(message: string, options: DeepSeekClientRequestOptions = {}) {
    const response = await this.request(message, options, true);
    this.updateStateFromHeaders(response);

    if (!response.ok) {
      throw await this.createRequestError(response);
    }

    if (!response.body) {
      throw new Error("Streaming response body is unavailable");
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";

    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";

      for (const rawLine of lines) {
        const line = rawLine.trim();
        if (!line) {
          continue;
        }

        const chunk = parseChunk(line);
        if (!chunk) {
          continue;
        }

        this.updateStateFromChunk(chunk);

        const text = chunk.choices
          .map((choice) => choice.delta?.content ?? "")
          .join("");

        if (text) {
          yield text;
        }
      }
    }

    const trailingLine = buffer.trim();
    if (!trailingLine) {
      return;
    }

    const chunk = parseChunk(trailingLine);
    if (!chunk) {
      return;
    }

    this.updateStateFromChunk(chunk);

    const text = chunk.choices.map((choice) => choice.delta?.content ?? "").join("");
    if (text) {
      yield text;
    }
  }

  async complete(
    message: string,
    options: DeepSeekClientRequestOptions = {}
  ): Promise<DeepSeekClientCompletionResult> {
    const response = await this.request(message, options, false);
    this.updateStateFromHeaders(response);

    if (!response.ok) {
      throw await this.createRequestError(response);
    }

    const payload = (await response.json()) as OpenAIChatCompletionResponse;
    this.updateStateFromResponse(payload);

    return {
      text: readTextFromResponse(payload),
      response: payload,
      sessionId: this.currentSessionId,
      parentMessageId: this.currentParentMessageId,
      responseMessageId: this.currentParentMessageId,
    };
  }

  protected async request(
    message: string,
    options: DeepSeekClientRequestOptions,
    stream: boolean
  ) {
    const body = {
      model: options.model ?? this.defaultModel,
      messages: [{ role: "user" as const, content: message }],
      stream,
      ...(this.resolveSessionId(options) ? { sessionId: this.resolveSessionId(options) } : {}),
      ...(this.resolveParentMessageId(options) != null
        ? { parent_message_id: this.resolveParentMessageId(options) }
        : {}),
      thinking_enabled: options.thinkingEnabled ?? this.defaultThinkingEnabled,
      search_enabled: options.searchEnabled ?? this.defaultSearchEnabled,
    };

    return this.fetchImpl(`${this.baseURL}/chat/completions`, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        ...(this.apiKey ? { authorization: `Bearer ${this.apiKey}` } : {}),
      },
      body: JSON.stringify(body),
    });
  }

  protected resolveSessionId(options: DeepSeekClientRequestOptions) {
    if (options.sessionId === null) {
      return undefined;
    }

    return options.sessionId ?? this.currentSessionId;
  }

  protected resolveParentMessageId(options: DeepSeekClientRequestOptions) {
    if (options.parentMessageId === null) {
      return undefined;
    }

    return options.parentMessageId ?? this.currentParentMessageId;
  }

  protected updateStateFromHeaders(response: Response) {
    const sessionId = response.headers.get("x-chat-session-id");
    const parentMessageId = response.headers.get("x-parent-message-id");

    if (sessionId) {
      this.currentSessionId = sessionId;
    }

    if (parentMessageId) {
      this.currentParentMessageId = parentMessageId;
    }
  }

  protected updateStateFromResponse(response: OpenAIChatCompletionResponse) {
    const choiceMessageId = response.choices.find((choice) => choice.message_id != null)?.message_id;
    const responseMessageId = response.response_message_id ?? choiceMessageId;

    if (responseMessageId != null) {
      this.currentParentMessageId = responseMessageId;
    }
  }

  protected updateStateFromChunk(chunk: OpenAIStreamChunk) {
    const choiceMessageId = chunk.choices.find((choice) => choice.message_id != null)?.message_id;
    const responseMessageId = chunk.response_message_id ?? choiceMessageId;

    if (responseMessageId != null) {
      this.currentParentMessageId = responseMessageId;
    }
  }

  protected async createRequestError(response: Response) {
    const contentType = response.headers.get("content-type") ?? "";

    if (contentType.includes("application/json")) {
      try {
        const payload = (await response.json()) as {
          error?: { message?: string } | string;
        };
        const message =
          typeof payload.error === "string"
            ? payload.error
            : payload.error?.message ?? `Request failed with status ${response.status}`;
        return new Error(message);
      } catch {
        return new Error(`Request failed with status ${response.status}`);
      }
    }

    try {
      const text = await response.text();
      return new Error(text || `Request failed with status ${response.status}`);
    } catch {
      return new Error(`Request failed with status ${response.status}`);
    }
  }
}
