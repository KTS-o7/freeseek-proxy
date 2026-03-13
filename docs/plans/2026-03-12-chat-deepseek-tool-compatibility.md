# Chat DeepSeek Tool Compatibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add OpenAI-compatible non-streaming tool-call support to the `chat.deepseek.com` web proxy so OpenCode can use tools through the existing custom provider.

**Architecture:** Keep the upstream transport on `chat.deepseek.com`, but add a proxy-side compatibility layer that accepts `tools`, `tool_choice`, and `tool` messages, injects a strict hidden instruction for tool output formatting, parses a reserved JSON envelope from the assistant reply, and converts that envelope into OpenAI `tool_calls`. Leave normal text chat behavior intact and keep streaming as text-only for now.

**Tech Stack:** TypeScript, Hono, Node test runner, existing Python DeepSeek web backend

---

### Task 1: Add failing tests for tool-call normalization and parsing

**Files:**
- Modify: `src/request-normalization.test.ts`
- Create: `src/tool-call-compat.test.ts`
- Test: `src/request-normalization.test.ts`
- Test: `src/tool-call-compat.test.ts`

**Step 1: Write the failing tests**

```ts
test("normalizeToolChoice maps string and object forms", () => {
  assert.deepEqual(normalizeToolChoice("auto"), { mode: "auto" });
  assert.deepEqual(normalizeToolChoice({ type: "function", function: { name: "bash" } }), {
    mode: "function",
    functionName: "bash",
  });
});

test("parseToolEnvelope returns a tool call for valid reserved JSON", () => {
  const parsed = parseToolEnvelope('{"opencode_tool_call":{"name":"bash","arguments":{"command":"pwd"}}}', tools);
  assert.equal(parsed?.toolCalls[0]?.function.name, "bash");
});
```

**Step 2: Run test to verify it fails**

Run: `node --import tsx --test src/request-normalization.test.ts src/tool-call-compat.test.ts`
Expected: FAIL because the new helpers do not exist yet.

**Step 3: Write minimal implementation**

```ts
export function normalizeToolChoice(...) { ... }
export function parseToolEnvelope(...) { ... }
```

**Step 4: Run test to verify it passes**

Run: `node --import tsx --test src/request-normalization.test.ts src/tool-call-compat.test.ts`
Expected: PASS

### Task 2: Add request/response compatibility helpers

**Files:**
- Create: `src/tool-call-compat.ts`
- Modify: `src/types.ts`
- Modify: `src/request-normalization.ts`
- Test: `src/tool-call-compat.test.ts`

**Step 1: Write the failing test for hidden instruction generation**

```ts
test("buildToolSystemPrompt includes tool names and reserved JSON contract", () => {
  const prompt = buildToolSystemPrompt(tools, { mode: "auto" });
  assert.match(prompt, /opencode_tool_call/);
  assert.match(prompt, /bash/);
});
```

**Step 2: Run test to verify it fails**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: FAIL because the helper does not exist yet.

**Step 3: Write minimal implementation**

```ts
export function buildToolSystemPrompt(tools, toolChoice) {
  return "...reserved JSON envelope...";
}
```

**Step 4: Run test to verify it passes**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: PASS

### Task 3: Accept tool metadata on incoming chat completion requests

**Files:**
- Modify: `src/index.ts`
- Modify: `src/types.ts`
- Test: `src/tool-call-compat.test.ts`

**Step 1: Write the failing test for request mapping**

```ts
test("buildProxyPrompt injects tool instruction when tools are provided", () => {
  const prompt = buildProxyPrompt(messages, tools, { mode: "auto" });
  assert.match(prompt, /opencode_tool_call/);
});
```

**Step 2: Run test to verify it fails**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: FAIL because `src/index.ts` still forwards only plain prompt text.

**Step 3: Write minimal implementation**

```ts
const toolChoice = normalizeToolChoice(body.tool_choice);
const prompt = buildProxyPrompt(body.messages, body.tools, toolChoice);
```

**Step 4: Run test to verify it passes**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: PASS

### Task 4: Convert tool-envelope assistant replies into OpenAI `tool_calls`

**Files:**
- Modify: `src/index.ts`
- Modify: `src/tool-call-compat.ts`
- Test: `src/tool-call-compat.test.ts`

**Step 1: Write the failing test for non-streaming OpenAI response shape**

```ts
test("createOpenAIResponse emits tool_calls instead of assistant text for valid envelope", () => {
  const response = createOpenAIResponse({ fullText: envelopeText, model: "deepseek-chat", ... });
  assert.equal(response.choices[0]?.message?.tool_calls?.[0]?.function.name, "bash");
});
```

**Step 2: Run test to verify it fails**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: FAIL because the proxy currently only returns `message.content`.

**Step 3: Write minimal implementation**

```ts
if (parsedToolEnvelope) {
  return c.json({
    ...,
    choices: [{ index: 0, message: { role: "assistant", content: null, tool_calls: parsed.toolCalls }, finish_reason: "tool_calls" }],
  });
}
```

**Step 4: Run test to verify it passes**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: PASS

### Task 5: Preserve normal text chat and tool-result follow-up messages

**Files:**
- Modify: `src/index.ts`
- Modify: `src/request-normalization.ts`
- Test: `src/tool-call-compat.test.ts`

**Step 1: Write the failing tests**

```ts
test("plain assistant text remains plain text when no tool envelope is present", () => {
  const parsed = parseToolEnvelope("hello", tools);
  assert.equal(parsed, null);
});

test("tool role messages are included in follow-up prompt formatting", () => {
  const prompt = buildProxyPrompt([
    { role: "assistant", content: null, tool_calls: [...] },
    { role: "tool", tool_call_id: "call_1", content: "done" },
  ], tools, { mode: "auto" });
  assert.match(prompt, /done/);
});
```

**Step 2: Run test to verify it fails**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: FAIL until follow-up formatting is implemented.

**Step 3: Write minimal implementation**

```ts
function formatConversationForDeepSeek(messages) {
  return ...
}
```

**Step 4: Run test to verify it passes**

Run: `node --import tsx --test src/tool-call-compat.test.ts`
Expected: PASS

### Task 6: Verify regression coverage and runtime behavior

**Files:**
- Test: `src/request-normalization.test.ts`
- Test: `src/tool-call-compat.test.ts`
- Modify: `README.md`

**Step 1: Run focused tests**

Run: `node --import tsx --test src/request-normalization.test.ts src/tool-call-compat.test.ts`
Expected: PASS

**Step 2: Run typecheck**

Run: `npm run typecheck`
Expected: PASS

**Step 3: Document the limitation**

```md
- Non-streaming tool-call compatibility is supported through a proxy JSON envelope.
- Streaming remains text-only.
```

**Step 4: Run a manual verification request**

Run: `curl -X POST http://127.0.0.1:9123/v1/chat/completions ...`
Expected: a plain text response for normal chat, and an OpenAI `tool_calls` response when the model emits the reserved JSON envelope.
