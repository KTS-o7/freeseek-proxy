import "dotenv/config";
import { readFile } from "node:fs/promises";
import { Hono, type Context } from "hono";
import { serve } from "@hono/node-server";
import { v4 as uuidv4 } from "uuid";
import {
  extractPromptFromMessages,
  normalizeAssistantContent,
  normalizeParentMessageId,
} from "./request-normalization.js";
import {
  buildProxyPrompt,
  hasToolCompatibilityRequest,
  normalizeToolChoice,
  parseToolEnvelope,
} from "./tool-call-compat.js";
import type { OpenAIChatCompletionRequest, OpenAITool } from "./types.js";

const PORT = parseInt(process.env.PORT ?? "9123", 10);
const BACKEND = process.env.BACKEND_URL ?? "http://127.0.0.1:8081";
const API_KEY = process.env.API_KEY ?? "";

type ChatCompletionBody = OpenAIChatCompletionRequest & { sessionId?: string };

// ─── DeepSeek internal SSE wire format ────────────────────────────────────────

type DeepSeekSSEData = {
  v?: string | { response?: { message_id?: number | string; fragments?: Array<{ type?: string; content: string }> } };
  p?: string;
  o?: "APPEND" | "SET" | "BATCH";
  response_message_id?: number | string;
  content?: string;
};

type ParsedSSEEvent = {
  content?: string;
  responseMessageId?: number | string;
};

// ─── Backend error payload (from Python FastAPI backend) ─────────────────────

type BackendErrorPayload = {
  error?:
    | string
    | {
        type?: string;
        message?: string;
        status_code?: number;
      };
};

// ─── OpenAI-spec error builder ────────────────────────────────────────────────

function makeErrorBody(message: string, type = "server_error", code: string | null = null, param: string | null = null) {
  return { error: { message, type, param, code } };
}

// ─── Hono app ─────────────────────────────────────────────────────────────────

const app = new Hono();

app.get("/", async (c) => {
  const html = await readFile(new URL("../public/index.html", import.meta.url), "utf8");
  return c.html(html);
});

app.use("/v1/*", async (c, next) => {
  if (!API_KEY) return next();
  const auth = c.req.header("authorization");
  if (!auth || auth !== `Bearer ${API_KEY}`) {
    return c.json(makeErrorBody("Invalid API key", "authentication_error", "invalid_api_key"), 401);
  }
  return next();
});

app.get("/health", (c) => c.json({ status: "ok" }));

app.get("/v1/models", (c) =>
  c.json({
    object: "list",
    data: [
      { id: "deepseek-chat",      object: "model", created: 1704067200, owned_by: "deepseek" },
      { id: "deepseek-coder",     object: "model", created: 1704067200, owned_by: "deepseek" },
      { id: "deepseek-reasoner",  object: "model", created: 1704067200, owned_by: "deepseek" },
      { id: "taalas-llama3.1-8b", object: "model", created: 1704067200, owned_by: "taalas"   },
    ],
  })
);

// ─── DeepSeek session helper ──────────────────────────────────────────────────

async function createSessionId() {
  const sessionResp = await fetch(`${BACKEND}/chat/session`, { method: "POST" });

  if (!sessionResp.ok) {
    throw new Error(`Session creation failed with status ${sessionResp.status}`);
  }

  let sessionData: { session_id?: string };

  try {
    sessionData = (await sessionResp.json()) as { session_id?: string };
  } catch {
    throw new Error("Session creation returned invalid JSON");
  }

  if (!sessionData.session_id) {
    throw new Error("Session creation did not return a session_id");
  }

  return sessionData.session_id;
}

// ─── Backend error translation ────────────────────────────────────────────────

async function createBackendErrorResponse(c: Context, resp: Response) {
  try {
    const data = (await resp.json()) as BackendErrorPayload;
    const backendError = typeof data.error === "object" && data.error !== null ? data.error : null;

    if (backendError?.message) {
      const statusCode = typeof backendError.status_code === "number" ? backendError.status_code : resp.status;
      const errorType = backendError.type ?? (statusCode === 401 ? "authentication_error" : statusCode === 429 ? "rate_limit_error" : "server_error");
      return new Response(
        JSON.stringify(makeErrorBody(backendError.message, errorType)),
        { status: statusCode, headers: { "content-type": "application/json; charset=UTF-8" } }
      );
    }
  } catch { /* fall through */ }

  return c.json(makeErrorBody(`Backend error ${resp.status}`), 502);
}

