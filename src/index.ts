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

type BackendErrorPayload = {
  error?:
    | string
    | {
        type?: string;
        message?: string;
        status_code?: number;
      };
};

const app = new Hono();

app.get("/", async (c) => {
  const html = await readFile(new URL("../public/index.html", import.meta.url), "utf8");
  return c.html(html);
});

app.use("/v1/*", async (c, next) => {
  if (!API_KEY) return next();
  const auth = c.req.header("authorization");
  if (!auth || auth !== `Bearer ${API_KEY}`) {
    return c.json({ error: { message: "Invalid API key", type: "invalid_request_error", code: "invalid_api_key" } }, 401);
  }
  return next();
});

app.get("/health", (c) => c.json({ status: "ok" }));

app.get("/v1/models", (c) =>
  c.json({
    object: "list",
    data: [
      { id: "deepseek-chat", object: "model", created: 1704067200, owned_by: "deepseek" },
      { id: "deepseek-coder", object: "model", created: 1704067200, owned_by: "deepseek" },
      { id: "deepseek-reasoner", object: "model", created: 1704067200, owned_by: "deepseek" },
    ],
  })
);

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

async function createBackendErrorResponse(c: Context, resp: Response) {
  try {
    const data = (await resp.json()) as BackendErrorPayload;
    const backendError = typeof data.error === "object" && data.error !== null ? data.error : null;

    if (backendError?.message) {
      const statusCode = typeof backendError.status_code === "number" ? backendError.status_code : resp.status;
      return new Response(
        JSON.stringify({
          error: {
            message: backendError.message,
            type: backendError.type ?? "invalid_request_error",
            ...(backendError.status_code != null ? { status_code: backendError.status_code } : {}),
          },
        }),
        {
          status: statusCode,
          headers: { "content-type": "application/json; charset=UTF-8" },
        }
      );
    }
  } catch {}

  return c.json({ error: { message: `Backend error ${resp.status}` } }, 502);
}

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
      .filter((fragmentContent) => Boolean(fragmentContent))
      .join("");

    return content || null;
  }

  if (typeof data.content === "string") {
    return data.content;
  }

  return null;
}

function parseSSELine(line: string, includeThinking: boolean): ParsedSSEEvent | null {
  if (!line.startsWith("data: ")) {
    return null;
  }

  const jsonStr = line.slice(6);
  if (jsonStr === "[DONE]") {
    return null;
  }

  try {
    const data = JSON.parse(jsonStr) as DeepSeekSSEData;
    const responseMessageId = data.response_message_id ?? (typeof data.v === "object" ? data.v?.response?.message_id : undefined);
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
    if (done) {
      break;
    }

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const event = parseSSELine(line.trim(), includeThinking);
      if (!event) {
        continue;
      }

      if (event.responseMessageId != null) {
        return event.responseMessageId;
      }

      if (event.content) {
        return undefined;
      }
    }
  }

  const trailingLine = buffer.trim();
  if (!trailingLine) {
    return undefined;
  }

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
    if (done) {
      break;
    }

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";

    for (const line of lines) {
      const event = parseSSELine(line.trim(), includeThinking);
      if (!event) {
        continue;
      }

      if (event.responseMessageId != null) {
        responseMessageId = event.responseMessageId;
      }

      if (event.content) {
        fullText += event.content;
      }
    }
  }

  const trailingLine = buffer.trim();
  if (trailingLine) {
    const event = parseSSELine(trailingLine, includeThinking);
    if (event?.responseMessageId != null) {
      responseMessageId = event.responseMessageId;
    }
    if (event?.content) {
      fullText += event.content;
    }
  }

  return { fullText, responseMessageId };
}

function buildUsage(text: string) {
  return {
    prompt_tokens: Math.ceil(text.length / 4),
    completion_tokens: Math.ceil(text.length / 4),
    total_tokens: Math.ceil(text.length / 2),
  };
}

