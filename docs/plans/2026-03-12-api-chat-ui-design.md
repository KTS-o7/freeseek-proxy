# API Chat UI Design

## Overview

Add a minimal single-page chat UI to the existing Hono proxy so the API can be used directly from the browser. The page should feel quiet and focused, following the provided reference with a centered prompt-first layout, soft neutral tones, and minimal chrome.

The UI will use Oat as the base component library and rely on a small amount of custom CSS for layout, spacing, and typography refinement. It will be served by the same backend process to keep setup simple.

## Goals

- Serve a lightweight HTML interface from the current app.
- Keep the interface visually minimal and centered.
- Support streaming assistant responses from `/v1/chat/completions`.
- Work on desktop and mobile without adding a frontend build step.

## Non-Goals

- No authentication/settings panel.
- No model picker or advanced request controls.
- No multi-page app or client-side routing.
- No persisted chat history.

## Architecture

The server will expose a new `/` route that returns a static HTML document. That document will load Oat from CDN, define a small app shell, and include inline client-side JavaScript for message rendering and streaming.

The browser will send requests to the existing `/v1/chat/completions` endpoint using `stream: true`. The client will consume the SSE response with `ReadableStream`, parse `data:` lines, append content chunks into the active assistant message, and finish cleanly on `[DONE]`.

## UI Structure

The page will have three core regions:

1. A small centered heading and short supporting label.
2. A transcript area that stays hidden or visually quiet until messages exist.
3. A composer card with a textarea and submit button.

The composition should resemble the supplied reference: large calm heading, roomy spacing, subtle borders, rounded surfaces, and restrained contrast.

## Interaction Flow

1. User enters a prompt and submits.
2. The prompt is appended to the transcript immediately.
3. An empty assistant message row is created.
4. The client sends a streaming request.
5. Incoming chunks are appended into the assistant message in real time.
6. The composer is re-enabled when the stream completes or fails.

Enter submits the form, while Shift+Enter inserts a newline.

## States and Error Handling

- Disable the submit button and textarea while a request is active.
- Show a lightweight status indicator such as `Streaming...` while the response is in progress.
- If the request fails, render a compact inline error message in the transcript.
- If the transcript is empty, keep the page focused on the prompt-first layout.

## Styling Direction

- Use a soft off-white page background.
- Use a serif-forward heading to match the reference tone.
- Keep custom styling minimal and mostly structural.
- Let Oat provide default semantic styling for form controls and buttons.
- Avoid heavy shadows, gradients, or loud accent colors.

## Testing

- Verify `/` serves the UI successfully.
- Verify a prompt streams content into the assistant bubble.
- Verify failure responses surface as inline errors.
- Verify the layout remains usable on narrow screens.

## Implementation Notes

Keep everything dependency-light:

- No frontend framework.
- No bundler.
- One HTML document is enough for the first version.
- Structure the inline script so streaming logic can be extracted later if the UI grows.
