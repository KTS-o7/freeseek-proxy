# DeepSeek Feature Parity Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add the missing DeepSeek features to the proxy: email/password auth, saved login state, thinking/search/threading, Cloudflare cookie refresh, typed errors, no-timeout streaming, and sync/async client APIs.

**Architecture:** Keep the OpenAI-compatible HTTP proxy in `src/index.ts` and make `src/backend.py` the DeepSeek-specific integration layer. Add reusable Python auth/cookie/error helpers behind the backend and expose a small TypeScript client surface for direct programmatic use.

**Tech Stack:** TypeScript, Hono, FastAPI, curl-cffi, DrissionPage helpers, existing `dsk/pow.py`, Node fetch streams

---

### Task 1: Auth and Persistence Foundation

**Files:**
- Create: `src/backend_auth.py`
- Modify: `src/backend.py`
- Modify: `.env.example`
- Modify: `README.md`

**Step 1: Add credential loading and persistence**

- Support `DEEPSEEK_EMAIL`, `DEEPSEEK_PASSWORD`, `DEEPSEEK_SAVE_LOGIN`, and `DEEPSEEK_LOGIN_FILE`.
- Read saved login state from `login.json`-style storage when present.

**Step 2: Add email/password login flow**

- Call `POST https://coder.deepseek.com/api/v0/users/login` with:

```json
{
  "email": "<email>",
  "mobile": "",
  "password": "<password>",
  "area_code": ""
}
```

- Persist successful login response when saving is enabled.

**Step 3: Reuse token auth as fallback**

- Prefer explicit `DEEPSEEK_AUTH_TOKEN` when set.
- Otherwise login with email/password and extract bearer token from saved login payload.

**Step 4: Commit**

```bash
git add src/backend_auth.py src/backend.py .env.example README.md
git commit -m "feat: add email login and credential persistence"
```

### Task 2: Thinking, Search, and Threading

**Files:**
- Modify: `src/index.ts`
- Modify: `src/types.ts`
- Modify: `src/deepseek-client.ts`
- Modify: `public/index.html`
- Modify: `README.md`

**Step 1: Extend request types**

- Accept `thinking_enabled`, `search_enabled`, and `parent_message_id` on inbound requests.

**Step 2: Pass new fields through to backend and DeepSeek**

- Remove hardcoded `false` flags.
- Preserve `parent_message_id` when supplied.

**Step 3: Surface threading metadata**

- Capture upstream `response_message_id` from SSE.
- Return it in headers and non-stream JSON metadata so callers can continue threads.

**Step 4: Update built-in UI/docs**

- Add simple toggles/fields for thinking/search and parent message id.

**Step 5: Commit**

```bash
git add src/index.ts src/types.ts src/deepseek-client.ts public/index.html README.md
git commit -m "feat: add thinking search and threaded chat support"
```

### Task 3: Cloudflare Recovery, Typed Errors, Long-Running Requests

**Files:**
- Create: `src/backend_errors.py`
- Create: `src/backend_cookies.py`
- Modify: `src/backend.py`
- Modify: `src/index.ts`
- Modify: `.env.example`
- Modify: `README.md`

**Step 1: Add backend exception types**

- Define `AuthenticationError`, `RateLimitError`, `NetworkError`, `CloudflareError`, and `APIError`.

**Step 2: Add cookie refresh integration**

- Load cookies from env and optional JSON file.
- Detect Cloudflare HTML / challenge responses.
- Reuse or invoke bypass flow to refresh cookies and retry.

**Step 3: Return structured backend errors**

- Include stable JSON like:

```json
{
  "error": {
    "type": "authentication_error",
    "message": "Invalid or expired authentication token",
    "status_code": 401
  }
}
```

**Step 4: Remove hard request timeouts**

- Eliminate the proxy's 60s upstream timeout.
- Do not cap long-running stream requests.

**Step 5: Commit**

```bash
git add src/backend_errors.py src/backend_cookies.py src/backend.py src/index.ts .env.example README.md
git commit -m "feat: add cloudflare recovery typed errors and long-running requests"
```

### Task 4: Sync and Async Client APIs

**Files:**
- Create: `src/client/async-client.ts`
- Create: `src/client/sync-client.ts`
- Create: `src/client/index.ts`
- Modify: `src/types.ts`
- Modify: `package.json`
- Modify: `README.md`

**Step 1: Add reusable async client**

- Support non-streaming completions, streaming completions, session creation, and optional thread continuation.

**Step 2: Add sync-style wrapper**

- Expose a convenience client for non-streaming calls and stream collection over standard fetch primitives.

**Step 3: Export client entrypoints**

- Add package exports/types for client consumers.

**Step 4: Document both usage styles**

- Include small async and sync examples.

**Step 5: Commit**

```bash
git add src/client src/types.ts package.json README.md
git commit -m "feat: add sync and async client APIs"
```

### Task 5: Verification

**Files:**
- Modify: none expected

**Step 1: Run typecheck**

Run: `npm run typecheck`

**Step 2: Run backend syntax check**

Run: `python3 -m py_compile src/backend.py src/backend_auth.py src/backend_errors.py src/backend_cookies.py`

**Step 3: Inspect git state**

Run: `git status --short`