// ─── DeepSeek SSE parsing ─────────────────────────────────────────────────────

function extractDeepSeekContent(data: DeepSeekSSEData, includeThinking: boolean) {
  if (typeof data.v === "string") {
    if (data.p === "response/status" || data.p === "response/accumulated_token_usage") {
      return null;
    }
    if (!includeThinking && data.p?.includes("thinking")) {
      return null;
    }
    return data.v;
  }

  const fragments = data.v?.response?.fragments;
  if (fragments?.length) {
    const content = fragments
      .filter((fragment) => includeThinking || fragment.type !== "thinking")
      .map((fragment) => fragment.content)
      .filter(Boolean)
      .join("");
    return content || null;
  }

  if (typeof data.content === "string") {
    return data.content;
  }

  return null;
}

function parseSSELine(line: string, includeThinking: boolean): ParsedSSEEvent | null {
  if (!line.startsWith("data: ")) return null;

  const jsonStr = line.slice(6);
  if (jsonStr === "[DONE]") return null;

  try {
    const data = JSON.parse(jsonStr) as DeepSeekSSEData;
    const responseMessageId =
      data.response_message_id ??
      (typeof data.v === "object" ? data.v?.response?.message_id : undefined);
    const content = extractDeepSeekContent(data, includeThinking);

    return {
      ...(content ? { content } : {}),
      ...(responseMessageId != null ? { responseMessageId } : {}),
    };
  } catch {
    return null;
  }
}

async function collectResponseMessageId(body: ReadableStream<Uint8Array>, includeThinking: boolean) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const event = parseSSELine(line.trim(), includeThinking);
      if (!event) continue;
      if (event.responseMessageId != null) return event.responseMessageId;
      if (event.content) return undefined;
    }
  }

  const trailingLine = buffer.trim();
  if (!trailingLine) return undefined;
  return parseSSELine(trailingLine, includeThinking)?.responseMessageId;
}

async function collectFullResponse(body: ReadableStream<Uint8Array>, includeThinking: boolean) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let fullText = "";
  let responseMessageId: number | string | undefined;

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const event = parseSSELine(line.trim(), includeThinking);
      if (!event) continue;
      if (event.responseMessageId != null) responseMessageId = event.responseMessageId;
      if (event.content) fullText += event.content;
    }
  }

  const trailingLine = buffer.trim();
  if (trailingLine) {
    const event = parseSSELine(trailingLine, includeThinking);
    if (event?.responseMessageId != null) responseMessageId = event.responseMessageId;
    if (event?.content) fullText += event.content;
  }

  return { fullText, responseMessageId };
}

// ─── OpenAI-spec response builders ───────────────────────────────────────────

/**
 * Estimate token counts. prompt_tokens covers the input messages; completion
 * covers the generated text. Neither is exact — we have no tokenizer — but
 * the asymmetry is more honest than returning the same value for both.
 */
function buildUsage(promptText: string, completionText: string) {
  return {
    prompt_tokens: Math.ceil(promptText.length / 4),
    completion_tokens: Math.ceil(completionText.length / 4),
    total_tokens: Math.ceil((promptText.length + completionText.length) / 4),
  };
}

function makeChatCompletionId() {
  return `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`;
}

