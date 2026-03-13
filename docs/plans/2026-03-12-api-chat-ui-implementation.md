# API Chat UI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a minimal Oat-based HTML chat page to the Hono proxy that streams assistant responses from `/v1/chat/completions`.

**Architecture:** Serve a single static HTML file from the existing Hono server at `/` and keep all client behavior in a small inline script. Use the current `/v1/chat/completions` endpoint with `stream: true`, parse SSE `data:` lines in the browser, and append chunks into the active assistant message.

**Tech Stack:** TypeScript, Hono, `@hono/node-server`, HTML, Oat CDN, browser Fetch API, ReadableStream, Server-Sent Events

---

### Task 1: Serve a static homepage from Hono

**Files:**
- Create: `public/index.html`
- Modify: `src/index.ts`

**Step 1: Add a homepage route in the server**

Use Hono static serving so `/` returns the HTML page while existing API routes stay unchanged.

```ts
import { serveStatic } from "@hono/node-server/serve-static";

app.use("/*", cors());
app.use("/assets/*", serveStatic({ root: "./public" }));
app.get("/", serveStatic({ path: "./public/index.html" }));
```

**Step 2: Verify the server still typechecks**

Run: `npm run typecheck`
Expected: TypeScript finishes without errors.

**Step 3: Start the app and verify the route exists**

Run: `npm run start`
Expected: Server logs the local URL and opening `/` returns HTML instead of JSON.

**Step 4: Commit**

```bash
git add src/index.ts public/index.html
git commit -m "feat: serve chat homepage"
```

### Task 2: Build the minimal prompt-first layout

**Files:**
- Modify: `public/index.html`

**Step 1: Add the page shell markup**

Create a quiet, centered layout with a heading, empty transcript area, status row, and composer form.

```html
<main class="shell">
  <section class="hero">
    <p class="eyebrow">API chat</p>
    <h1>What would you like to do?</h1>
  </section>

  <section id="messages" aria-live="polite" hidden></section>

  <form id="chat-form" class="composer">
    <label for="prompt" class="sr-only">Message</label>
    <textarea id="prompt" name="prompt" rows="3" placeholder="Ask anything..."></textarea>
    <footer class="composer-actions">
      <small id="status">Ready</small>
      <button type="submit">Send</button>
    </footer>
  </form>
</main>
```

**Step 2: Load Oat and add minimal custom styling**

Keep styling mostly structural: soft background, serif heading, subtle border, generous spacing, mobile-safe widths.

```html
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/oatcss/dist/oat.min.css">
<script type="module" src="https://cdn.jsdelivr.net/npm/oatcss/dist/oat.min.js"></script>
<style>
  :root {
    --page-bg: #f5f3ef;
    --card-bg: rgba(255, 255, 255, 0.72);
    --ink: #6f655d;
    --line: rgba(111, 101, 93, 0.14);
  }

  body {
    margin: 0;
    min-height: 100vh;
    background: var(--page-bg);
    color: var(--ink);
    font-family: Georgia, "Times New Roman", serif;
  }
  .shell { max-width: 52rem; margin: 0 auto; padding: 10vh 1rem 2rem; }
  .composer { background: var(--card-bg); border: 1px solid var(--line); border-radius: 1.25rem; }
</style>
```

**Step 3: Manually verify the empty state**

Run: `npm run start`
Expected: `/` shows the centered heading and a single minimal composer that resembles the provided reference.

**Step 4: Commit**

```bash
git add public/index.html
git commit -m "feat: add minimal chat layout"
```

### Task 3: Implement streaming chat behavior

**Files:**
- Modify: `public/index.html`

**Step 1: Add message rendering helpers**

Write small DOM helpers for user, assistant, and error messages so the transcript stays easy to update.

```html
<script>
  const messagesEl = document.querySelector("#messages");

  function appendMessage(role, text) {
    messagesEl.hidden = false;
    const article = document.createElement("article");
    article.dataset.role = role;
    article.innerHTML = `<strong>${role === "user" ? "You" : "Assistant"}</strong><p></p>`;
    article.querySelector("p").textContent = text;
    messagesEl.append(article);
    article.scrollIntoView({ block: "end", behavior: "smooth" });
    return article.querySelector("p");
  }
```

