export type OpenAIMessageContentPart = {
  type?: string;
  text?: string;
  content?: string;
};

export type OpenAIMessageContent = string | OpenAIMessageContentPart[];

export interface OpenAIFunctionTool {
  type: "function";
  function: {
    name: string;
    description?: string;
    parameters?: unknown;
    strict?: boolean;
  };
}

export type OpenAITool = OpenAIFunctionTool;

export type OpenAIToolChoice =
  | "none"
  | "auto"
  | "required"
  | {
      type: "function";
      function: {
        name: string;
      };
    };

export interface OpenAIMessageToolCall {
  id: string;
  type: "function";
  function: {
    name: string;
    arguments: string;
  };
}

export interface OpenAIMessage {
  role: "system" | "user" | "assistant" | "tool";
  content?: OpenAIMessageContent | null;
  tool_call_id?: string;
  tool_calls?: OpenAIMessageToolCall[];
}

export interface OpenAIChatCompletionRequest {
  model: string;
  messages: OpenAIMessage[];
  stream?: boolean;
  temperature?: number;
  max_tokens?: number;
  top_p?: number;
  frequency_penalty?: number;
  presence_penalty?: number;
  thinking_enabled?: boolean;
  search_enabled?: boolean;
  parent_message_id?: number | string | null;
  tools?: OpenAITool[];
  tool_choice?: OpenAIToolChoice;
}

export interface OpenAIChoice {
  index: number;
  message_id?: number | string;
  message?: {
    role: "assistant";
    content: string | null;
    tool_calls?: OpenAIMessageToolCall[];
  };
  delta?: {
    role?: "assistant";
    content?: string;
    tool_calls?: Array<{
      index: number;
      id?: string;
      type?: "function";
      function?: {
        name?: string;
        arguments?: string;
      };
    }>;
  };
  finish_reason: "stop" | "tool_calls" | string | null;
}

export interface OpenAIUsage {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface OpenAIChatCompletionResponse {
  id: string;
  object: "chat.completion";
  created: number;
  model: string;
  choices: OpenAIChoice[];
  usage?: OpenAIUsage;
  response_message_id?: number | string;
}

export interface OpenAIStreamChunk {
  id: string;
  object: "chat.completion.chunk";
  created: number;
  model: string;
  choices: OpenAIChoice[];
  response_message_id?: number | string;
}

export type DeepSeekMessageId = number | string;

export type DeepSeekClientFetch = typeof globalThis.fetch;

export interface DeepSeekClientOptions {
  baseURL?: string;
  apiKey?: string;
  model?: string;
  sessionId?: string;
  parentMessageId?: DeepSeekMessageId | null;
  thinkingEnabled?: boolean;
  searchEnabled?: boolean;
  fetch?: DeepSeekClientFetch;
}

export interface DeepSeekClientRequestOptions {
  model?: string;
  sessionId?: string | null;
  parentMessageId?: DeepSeekMessageId | null;
  thinkingEnabled?: boolean;
  searchEnabled?: boolean;
}

export interface DeepSeekClientState {
  sessionId?: string;
  parentMessageId?: DeepSeekMessageId;
}

export interface DeepSeekClientCompletionResult extends DeepSeekClientState {
  text: string;
  response: OpenAIChatCompletionResponse;
  responseMessageId?: DeepSeekMessageId;
}

// DeepSeek API types
export interface DeepSeekChatRequest {
  chat_session_id: string;
  parent_message_id: number | string | null;
  prompt: string;
  ref_file_ids: string[];
  thinking_enabled: boolean;
  search_enabled: boolean;
  preempt: boolean;
}

export interface DeepSeekFragment {
  id: number;
  type: string;
  content: string;
  references: unknown[];
  stage_id: number;
}

export interface DeepSeekMessage {
  message_id: number;
  parent_id: number;
  model: string;
  role: string;
  thinking_enabled: boolean;
  ban_edit: boolean;
  ban_regenerate: boolean;
  status: string;
  accumulated_token_usage: number;
  files: unknown[];
  feedback: unknown;
  inserted_at: number;
  search_enabled: boolean;
  fragments: DeepSeekFragment[];
  has_pending_fragment: boolean;
  auto_continue: boolean;
}

export interface DeepSeekSSEData {
  v?: string | {
    response?: DeepSeekMessage;
  };
  p?: string;
  o?: "APPEND" | "SET" | "BATCH";
  request_message_id?: number | string;
  response_message_id?: number | string;
  updated_at?: number;
  content?: string;
}