function buildChatCompletionResponse(args: {
  model: string;
  promptText: string;
  fullText: string;
  tools: OpenAITool[];
  toolChoice: ReturnType<typeof normalizeToolChoice>;
  /** DeepSeek-internal session tracking — returned as custom headers, not in body */
  sessionId: string;
  responseMessageId?: number | string;
}) {
  const parsedToolEnvelope = parseToolEnvelope(args.fullText, args.tools);

  if (args.toolChoice.mode === "required" && (!parsedToolEnvelope || parsedToolEnvelope.toolCalls.length !== 1)) {
    throw new Error("Tool choice required but the model did not emit exactly one valid tool call");
  }

  if (args.toolChoice.mode === "function") {
    if (!parsedToolEnvelope || parsedToolEnvelope.toolCalls.length < 1) {
      throw new Error(`Tool choice required function '${args.toolChoice.functionName}' but no valid tool call was emitted`);
    }
    const invalidToolCall = parsedToolEnvelope.toolCalls.some(
      (tc) => tc.function.name !== args.toolChoice.functionName
    );
    if (invalidToolCall) {
      throw new Error(`Tool choice required function '${args.toolChoice.functionName}' but a different tool call was emitted`);
    }
  }

  const id = makeChatCompletionId();
  const created = Math.floor(Date.now() / 1000);
  const usage = buildUsage(args.promptText, args.fullText);

  if (parsedToolEnvelope) {
    return {
      id,
      object: "chat.completion" as const,
      created,
      model: args.model,
      choices: [{
        index: 0,
        message: { role: "assistant" as const, content: null, tool_calls: parsedToolEnvelope.toolCalls },
        finish_reason: "tool_calls" as const,
        logprobs: null,
      }],
      usage,
    };
  }

  return {
    id,
    object: "chat.completion" as const,
    created,
    model: args.model,
    choices: [{
      index: 0,
      message: { role: "assistant" as const, content: args.fullText },
      finish_reason: "stop" as const,
      logprobs: null,
    }],
    usage,
  };
}

/**
 * When tool-call compatibility mode is active we collect the full response
 * before streaming, then re-emit it as a minimal OpenAI streaming sequence.
 */
function buildToolCompatibleStreamResponse(args: {
  payload: ReturnType<typeof buildChatCompletionResponse>;
  model: string;
  sessionId: string;
  responseMessageId?: number | string;
}) {
  const streamId = args.payload.id;
  const created = args.payload.created;
  const encoder = new TextEncoder();

  const readable = new ReadableStream({
    start(controller) {
      const send = (data: object | string) => {
        const payload = typeof data === "string" ? data : JSON.stringify(data);
        controller.enqueue(encoder.encode(`data: ${payload}\n\n`));
      };

      // First chunk: role delta
      send({
        id: streamId, object: "chat.completion.chunk", created, model: args.model,
        choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null, logprobs: null }],
      });

      const message = args.payload.choices[0]?.message;
      const toolCalls = message && "tool_calls" in message ? message.tool_calls : undefined;

      if (toolCalls?.length) {
        // Tool-call delta chunk
        send({
          id: streamId, object: "chat.completion.chunk", created, model: args.model,
          choices: [{
            index: 0,
            delta: {
              tool_calls: toolCalls.map((tc, i) => ({
                index: i,
                id: tc.id,
                type: tc.type,
                function: { name: tc.function.name, arguments: tc.function.arguments },
              })),
            },
            finish_reason: null,
            logprobs: null,
          }],
        });
        // Final chunk
        send({
          id: streamId, object: "chat.completion.chunk", created, model: args.model,
          choices: [{ index: 0, delta: {}, finish_reason: "tool_calls", logprobs: null }],
        });
      } else {
        // Content delta chunk
        send({
          id: streamId, object: "chat.completion.chunk", created, model: args.model,
          choices: [{
            index: 0,
            delta: { content: normalizeAssistantContent(message?.content ?? "") ?? "" },
            finish_reason: null,
            logprobs: null,
          }],
        });
        // Final chunk
        send({
          id: streamId, object: "chat.completion.chunk", created, model: args.model,
          choices: [{ index: 0, delta: {}, finish_reason: "stop", logprobs: null }],
        });
      }

      send("[DONE]");
      controller.close();
    },
  });

  return new Response(readable, {
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      // DeepSeek-specific session tracking headers (not part of OpenAI spec)
      "x-chat-session-id": args.sessionId,
      ...(args.responseMessageId != null ? { "x-parent-message-id": String(args.responseMessageId) } : {}),
    },
  });
}

// ─── Taalas (chatjimmy.ai) provider ──────────────────────────────────────────

/** Maps OpenAI-style Taalas model IDs to chatjimmy.ai selectedModel values. */
const TAALAS_MODEL_MAP: Record<string, string> = {
  "taalas-llama3.1-8b": "llama3.1-8B",
};

function isTaalasModel(model: string): boolean {
  return model.startsWith("taalas-");
}

