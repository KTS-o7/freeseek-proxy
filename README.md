# freeseek-proxy

An OpenAI-compatible API proxy for [DeepSeek](https://chat.deepseek.com) and [Taalas (chatjimmy.ai)](https://chatjimmy.ai), enabling use with any OpenAI-compatible client including opencode, Claude Code, Cursor, LangChain, and more.

> **New in v2:** The entire stack is now a single self-contained Go binary — no Python, no Node.js, no runtime dependencies. Copy it to your VPS and run it.

---

## Features

- **Single Go binary** — ~12MB, ~20-50MB RAM, no runtime needed
- **OpenAI-compatible API** — drop-in replacement for `api.openai.com`
- **DeepSeek models** — `deepseek-chat` and `deepseek-reasoner` (thinking on, returned as `reasoning_content`). chat.deepseek.com picks the model version
- **Taalas (chatjimmy.ai)** — free Llama 3.1 8B model, no auth required
- Streaming and non-streaming responses
- Tool-call compatibility (emulated via prompt injection)
- Cloudflare bypass via Chrome TLS fingerprint spoofing (utls)
- Automatic PoW (Proof-of-Work) challenge solving
- Cookie management with optional auto-refresh
- Optional API key auth to protect your proxy
- Built-in browser chat UI at `/`
- Full conversation context: each request sends the whole message list, including system messages
- Cleans up after itself: chats it opens in your DeepSeek account are deleted after the reply
- Recovers from an expired token by moving to the next token source (saved login, then email + password)

---

## Quick Start

### Option A — Pre-built binary (recommended for VPS)

```bash
# Download or build the binary (see Build section below)
# Copy .env and the WASM file to your server
cp .env.example .env
# Edit .env with your credentials (see Configuration)
./freeseek-proxy
```

### Option B — Build from source

Requirements: Go 1.22+

```bash
git clone https://github.com/KTS-o7/freeseek-proxy
cd freeseek-proxy

# Build for your current OS
go build -o freeseek-proxy ./cmd/proxy/

# Cross-compile for Linux VPS (from macOS/Windows)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o freeseek-proxy-linux ./cmd/proxy/

cp .env.example .env
# Edit .env with your credentials
./freeseek-proxy
```

The proxy starts on `http://localhost:9123` by default.

---

## Deploy to VPS

```bash
# Build Linux binary locally
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o freeseek-proxy-linux ./cmd/proxy/

# Copy binary + env + WASM file to server
scp freeseek-proxy-linux user@your-vps:/opt/freeseek/freeseek-proxy
scp .env user@your-vps:/opt/freeseek/.env
scp -r dsk/ user@your-vps:/opt/freeseek/

# SSH in and run
ssh user@your-vps
cd /opt/freeseek
chmod +x freeseek-proxy
./freeseek-proxy
```

> **Note:** The WASM binary at `dsk/wasm/sha3_wasm_bg.7b9ca65ddd.wasm` must be present alongside the binary. It is used for the PoW solver and is loaded at runtime from the working directory.

To run as a background service:

```bash
nohup ./freeseek-proxy > freeseek.log 2>&1 &
```

Or create a systemd unit:

```ini
[Unit]
Description=freeseek-proxy
After=network.target

[Service]
WorkingDirectory=/opt/freeseek
ExecStart=/opt/freeseek/freeseek-proxy
Restart=on-failure
EnvironmentFile=/opt/freeseek/.env

[Install]
WantedBy=multi-user.target
```

---

## Configuration

Copy `.env.example` to `.env` and fill in the values:

```bash
# DeepSeek bearer token (from your browser session)
# Get it from: DevTools → Network → any /api/v0/chat/completion request → Authorization header
DEEPSEEK_AUTH_TOKEN=

# OR: log in with email/password (proxy fetches the token automatically)
DEEPSEEK_EMAIL=
DEEPSEEK_PASSWORD=
DEEPSEEK_SAVE_LOGIN=true        # save token to login.json for reuse
DEEPSEEK_LOGIN_FILE=login.json

# Browser cookies for Cloudflare/WAF bypass
# Get from: DevTools → Application → Cookies → chat.deepseek.com
# Include at minimum: aws-waf-token and ds_session_id
DEEPSEEK_COOKIES=aws-waf-token=...; ds_session_id=...

# Optional: save/load cookies from a JSON file
DEEPSEEK_COOKIE_FILE=cookies.json

# Optional: shell command to refresh cookies when Cloudflare blocks a request
DEEPSEEK_COOKIE_REFRESH_COMMAND=

# Optional: protect this proxy with a Bearer key
API_KEY=

# Address to listen on (default: 127.0.0.1; put nginx in front for public access)
HOST=127.0.0.1

# Server port (default: 9123)
PORT=9123

# Keep the DeepSeek chats the proxy creates (default: false, they are deleted after each reply)
DEEPSEEK_KEEP_SESSIONS=false
```

### Getting Your DeepSeek Auth Token

1. Open [chat.deepseek.com](https://chat.deepseek.com) and sign in
2. Open DevTools (F12) → Console and run `copy(localStorage.getItem('userToken'))`
3. The clipboard now holds `{"value":"<TOKEN>",...}`. Put `<TOKEN>` in `DEEPSEEK_AUTH_TOKEN`
4. Optional: copy the `aws-waf-token` and `ds_session_id` cookies (Application → Cookies) into `DEEPSEEK_COOKIES`

A token stops working after a while. When DeepSeek rejects it, requests fail with HTTP 503 and a message that says so. Set `DEEPSEEK_EMAIL` and `DEEPSEEK_PASSWORD` if you want the proxy to log in again by itself (this does not work for accounts that sign in with Google).

---

## Available Models

| Model ID | Type | Provider |
|----------|------|----------|
| `deepseek-chat` | DeepSeek, thinking off (turn on with `thinking_enabled`) | DeepSeek |
| `deepseek-reasoner` | DeepSeek with thinking always on | DeepSeek |

chat.deepseek.com decides which model version answers; these names do not pin a version. Its API accepts only one `model_type` ("default"); in November 2026 "expert" and other values all came back as "default". The thinking text comes back in `reasoning_content` (streamed as `delta.reasoning_content`), separate from the answer. The old names still work: `deepseek-v3` and `deepseek-coder` map to `deepseek-chat`; `deepseek-r1` and `deepseek-expert` map to `deepseek-reasoner`.
| `taalas-llama3.1-8b` | Llama 3.1 8B (free, no auth) | chatjimmy.ai |

---

## API Reference

All endpoints are OpenAI-compatible.

### `GET /health`

```bash
curl http://localhost:9123/health
# {"status":"ok"}
```

### `GET /v1/models`

```bash
curl http://localhost:9123/v1/models
```

### `POST /v1/chat/completions`

Standard OpenAI chat completions. Supports all standard fields plus these DeepSeek-specific extensions:

| Field | Type | Description |
|-------|------|-------------|
| `thinking_enabled` | bool | Turn on DeepSeek thinking for `deepseek-chat`. The reasoning comes back in `reasoning_content` |
| `search_enabled` | bool | Enable DeepSeek web search |
| `sessionId` | string | Advanced: continue an existing DeepSeek chat instead of sending the full history. Needs `DEEPSEEK_KEEP_SESSIONS=true` |
| `parent_message_id` | string\|number | With `sessionId`: the message to reply to |
| `tools` | array | OpenAI-spec tool definitions (emulated via prompt injection) |
| `tool_choice` | string\|object | Tool choice mode (`auto`, `none`, `required`, or specific function) |

**Non-streaming:**

```bash
curl http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1748000000,
  "model": "deepseek-chat",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "Hi! How can I help?"},
    "finish_reason": "stop",
    "logprobs": null
  }],
  "usage": {"prompt_tokens": 2, "completion_tokens": 8, "total_tokens": 10}
}
```

**Streaming:**

```bash
curl http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-chat",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

```
data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant"},...}]}
data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hi"},...}]}
data: {"id":"chatcmpl-abc123","object":"chat.completion.chunk","choices":[{"delta":{},"finish_reason":"stop",...}]}
data: [DONE]
```

**Response headers:**

| Header | Description |
|--------|-------------|
| `x-chat-session-id` | Only with `DEEPSEEK_KEEP_SESSIONS=true`: the DeepSeek chat ID, to pass as `sessionId` |
| `x-parent-message-id` | The reply's message ID, to pass as `parent_message_id` with `sessionId` |

### `GET /`

Built-in chat page: model picker, thinking and search toggles, Markdown replies, stop and new chat. The API key is kept in the browser. No build step; the page is one embedded HTML file.

---

## Usage with opencode

The repo includes an `opencode.json` that configures opencode to use this proxy. It works out of the box if the proxy is running on port 9123:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "My-deepseek": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "My-deepseek",
      "options": {
        "baseURL": "http://localhost:9123/v1",
        "apiKey": "sk-local"
      },
      "models": {
        "deepseek-chat": {"name": "DeepSeek (default)"},
        "deepseek-reasoner": {"name": "DeepSeek (thinking)"},
        "taalas-llama3.1-8b": {"name": "Llama 3.1 8B (free)"}
      }
    }
  }
}
```

If you set `API_KEY` in `.env`, update `apiKey` in `opencode.json` to match.

## Usage with other OpenAI-compatible clients

```bash
export OPENAI_BASE_URL=http://localhost:9123/v1
export OPENAI_API_KEY=your_api_key_if_set   # leave blank if API_KEY not set
```

Works with: Claude Code, Cursor, Aider, LangChain, LlamaIndex, Continue, and any tool that supports a custom OpenAI base URL.

---

## Architecture

```
Client (opencode / curl / any OpenAI-compatible tool)
        │
        │  POST /v1/chat/completions  (OpenAI spec)
        ▼
┌─────────────────────────────────────────────────────┐
│  freeseek-proxy  (single Go binary, port 9123)      │
│                                                     │
│  cmd/proxy/          HTTP router (chi)              │
│  internal/config/    env loading                    │
│  internal/auth/      bearer token + login           │
│  internal/cookies/   cookie load/save/refresh       │
│  internal/client/    utls Chrome-120 HTTP client    │
│  internal/pow/       WASM PoW solver                │
│  internal/proxy/     DeepSeek SSE parser            │
│  internal/tools/     tool-call emulation            │
│  internal/taalas/    chatjimmy.ai proxy             │
└──────────┬──────────────────────────────────────────┘
           │  HTTPS (Chrome TLS fingerprint)
           ▼
    chat.deepseek.com/api/v0/   (DeepSeek)
    chatjimmy.ai/api/chat        (Taalas)
```

**Key implementation details:**

- **Cloudflare bypass:** Uses [`utls`](https://github.com/refraction-networking/utls) to spoof the Chrome 120 TLS ClientHello fingerprint, same strategy as the Python `curl-cffi` backend it replaces
- **PoW solver:** Loads DeepSeek's own SHA-3 WASM binary via [wazero](https://github.com/tetratelabs/wazero) — no Python/wasmtime needed
- **Tool calls:** Emulated via prompt injection (DeepSeek's web endpoint doesn't support native tool calling). The model is instructed to emit JSON envelopes which the proxy parses and converts to OpenAI-spec `tool_calls`
- **HTML UI:** Embedded directly in the binary via `go:embed`

---

## Building

```bash
# macOS/Linux
go build -o freeseek-proxy ./cmd/proxy/

# Linux (from any OS)
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o freeseek-proxy-linux ./cmd/proxy/

# Windows
GOOS=windows GOARCH=amd64 go build -o freeseek-proxy.exe ./cmd/proxy/

# Run tests
go test ./...
```

Binary sizes (approx):
- macOS arm64: ~16MB
- Linux amd64 (stripped): ~12MB

Memory usage at runtime: ~20-50MB