function buildChatCompletionResponse(args: {
  model: string;
  fullText: string;
  responseMessageId?: number | string;
  tools: OpenAITool[];
  toolChoice: ReturnType<typeof normalizeToolChoice>;
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
      (toolCall) => toolCall.function.name !== args.toolChoice.functionName
    );
    if (invalidToolCall) {
      throw new Error(`Tool choice required function '${args.toolChoice.functionName}' but a different tool call was emitted`);
    }
  }

  if (parsedToolEnvelope) {
    return {
      id: `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`,
      object: "chat.completion",
      created: Math.floor(Date.now() / 1000),
      model: args.model,
      choices: [{
        index: 0,
        message: { role: "assistant", content: null, tool_calls: parsedToolEnvelope.toolCalls },
        finish_reason: "tool_calls",
      }],
      ...(args.responseMessageId != null ? { response_message_id: args.responseMessageId } : {}),
      usage: buildUsage(args.fullText),
    };
  }

  return {
    id: `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`,
    object: "chat.completion",
    created: Math.floor(Date.now() / 1000),
    model: args.model,
    choices: [{
      index: 0,
      message: { role: "assistant", content: args.fullText },
      finish_reason: "stop",
    }],
    ...(args.responseMessageId != null ? { response_message_id: args.responseMessageId } : {}),
    usage: buildUsage(args.fullText),
  };
}

