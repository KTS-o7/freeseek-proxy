export type ChatMessageContentPart = {
  type?: string;
  text?: string;
  content?: string;
};

export type ChatMessageContent =
  | string
  | ChatMessageContentPart[]
  | null
  | undefined;

export type ChatMessageLike = {
  role?: string;
  content?: ChatMessageContent;
};

export type ParentMessageId = number | string | null | undefined;

export function normalizeMessageContent(content: ChatMessageContent): string {
  if (typeof content === "string") {
    return content;
  }

  if (!Array.isArray(content)) {
    return "";
  }

  return content
    .map((part) => {
      if (!part || typeof part !== "object") {
        return "";
      }

      if (typeof part.text === "string") {
        return part.text;
      }

      if (typeof part.content === "string") {
        return part.content;
      }

      return "";
    })
    .join("");
}

export function extractPromptFromMessages(messages: ChatMessageLike[]): string {
  return normalizeMessageContent(messages[messages.length - 1]?.content);
}

export function normalizeParentMessageId(parentMessageId: ParentMessageId): ParentMessageId {
  if (typeof parentMessageId !== "string") {
    return parentMessageId;
  }

  if (/^\d+$/.test(parentMessageId)) {
    return Number(parentMessageId);
  }

  return parentMessageId;
}

export function normalizeAssistantContent(content: ChatMessageContent | null): string | null {
  if (content == null) {
    return null;
  }

  return normalizeMessageContent(content);
}