async function handleTaalasCompletion(c: Context, body: ChatCompletionBody) {
  if (!body.messages?.length) {
    return c.json(makeErrorBody("messages is required", "invalid_request_error", null, "messages"), 400);
  }

  const model = body.model || "taalas-llama3.1-8b";
  const taalasModel = TAALAS_MODEL_MAP[model] ?? "llama3.1-8B";

  const resp = await fetch(`${BACKEND}/taalas/completion`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ messages: body.messages, model: taalasModel }),
  });

  if (!resp.ok) {
    return createBackendErrorResponse(c, resp);
  }

  // Collect the single-chunk SSE response from the backend
  let fullText = "";
  if (resp.body) {
    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split("\n");
      buffer = lines.pop() ?? "";
      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed.startsWith("data: ") || trimmed === "data: [DONE]") continue;
        try {
          const data = JSON.parse(trimmed.slice(6)) as { content?: string };
          if (data.content) fullText += data.content;
        } catch { /* skip malformed lines */ }
      }
    }
    // flush any trailing content
    if (buffer.trim().startsWith("data: ") && buffer.trim() !== "data: [DONE]") {
      try {
        const data = JSON.parse(buffer.trim().slice(6)) as { content?: string };
        if (data.content) fullText += data.content;
      } catch { /* skip */ }
    }
  }

  // Estimate prompt text for usage
  const promptText = body.messages.map((m) => (typeof m.content === "string" ? m.content : "")).join(" ");

  const id = makeChatCompletionId();
  const created = Math.floor(Date.now() / 1000);
  const usage = buildUsage(promptText, fullText);

  if (body.stream) {
    const encoder = new TextEncoder();
    const readable = new ReadableStream({
      start(controller) {
        const send = (data: object | string) => {
          const payload = typeof data === "string" ? data : JSON.stringify(data);
          controller.enqueue(encoder.encode(`data: ${payload}\n\n`));
        };
        send({ id, object: "chat.completion.chunk", created, model,
          choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null, logprobs: null }] });
        send({ id, object: "chat.completion.chunk", created, model,
          choices: [{ index: 0, delta: { content: fullText }, finish_reason: null, logprobs: null }] });
        send({ id, object: "chat.completion.chunk", created, model,
          choices: [{ index: 0, delta: {}, finish_reason: "stop", logprobs: null }],
          usage });
        send("[DONE]");
        controller.close();
      },
    });
    return new Response(readable, {
      headers: { "Content-Type": "text/event-stream", "Cache-Control": "no-cache", Connection: "keep-alive" },
    });
  }

  return c.json({
    id,
    object: "chat.completion",
    created,
    model,
    choices: [{
      index: 0,
      message: { role: "assistant", content: fullText },
      finish_reason: "stop",
      logprobs: null,
    }],
    usage,
  });
}

// ─── DeepSeek handler ─────────────────────────────────────────────────────────

