# DeepSeek Proxy

An OpenAI-compatible API proxy for DeepSeek, enabling use with any OpenAI-compatible client including opencode, Claude Code, Cursor, and other AI coding tools.

## Features

- OpenAI-compatible `/v1/chat/completions` endpoint
- Streaming and non-streaming responses
- Multiple DeepSeek models: `deepseek-chat`, `deepseek-coder`, `deepseek-reasoner`
- Optional API key authentication
- Built-in chat UI at `/`
- Automatic session management
- Optional request controls for thinking, search, and parent message threading
- No enforced proxy timeout for long-running conversations
- Optional automatic cookie refresh via a cookie file and refresh command

## Quick Start

Recommended environment:

- Python `3.11`
- Bun for JavaScript dependency installation and running the proxy

Python `3.14` is not recommended for this project. The backend depends on native/runtime packages such as `wasmtime`, `curl-cffi`, and `numpy`, and the PoW/WASM flow was verified working with Python `3.11`. In local testing, Python `3.14` was not reliable for this stack.

```bash
# Create and activate a Python 3.11 virtual environment
python3.11 -m venv .venv311
source .venv311/bin/activate

# Install Python backend dependencies
pip install -U pip setuptools wheel
pip install fastapi uvicorn curl-cffi python-dotenv wasmtime numpy

# Install Bun if needed
curl -fsSL https://bun.sh/install | bash

# Install JavaScript dependencies
bun install

# Copy environment template
cp .env.example .env

# Configure either:
# - DEEPSEEK_AUTH_TOKEN from your browser session, or
# - DEEPSEEK_EMAIL / DEEPSEEK_PASSWORD to log in directly
#
# Set DEEPSEEK_SAVE_LOGIN=true to save the login to login.json

# Start the Python backend
python src/backend.py

# In another terminal, start the proxy
bun run dev
```

The proxy runs on `http://localhost:9123` by default.

Long-running conversations are not cut off by a proxy-side timeout. The proxy waits for the backend response to finish.

## Setup Notes and Pitfalls

- Use Python `3.11` for the backend. This was verified to work with the WASM-based PoW solver.
- Do not use Python `3.14` for this project unless you have independently verified the full dependency stack on your machine.
- If the backend process is terminated with just `killed` during chat completion, try Python `3.11` first before debugging the application logic. In local testing, the PoW/WASM path worked on `3.11`.
- Prefer `bun install` and `bun run dev` for the JavaScript side.
- Avoid mixing `npm install` and `bun install` in the same checkout. Mixed lockfiles and stale `node_modules` can cause native package issues such as `esbuild` platform mismatches or watch-mode failures.
- If the proxy fails with `esbuild`/`tsx` native module errors, remove `node_modules`, keep the Bun lockfile, reinstall with Bun, and start again.

## Configuration

Edit `.env` or set environment variables:

```bash
# Use either a DeepSeek auth token...
DEEPSEEK_AUTH_TOKEN=

# ...or DeepSeek account credentials
DEEPSEEK_EMAIL=
DEEPSEEK_PASSWORD=

# Optional: Save login credentials/session to disk
DEEPSEEK_SAVE_LOGIN=true
DEEPSEEK_LOGIN_FILE=login.json

# Optional: Browser cookies for WAF/CSRF protection
DEEPSEEK_COOKIES=your_cookies_here

# Optional: Cookie refresh support
DEEPSEEK_COOKIE_FILE=cookies.json
DEEPSEEK_COOKIE_REFRESH_COMMAND=

# Optional: Protect proxy with API key
API_KEY=your_api_key

# Optional: Change port (default: 9123)
PORT=9123
```

Notes:

- Set `DEEPSEEK_AUTH_TOKEN` if you already have a browser token.
- Or set `DEEPSEEK_EMAIL` and `DEEPSEEK_PASSWORD` to let the backend log in for you.
- `DEEPSEEK_SAVE_LOGIN=true` stores the login response in `login.json` for reuse.
- `DEEPSEEK_COOKIES` and the cookie refresh settings are only needed when DeepSeek or Cloudflare requires browser cookies.

## Usage with opencode

Add as a custom provider in your `opencode.json`:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "deepseek": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "DeepSeek",
      "options": {
        "baseURL": "http://localhost:9123/v1"
      },
      "models": {
        "deepseek-chat": {
          "name": "DeepSeek Chat"
        },
        "deepseek-coder": {
          "name": "DeepSeek Coder"
        },
        "deepseek-reasoner": {
          "name": "DeepSeek Reasoner"
        }
      }
    }
  }
}
```

If you set an `API_KEY`, add it to the configuration:

```json
{
  "provider": {
    "deepseek": {
      "options": {
        "baseURL": "http://localhost:9123/v1",
        "apiKey": "your_api_key"
      }
    }
  }
}
```

## Usage with Claude Code

Set the `ANTHROPIC_BASE_URL` environment variable to point to this proxy:

```bash
export ANTHROPIC_BASE_URL=http://localhost:9123/v1
export ANTHROPIC_API_KEY=your_api_key_if_set
```

Or use it with other OpenAI-compatible tools by setting:

```bash
export OPENAI_BASE_URL=http://localhost:9123/v1
export OPENAI_API_KEY=your_api_key_if_set
```

## Running Locally

The project has two processes:

1. `src/backend.py` - Python backend that handles DeepSeek auth, PoW, cookies, and upstream streaming
2. `src/index.ts` - OpenAI-compatible proxy and built-in browser UI

Typical local startup:

```bash
# terminal 1
source .venv311/bin/activate
python src/backend.py

