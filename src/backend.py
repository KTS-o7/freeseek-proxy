"""
Python backend for DeepSeek API communication.
Handles POW solving, session management, and SSE streaming.
Start this first: python3 src/backend.py
"""

import json
import os
import subprocess
import sys

from curl_cffi import requests
from dotenv import load_dotenv
from fastapi import FastAPI, Request
from fastapi.responses import StreamingResponse
import uvicorn

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))

from dsk.pow import DeepSeekPOW
from src.backend_auth import DeepSeekAuthManager
from src.backend_cookies import DeepSeekCookieManager
from src.backend_errors import (
    APIError,
    AuthenticationError,
    CloudflareError,
    DeepSeekError,
    NetworkError,
    RateLimitError,
    error_response,
)


load_dotenv()


BASE_URL = "https://chat.deepseek.com/api/v0"
POW_TARGET_PATH = "/api/v0/chat/completion"

app = FastAPI()
pow_solver = DeepSeekPOW()
auth_manager = DeepSeekAuthManager()
cookie_manager = DeepSeekCookieManager()


def build_headers() -> dict[str, str]:
    return {
        "accept": "*/*",
        "accept-language": "en,fr-FR;q=0.9,fr;q=0.8,es-ES;q=0.7,es;q=0.6,en-US;q=0.5",
        "authorization": f"Bearer {auth_manager.get_bearer_token()}",
        "content-type": "application/json",
        "origin": "https://chat.deepseek.com",
        "referer": "https://chat.deepseek.com/",
        "user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
        "x-app-version": "2.0.0",
        "x-client-locale": "en_US",
        "x-client-platform": "web",
        "x-client-version": "2.0.0",
    }


def _response_text(response) -> str:
    try:
        return response.text or ""
    except Exception:
        return ""


def is_cloudflare_response(response) -> bool:
    body = _response_text(response)
    content_type = (response.headers.get("content-type") or "").lower()
    if "just a moment" in body.lower():
        return True
    return response.status_code in {403, 503} and "text/html" in content_type


def raise_for_deepseek_response(response) -> None:
    if is_cloudflare_response(response):
        raise CloudflareError("DeepSeek request was blocked by Cloudflare", status_code=response.status_code)

    if response.status_code == 401:
        raise AuthenticationError("Invalid or expired DeepSeek authentication", status_code=401)

    if response.status_code == 429:
        raise RateLimitError("DeepSeek rate limit reached", status_code=429)

    if response.status_code >= 500:
        raise APIError("DeepSeek upstream API error", status_code=response.status_code)

    if response.status_code >= 400:
        message = _response_text(response)[:500] or "DeepSeek request failed"
        raise APIError(message, status_code=response.status_code)


def _post_to_deepseek(path: str, headers: dict[str, str], body, cookies: dict[str, str], stream: bool = False):
    try:
        response = requests.post(  # type: ignore[call-arg]
            f"{BASE_URL}{path}",
            headers=headers,
            json=body,
            cookies=cookies,
            impersonate="chrome120",
            stream=stream,
        )
    except Exception as exc:
        raise NetworkError(f"DeepSeek network request failed: {exc}") from exc

    cookie_manager.update_from_response(response)
    raise_for_deepseek_response(response)
    return response


def deepseek_post(path: str, headers: dict[str, str], body, stream: bool = False):
    cookies = cookie_manager.load_cookies()
    try:
        return _post_to_deepseek(path, headers, body, cookies, stream=stream)
    except CloudflareError:
        try:
            refreshed_cookies = cookie_manager.refresh_cookies()
        except subprocess.CalledProcessError as exc:  # type: ignore[name-defined]
            raise CloudflareError(f"Cookie refresh command failed: {exc}") from exc
        except Exception as exc:
            raise CloudflareError(f"Cookie refresh failed: {exc}") from exc

        response = _post_to_deepseek(path, headers, body, refreshed_cookies, stream=stream)
        return response


def get_pow_header(headers: dict[str, str]) -> str:
    response = deepseek_post(
        "/chat/create_pow_challenge",
        headers,
        {"target_path": POW_TARGET_PATH},
    )
    try:
        challenge = response.json()["data"]["biz_data"]["challenge"]
    except Exception as exc:
        raise APIError("DeepSeek POW challenge response was invalid") from exc
    return pow_solver.solve_challenge(challenge)


MODEL_TYPE_MAP = {
    "deepseek-reasoner": "expert",
    "deepseek-chat": "default",
    "deepseek-coder": "default",
}


def resolve_model_type(model: str) -> str:
    """Map an OpenAI-style model name to DeepSeek's model_type field."""
    return MODEL_TYPE_MAP.get(model, "default")


def create_completion_response(body: dict, stream: bool):
    headers = build_headers()
    pow_header = get_pow_header(headers)
    request_headers = {**headers, "x-ds-pow-response": pow_header}

    # Derive model_type from the OpenAI-style model name, then strip the
    # model field so it is never forwarded to DeepSeek's API.
    model_type = resolve_model_type(body.get("model", ""))
    ds_body = {k: v for k, v in body.items() if k != "model"}
    if "model_type" not in ds_body:
        ds_body["model_type"] = model_type

    return deepseek_post("/chat/completion", request_headers, ds_body, stream=stream)


def parse_stream_chunk(chunk: bytes):
    line = chunk.decode("utf-8", errors="replace")
    if not line.startswith("data: "):
        return line, None

    data_str = line[6:]
    if data_str == "[DONE]":
        return line, "[DONE]"

    try:
        return line, json.loads(data_str)
    except Exception:
        return line, None