async function handleChatCompletion(c: Context, body: ChatCompletionBody) {
  if (!body.messages?.length) {
    return c.json(makeErrorBody("messages is required", "invalid_request_error", null, "messages"), 400);
  }

  const model = body.model || "deepseek-chat";
  const tools = body.tools ?? [];
  const normalizedToolChoice = normalizeToolChoice(body.tool_choice);
  const toolCompatibilityMode = hasToolCompatibilityRequest(body.messages, tools, body.tool_choice);
  const prompt = toolCompatibilityMode
    ? buildProxyPrompt(body.messages, tools, normalizedToolChoice)
    : extractPromptFromMessages(body.messages);
  const sessionId = body.sessionId || (await createSessionId());
  const thinkingEnabled = body.thinking_enabled ?? false;
  const searchEnabled = body.search_enabled ?? false;

  // Estimate prompt text for usage (before we discard messages)
  const promptText = body.messages.map((m) => (typeof m.content === "string" ? m.content : "")).join(" ");

  const dsBody = {
    chat_session_id: sessionId,
    parent_message_id: normalizeParentMessageId(body.parent_message_id) ?? null,
    model,       // consumed by Python backend to set model_type; not forwarded to DeepSeek
    prompt,
    ref_file_ids: [],
    thinking_enabled: thinkingEnabled,
    search_enabled: searchEnabled,
    preempt: false,
  };

  if (body.stream && !toolCompatibilityMode) {
    const resp = await fetch(`${BACKEND}/chat/completion`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(dsBody),
    });

    if (!resp.ok) return createBackendErrorResponse(c, resp);
    if (!resp.body) return c.json(makeErrorBody("Backend stream unavailable"), 502);

    const [headerBody, streamBody] = resp.body.tee();
    const initialResponseMessageId = await collectResponseMessageId(headerBody, thinkingEnabled);

    const streamId = makeChatCompletionId();
    const created = Math.floor(Date.now() / 1000);

    const readable = new ReadableStream({
      async start(controller) {
        let latestResponseMessageId = initialResponseMessageId;
        const encoder = new TextEncoder();
        const send = (data: object) => {
          controller.enqueue(encoder.encode(`data: ${JSON.stringify(data)}\n\n`));
        };

        // First chunk: role delta
        send({
          id: streamId, object: "chat.completion.chunk", created, model,
          choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null, logprobs: null }],
        });

        try {
          const reader = streamBody.getReader();
          const decoder = new TextDecoder();
          let buffer = "";

          const flushLine = (line: string) => {
            const event = parseSSELine(line.trim(), thinkingEnabled);
            if (!event || (!event.content && event.responseMessageId == null)) return;
            if (event.responseMessageId != null) latestResponseMessageId = event.responseMessageId;
            if (!event.content) return; // responseMessageId-only events don't emit a chunk
            send({
              id: streamId, object: "chat.completion.chunk", created, model,
              choices: [{ index: 0, delta: { content: event.content }, finish_reason: null, logprobs: null }],
            });
          };

          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split("\n");
            buffer = lines.pop() ?? "";
            for (const line of lines) flushLine(line.trim());
          }
          const trailingLine = buffer.trim();
          if (trailingLine) flushLine(trailingLine);

          // Final chunk: stop
          send({
            id: streamId, object: "chat.completion.chunk", created, model,
            choices: [{ index: 0, delta: {}, finish_reason: "stop", logprobs: null }],
          });
          controller.enqueue(encoder.encode("data: [DONE]\n\n"));
          controller.close();
        } catch (error) {
          controller.error(error instanceof Error ? error : new Error("Streaming failed"));
        }
      },
    });

    return new Response(readable, {
      headers: {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
        "x-chat-session-id": sessionId,
        ...(initialResponseMessageId != null ? { "x-parent-message-id": String(initialResponseMessageId) } : {}),
      },
    });
  }

  // Non-streaming (or tool-compatibility stream which we buffer first)
  const resp = await fetch(`${BACKEND}/chat/completion`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(dsBody),
  });

  if (!resp.ok) return createBackendErrorResponse(c, resp);

  let fullText = "";
  let responseMessageId: number | string | undefined;
  if (resp.body) {
    const collected = await collectFullResponse(resp.body, thinkingEnabled);
    fullText = collected.fullText;
    responseMessageId = collected.responseMessageId;
  }

  c.header("x-chat-session-id", sessionId);
  if (responseMessageId != null) c.header("x-parent-message-id", String(responseMessageId));

  const payload = buildChatCompletionResponse({
    model, promptText, fullText, tools, toolChoice: normalizedToolChoice, sessionId, responseMessageId,
  });

  if (body.stream) {
    // tool-compatibility mode: stream the buffered response
    return buildToolCompatibleStreamResponse({ payload, model, sessionId, responseMessageId });
  }

  return c.json(payload);
}

// ─── Route ────────────────────────────────────────────────────────────────────

app.post("/v1/chat/completions", async (c) => {
  let body: ChatCompletionBody;
  try {
    body = await c.req.json() as ChatCompletionBody;
  } catch {
    return c.json(makeErrorBody("Could not parse request body as JSON", "invalid_request_error"), 400);
  }

  try {
    const model = body.model ?? "deepseek-chat";
    if (isTaalasModel(model)) {
      return await handleTaalasCompletion(c, body);
    }
    return await handleChatCompletion(c, body);
  } catch (error) {
    return c.json(
      makeErrorBody(error instanceof Error ? error.message : "Chat request failed"),
      502
    );
  }
});

console.log(`DeepSeek + Taalas → OpenAI proxy on http://localhost:${PORT}`);
console.log(`  Python backend expected at ${BACKEND}`);
serve({ fetch: app.fetch, port: PORT });
