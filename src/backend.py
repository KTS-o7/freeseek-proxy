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
        "x-app-version": "20241129.1",
        "x-client-locale": "en_US",
        "x-client-platform": "web",
        "x-client-version": "1.0.0-always",
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


def create_completion_response(body: dict, stream: bool):
    headers = build_headers()
    pow_header = get_pow_header(headers)
    request_headers = {**headers, "x-ds-pow-response": pow_header}
    return deepseek_post("/chat/completion", request_headers, body, stream=stream)


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


if __name__ == "__main__":
    print("DeepSeek backend running on http://localhost:8081")
    uvicorn.run(app, host="127.0.0.1", port=8081)
