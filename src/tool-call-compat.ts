import { v4 as uuidv4 } from "uuid";

import type {
  OpenAIMessage,
  OpenAIMessageToolCall,
  OpenAITool,
  OpenAIToolChoice,
} from "./types.js";
import { normalizeMessageContent } from "./request-normalization.js";

export type NormalizedToolChoice = {
  mode: "none" | "auto" | "required" | "function";
  functionName?: string;
};

export type ParsedToolEnvelope = {
  toolCalls: OpenAIMessageToolCall[];
};

const TOOL_CALL_KEY = "opencode_tool_call";
const TOOL_CALLS_KEY = "opencode_tool_calls";

function extractJsonObjectCandidates(text: string) {
  const candidates: string[] = [];

  for (let start = 0; start < text.length; start++) {
    if (text[start] !== "{") {
      continue;
    }

    let depth = 0;
    let inString = false;
    let escaped = false;

    for (let index = start; index < text.length; index++) {
      const char = text[index];

      if (inString) {
        if (escaped) {
          escaped = false;
          continue;
        }

        if (char === "\\") {
          escaped = true;
          continue;
        }

        if (char === '"') {
          inString = false;
        }

        continue;
      }

      if (char === '"') {
        inString = true;
        continue;
      }

      if (char === "{") {
        depth += 1;
        continue;
      }

      if (char === "}") {
        depth -= 1;
        if (depth === 0) {
          candidates.push(text.slice(start, index + 1));
          break;
        }
      }
    }
  }

  return candidates;
}

function stringifySchema(value: unknown) {
  try {
    return JSON.stringify(value ?? {}, null, 2);
  } catch {
    return "{}";
  }
}

export function normalizeToolChoice(toolChoice?: OpenAIToolChoice): NormalizedToolChoice {
  if (!toolChoice || toolChoice === "auto") {
    return { mode: "auto" };
  }

  if (toolChoice === "none" || toolChoice === "required") {
    return { mode: toolChoice };
  }

  if (toolChoice.type === "function" && toolChoice.function?.name) {
    return {
      mode: "function",
      functionName: toolChoice.function.name,
    };
  }

  return { mode: "auto" };
}

export function buildToolSystemPrompt(tools: OpenAITool[], toolChoice: NormalizedToolChoice) {
  const availableTools = tools
    .filter((tool) => tool.type === "function" && tool.function?.name)
    .map((tool) => {
      const description = tool.function.description ? `Description: ${tool.function.description}` : "Description: none";
      const parameters = `JSON Schema: ${stringifySchema(tool.function.parameters)}`;
      return `- ${tool.function.name}\n  ${description}\n  ${parameters}`;
    })
    .join("\n");

  const toolChoiceRule =
    toolChoice.mode === "required"
      ? "You must respond with exactly one tool call JSON object before any natural language answer."
      : toolChoice.mode === "function" && toolChoice.functionName
        ? `You must respond with a tool call for the function named ${toolChoice.functionName} before any natural language answer.`
        : toolChoice.mode === "none"
          ? "Do not call any tool. Respond in natural language only."
          : "Call a tool only when it is genuinely needed. Otherwise answer normally.";

  return [
    "You are replying through an OpenAI-compatible proxy that supports tool use.",
    toolChoiceRule,
    `If you decide to call a tool, output only valid JSON with this exact top-level shape: {\"${TOOL_CALL_KEY}\": {\"name\": \"tool_name\", \"arguments\": { ... }}}.`,
    `If you need multiple tool calls in one reply, output only valid JSON with this exact top-level shape: {\"${TOOL_CALLS_KEY}\": [{\"name\": \"tool_name\", \"arguments\": { ... }}]}.`,
    "Do not wrap the JSON in markdown.",
    "Do not include explanatory text before or after the JSON.",
    "If you are responding after receiving a tool result and no further tool is needed, answer normally in plain text.",
    "Available tools:",
    availableTools || "- none",
  ].join("\n\n");
}

function formatToolCall(toolCall: OpenAIMessageToolCall) {
  return `${toolCall.function.name}(${toolCall.function.arguments})`;
}

