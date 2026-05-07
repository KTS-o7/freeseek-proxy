package errors

import (
	"encoding/json"
	"net/http"
	"strings"
)

type Kind string

const (
	KindAuth       Kind = "authentication_error"
	KindRateLimit  Kind = "rate_limit_error"
	KindNetwork    Kind = "network_error"
	KindCloudflare Kind = "cloudflare_error"
	KindAPI        Kind = "api_error"
	KindServer     Kind = "server_error"
	KindInvalid    Kind = "invalid_request_error"
)

type ProxyError struct {
	Msg        string
	StatusCode int
	kind       Kind
}

func (e *ProxyError) Error() string { return e.Msg }

func Auth(msg string) *ProxyError        { return &ProxyError{Msg: msg, StatusCode: 401, kind: KindAuth} }
func RateLimit(msg string) *ProxyError   { return &ProxyError{Msg: msg, StatusCode: 429, kind: KindRateLimit} }
func Network(msg string) *ProxyError     { return &ProxyError{Msg: msg, StatusCode: 502, kind: KindNetwork} }
func Cloudflare(msg string) *ProxyError  { return &ProxyError{Msg: msg, StatusCode: 503, kind: KindCloudflare} }
func API(msg string, code int) *ProxyError { return &ProxyError{Msg: msg, StatusCode: code, kind: KindAPI} }
func Server(msg string) *ProxyError      { return &ProxyError{Msg: msg, StatusCode: 502, kind: KindServer} }
func Invalid(msg string) *ProxyError     { return &ProxyError{Msg: msg, StatusCode: 400, kind: KindInvalid} }

type openAIError struct {
	Error struct {
		Message string  `json:"message"`
		Type    Kind    `json:"type"`
		Param   *string `json:"param"`
		Code    *string `json:"code"`
	} `json:"error"`
}

func WriteJSON(w http.ResponseWriter, err *ProxyError) {
	body := openAIError{}
	body.Error.Message = err.Msg
	body.Error.Type = err.kind
	b, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(err.StatusCode)
	w.Write(b)
}

func IsCloudflare(statusCode int, contentType, body string) bool {
	if strings.Contains(strings.ToLower(body), "just a moment") {
		return true
	}
	return (statusCode == 403 || statusCode == 503) && strings.Contains(contentType, "text/html")
}
