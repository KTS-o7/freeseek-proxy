import type {
  DeepSeekClientCompletionResult,
  DeepSeekClientOptions,
  DeepSeekClientRequestOptions,
} from "../types.js";
import { AsyncDeepSeekClient } from "./async-client.js";

export class SyncDeepSeekClient {
  private readonly client: AsyncDeepSeekClient;

  constructor(options: DeepSeekClientOptions = {}) {
    this.client = new AsyncDeepSeekClient(options);
  }

  get sessionId() {
    return this.client.sessionId;
  }

  get parentMessageId() {
    return this.client.parentMessageId;
  }

  newChat() {
    this.client.newChat();
  }

  chat(
    message: string,
    options: DeepSeekClientRequestOptions = {}
  ): Promise<DeepSeekClientCompletionResult> {
    return this.client.complete(message, options);
  }

  complete(
    message: string,
    options: DeepSeekClientRequestOptions = {}
  ): Promise<DeepSeekClientCompletionResult> {
    return this.client.complete(message, options);
  }
}
