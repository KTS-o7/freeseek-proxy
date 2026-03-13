import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from curl_cffi import requests
from dotenv import load_dotenv


load_dotenv()


LOGIN_URL = "https://coder.deepseek.com/api/v0/users/login"
DEFAULT_LOGIN_FILE = Path(__file__).resolve().parent.parent / "login.json"


def _env_flag(name: str, default: bool = False) -> bool:
    value = os.getenv(name)
    if value is None:
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


@dataclass(frozen=True)
class AuthSettings:
    auth_token: str
    email: str
    password: str
    save_login: bool
    login_file: Path


def load_auth_settings() -> AuthSettings:
    login_file = os.getenv("DEEPSEEK_LOGIN_FILE")
    return AuthSettings(
        auth_token=os.getenv("DEEPSEEK_AUTH_TOKEN", "").strip(),
        email=os.getenv("DEEPSEEK_EMAIL", "").strip(),
        password=os.getenv("DEEPSEEK_PASSWORD", ""),
        save_login=_env_flag("DEEPSEEK_SAVE_LOGIN", default=False),
        login_file=Path(login_file).expanduser() if login_file else DEFAULT_LOGIN_FILE,
    )


def read_saved_login(login_file: Path):
    try:
        with login_file.open("r", encoding="utf-8") as handle:
            return json.load(handle)
    except FileNotFoundError:
        return None
    except json.JSONDecodeError:
        return None


def extract_bearer_token(login_payload) -> str:
    if not isinstance(login_payload, dict):
        return ""

    data = login_payload.get("data")
    if not isinstance(data, dict):
        return ""

    user = data.get("user")
    if not isinstance(user, dict):
        return ""

    token = user.get("token")
    return token.strip() if isinstance(token, str) else ""


def login_with_password(settings: AuthSettings):
    if not settings.email or not settings.password:
        raise RuntimeError(
            "DeepSeek login requires DEEPSEEK_EMAIL and DEEPSEEK_PASSWORD"
        )

    response = requests.post(
        LOGIN_URL,
        json={
            "email": settings.email,
            "mobile": "",
            "password": settings.password,
            "area_code": "",
        },
        impersonate="chrome120",
    )

    if response.status_code != 200:
        raise RuntimeError(f"DeepSeek login failed: {response.status_code} {response.text[:200]}")

    payload = response.json()

    if settings.save_login:
        settings.login_file.parent.mkdir(parents=True, exist_ok=True)
        with settings.login_file.open("w", encoding="utf-8") as handle:
            json.dump(payload, handle, indent=2)

    return payload


class DeepSeekAuthManager:
    def __init__(self, settings: Optional[AuthSettings] = None):
        self.settings = settings or load_auth_settings()
        self._token = ""

    def get_bearer_token(self) -> str:
        if self.settings.auth_token:
            return self.settings.auth_token

        if self._token:
            return self._token

        saved_login = read_saved_login(self.settings.login_file)
        saved_token = extract_bearer_token(saved_login)
        if saved_token:
            self._token = saved_token
            return saved_token

        login_payload = login_with_password(self.settings)
        token = extract_bearer_token(login_payload)
        if not token:
            raise RuntimeError("DeepSeek login succeeded but no bearer token was returned")

        self._token = token
        return token