function parseToolArguments(argumentsValue: unknown) {
  if (argumentsValue && typeof argumentsValue === "object") {
    return argumentsValue;
  }

  if (typeof argumentsValue === "string") {
    try {
      const parsed = JSON.parse(argumentsValue);
      if (parsed && typeof parsed === "object") {
        return parsed;
      }
    } catch {}
  }

  return null;
}

function buildToolCall(name: string, argumentsValue: unknown): OpenAIMessageToolCall | null {
  const parsedArguments = parseToolArguments(argumentsValue);
  if (!parsedArguments) {
    return null;
  }

  return {
    id: `call_${uuidv4().replace(/-/g, "")}`,
    type: "function",
    function: {
      name,
      arguments: JSON.stringify(parsedArguments),
    },
  };
}

export function formatConversationForDeepSeek(messages: OpenAIMessage[]) {
  return messages
    .map((message) => {
      if (message.role === "system") {
        return `System: ${normalizeMessageContent(message.content)}`;
      }

      if (message.role === "user") {
        return `User: ${normalizeMessageContent(message.content)}`;
      }

      if (message.role === "assistant") {
        const parts: string[] = [];

        if (message.tool_calls?.length) {
          parts.push(`Assistant tool request: ${message.tool_calls.map(formatToolCall).join(", ")}`);
        }

        const content = normalizeMessageContent(message.content);
        if (content) {
          parts.push(`Assistant: ${content}`);
        }

        if (parts.length) {
          return parts.join("\n");
        }

        return "Assistant:";
      }

      if (message.role === "tool") {
        return `Tool (${message.tool_call_id ?? "unknown"}) result: ${normalizeMessageContent(message.content)}`;
      }

      return "";
    })
    .filter(Boolean)
    .join("\n\n");
}

export function hasToolCompatibilityRequest(messages: OpenAIMessage[], tools?: OpenAITool[], toolChoice?: OpenAIToolChoice) {
  return Boolean(
    (tools && tools.length > 0) ||
      toolChoice ||
      messages.some((message) => message.role === "tool" || (message.role === "assistant" && (message.tool_calls?.length ?? 0) > 0))
  );
}

export function buildProxyPrompt(messages: OpenAIMessage[], tools: OpenAITool[], toolChoice: NormalizedToolChoice) {
  const promptSections = [buildToolSystemPrompt(tools, toolChoice), formatConversationForDeepSeek(messages), "Reply to the latest conversation turn."];
  return promptSections.filter(Boolean).join("\n\n");
}

export function parseToolEnvelope(text: string, tools: OpenAITool[]): ParsedToolEnvelope | null {
  for (const candidate of extractJsonObjectCandidates(text.trim())) {
    let payload: unknown;
    try {
      payload = JSON.parse(candidate);
    } catch {
      continue;
    }

    if (!payload || typeof payload !== "object") {
      continue;
    }

    const payloadRecord = payload as Record<string, unknown>;
    const singleToolCall = payloadRecord[TOOL_CALL_KEY];
    const multipleToolCalls = payloadRecord[TOOL_CALLS_KEY];

    const rawCalls = Array.isArray(multipleToolCalls)
      ? multipleToolCalls
      : singleToolCall && typeof singleToolCall === "object"
        ? [singleToolCall]
        : [];

    if (!rawCalls.length) {
      continue;
    }

    const toolCalls = rawCalls
      .map((toolCall) => {
        if (!toolCall || typeof toolCall !== "object") {
          return null;
        }

        const name = (toolCall as Record<string, unknown>).name;
        const args = (toolCall as Record<string, unknown>).arguments;
        if (typeof name !== "string" || !name) {
          return null;
        }

        const toolExists = tools.some((tool) => tool.type === "function" && tool.function.name === name);
        if (!toolExists) {
          return null;
        }

        return buildToolCall(name, args);
      })
      .filter((toolCall): toolCall is OpenAIMessageToolCall => toolCall !== null);

    if (!toolCalls.length) {
      continue;
    }

    return { toolCalls };
  }

  return null;
}
