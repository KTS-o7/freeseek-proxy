import json, base64, wasmtime, numpy as np, sys, os, uuid

# Point to local dsk directory
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))
from dsk.pow import DeepSeekPOW

from dotenv import load_dotenv
from curl_cffi import requests

load_dotenv()

AUTH_TOKEN = os.getenv("DEEPSEEK_AUTH_TOKEN", "")
COOKIES_RAW = os.getenv("DEEPSEEK_COOKIES", "")

if not AUTH_TOKEN:
    print("Set DEEPSEEK_AUTH_TOKEN in .env")
    sys.exit(1)

BASE_URL = "https://chat.deepseek.com/api/v0"

# Parse cookies from the raw string
cookies = {}
for part in COOKIES_RAW.split(";"):
    part = part.strip()
    if "=" in part:
        k, v = part.split("=", 1)
        cookies[k.strip()] = v.strip()

headers = {
    "accept": "*/*",
    "accept-language": "en,fr-FR;q=0.9,fr;q=0.8,es-ES;q=0.7,es;q=0.6,en-US;q=0.5",
    "authorization": f"Bearer {AUTH_TOKEN}",
    "content-type": "application/json",
    "origin": "https://chat.deepseek.com",
    "referer": "https://chat.deepseek.com/",
    "user-agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36",
    "x-app-version": "20241129.1",
    "x-client-locale": "en_US",
    "x-client-platform": "web",
    "x-client-version": "1.0.0-always",
}

# Step 1: Get POW challenge
print("=== Getting POW challenge ===")
pow_resp = requests.post(
    f"{BASE_URL}/chat/create_pow_challenge",
    headers=headers,
    json={"target_path": "/api/v0/chat/completion"},
    cookies=cookies,
    impersonate="chrome120",
)
print(f"POW status: {pow_resp.status_code}")
pow_data = pow_resp.json()
print(f"POW data: {json.dumps(pow_data, indent=2)[:500]}")

challenge_data = pow_data["data"]["biz_data"]["challenge"]
print(f"\nChallenge: {challenge_data['challenge'][:32]}...")
print(f"Salt: {challenge_data['salt']}")
print(f"Difficulty: {challenge_data['difficulty']}")

# Step 2: Solve POW
print("\n=== Solving POW ===")
solver = DeepSeekPOW()
pow_header = solver.solve_challenge(challenge_data)
print(f"POW header (first 100 chars): {pow_header[:100]}...")

# Step 2.5: Create a valid chat session
print("\n=== Creating chat session ===")
session_resp = requests.post(
    f"{BASE_URL}/chat_session/create",
    headers=headers,
    json={"character_id": None},
    cookies=cookies,
    impersonate="chrome120",
)
print(f"Session status: {session_resp.status_code}")
session_data = session_resp.json()
print(f"Session data: {json.dumps(session_data, indent=2)[:500]}")
session_id = session_data["data"]["biz_data"]["id"]
print(f"Session ID: {session_id}")

# Step 3: Send completion request
print("\n=== Sending completion ===")
completion_headers = {**headers, "x-ds-pow-response": pow_header}

completion_resp = requests.post(
    f"{BASE_URL}/chat/completion",
    headers=completion_headers,
    json={
        "chat_session_id": session_id,
        "parent_message_id": None,
        "prompt": "Say hello in 3 words",
        "ref_file_ids": [],
        "thinking_enabled": False,
        "search_enabled": False,
        "preempt": False,
    },
    cookies=cookies,
    impersonate="chrome120",
    stream=True,
)

print(f"Completion status: {completion_resp.status_code}")

if completion_resp.status_code != 200:
    print(f"Error: {completion_resp.text[:500]}")
    sys.exit(1)

print("\n=== Raw SSE Response ===")
raw_lines = []
for chunk in completion_resp.iter_lines():
    if not chunk:
        continue
    line = chunk.decode("utf-8", errors="replace")
    raw_lines.append(line)
    print(line)

print(f"\n=== END RAW ({len(raw_lines)} lines) ===")

print("\n=== Parsing response ===")
full_text = ""
for line in raw_lines:
    if not line.startswith("data: "):
        continue
    data_str = line[6:]
    if data_str == "[DONE]":
        break
    try:
        data = json.loads(data_str)
        # Initial message with fragments
        if data.get("v", {}).get("response", {}).get("fragments"):
            frag = data["v"]["response"]["fragments"][0]
            text = frag.get("content", "")
            if text:
                full_text += text
                print(text, end="", flush=True)
        # Streaming appends
        elif isinstance(data.get("v"), str):
            full_text += data["v"]
            print(data["v"], end="", flush=True)
        # APPEND operation
        elif data.get("o") == "APPEND" and isinstance(data.get("v"), str):
            full_text += data["v"]
            print(data["v"], end="", flush=True)
    except:
        pass

print(f"\n\n=== Full text: {full_text} ===")
