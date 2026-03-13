import json
import os
import subprocess
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
DEFAULT_COOKIE_FILE = REPO_ROOT / "cookies.json"


def parse_cookie_string(raw_value: str) -> dict[str, str]:
    cookies: dict[str, str] = {}
    for part in raw_value.split(";"):
        part = part.strip()
        if not part or "=" not in part:
            continue
        key, value = part.split("=", 1)
        cookies[key.strip()] = value.strip()
    return cookies


def _normalize_cookie_payload(payload) -> dict[str, str]:
    if isinstance(payload, dict):
        nested_cookies = payload.get("cookies")
        if isinstance(nested_cookies, (dict, list)):
            return _normalize_cookie_payload(nested_cookies)

        return {
            str(key).strip(): str(value).strip()
            for key, value in payload.items()
            if str(key).strip()
        }

    if isinstance(payload, list):
        cookies: dict[str, str] = {}
        for item in payload:
            if not isinstance(item, dict):
                continue
            name = item.get("name")
            value = item.get("value")
            if isinstance(name, str) and name.strip():
                cookies[name.strip()] = "" if value is None else str(value).strip()
        return cookies

    return {}


def _extract_response_cookies(response) -> dict[str, str]:
    cookie_source = getattr(response, "cookies", None)
    if cookie_source is None:
        return {}

    if hasattr(cookie_source, "items"):
        return {
            str(key).strip(): str(value).strip()
            for key, value in cookie_source.items()
            if str(key).strip()
        }

    cookies: dict[str, str] = {}
    for item in cookie_source:
        name = getattr(item, "name", "")
        value = getattr(item, "value", "")
        if name:
            cookies[str(name).strip()] = str(value).strip()
    return cookies


class DeepSeekCookieManager:
    def __init__(self):
        cookie_file = os.getenv("DEEPSEEK_COOKIE_FILE", "").strip()
        self.cookie_file = Path(cookie_file).expanduser() if cookie_file else None
        if self.cookie_file is None and DEFAULT_COOKIE_FILE.exists():
            self.cookie_file = DEFAULT_COOKIE_FILE
        self.refresh_command = os.getenv("DEEPSEEK_COOKIE_REFRESH_COMMAND", "").strip()

    def load_cookies(self) -> dict[str, str]:
        cookies = self._load_file_cookies()
        cookies.update(parse_cookie_string(os.getenv("DEEPSEEK_COOKIES", "")))
        return cookies

    def _load_file_cookies(self) -> dict[str, str]:
        if self.cookie_file is None or not self.cookie_file.exists():
            return {}

        try:
            with self.cookie_file.open("r", encoding="utf-8") as handle:
                payload = json.load(handle)
        except (OSError, json.JSONDecodeError):
            return {}

        return _normalize_cookie_payload(payload)

    def save_cookies(self, cookies: dict[str, str]) -> None:
        if self.cookie_file is None:
            return

        self.cookie_file.parent.mkdir(parents=True, exist_ok=True)
        with self.cookie_file.open("w", encoding="utf-8") as handle:
            json.dump(cookies, handle, indent=2, sort_keys=True)

    def update_from_response(self, response) -> dict[str, str]:
        response_cookies = _extract_response_cookies(response)
        if not response_cookies:
            return self.load_cookies()

        cookies = self.load_cookies()
        cookies.update(response_cookies)
        self.save_cookies(cookies)
        return cookies

    def refresh_cookies(self) -> dict[str, str]:
        if not self.refresh_command:
            return self.load_cookies()

        subprocess.run(self.refresh_command, shell=True, check=True)
        cookies = self.load_cookies()
        self.save_cookies(cookies)
        return cookies
