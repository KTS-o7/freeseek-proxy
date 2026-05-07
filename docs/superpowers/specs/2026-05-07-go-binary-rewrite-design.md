# Design: Single Go Binary Rewrite

**Date:** 2026-05-07  
**Status:** Approved

## Goal

Replace the current two-process stack (TypeScript/Hono proxy + Python/FastAPI backend) with a single self-contained Go binary that can be copied to a VPS and run with no external runtime dependencies.

**Motivation:** The target VPS has ~1.5GB usable RAM and 5GB disk. The current stack requires Node.js (~100MB RAM) and Python (~150MB RAM) running concurrently. A Go binary runs in ~20–50MB RAM, has no runtime dependency, compiles to a ~10–15MB static binary, and is trivial to deploy.

---

## Current Architecture (to be replaced)

```
Client
  │  POST /v1/chat/completions (OpenAI spec)
  ▼
src/index.ts          Hono HTTP server, port 9123 (TypeScript / Node.js)
  │  POST /chat/session
  │  POST /chat/completion         (internal HTTP, localhost)
  ▼
src/backend.py        FastAPI server, port 8081 (Python / uvicorn)
  │  curl-cffi Chrome impersonation
  │  PoW solving via WASM + wasmtime
  ▼
chat.deepseek.com / chatjimmy.ai
```

---

## Target Architecture

```
Client
  │  POST /v1/chat/completions (OpenAI spec)
  ▼
cmd/proxy/main.go     Single Go HTTP server, port 9123
  │  internal packages handle all logic
  ▼
chat.deepseek.com / chatjimmy.ai
  (via utls Chrome-120 TLS fingerprint spoofing)
```

---

## Package Layout

```
freeseek-proxy/
├── cmd/
│   └── proxy/
│       └── main.go             Entry point: config loading, router setup, server start
├── internal/
│   ├── auth/
│   │   └── manager.go          Bearer token loading; optional email/password login
│   ├── cookies/
│   │   └── manager.go          Cookie load/save from env or JSON file; refresh on CF error
│   ├── pow/
│   │   └── solver.go           Native Go SHA-3 PoW solver (replaces dsk/pow.py + WASM)
│   ├── client/
│   │   └── deepseek.go         utls-backed HTTP client with Chrome-120 TLS fingerprint
│   ├── proxy/
│   │   └── sse.go              DeepSeek SSE parsing; OpenAI SSE/JSON response building
│   ├── tools/
│   │   └── compat.go           Tool-call prompt injection + JSON envelope parser
│   └── taalas/
│       └── taalas.go           chatjimmy.ai proxy (plain HTTPS, no auth/PoW)
├── public/
│   └── index.html              Embedded via go:embed (existing file, unchanged)
├── go.mod
├── go.sum
└── .env                        Unchanged — same env vars as before
```

---

## HTTP Routes

All routes are served by the single binary on `$PORT` (default 9123).

| Method | Path | Handler |
|--------|------|---------|
| GET | `/` | Serve embedded `public/index.html` |
| GET | `/health` | `{"status":"ok"}` |
| GET | `/v1/models` | OpenAI-spec model list |
| POST | `/v1/chat/completions` | Route to DeepSeek or Taalas handler |

Auth middleware: if `API_KEY` env var is set, all `/v1/*` requests must have `Authorization: Bearer <API_KEY>`.

---

## Environment Variables

All existing `.env` variables are preserved with identical semantics:

| Variable | Purpose |
|----------|---------|
| `PORT` | Listen port (default: 9123) |
| `API_KEY` | Optional bearer key to protect this proxy |
| `DEEPSEEK_AUTH_TOKEN` | Direct bearer token for DeepSeek API |
| `DEEPSEEK_EMAIL` | Email for login flow |
| `DEEPSEEK_PASSWORD` | Password for login flow |
| `DEEPSEEK_SAVE_LOGIN` | Persist login response to `login.json` |
| `DEEPSEEK_LOGIN_FILE` | Path to login JSON file (default: `login.json`) |
| `DEEPSEEK_COOKIES` | Raw cookie string |
| `DEEPSEEK_COOKIE_FILE` | Path to cookies JSON file |
| `DEEPSEEK_COOKIE_REFRESH_COMMAND` | Shell command to refresh cookies on CF block |

---

## Key Component Designs

### `internal/client` — Chrome TLS Fingerprint Spoofing

Uses `github.com/refraction-networking/utls` to create an `http.Transport` backed by `utls.UClient` with the `utls.HelloChrome_120` preset. This matches the `impersonate="chrome120"` strategy in the Python `curl-cffi` code, spoofing the TLS ClientHello to pass Cloudflare's browser fingerprint check.

All DeepSeek requests (session creation, PoW challenge, chat completion) go through this transport. Taalas requests use a standard `net/http` client (no WAF on chatjimmy.ai).

### `internal/pow` — Native SHA-3 PoW Solver

Replaces the Python `dsk/pow.py` + `dsk/wasm/` WASM binary entirely.

**Algorithm** (reverse-engineered from the WASM code):
1. Receive challenge config: `{algorithm, challenge, salt, difficulty, expire_at, signature, target_path}`
2. Construct prefix: `salt + "_" + expire_at + "_"`
3. Iterate `n = 0, 1, 2, ...`
4. Compute `sha3_256(challenge + prefix + strconv.Itoa(n))`
5. Check that the hash has `difficulty` leading zero bits
6. Return `n` when found
7. Encode result as `base64(json({algorithm, challenge, salt, answer: n, signature, target_path}))`