**Step 2: Submit `stream: true` requests**

Send the OpenAI-compatible payload to the existing endpoint and create an empty assistant bubble before reading the response.

```js
const response = await fetch("/v1/chat/completions", {
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify({
    model: "deepseek-chat",
    stream: true,
    messages: [{ role: "user", content: prompt }],
  }),
});
```

**Step 3: Parse the SSE stream incrementally**

Read the response body with a stream reader, split by newline, ignore non-`data:` lines, stop on `[DONE]`, and append `delta.content` to the active assistant paragraph.

```js
const reader = response.body.getReader();
const decoder = new TextDecoder();
let buffer = "";

while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  buffer += decoder.decode(value, { stream: true });
  const lines = buffer.split("\n");
  buffer = lines.pop() ?? "";

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("data: ")) continue;
    const payload = trimmed.slice(6);
    if (payload === "[DONE]") return;
    const chunk = JSON.parse(payload);
    const delta = chunk.choices?.[0]?.delta?.content;
    if (delta) assistantNode.textContent += delta;
  }
}
```

**Step 4: Verify streaming behavior manually**

Run: `npm run start`
Expected: After submit, the assistant bubble appears immediately and fills token-by-token until the stream ends.

**Step 5: Commit**

```bash
git add public/index.html
git commit -m "feat: stream chat responses in browser"
```

### Task 4: Add input locking, errors, and keyboard behavior

**Files:**
- Modify: `public/index.html`

**Step 1: Disable input while a request is active**

Wrap the request in a `try/finally` block and toggle the textarea/button disabled state with a small status label.

```js
function setPending(isPending) {
  promptEl.disabled = isPending;
  submitEl.disabled = isPending;
  statusEl.textContent = isPending ? "Streaming..." : "Ready";
}
```

**Step 2: Add inline error rendering**

Handle non-OK responses and parsing failures by appending a small error message into the transcript instead of crashing silently.

```js
if (!response.ok) {
  throw new Error(`Request failed with status ${response.status}`);
}

catch (error) {
  appendMessage("error", error instanceof Error ? error.message : "Something went wrong.");
}
```

**Step 3: Support Enter to send and Shift+Enter for newline**

Use a `keydown` handler on the textarea so the form feels chat-like without breaking multiline input.

```js
promptEl.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    form.requestSubmit();
  }
});
```

**Step 4: Run final verification**

Run: `npm run typecheck`
Expected: PASS.

Run: `npm run start`
Expected:
- `/` loads successfully.
- Empty state stays minimal before the first message.
- Streaming text appears progressively.
- Errors render inline.
- Mobile width remains usable.

**Step 5: Commit**

```bash
git add public/index.html src/index.ts
git commit -m "feat: polish streaming chat experience"
```

### Task 5: Final review and cleanup

**Files:**
- Review: `public/index.html`
- Review: `src/index.ts`
- Review: `docs/plans/2026-03-12-api-chat-ui-design.md`

**Step 1: Confirm the UI still matches the design intent**

Checklist:
- Calm, off-white background
- Serif-forward heading
- Minimal chrome
- No extra controls beyond the chat input and send action
- Transcript remains visually secondary until used

**Step 2: Re-run the manual smoke test**

Run: `npm run start`
Expected: End-to-end chat flow works from browser input through streamed assistant output.

**Step 3: Prepare a concise change summary**

Document:
- What route was added
- What static assets were created
- How streaming is parsed on the client
- Any follow-up ideas, such as model selection or persisted history

**Step 4: Commit**

```bash
git add public/index.html src/index.ts docs/plans/2026-03-12-api-chat-ui-design.md docs/plans/2026-03-12-api-chat-ui-implementation.md
git commit -m "feat: add minimal streaming chat UI"
```
