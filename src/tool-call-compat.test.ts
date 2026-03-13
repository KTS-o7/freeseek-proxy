import test from "node:test";
import assert from "node:assert/strict";

import {
  buildProxyPrompt,
  buildToolSystemPrompt,
  formatConversationForDeepSeek,
  normalizeToolChoice,
  parseToolEnvelope,
} from "./tool-call-compat.js";
import type { OpenAITool } from "./types.js";

const tools: OpenAITool[] = [
  {
    type: "function",
    function: {
      name: "bash",
      description: "Run a shell command",
      parameters: {
        type: "object",
        properties: {
          command: { type: "string" },
        },
        required: ["command"],
      },
    },
  },
];

test("normalizeToolChoice maps string and object forms", () => {
  assert.deepEqual(normalizeToolChoice("auto"), { mode: "auto" });
  assert.deepEqual(normalizeToolChoice({ type: "function", function: { name: "bash" } }), {
    mode: "function",
    functionName: "bash",
  });
});

test("buildToolSystemPrompt includes tool names and reserved JSON contract", () => {
  const prompt = buildToolSystemPrompt(tools, { mode: "auto" });
  assert.match(prompt, /opencode_tool_call/);
  assert.match(prompt, /opencode_tool_calls/);
  assert.match(prompt, /bash/);
});

test("parseToolEnvelope returns a tool call for valid reserved JSON", () => {
  const parsed = parseToolEnvelope(
    JSON.stringify({
      opencode_tool_call: {
        name: "bash",
        arguments: {
          command: "pwd",
        },
      },
    }),
    tools
  );

  assert.equal(parsed?.toolCalls[0]?.function.name, "bash");
  assert.equal(parsed?.toolCalls[0]?.function.arguments, JSON.stringify({ command: "pwd" }));
});

test("parseToolEnvelope accepts reserved JSON followed by stray text", () => {
  const parsed = parseToolEnvelope(
    '{"opencode_tool_call":{"name":"bash","arguments":{"command":"pwd"}}}extra text',
    tools
  );

  assert.equal(parsed?.toolCalls[0]?.function.name, "bash");
});

test("parseToolEnvelope parses stringified arguments", () => {
  const parsed = parseToolEnvelope(
    '{"opencode_tool_call":{"name":"bash","arguments":"{\\"command\\":\\"pwd\\"}"}}',
    tools
  );

  assert.equal(parsed?.toolCalls[0]?.function.arguments, JSON.stringify({ command: "pwd" }));
});

test("parseToolEnvelope supports multiple tool calls", () => {
  const parsed = parseToolEnvelope(
    JSON.stringify({
      opencode_tool_calls: [
        { name: "bash", arguments: { command: "pwd" } },
        { name: "bash", arguments: { command: "ls" } },
      ],
    }),
    tools
  );

  assert.equal(parsed?.toolCalls.length, 2);
  assert.equal(parsed?.toolCalls[1]?.function.arguments, JSON.stringify({ command: "ls" }));
});

test("plain assistant text remains plain text when no tool envelope is present", () => {
  const parsed = parseToolEnvelope("hello", tools);
  assert.equal(parsed, null);
});

test("buildProxyPrompt injects tool instruction when tools are provided", () => {
  const prompt = buildProxyPrompt([{ role: "user", content: "show cwd" }], tools, { mode: "auto" });
  assert.match(prompt, /opencode_tool_call/);
  assert.match(prompt, /show cwd/);
});

test("tool role messages are included in follow-up prompt formatting", () => {
  const prompt = formatConversationForDeepSeek([
    {
      role: "assistant",
      content: null,
      tool_calls: [
        {
          id: "call_1",
          type: "function",
          function: {
            name: "bash",
            arguments: JSON.stringify({ command: "pwd" }),
          },
        },
      ],
    },
    { role: "tool", tool_call_id: "call_1", content: "done" },
  ]);
  assert.match(prompt, /done/);
  assert.match(prompt, /bash/);
});
