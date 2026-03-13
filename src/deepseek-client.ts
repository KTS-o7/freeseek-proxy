import type {
  DeepSeekChatRequest,
  DeepSeekSSEData,
  OpenAIChatCompletionRequest,
  OpenAIStreamChunk,
} from "./types.js";
import { v4 as uuidv4 } from "uuid";
import { execFileSync } from "child_process";
import {
  extractPromptFromMessages,
  normalizeParentMessageId,
} from "./request-normalization.js";

const DEEPSEEK_BASE = "https://chat.deepseek.com";

// ── Helpers ────────────────────────────────────────────────────────────

function createHeaders(opts: {
  authToken: string;
  cookies: string;
}): Record<string, string> {
  return {
    accept: "*/*",
    "accept-language":
      "en,fr-FR;q=0.9,fr;q=0.8,es-ES;q=0.7,es;q=0.6,en-US;q=0.5,am;q=0.4,de;q=0.3",
    authorization: `Bearer ${opts.authToken}`,
    "content-type": "application/json",
    cookie: opts.cookies,
    origin: DEEPSEEK_BASE,
    referer: `${DEEPSEEK_BASE}/`,
    "user-agent":
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
    "x-app-version": "20241129.1",
    "x-client-locale": "en_US",
    "x-client-platform": "web",
    "x-client-version": "1.0.0-always",
  };
}

// ── POW Challenge (via Python WASM solver) ─────────────────────────────

// PoW solving is handled by Python subprocess which uses the WASM sha3 solver
const POW_PYTHON = `
import sys, json, base64, wasmtime, numpy as np

WASM_PATH = sys.argv[1]
CHALLENGE_JSON = sys.argv[2]

config = json.loads(CHALLENGE_JSON)

engine = wasmtime.Engine()
with open(WASM_PATH, 'rb') as f:
    wasm_bytes = f.read()
module = wasmtime.Module(engine, wasm_bytes)
store = wasmtime.Store(engine)
linker = wasmtime.Linker(engine)
linker.define_wasi()
instance = linker.instantiate(store, module)
memory = instance.exports(store)["memory"]

def write_mem(text):
    encoded = text.encode('utf-8')
    ptr = instance.exports(store)["__wbindgen_export_0"](store, len(encoded), 1)
    mv = memory.data_ptr(store)
    for i, b in enumerate(encoded):
        mv[ptr + i] = b
    return ptr, len(encoded)

prefix = f"{config['salt']}_{config['expire_at']}_"
retptr = instance.exports(store)["__wbindgen_add_to_stack_pointer"](store, -16)
cp, cl = write_mem(config['challenge'])
pp, pl = write_mem(prefix)

instance.exports(store)["wasm_solve"](store, retptr, cp, cl, pp, pl, float(config['difficulty']))

mv = memory.data_ptr(store)
status = int.from_bytes(bytes(mv[retptr:retptr+4]), 'little', signed=True)
value_bytes = bytes(mv[retptr+8:retptr+16])
value = np.frombuffer(value_bytes, dtype=np.float64)[0]
answer = int(value)

result = {
    'algorithm': config['algorithm'],
    'challenge': config['challenge'],
    'salt': config['salt'],
    'answer': answer,
    'signature': config['signature'],
    'target_path': config['target_path']
}
print(base64.b64encode(json.dumps(result).encode()).decode())
`;

// Use the xtekky deepseek4free WASM directly
const WASM_PATH = "/Users/nainish/development/complianceos-basecamp/deepseek-proxy/../xtekky-deepseek4free/dsk/wasm/sha3_wasm_bg.7b9ca65ddd.wasm";

function solvePOW(config: Record<string, unknown>): string {
  try {
    const result = execFileSync(
      "python3",
      ["-c", POW_PYTHON, WASM_PATH, JSON.stringify(config)],
      { encoding: "utf-8", timeout: 30000 }
    );
    return result.trim();
  } catch (err) {
    throw new Error(`POW solver failed: ${err}`);
  }
}

// ── DeepSeek API Client ────────────────────────────────────────────────

export class DeepSeekClient {
  private authToken: string;
  private cookies: string;
  private chatSessionId: string;

  constructor(opts: {
    authToken: string;
    cookies?: string;
    chatSessionId?: string;
  }) {
    this.authToken = opts.authToken;
    this.cookies = opts.cookies ?? "";
    this.chatSessionId = opts.chatSessionId ?? uuidv4();
  }

  private async getPOWHeader(targetPath: string): Promise<string> {
    const resp = await fetch(
      `${DEEPSEEK_BASE}/api/v0/chat/create_pow_challenge`,
      {
        method: "POST",
        headers: createHeaders({
          authToken: this.authToken,
          cookies: this.cookies,
        }),
        body: JSON.stringify({ target_path: targetPath }),
      }
    );

    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(
        `POW challenge failed ${resp.status}: ${text.slice(0, 500)}`
      );
    }

    const data = await resp.json();
    const challenge = data?.data?.biz_data?.challenge;
    if (!challenge) {
      throw new Error("Invalid POW challenge response");
    }

