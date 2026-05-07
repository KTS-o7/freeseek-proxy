# freeseek-proxy

An OpenAI-compatible API proxy for [DeepSeek](https://chat.deepseek.com) and [Taalas (chatjimmy.ai)](https://chatjimmy.ai), enabling use with any OpenAI-compatible client including opencode, Claude Code, Cursor, LangChain, and more.

> **New in v2:** The entire stack is now a single self-contained Go binary — no Python, no Node.js, no runtime dependencies. Copy it to your VPS and run it.

---

## Features

- **Single Go binary** — ~12MB, ~20-50MB RAM, no runtime needed
- **OpenAI-compatible API** — drop-in replacement for `api.openai.com`
- **DeepSeek models** — `deepseek-v3`, `deepseek-r1` (and legacy aliases)
- **Taalas (chatjimmy.ai)** — free Llama 3.1 8B model, no auth required
- Streaming and non-streaming responses
- Tool-call compatibility (emulated via prompt injection)
- Cloudflare bypass via Chrome TLS fingerprint spoofing (utls)
- Automatic PoW (Proof-of-Work) challenge solving
- Cookie management with optional auto-refresh
- Optional API key auth to protect your proxy
- Built-in browser chat UI at `/`
- Session threading via `x-chat-session-id` / `x-parent-message-id` headers

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

# Server port (default: 9123)
PORT=9123
```

### Getting Your DeepSeek Auth Token

1. Open [chat.deepseek.com](https://chat.deepseek.com) and sign in
2. Open DevTools (F12) → Network tab
3. Send any message
4. Find the request to `/api/v0/chat/completion`
5. Copy the `Authorization: Bearer <TOKEN>` header value (everything after `Bearer `)
6. Also copy the cookies from the `Cookie` header (especially `aws-waf-token` and `ds_session_id`)

---

## Available Models

| Model ID | Type | Provider |
|----------|------|----------|
| `deepseek-v3` | General chat | DeepSeek |
| `deepseek-r1` | Reasoning | DeepSeek |
| `deepseek-chat` | Alias for deepseek-v3 | DeepSeek |
| `deepseek-coder` | Alias for deepseek-v3 | DeepSeek |
| `deepseek-reasoner` | Alias for deepseek-r1 | DeepSeek |
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
| `thinking_enabled` | bool | Enable DeepSeek extended thinking (R1 model) |
| `search_enabled` | bool | Enable DeepSeek web search |
| `parent_message_id` | string\|number | Continue a threaded conversation |
| `sessionId` | string | Reuse an existing DeepSeek chat session |
| `tools` | array | OpenAI-spec tool definitions (emulated via prompt injection) |
| `tool_choice` | string\|object | Tool choice mode (`auto`, `none`, `required`, or specific function) |

**Non-streaming:**

```bash
curl http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "deepseek-v3",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": false
  }'
```

```json
{
  "id": "chatcmpl-abc123",
  "object": "chat.completion",
  "created": 1748000000,
  "model": "deepseek-v3",
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
    "model": "deepseek-v3",
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
| `x-chat-session-id` | DeepSeek session ID — pass as `sessionId` in next request to continue the conversation |
| `x-parent-message-id` | Latest message ID — pass as `parent_message_id` in next request for threaded replies |

### `GET /`

Built-in browser chat UI for testing. Supports thinking mode, search, and conversation threading.

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
        "deepseek-v3": {"name": "DeepSeek V3"},
        "deepseek-r1": {"name": "DeepSeek R1"},
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

---

## Legacy Stack (TypeScript + Python)

The original two-process stack (`src/index.ts` + `src/backend.py`) is still present in the repository for reference but is no longer the recommended way to run the proxy. The Go binary replaces both processes with equivalent functionality.

If you need the legacy stack:

```bash
# Python 3.11 backend
python3.11 -m venv .venv311
source .venv311/bin/activate
pip install fastapi uvicorn "curl-cffi==0.7.4" python-dotenv wasmtime numpy
python src/backend.py

# TypeScript proxy (separate terminal)
bun install
bun run dev
```