def extract_stream_text(data) -> str:
    if not isinstance(data, dict):
        return ""

    value = data.get("v")
    if isinstance(value, str):
        if data.get("p") in {"response/status", "response/accumulated_token_usage"}:
            return ""
        return value

    fragments = value.get("response", {}).get("fragments") if isinstance(value, dict) else None
    if not isinstance(fragments, list):
        return ""

    return "".join(
        fragment.get("content", "")
        for fragment in fragments
        if isinstance(fragment, dict) and isinstance(fragment.get("content"), str)
    )


@app.exception_handler(DeepSeekError)
async def handle_deepseek_error(_: Request, exc: DeepSeekError):
    return error_response(exc)


@app.exception_handler(Exception)
async def handle_unexpected_error(_: Request, exc: Exception):
    return error_response(APIError(f"Unexpected backend error: {exc}", status_code=500))


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.post("/chat/session")
async def create_session():
    response = deepseek_post(
        "/chat_session/create",
        build_headers(),
        {"character_id": None},
    )
    try:
        session_id = response.json()["data"]["biz_data"]["id"]
    except Exception as exc:
        raise APIError("DeepSeek session response was invalid") from exc
    return {"session_id": session_id}


@app.post("/chat/completion")
async def chat_completion(request: Request):
    body = await request.json()
    response = create_completion_response(body, stream=True)

    def generate():
        for chunk in response.iter_lines():
            if not chunk:
                continue

            line, data = parse_stream_chunk(chunk)
            if isinstance(data, dict) and data.get("p") in {
                "response/status",
                "response/accumulated_token_usage",
            }:
                continue

            yield line.encode("utf-8") + b"\n"

    return StreamingResponse(generate(), media_type="text/plain")


@app.post("/chat/completion/sync")
async def chat_completion_sync(request: Request):
    body = await request.json()
    response = create_completion_response(body, stream=True)

    full_text = ""
    for chunk in response.iter_lines():
        if not chunk:
            continue

        _, data = parse_stream_chunk(chunk)
        if data == "[DONE]":
            break

        full_text += extract_stream_text(data)

    return {"text": full_text}


# ---------------------------------------------------------------------------
# Taalas (chatjimmy.ai) provider
# No auth, no PoW, no cookies — plain HTTP proxy.
# ---------------------------------------------------------------------------

TAALAS_BASE_URL = "https://chatjimmy.ai/api"
TAALAS_STATS_DELIMITER_START = "<|stats|>"
TAALAS_STATS_DELIMITER_END = "<|/stats|>"


def _build_taalas_headers() -> dict[str, str]:
    return {
        "accept": "*/*",
        "accept-language": "en-US,en;q=0.9",
        "content-type": "application/json",
        "origin": "https://chatjimmy.ai",
        "referer": "https://chatjimmy.ai/",
        "user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
    }


def _parse_taalas_response(raw: str) -> tuple[str, dict]:
    """Split the Taalas response into (text, stats_dict).

    The wire format is: <text><|stats|>{...json...}<|/stats|>
    """
    stats_start = raw.find(TAALAS_STATS_DELIMITER_START)
    if stats_start == -1:
        return raw.strip(), {}

    text = raw[:stats_start].strip()
    stats_raw = raw[stats_start + len(TAALAS_STATS_DELIMITER_START):]
    stats_end = stats_raw.find(TAALAS_STATS_DELIMITER_END)
    stats_str = stats_raw[:stats_end] if stats_end != -1 else stats_raw
    try:
        stats = json.loads(stats_str)
    except Exception:
        stats = {}
    return text, stats


@app.post("/taalas/completion")
async def taalas_completion(request: Request):
    """Proxy a chat request to chatjimmy.ai and return the text as a plain SSE stream."""
    body = await request.json()
    messages: list[dict] = body.get("messages", [])
    model: str = body.get("model", "llama3.1-8B")
    system_prompt: str = body.get("system_prompt", "")
    top_k: int = body.get("topK", 8)

    # chatjimmy.ai only takes the last user message as the prompt;
    # for multi-turn, concatenate all messages into the prompt field.
    if len(messages) == 1:
        prompt = messages[0].get("content", "")
    else:
        prompt = "\n".join(
            f"{m.get('role', 'user').capitalize()}: {m.get('content', '')}"
            for m in messages
        )

    payload = {
        "messages": [{"role": "user", "content": prompt}],
        "chatOptions": {
            "selectedModel": model,
            "systemPrompt": system_prompt,
            "topK": top_k,
        },
        "attachment": None,
    }

    try:
        response = requests.post(  # type: ignore[call-arg]
            f"{TAALAS_BASE_URL}/chat",
            headers=_build_taalas_headers(),
            json=payload,
            impersonate="chrome120",
        )
    except Exception as exc:
        raise NetworkError(f"Taalas network request failed: {exc}") from exc

    if response.status_code != 200:
        raise APIError(
            f"Taalas API returned HTTP {response.status_code}",
            status_code=response.status_code,
        )

    text, _stats = _parse_taalas_response(response.text)

    # Emit as a single SSE data line so the TypeScript layer can process it
    # identically to DeepSeek's streaming format.
    def generate():
        chunk = json.dumps({"content": text})
        yield f"data: {chunk}\n\n".encode("utf-8")
        yield b"data: [DONE]\n\n"

    return StreamingResponse(generate(), media_type="text/plain")


if __name__ == "__main__":
    print("DeepSeek + Taalas backend running on http://localhost:8081")
    uvicorn.run(app, host="127.0.0.1", port=8081)