# terminal 2
bun run dev
```

## Programmatic Client Usage

The package also exposes `AsyncDeepSeekClient` and `SyncDeepSeekClient` helpers for direct usage against the proxy. Reuse the returned `sessionId` plus the latest `responseMessageId`/`parentMessageId` to keep a conversation stateful across turns, and set `thinkingEnabled` or `searchEnabled` when you want those DeepSeek features enabled.

**AsyncDeepSeekClient streaming:**

```ts
import { AsyncDeepSeekClient } from "deepseek-proxy/client";

const client = new AsyncDeepSeekClient({
  baseURL: "http://localhost:9123/v1",
  apiKey: process.env.OPENAI_API_KEY,
});

for await (const text of client.chat("Outline a rollout plan.", {
  thinkingEnabled: true,
  searchEnabled: false,
})) {
  process.stdout.write(text);
}

console.log(client.sessionId);
console.log(client.parentMessageId);
```

**SyncDeepSeekClient non-streaming:**

```ts
import { SyncDeepSeekClient } from "deepseek-proxy/client";

const client = new SyncDeepSeekClient({
  baseURL: "http://localhost:9123/v1",
  apiKey: process.env.OPENAI_API_KEY,
});

const result = await client.chat("Summarize the latest deploy status.", {
  thinkingEnabled: false,
  searchEnabled: true,
});

console.log(result.text);
console.log(result.responseMessageId);
```

Both clients keep the latest `sessionId` and `parentMessageId` on the instance, so follow-up calls continue the same conversation unless you call `newChat()`.

## API Endpoints

### GET `/health`

Health check endpoint.

```bash
curl http://localhost:9123/health
# {"status":"ok"}
```

### GET `/v1/models`

List available models.

```bash
curl http://localhost:9123/v1/models
```

Response:
```json
{
  "object": "list",
  "data": [
    {"id": "deepseek-chat", "object": "model", "owned_by": "deepseek"},
    {"id": "deepseek-coder", "object": "model", "owned_by": "deepseek"},
    {"id": "deepseek-reasoner", "object": "model", "owned_by": "deepseek"}
  ]
}
```

### POST `/v1/chat/completions`

Create a chat completion. Supports both streaming and non-streaming.

Optional request fields:

- `thinking_enabled` - boolean toggle to enable DeepSeek thinking mode
- `search_enabled` - boolean toggle to enable DeepSeek search mode
- `parent_message_id` - message ID to continue a thread from a specific parent message; numeric strings are normalized automatically
- `tools` / `tool_choice` - OpenAI-style tool metadata for proxy-side tool-call compatibility

Tool compatibility notes:

- Non-streaming tool calls are emulated through the proxy for `chat.deepseek.com`
- Streaming tool calls are also synthesized by the proxy after the upstream response completes
- This is compatibility behavior for coding agents like OpenCode; it is not native tool calling from the DeepSeek web endpoint

Useful response metadata:

- `x-chat-session-id` header - current DeepSeek session id
- `x-parent-message-id` header - latest response message id for threaded follow-ups
- `response_message_id` - included in non-streaming JSON responses and streamed chunks when available

**Non-streaming:**

```bash
curl -X POST http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}],
    "thinking_enabled": false,
    "search_enabled": false,
    "parent_message_id": "optional-parent-message-id"
  }'
```

**Streaming:**

```bash
curl -X POST http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true,
    "thinking_enabled": true,
    "search_enabled": false
  }'
```

**With authentication:**

```bash
curl -X POST http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your_api_key" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}],
    "thinking_enabled": false,
    "search_enabled": true
  }'
```

### GET `/`

Built-in chat UI for testing the API directly in your browser. The page includes simple controls for `thinking_enabled`, `search_enabled`, and an optional `parent_message_id`, and it keeps the latest session and parent message IDs for follow-up turns.

## Authentication Modes

- `DEEPSEEK_AUTH_TOKEN` - use an existing browser token directly
- `DEEPSEEK_EMAIL` + `DEEPSEEK_PASSWORD` - log in through the backend and optionally persist the login response
- `API_KEY` - optional auth layer for clients calling your local proxy

## Available Models

| Model ID | Description |
|----------|-------------|
| `deepseek-chat` | General-purpose chat model |
| `deepseek-coder` | Code-focused model |
| `deepseek-reasoner` | Reasoning model |

## Getting Your DeepSeek Auth Token

1. Open [chat.deepseek.com](https://chat.deepseek.com) in your browser
2. Sign in to your account
3. Open Developer Tools (F12) → Network tab
4. Send a message in the chat
5. Find a request to `/api/v0/chat/completion`
6. Copy the `Authorization` header value (without "Bearer ")
7. Also copy cookies if needed for WAF protection

If your cookies are short-lived, you can optionally manage them outside the proxy with:

- `DEEPSEEK_COOKIE_FILE` pointing to a JSON cookie file such as `cookies.json`
- `DEEPSEEK_COOKIE_REFRESH_COMMAND` set to a command that refreshes that file automatically

This gives you an automatic cookie refresh path without changing client requests.

## Development

```bash
# Development mode with hot reload
bun run dev

# Python backend
source .venv311/bin/activate
python src/backend.py

# Production mode
bun run start

# Type checking
bun run typecheck
```

## Architecture

The proxy translates OpenAI API requests to DeepSeek's internal API format:

1. Receives OpenAI-format requests at `/v1/chat/completions`
2. Creates or reuses a chat session
3. Translates to DeepSeek's API format
4. Forwards request to DeepSeek
5. Translates response back to OpenAI format
6. Returns to client

The proxy handles:
- SSE stream parsing and reformatting
- Session management
- Token estimation for usage reporting
- Error translation
