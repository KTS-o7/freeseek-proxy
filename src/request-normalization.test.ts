import test from "node:test";
import assert from "node:assert/strict";

import {
  extractPromptFromMessages,
  normalizeAssistantContent,
  normalizeMessageContent,
  normalizeParentMessageId,
} from "./request-normalization.js";

test("normalizeMessageContent returns plain strings unchanged", () => {
  assert.equal(normalizeMessageContent("Hello"), "Hello");
});

test("normalizeMessageContent flattens OpenAI text content arrays", () => {
  assert.equal(
    normalizeMessageContent([
      { type: "text", text: "Hello" },
      { type: "text", text: " there" },
    ]),
    "Hello there"
  );
});

test("extractPromptFromMessages uses normalized last message content", () => {
  assert.equal(
    extractPromptFromMessages([
      { role: "system", content: "You are helpful" },
      {
        role: "user",
        content: [
          { type: "text", text: "Hello" },
          { type: "text", text: "!" },
        ],
      },
    ]),
    "Hello!"
  );
});

test("normalizeParentMessageId converts numeric strings to numbers", () => {
  assert.equal(normalizeParentMessageId("4"), 4);
});

test("normalizeParentMessageId keeps non-numeric ids unchanged", () => {
  assert.equal(normalizeParentMessageId("msg_4"), "msg_4");
});

test("normalizeAssistantContent preserves null content", () => {
  assert.equal(normalizeAssistantContent(null), null);
});