function buildToolCompatibleStreamResponse(args: {
  payload: ReturnType<typeof buildChatCompletionResponse>;
  model: string;
  responseMessageId?: number | string;
  sessionId: string;
}) {
  const streamId = `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`;
  const created = Math.floor(Date.now() / 1000);
  const encoder = new TextEncoder();

  const readable = new ReadableStream({
    start(controller) {
      const send = (data: object | string) => {
        const payload = typeof data === "string" ? data : JSON.stringify(data);
        controller.enqueue(encoder.encode(`data: ${payload}\n\n`));
      };

      send({
        id: streamId,
        object: "chat.completion.chunk",
        created,
        model: args.model,
        choices: [{ index: 0, message_id: args.responseMessageId, delta: { role: "assistant" }, finish_reason: null }],
        ...(args.responseMessageId != null ? { response_message_id: args.responseMessageId } : {}),
      });

      const message = args.payload.choices[0]?.message;
      const toolCalls = message && "tool_calls" in message ? message.tool_calls : undefined;
      if (toolCalls?.length) {
        send({
          id: streamId,
          object: "chat.completion.chunk",
          created,
          model: args.model,
          choices: [{
            index: 0,
            message_id: args.responseMessageId,
            delta: {
              tool_calls: toolCalls.map((toolCall: NonNullable<typeof toolCalls>[number], index: number) => ({
                index,
                id: toolCall.id,
                type: toolCall.type,
                function: {
                  name: toolCall.function.name,
                  arguments: toolCall.function.arguments,
                },
              })),
            },
            finish_reason: null,
          }],
          ...(args.responseMessageId != null ? { response_message_id: args.responseMessageId } : {}),
        });

        send({
          id: streamId,
          object: "chat.completion.chunk",
          created,
          model: args.model,
          choices: [{ index: 0, delta: {}, finish_reason: "tool_calls" }],
        });
      } else {
        send({
          id: streamId,
          object: "chat.completion.chunk",
          created,
          model: args.model,
          choices: [{
            index: 0,
            message_id: args.responseMessageId,
            delta: { content: normalizeAssistantContent(message?.content ?? "") ?? "" },
            finish_reason: null,
          }],
          ...(args.responseMessageId != null ? { response_message_id: args.responseMessageId } : {}),
        });

        send({
          id: streamId,
          object: "chat.completion.chunk",
          created,
          model: args.model,
          choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
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
      "x-chat-session-id": args.sessionId,
      ...(args.responseMessageId != null ? { "x-parent-message-id": String(args.responseMessageId) } : {}),
    },
  });
}

async function handleChatCompletion(c: Context) {
  let body: ChatCompletionBody;
  try {
    body = await c.req.json();
  } catch {
    return c.json({ error: { message: "Invalid JSON" } }, 400);
  }

  if (!body.messages?.length) {
    return c.json({ error: { message: "messages required" } }, 400);
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

  const dsBody = {
    chat_session_id: sessionId,
    parent_message_id: normalizeParentMessageId(body.parent_message_id) ?? null,
    prompt,
    ref_file_ids: [],
    thinking_enabled: thinkingEnabled,
    search_enabled: searchEnabled,
    preempt: false,
  };

  if (body.stream && !toolCompatibilityMode) {
    // Streaming: proxy SSE from backend
    const resp = await fetch(`${BACKEND}/chat/completion`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(dsBody),
    });

    if (!resp.ok) {
      return createBackendErrorResponse(c, resp);
    }

    if (!resp.body) {
      return c.json({ error: { message: "Backend stream unavailable" } }, 502);
    }

    const [headerBody, streamBody] = resp.body.tee();
    const initialResponseMessageId = await collectResponseMessageId(headerBody, thinkingEnabled);

    const streamId = `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`;
    const created = Math.floor(Date.now() / 1000);

    const readable = new ReadableStream({
      async start(controller) {
        let latestResponseMessageId = initialResponseMessageId;
        const encoder = new TextEncoder();
        const send = (data: object) => {
          controller.enqueue(encoder.encode(`data: ${JSON.stringify(data)}\n\n`));
        };

        send({
          id: streamId, object: "chat.completion.chunk", created, model,
          choices: [{ index: 0, message_id: latestResponseMessageId, delta: { role: "assistant" }, finish_reason: null }],
          ...(latestResponseMessageId != null ? { response_message_id: latestResponseMessageId } : {}),
        });

        try {
          const reader = streamBody.getReader();

          const decoder = new TextDecoder();
          let buffer = "";
          const flushLine = (line: string) => {
            const event = parseSSELine(line.trim(), thinkingEnabled);
            if (!event || (!event.content && event.responseMessageId == null)) {
              return;
            }

            if (event.responseMessageId != null) {
              latestResponseMessageId = event.responseMessageId;
            }

            send({
              id: streamId,
              object: "chat.completion.chunk",
              created,
              model,
              choices: [{
                index: 0,
                message_id: latestResponseMessageId,
                delta: event.content ? { content: event.content } : {},
                finish_reason: null,
              }],
              ...(latestResponseMessageId != null ? { response_message_id: latestResponseMessageId } : {}),
            });
          };

          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buffer += decoder.decode(value, { stream: true });
            const lines = buffer.split("\n");
            buffer = lines.pop() ?? "";
            for (const line of lines) {
              flushLine(line.trim());
            }
          }

          const trailingLine = buffer.trim();
          if (trailingLine) flushLine(trailingLine);

          send({
            id: streamId, object: "chat.completion.chunk", created, model,
            choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
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

  const resp = await fetch(`${BACKEND}/chat/completion`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(dsBody),
  });

  if (!resp.ok) {
    return createBackendErrorResponse(c, resp);
  }

  let fullText = "";
  let responseMessageId: number | string | undefined;
  if (resp.body) {
    const collected = await collectFullResponse(resp.body, thinkingEnabled);
    fullText = collected.fullText;
    responseMessageId = collected.responseMessageId;
  }

  c.header("x-chat-session-id", sessionId);
  if (responseMessageId != null) {
    c.header("x-parent-message-id", String(responseMessageId));
  }

  const payload = buildChatCompletionResponse({
    model,
    fullText,
    responseMessageId,
    tools,
    toolChoice: normalizedToolChoice,
  });

  if (body.stream) {
    return buildToolCompatibleStreamResponse({
      payload,
      model,
      responseMessageId,
      sessionId,
    });
  }

  return c.json(payload);
}

app.post("/v1/chat/completions", async (c) => {
  try {
    return await handleChatCompletion(c);
  } catch (error) {
    return c.json(
      { error: { message: error instanceof Error ? error.message : "Chat request failed" } },
      502
    );
  }
});

console.log(`DeepSeek → OpenAI proxy on http://localhost:${PORT}`);
console.log(`  Python backend expected at ${BACKEND}`);
serve({ fetch: app.fetch, port: PORT });