Implementation: ~40 lines using `golang.org/x/crypto/sha3`. No WASM, no subprocess.

### `internal/proxy` — SSE Parsing and OpenAI Response Building

Ports the SSE parsing logic from `src/index.ts`:
- Reads DeepSeek's internal SSE wire format (`data: {"v": ..., "p": ..., "o": ...}`)
- Extracts text content, skipping `response/status` and `response/accumulated_token_usage` paths
- Optionally includes/excludes thinking fragments based on `thinking_enabled`
- Tracks `response_message_id` for the `x-parent-message-id` header
- Rebuilds as OpenAI-spec streaming chunks (`chat.completion.chunk`) or a full `chat.completion` object
- Token usage is estimated at `len(text)/4` (same heuristic as the TypeScript code)

### `internal/tools` — Tool-Call Compatibility Layer

Ports the tool-call emulation from `src/tool-call-compat.ts`:
- Detects when tool-call compatibility mode is needed (tools present, tool_choice set, or message history contains tool results)
- Builds a system prompt that instructs the model to emit `{"opencode_tool_call": {...}}` or `{"opencode_tool_calls": [...]}` JSON
- Serializes the full conversation history as a flat text prompt for DeepSeek
- Parses the JSON tool envelope from the model's response
- Reconstructs OpenAI-spec `tool_calls` objects with UUIDs
- Validates against `tool_choice` constraints (required, function-specific, none)

### `internal/auth` — Authentication Manager

- Loads bearer token from `DEEPSEEK_AUTH_TOKEN` env var
- Falls back to `DEEPSEEK_LOGIN_FILE` JSON
- Falls back to email/password login via `POST https://coder.deepseek.com/api/v0/users/login`
- Optionally persists the login response to `DEEPSEEK_SAVE_LOGIN` / `DEEPSEEK_LOGIN_FILE`

### `internal/cookies` — Cookie Manager

- Loads cookies from `DEEPSEEK_COOKIES` env var (raw string) or `DEEPSEEK_COOKIE_FILE` (JSON)
- Updates cookies from `Set-Cookie` response headers after each request
- On `CloudflareError`: runs `DEEPSEEK_COOKIE_REFRESH_COMMAND` via shell, reloads, retries once

### `internal/taalas` — Taalas Proxy

Ports `src/backend.py` `/taalas/completion` and `src/index.ts` `handleTaalasCompletion`:
- POSTs to `https://chatjimmy.ai/api/chat` using standard `net/http` (Chrome UA headers)
- Strips `<|stats|>...<|/stats|>` from the response
- Emits OpenAI-spec streaming or non-streaming response

---

## Models

| OpenAI model ID | DeepSeek `model_type` |
|-----------------|-----------------------|
| `deepseek-v3` | `default` |
| `deepseek-r1` | `expert` |
| `deepseek-chat` | `default` |
| `deepseek-coder` | `default` |
| `deepseek-reasoner` | `expert` |
| `taalas-llama3.1-8b` | routed to Taalas |

---

## Error Handling

All errors are returned as OpenAI-spec JSON:
```json
{"error": {"message": "...", "type": "...", "param": null, "code": null}}
```

Error type mapping:
- HTTP 401 → `authentication_error`
- HTTP 429 → `rate_limit_error`
- Cloudflare block (403/503 with HTML body) → `server_error` with descriptive message
- Network failure → `server_error`
- Invalid request → `invalid_request_error`

---

## Build and Deployment

```bash
# Build
go build -o freeseek-proxy ./cmd/proxy

# Cross-compile for Linux VPS (from macOS)
GOOS=linux GOARCH=amd64 go build -o freeseek-proxy-linux ./cmd/proxy

# Run on VPS
cp freeseek-proxy-linux /path/on/vps/freeseek-proxy
cp .env /path/on/vps/.env
./freeseek-proxy
```

The `public/index.html` is embedded in the binary via `go:embed` — no separate file needed.
The `dsk/` directory is no longer needed after the Go rewrite.

---

## Go Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/refraction-networking/utls` | Chrome TLS fingerprint spoofing |
| `golang.org/x/crypto` | SHA-3 for PoW solver |
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/google/uuid` | UUID generation for chat IDs and tool call IDs |
| `github.com/joho/godotenv` | `.env` file loading |

---

## What Is Not Changing

- `public/index.html` — embedded as-is
- `.env` format and all env var names
- `opencode.json` — points to `localhost:9123/v1`, unchanged
- OpenAI-compatible API surface (`/v1/chat/completions`, `/v1/models`, etc.)
- Custom response headers (`x-chat-session-id`, `x-parent-message-id`)
- Tool-call emulation behavior (same prompts, same JSON envelope format)
- Model aliases and routing

---

## What Is Being Removed

- `src/index.ts` and all TypeScript source files
- `src/backend.py`, `src/backend_auth.py`, `src/backend_cookies.py`, `src/backend_errors.py`
- `dsk/pow.py` and `dsk/wasm/sha3_wasm_bg.7b9ca65ddd.wasm`
- `package.json`, `tsconfig.json`, `bun.lock`
- Python and Node.js runtime requirements
- `start.sh` (replaced by just running the binary)