    return solvePOW(challenge);
  }

  private getHeaders(powResponse?: string): Record<string, string> {
    return {
      ...createHeaders({
        authToken: this.authToken,
        cookies: this.cookies,
      }),
      ...(powResponse ? { "x-ds-pow-response": powResponse } : {}),
    };
  }

  // ── Non-streaming completion ─────────────────────────────────────────
  async chat(
    req: OpenAIChatCompletionRequest
  ): Promise<{
    text: string;
    usage: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
    response_message_id?: number | string;
  }> {
    const targetPath = "/api/v0/chat/completion";
    const powHeader = await this.getPOWHeader(targetPath);

    const dsBody: DeepSeekChatRequest = {
      chat_session_id: this.chatSessionId,
      parent_message_id: normalizeParentMessageId(req.parent_message_id) ?? null,
      prompt: extractPromptFromMessages(req.messages),
      ref_file_ids: [],
      thinking_enabled: req.thinking_enabled ?? false,
      search_enabled: req.search_enabled ?? false,
      preempt: false,
    };

    const resp = await fetch(`${DEEPSEEK_BASE}${targetPath}`, {
      method: "POST",
      headers: this.getHeaders(powHeader),
      body: JSON.stringify(dsBody),
    });

    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(
        `DeepSeek API error ${resp.status}: ${text.slice(0, 500)}`
      );
    }

    const rawBody = await resp.text();
    const parsed = this.parseSSEString(rawBody, req.thinking_enabled ?? false);
    return {
      text: parsed.text,
      usage: {
        prompt_tokens: Math.ceil(parsed.text.length / 4),
        completion_tokens: Math.ceil(parsed.text.length / 4),
        total_tokens: Math.ceil(parsed.text.length / 2),
      },
      response_message_id: parsed.responseMessageId,
    };
  }

  // ── Streaming completion ─────────────────────────────────────────────
  async *chatStream(
    req: OpenAIChatCompletionRequest
  ): AsyncGenerator<OpenAIStreamChunk> {
    const targetPath = "/api/v0/chat/completion";
    const powHeader = await this.getPOWHeader(targetPath);

    const dsBody: DeepSeekChatRequest = {
      chat_session_id: this.chatSessionId,
      parent_message_id: normalizeParentMessageId(req.parent_message_id) ?? null,
      prompt: extractPromptFromMessages(req.messages),
      ref_file_ids: [],
      thinking_enabled: req.thinking_enabled ?? false,
      search_enabled: req.search_enabled ?? false,
      preempt: false,
    };

    const resp = await fetch(`${DEEPSEEK_BASE}${targetPath}`, {
      method: "POST",
      headers: this.getHeaders(powHeader),
      body: JSON.stringify(dsBody),
    });

    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(
        `DeepSeek API error ${resp.status}: ${text.slice(0, 500)}`
      );
    }

    const streamId = `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`;
    const created = Math.floor(Date.now() / 1000);
    const model = req.model || "deepseek-chat";

    yield {
      id: streamId,
      object: "chat.completion.chunk",
      created,
      model,
      choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }],
    };

    for await (const event of this.readSSEChunks(resp.body!, req.thinking_enabled ?? false)) {
      if (!event.content && event.responseMessageId == null) {
        continue;
      }

      yield {
        id: streamId,
        object: "chat.completion.chunk",
        created,
        model,
        choices: [{
          index: 0,
          message_id: event.responseMessageId,
          delta: event.content ? { content: event.content } : {},
          finish_reason: null,
        }],
        response_message_id: event.responseMessageId,
      };
    }

    yield {
      id: streamId,
      object: "chat.completion.chunk",
      created,
      model,
      choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
    };
  }

  // ── SSE Parsing ──────────────────────────────────────────────────────

  private parseSSEString(raw: string, includeThinking: boolean): {
    text: string;
    responseMessageId?: number | string;
  } {
    let text = "";
    let responseMessageId: number | string | undefined;

    for (const line of raw.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed || !trimmed.startsWith("data: ")) continue;
      const jsonStr = trimmed.slice(6);
      if (jsonStr === "[DONE]") break;
      try {
        const data: DeepSeekSSEData = JSON.parse(jsonStr);
        const event = this.parseSSEEvent(data, includeThinking);
        if (event.responseMessageId != null) {
          responseMessageId = event.responseMessageId;
        }
        if (event.content) {
          text += event.content;
        }
      } catch {
        // skip
      }
    }

    return { text, responseMessageId };
  }

  private async *readSSEChunks(
    body: ReadableStream<Uint8Array>,
    includeThinking: boolean
  ): AsyncGenerator<{ content?: string; responseMessageId?: number | string }> {
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
        const trimmed = line.trim();
        if (!trimmed || !trimmed.startsWith("data: ")) continue;
        const jsonStr = trimmed.slice(6);
        if (jsonStr === "[DONE]") return;
        try {
          const data: DeepSeekSSEData = JSON.parse(jsonStr);
          const event = this.parseSSEEvent(data, includeThinking);
          if (event.content || event.responseMessageId != null) {
            yield event;
          }
        } catch {
          // skip
        }
      }
    }

    const trailingLine = buffer.trim();
    if (!trailingLine || !trailingLine.startsWith("data: ")) {
      return;
    }

    const jsonStr = trailingLine.slice(6);
    if (jsonStr === "[DONE]") {
      return;
    }

    try {
      const data: DeepSeekSSEData = JSON.parse(jsonStr);
      const event = this.parseSSEEvent(data, includeThinking);
      if (event.content || event.responseMessageId != null) {
        yield event;
      }
    } catch {
      // skip
    }
  }

  private parseSSEEvent(
    data: DeepSeekSSEData,
    includeThinking: boolean
  ): { content?: string; responseMessageId?: number | string } {
    const responseMessageId = data.response_message_id ?? (typeof data.v === "object" ? data.v?.response?.message_id : undefined);
    const content = this.extractContentDelta(data, includeThinking);

    return {
      ...(content ? { content } : {}),
      ...(responseMessageId != null ? { responseMessageId } : {}),
    };
  }

  private extractContentDelta(
    data: DeepSeekSSEData,
    includeThinking: boolean
  ): string | null {
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
        .filter((fragmentContent) => Boolean(fragmentContent))
        .join("");

      return content || null;
    }

    if (typeof data.content === "string") {
      return data.content;
    }

    return null;
  }
}
