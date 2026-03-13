from fastapi.responses import JSONResponse


class DeepSeekError(Exception):
    error_type = "deepseek_error"
    default_status_code = 500

    def __init__(self, message: str, status_code: int = None):
        super().__init__(message)
        self.message = message
        self.status_code = status_code or self.default_status_code

    def to_dict(self) -> dict:
        return {
            "error": {
                "type": self.error_type,
                "message": self.message,
                "status_code": self.status_code,
            }
        }


class AuthenticationError(DeepSeekError):
    error_type = "authentication_error"
    default_status_code = 401


class RateLimitError(DeepSeekError):
    error_type = "rate_limit_error"
    default_status_code = 429


class NetworkError(DeepSeekError):
    error_type = "network_error"
    default_status_code = 502


class CloudflareError(DeepSeekError):
    error_type = "cloudflare_error"
    default_status_code = 503


class APIError(DeepSeekError):
    error_type = "api_error"
    default_status_code = 502


def error_response(error: DeepSeekError) -> JSONResponse:
    return JSONResponse(status_code=error.status_code, content=error.to_dict())
