# Go Binary Rewrite Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the TypeScript + Python two-process stack with a single self-contained Go binary that serves the same OpenAI-compatible API.

**Architecture:** One Go HTTP server (chi router) on port 9123 handles all routes. Chrome TLS fingerprinting via utls replaces curl-cffi. Native Go SHA-3 replaces the WASM PoW solver. The HTML chat UI is embedded via go:embed.

**Tech Stack:** Go 1.22+, github.com/go-chi/chi/v5, github.com/refraction-networking/utls, golang.org/x/crypto/sha3, github.com/google/uuid, github.com/joho/godotenv

---

## File Map

```
cmd/proxy/main.go                  entry point, config, router, server
internal/config/config.go          env var loading (all settings in one place)
internal/errors/errors.go          error types + OpenAI-spec JSON helpers
internal/auth/auth.go              bearer token loading + email/password login
internal/cookies/cookies.go        cookie load/save/refresh
internal/client/client.go          utls Chrome-120 HTTP transport + DeepSeek requests
internal/pow/solver.go             native SHA-3 PoW solver
internal/proxy/sse.go              DeepSeek SSE parser
internal/proxy/response.go         OpenAI response builders (streaming + non-streaming)
internal/tools/compat.go           tool-call prompt injection + envelope parser
internal/taalas/taalas.go          chatjimmy.ai proxy
public/index.html                  unchanged, embedded via go:embed
go.mod / go.sum                    module definition
```

---

## Task 1: Go module + dependencies

**Files:**
- Create: `go.mod`
- Create: `go.sum` (auto-generated)

- [ ] **Step 1: Initialise the Go module**

```bash
cd /Users/kts/Documents/side-projects/freeseek-proxy
go mod init github.com/KTS-o7/freeseek-proxy
```

- [ ] **Step 2: Add dependencies**

```bash
go get github.com/go-chi/chi/v5
go get github.com/refraction-networking/utls
go get golang.org/x/crypto
go get github.com/google/uuid
go get github.com/joho/godotenv
```

- [ ] **Step 3: Verify go.mod lists all five deps**

```bash
grep -E "chi|utls|crypto|uuid|godotenv" go.mod
```
Expected: five matching lines.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: initialise Go module with dependencies"
```

---

## Task 2: internal/config — env loading

**Files:**
- Create: `internal/config/config.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/config
touch internal/config/config.go
```

- [ ] **Step 2: Write config.go**

```go
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Port                 int
	APIKey               string
	AuthToken            string
	Email                string
	Password             string
	SaveLogin            bool
	LoginFile            string
	Cookies              string
	CookieFile           string
	CookieRefreshCommand string
}

func Load() *Config {
	return &Config{
		Port:                 envInt("PORT", 9123),
		APIKey:               os.Getenv("API_KEY"),
		AuthToken:            strings.TrimSpace(os.Getenv("DEEPSEEK_AUTH_TOKEN")),
		Email:                strings.TrimSpace(os.Getenv("DEEPSEEK_EMAIL")),
		Password:             os.Getenv("DEEPSEEK_PASSWORD"),
		SaveLogin:            envBool("DEEPSEEK_SAVE_LOGIN", false),
		LoginFile:            envString("DEEPSEEK_LOGIN_FILE", "login.json"),
		Cookies:              os.Getenv("DEEPSEEK_COOKIES"),
		CookieFile:           os.Getenv("DEEPSEEK_COOKIE_FILE"),
		CookieRefreshCommand: os.Getenv("DEEPSEEK_COOKIE_REFRESH_COMMAND"),
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envString(key, def string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return def
	}
	return filepath.Clean(v)
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/config/...
```
Expected: no output (success).

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add config package for env loading"
```

---

## Task 3: internal/errors — error types

**Files:**
- Create: `internal/errors/errors.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/errors
touch internal/errors/errors.go
```

- [ ] **Step 2: Write errors.go**

```go
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
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/errors/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/errors/errors.go
git commit -m "feat: add errors package with OpenAI-spec JSON helpers"
```

---

## Task 4: internal/cookies — cookie manager

**Files:**
- Create: `internal/cookies/cookies.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/cookies
touch internal/cookies/cookies.go
```

- [ ] **Step 2: Write cookies.go**

```go
package cookies

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KTS-o7/freeseek-proxy/internal/config"
)

type Manager struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Load merges cookies from file and from DEEPSEEK_COOKIES env var.
func (m *Manager) Load() map[string]string {
	cookies := m.loadFile()
	for k, v := range parseString(m.cfg.Cookies) {
		cookies[k] = v
	}
	return cookies
}

// UpdateFromResponse merges Set-Cookie headers into the saved cookie file.
func (m *Manager) UpdateFromResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	extra := map[string]string{}
	for _, c := range resp.Cookies() {
		if c.Name != "" {
			extra[c.Name] = c.Value
		}
	}
	if len(extra) == 0 {
		return
	}
	merged := m.Load()
	for k, v := range extra {
		merged[k] = v
	}
	m.saveFile(merged)
}

// Refresh runs the configured refresh command and reloads cookies.
func (m *Manager) Refresh() (map[string]string, error) {
	if m.cfg.CookieRefreshCommand == "" {
		return m.Load(), nil
	}
	cmd := exec.Command("sh", "-c", m.cfg.CookieRefreshCommand)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return m.Load(), nil
}

func (m *Manager) loadFile() map[string]string {
	path := m.cookieFilePath()
	if path == "" {
		return map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	// Try flat map first, then list of {name,value} objects.
	var flat map[string]string
	if json.Unmarshal(data, &flat) == nil {
		return flat
	}
	var list []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.Unmarshal(data, &list) == nil {
		out := map[string]string{}
		for _, item := range list {
			if item.Name != "" {
				out[item.Name] = item.Value
			}
		}
		return out
	}
	return map[string]string{}
}

func (m *Manager) saveFile(cookies map[string]string) {
	path := m.cookieFilePath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.MarshalIndent(cookies, "", "  ")
	_ = os.WriteFile(path, b, 0o600)
}

func (m *Manager) cookieFilePath() string {
	if m.cfg.CookieFile != "" {
		return m.cfg.CookieFile
	}
	if _, err := os.Stat("cookies.json"); err == nil {
		return "cookies.json"
	}
	return ""
}

// parseString parses "key=value; key2=value2" cookie strings.
func parseString(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		idx := strings.Index(part, "=")
		k := strings.TrimSpace(part[:idx])
		v := strings.TrimSpace(part[idx+1:])
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// Header returns cookies formatted as a Cookie header value.
func Header(cookies map[string]string) string {
	parts := make([]string, 0, len(cookies))
	for k, v := range cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/cookies/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/cookies/cookies.go
git commit -m "feat: add cookies package"
```

---

## Task 5: internal/auth — bearer token manager

**Files:**
- Create: `internal/auth/auth.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/auth
touch internal/auth/auth.go
```

- [ ] **Step 2: Write auth.go**

```go
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/KTS-o7/freeseek-proxy/internal/config"
)

const loginURL = "https://coder.deepseek.com/api/v0/users/login"

type Manager struct {
	cfg   *config.Config
	token string
}

func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Token returns a valid DeepSeek bearer token, loading or fetching as needed.
func (m *Manager) Token() (string, error) {
	if m.cfg.AuthToken != "" {
		return m.cfg.AuthToken, nil
	}
	if m.token != "" {
		return m.token, nil
	}
	// Try saved login file.
	if t := m.tokenFromFile(); t != "" {
		m.token = t
		return t, nil
	}
	// Login with email/password.
	t, payload, err := m.login()
	if err != nil {
		return "", err
	}
	if m.cfg.SaveLogin {
		m.saveLogin(payload)
	}
	m.token = t
	return t, nil
}

func (m *Manager) tokenFromFile() string {
	data, err := os.ReadFile(m.cfg.LoginFile)
	if err != nil {
		return ""
	}
	return extractToken(data)
}

func (m *Manager) login() (string, []byte, error) {
	if m.cfg.Email == "" || m.cfg.Password == "" {
		return "", nil, fmt.Errorf("DeepSeek auth: set DEEPSEEK_AUTH_TOKEN or DEEPSEEK_EMAIL+DEEPSEEK_PASSWORD")
	}
	body, _ := json.Marshal(map[string]string{
		"email":     m.cfg.Email,
		"mobile":    "",
		"password":  m.cfg.Password,
		"area_code": "",
	})
	resp, err := http.Post(loginURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("DeepSeek login request failed: %w", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("DeepSeek login failed: HTTP %d", resp.StatusCode)
	}
	t := extractToken(buf.Bytes())
	if t == "" {
		return "", nil, fmt.Errorf("DeepSeek login: no token in response")
	}
	return t, buf.Bytes(), nil
}

func (m *Manager) saveLogin(payload []byte) {
	_ = os.MkdirAll(filepath.Dir(m.cfg.LoginFile), 0o755)
	_ = os.WriteFile(m.cfg.LoginFile, payload, 0o600)
}

func extractToken(data []byte) string {
	var v struct {
		Data struct {
			User struct {
				Token string `json:"token"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &v) == nil {
		return v.Data.User.Token
	}
	return ""
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/auth/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/auth/auth.go
git commit -m "feat: add auth package for DeepSeek bearer token"
```

---

## Task 6: internal/pow — native SHA-3 PoW solver

**Files:**
- Create: `internal/pow/solver.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/pow
touch internal/pow/solver.go
```

- [ ] **Step 2: Write solver.go**

```go
package pow

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"golang.org/x/crypto/sha3"
)

// Challenge is the JSON structure returned by DeepSeek's create_pow_challenge endpoint.
type Challenge struct {
	Algorithm  string `json:"algorithm"`
	Challenge  string `json:"challenge"`
	Salt       string `json:"salt"`
	Difficulty int    `json:"difficulty"`
	ExpireAt   int64  `json:"expire_at"`
	Signature  string `json:"signature"`
	TargetPath string `json:"target_path"`
}

// Solve finds the integer n such that SHA3-256(challenge + salt + "_" + expireAt + "_" + n)
// has `difficulty` leading zero bits, then returns the base64-encoded JSON response.
func Solve(c Challenge) (string, error) {
	prefix := c.Salt + "_" + strconv.FormatInt(c.ExpireAt, 10) + "_"
	base := c.Challenge + prefix

	answer, err := findAnswer(base, c.Difficulty)
	if err != nil {
		return "", err
	}

	result := map[string]interface{}{
		"algorithm":   c.Algorithm,
		"challenge":   c.Challenge,
		"salt":        c.Salt,
		"answer":      answer,
		"signature":   c.Signature,
		"target_path": c.TargetPath,
	}
	b, _ := json.Marshal(result)
	return base64.StdEncoding.EncodeToString(b), nil
}

func findAnswer(base string, difficulty int) (int, error) {
	const maxIter = 10_000_000
	for n := 0; n < maxIter; n++ {
		candidate := base + strconv.Itoa(n)
		h := sha3.Sum256([]byte(candidate))
		if leadingZeroBits(h[:]) >= difficulty {
			return n, nil
		}
	}
	return 0, fmt.Errorf("pow: no solution found within %d iterations", maxIter)
}

// leadingZeroBits counts the number of leading zero bits in b.
func leadingZeroBits(b []byte) int {
	count := 0
	for _, byte_ := range b {
		if byte_ == 0 {
			count += 8
			continue
		}
		for mask := byte(0x80); mask > 0; mask >>= 1 {
			if byte_&mask != 0 {
				return count
			}
			count++
		}
		break
	}
	return count
}
```

- [ ] **Step 3: Write a quick test**

```bash
touch internal/pow/solver_test.go
```

```go
package pow

import (
	"strings"
	"testing"
)

func TestLeadingZeroBits(t *testing.T) {
	tests := []struct {
		b    []byte
		want int
	}{
		{[]byte{0x00}, 8},
		{[]byte{0x80}, 0},
		{[]byte{0x40}, 1},
		{[]byte{0x00, 0x80}, 8},
	}
	for _, tc := range tests {
		got := leadingZeroBits(tc.b)
		if got != tc.want {
			t.Errorf("leadingZeroBits(%x) = %d, want %d", tc.b, got, tc.want)
		}
	}
}

func TestSolveProducesBase64JSON(t *testing.T) {
	c := Challenge{
		Algorithm:  "DeepSeekHashV1",
		Challenge:  "testchallenge",
		Salt:       "testsalt",
		Difficulty: 1, // very low difficulty — fast test
		ExpireAt:   9999999999,
		Signature:  "sig",
		TargetPath: "/api/v0/chat/completion",
	}
	result, err := Solve(c)
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if result == "" {
		t.Fatal("Solve returned empty string")
	}
	// Result must be valid base64
	if strings.ContainsAny(result, " \n\t") {
		t.Errorf("result contains whitespace: %q", result)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/pow/... -v
```
Expected: both tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pow/solver.go internal/pow/solver_test.go
git commit -m "feat: add native Go SHA-3 PoW solver"
```

---

## Task 7: internal/client — utls Chrome HTTP client

**Files:**
- Create: `internal/client/client.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/client
touch internal/client/client.go
```

- [ ] **Step 2: Write client.go**

```go
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"

	utls "github.com/refraction-networking/utls"

	"github.com/KTS-o7/freeseek-proxy/internal/auth"
	"github.com/KTS-o7/freeseek-proxy/internal/cookies"
	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
	"github.com/KTS-o7/freeseek-proxy/internal/pow"
)

const (
	baseURL       = "https://chat.deepseek.com/api/v0"
	powTargetPath = "/api/v0/chat/completion"
	userAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// Client is a Chrome-impersonating HTTP client for the DeepSeek API.
type Client struct {
	http    *http.Client
	auth    *auth.Manager
	cookies *cookies.Manager
}

func New(a *auth.Manager, c *cookies.Manager) *Client {
	return &Client{
		http:    &http.Client{Transport: newUTLSTransport()},
		auth:    a,
		cookies: c,
	}
}

// Post sends a POST to path, handling Cloudflare errors with one cookie refresh retry.
func (c *Client) Post(ctx context.Context, path string, body interface{}, stream bool) (*http.Response, error) {
	resp, err := c.doPost(ctx, path, body, stream)
	if err != nil {
		return nil, err
	}
	if isCloudflareResp(resp) {
		resp.Body.Close()
		newCookies, refreshErr := c.cookies.Refresh()
		if refreshErr != nil {
			return nil, proxyerr.Cloudflare(fmt.Sprintf("Cloudflare block; cookie refresh failed: %v", refreshErr))
		}
		_ = newCookies
		return c.doPost(ctx, path, body, stream)
	}
	return resp, nil
}

func (c *Client) doPost(ctx context.Context, path string, body interface{}, stream bool) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, proxyerr.Server("failed to marshal request body")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, proxyerr.Server("failed to build request")
	}

	token, err := c.auth.Token()
	if err != nil {
		return nil, proxyerr.Auth(err.Error())
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en,fr-FR;q=0.9,fr;q=0.8,es-ES;q=0.7,es;q=0.6,en-US;q=0.5")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://chat.deepseek.com")
	req.Header.Set("Referer", "https://chat.deepseek.com/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-app-version", "2.0.0")
	req.Header.Set("x-client-locale", "en_US")
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("x-client-version", "2.0.0")

	ck := c.cookies.Load()
	if h := cookies.Header(ck); h != "" {
		req.Header.Set("Cookie", h)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, proxyerr.Network(fmt.Sprintf("DeepSeek request failed: %v", err))
	}
	c.cookies.UpdateFromResponse(resp)
	if err := checkResponse(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

// GetPowHeader fetches a PoW challenge and returns the solved base64 header value.
func (c *Client) GetPowHeader(ctx context.Context) (string, error) {
	resp, err := c.Post(ctx, "/chat/create_pow_challenge", map[string]string{"target_path": powTargetPath}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Data struct {
			BizData struct {
				Challenge pow.Challenge `json:"challenge"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", proxyerr.API("invalid PoW challenge response", 502)
	}
	return pow.Solve(result.Data.BizData.Challenge)
}

// PostWithPow adds the x-ds-pow-response header before POSTing.
func (c *Client) PostWithPow(ctx context.Context, path string, body map[string]interface{}, stream bool) (*http.Response, error) {
	powHeader, err := c.GetPowHeader(ctx)
	if err != nil {
		return nil, err
	}

	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, proxyerr.Server("failed to build request")
	}

	token, err := c.auth.Token()
	if err != nil {
		return nil, proxyerr.Auth(err.Error())
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en,fr-FR;q=0.9,fr;q=0.8,es-ES;q=0.7,es;q=0.6,en-US;q=0.5")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://chat.deepseek.com")
	req.Header.Set("Referer", "https://chat.deepseek.com/")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-app-version", "2.0.0")
	req.Header.Set("x-client-locale", "en_US")
	req.Header.Set("x-client-platform", "web")
	req.Header.Set("x-client-version", "2.0.0")
	req.Header.Set("x-ds-pow-response", powHeader)

	ck := c.cookies.Load()
	if h := cookies.Header(ck); h != "" {
		req.Header.Set("Cookie", h)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, proxyerr.Network(fmt.Sprintf("DeepSeek request failed: %v", err))
	}
	c.cookies.UpdateFromResponse(resp)

	if isCloudflareResp(resp) {
		resp.Body.Close()
		newCookies, refreshErr := c.cookies.Refresh()
		if refreshErr != nil {
			return nil, proxyerr.Cloudflare(fmt.Sprintf("Cloudflare block; cookie refresh failed: %v", refreshErr))
		}
		_ = newCookies
		return c.PostWithPow(ctx, path, body, stream)
	}

	if err := checkResponse(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
}

func checkResponse(resp *http.Response) error {
	if resp.StatusCode == 200 {
		return nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	body := string(bodyBytes)
	ct := resp.Header.Get("Content-Type")
	if proxyerr.IsCloudflare(resp.StatusCode, ct, body) {
		return proxyerr.Cloudflare("DeepSeek request blocked by Cloudflare")
	}
	switch resp.StatusCode {
	case 401:
		return proxyerr.Auth("Invalid or expired DeepSeek authentication")
	case 429:
		return proxyerr.RateLimit("DeepSeek rate limit reached")
	}
	if resp.StatusCode >= 500 {
		return proxyerr.API("DeepSeek upstream error", resp.StatusCode)
	}
	return proxyerr.API(body, resp.StatusCode)
}

func isCloudflareResp(resp *http.Response) bool {
	if resp.StatusCode != 403 && resp.StatusCode != 503 {
		return false
	}
	return resp.Header.Get("Content-Type") != "" &&
		len(resp.Header.Get("Content-Type")) > 0 &&
		resp.Header.Get("Server") == "cloudflare"
}

// newUTLSTransport returns an http.Transport that uses utls Chrome-120 fingerprint.
func newUTLSTransport() http.RoundTripper {
	return &utlsTransport{}
}

type utlsTransport struct{}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	conn, err := net.Dial("tcp", req.URL.Host+":443")
	if err != nil {
		// Try with explicit port
		host := req.URL.Hostname()
		conn, err = net.Dial("tcp", host+":443")
		if err != nil {
			return nil, err
		}
	}

	uconn := utls.UClient(conn, &utls.Config{
		ServerName:         req.URL.Hostname(),
		InsecureSkipVerify: false,
	}, utls.HelloChrome_120)

	if err := uconn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return uconn, nil
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}
	return transport.RoundTrip(req)
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/client/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/client/client.go
git commit -m "feat: add utls Chrome-120 HTTP client"
```

---

## Task 8: internal/proxy/sse.go — DeepSeek SSE parser

**Files:**
- Create: `internal/proxy/sse.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/proxy
touch internal/proxy/sse.go
```

- [ ] **Step 2: Write sse.go**

```go
package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// SSEEvent holds the parsed content and optional message ID from one DeepSeek SSE line.
type SSEEvent struct {
	Content           string
	ResponseMessageID interface{} // number or string
}

type deepSeekSSE struct {
	V interface{} `json:"v"` // string or object
	P string      `json:"p"`
	ResponseMsgID interface{} `json:"response_message_id"`
	Content       string      `json:"content"`
}

type dsResponse struct {
	MessageID interface{} `json:"message_id"`
	Fragments []struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"fragments"`
}

// ParseLine parses one "data: ..." SSE line.
// Returns nil when the line should be skipped.
func ParseLine(line string, includeThinking bool) *SSEEvent {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data: ") {
		return nil
	}
	payload := line[6:]
	if payload == "[DONE]" {
		return nil
	}

	var d deepSeekSSE
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		return nil
	}

	evt := &SSEEvent{}

	// Capture response_message_id at top level
	if d.ResponseMsgID != nil {
		evt.ResponseMessageID = d.ResponseMsgID
	}

	// Skip status/usage events
	if d.P == "response/status" || d.P == "response/accumulated_token_usage" {
		return evt // may still carry ResponseMessageID
	}
	// Skip thinking fragments when not requested
	if !includeThinking && strings.Contains(d.P, "thinking") {
		return evt
	}

	switch v := d.V.(type) {
	case string:
		evt.Content = v
	case map[string]interface{}:
		// Try to extract from v.response
		b, _ := json.Marshal(v)
		var resp struct {
			Response dsResponse `json:"response"`
		}
		if json.Unmarshal(b, &resp) == nil {
			if resp.Response.MessageID != nil {
				evt.ResponseMessageID = resp.Response.MessageID
			}
			for _, frag := range resp.Response.Fragments {
				if !includeThinking && frag.Type == "thinking" {
					continue
				}
				evt.Content += frag.Content
			}
		}
	}

	if evt.Content == "" && d.Content != "" {
		evt.Content = d.Content
	}

	return evt
}

// CollectFull reads a DeepSeek SSE stream and returns the full text + last message ID.
func CollectFull(r io.Reader, includeThinking bool) (text string, msgID interface{}) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		evt := ParseLine(scanner.Text(), includeThinking)
		if evt == nil {
			continue
		}
		if evt.ResponseMessageID != nil {
			msgID = evt.ResponseMessageID
		}
		text += evt.Content
	}
	return
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/proxy/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/sse.go
git commit -m "feat: add DeepSeek SSE parser"
```

---

## Task 9: internal/proxy/response.go — OpenAI response builders

**Files:**
- Create: `internal/proxy/response.go`

- [ ] **Step 1: Create the file**

```bash
touch internal/proxy/response.go
```

- [ ] **Step 2: Write response.go**

```go
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func ChatCmplID() string {
	return "chatcmpl-" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]
}

func buildUsage(promptText, completionText string) map[string]int {
	return map[string]int{
		"prompt_tokens":     (len(promptText) + 3) / 4,
		"completion_tokens": (len(completionText) + 3) / 4,
		"total_tokens":      (len(promptText) + len(completionText) + 3) / 4,
	}
}

// WriteNonStreaming writes a complete chat.completion JSON response.
func WriteNonStreaming(w http.ResponseWriter, model, promptText, fullText string, msgID interface{}, sessionID string) {
	id := ChatCmplID()
	created := time.Now().Unix()
	body := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       map[string]interface{}{"role": "assistant", "content": fullText},
				"finish_reason": "stop",
				"logprobs":      nil,
			},
		},
		"usage": buildUsage(promptText, fullText),
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("x-chat-session-id", sessionID)
	if msgID != nil {
		w.Header().Set("x-parent-message-id", fmt.Sprintf("%v", msgID))
	}
	json.NewEncoder(w).Encode(body)
}

// StreamChunk encodes and writes one SSE chunk.
func StreamChunk(w http.ResponseWriter, flusher http.Flusher, chunk map[string]interface{}) {
	b, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flusher.Flush()
}

// StreamDone writes the final [DONE] SSE sentinel.
func StreamDone(w http.ResponseWriter, flusher http.Flusher) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// SetStreamHeaders sets Content-Type and session headers for SSE.
func SetStreamHeaders(w http.ResponseWriter, sessionID string, msgID interface{}) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("x-chat-session-id", sessionID)
	if msgID != nil {
		w.Header().Set("x-parent-message-id", fmt.Sprintf("%v", msgID))
	}
}

// RoleChunk emits the opening role delta chunk.
func RoleChunk(id string, created int64, model string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{"role": "assistant"}, "finish_reason": nil, "logprobs": nil},
		},
	}
}

// ContentChunk emits a content delta chunk.
func ContentChunk(id string, created int64, model, content string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{"content": content}, "finish_reason": nil, "logprobs": nil},
		},
	}
}

// StopChunk emits the finish_reason=stop chunk.
func StopChunk(id string, created int64, model string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "stop", "logprobs": nil},
		},
	}
}

// ToolCallsChunk emits a tool_calls delta chunk.
func ToolCallsChunk(id string, created int64, model string, toolCalls interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{"tool_calls": toolCalls}, "finish_reason": nil, "logprobs": nil},
		},
	}
}

// ToolCallsStopChunk emits the finish_reason=tool_calls chunk.
func ToolCallsStopChunk(id string, created int64, model string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "delta": map[string]interface{}{}, "finish_reason": "tool_calls", "logprobs": nil},
		},
	}
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/proxy/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/response.go
git commit -m "feat: add OpenAI SSE/JSON response builders"
```

---

## Task 10: internal/tools — tool-call compatibility layer

**Files:**
- Create: `internal/tools/compat.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/tools
touch internal/tools/compat.go
```

- [ ] **Step 2: Write compat.go**

```go
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const toolCallKey  = "opencode_tool_call"
const toolCallsKey = "opencode_tool_calls"

// Tool mirrors the OpenAI function tool definition.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// ToolCall is an OpenAI-spec tool call result.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message mirrors the subset of OpenAI message fields we need.
type Message struct {
	Role       string     `json:"role"`
	Content    interface{} `json:"content"` // string or []ContentPart
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// NeedsCompatMode returns true when tool-call emulation is required.
func NeedsCompatMode(messages []Message, tools []Tool, toolChoice interface{}) bool {
	if len(tools) > 0 || toolChoice != nil {
		return true
	}
	for _, m := range messages {
		if m.Role == "tool" {
			return true
		}
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// BuildPrompt constructs the flat-text prompt including tool system prompt + conversation.
func BuildPrompt(messages []Message, tools []Tool, toolChoice interface{}) string {
	sections := []string{buildSystemPrompt(tools, toolChoice), formatConversation(messages), "Reply to the latest conversation turn."}
	return strings.Join(sections, "\n\n")
}

func buildSystemPrompt(tools []Tool, toolChoice interface{}) string {
	available := []string{}
	for _, t := range tools {
		if t.Type != "function" || t.Function.Name == "" {
			continue
		}
		desc := "Description: none"
		if t.Function.Description != "" {
			desc = "Description: " + t.Function.Description
		}
		params := "{}"
		if b, err := json.MarshalIndent(t.Function.Parameters, "", "  "); err == nil {
			params = string(b)
		}
		available = append(available, fmt.Sprintf("- %s\n  %s\n  JSON Schema: %s", t.Function.Name, desc, params))
	}

	rule := "Call a tool only when it is genuinely needed. Otherwise answer normally."
	switch tc := toolChoice.(type) {
	case string:
		switch tc {
		case "required":
			rule = "You must respond with exactly one tool call JSON object before any natural language answer."
		case "none":
			rule = "Do not call any tool. Respond in natural language only."
		}
	case map[string]interface{}:
		if fn, ok := tc["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				rule = fmt.Sprintf("You must respond with a tool call for the function named %s before any natural language answer.", name)
			}
		}
	}

	parts := []string{
		"You are replying through an OpenAI-compatible proxy that supports tool use.",
		rule,
		fmt.Sprintf(`If you decide to call a tool, output only valid JSON with this exact top-level shape: {"%s": {"name": "tool_name", "arguments": { ... }}}.`, toolCallKey),
		fmt.Sprintf(`If you need multiple tool calls in one reply, output only valid JSON with this exact top-level shape: {"%s": [{"name": "tool_name", "arguments": { ... }}]}.`, toolCallsKey),
		"Do not wrap the JSON in markdown.",
		"Do not include explanatory text before or after the JSON.",
		"If you are responding after receiving a tool result and no further tool is needed, answer normally in plain text.",
		"Available tools:",
		strings.Join(available, "\n"),
	}
	return strings.Join(parts, "\n\n")
}

func normalizeContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				} else if c, ok := m["content"].(string); ok {
					parts = append(parts, c)
				}
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func formatConversation(messages []Message) string {
	var lines []string
	for _, m := range messages {
		switch m.Role {
		case "system":
			lines = append(lines, "System: "+normalizeContent(m.Content))
		case "user":
			lines = append(lines, "User: "+normalizeContent(m.Content))
		case "assistant":
			if len(m.ToolCalls) > 0 {
				calls := []string{}
				for _, tc := range m.ToolCalls {
					calls = append(calls, fmt.Sprintf("%s(%s)", tc.Function.Name, tc.Function.Arguments))
				}
				lines = append(lines, "Assistant tool request: "+strings.Join(calls, ", "))
			}
			if c := normalizeContent(m.Content); c != "" {
				lines = append(lines, "Assistant: "+c)
			}
		case "tool":
			lines = append(lines, fmt.Sprintf("Tool (%s) result: %s", m.ToolCallID, normalizeContent(m.Content)))
		}
	}
	return strings.Join(lines, "\n\n")
}

// ParseEnvelope tries to extract tool calls from the model's text response.
// Returns nil if no valid tool envelope is found.
func ParseEnvelope(text string, tools []Tool) []ToolCall {
	toolNames := map[string]bool{}
	for _, t := range tools {
		if t.Type == "function" && t.Function.Name != "" {
			toolNames[t.Function.Name] = true
		}
	}

	for _, candidate := range extractJSONObjects(text) {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
			continue
		}
		var rawCalls []interface{}
		if multi, ok := payload[toolCallsKey].([]interface{}); ok {
			rawCalls = multi
		} else if single, ok := payload[toolCallKey].(map[string]interface{}); ok {
			rawCalls = []interface{}{single}
		}
		if len(rawCalls) == 0 {
			continue
		}
		var calls []ToolCall
		for _, raw := range rawCalls {
			m, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" || !toolNames[name] {
				continue
			}
			argsRaw := m["arguments"]
			argsStr := ""
			switch a := argsRaw.(type) {
			case string:
				argsStr = a
			default:
				b, _ := json.Marshal(a)
				argsStr = string(b)
			}
			calls = append(calls, ToolCall{
				ID:   "call_" + strings.ReplaceAll(uuid.New().String(), "-", ""),
				Type: "function",
				Function: ToolCallFunction{Name: name, Arguments: argsStr},
			})
		}
		if len(calls) > 0 {
			return calls
		}
	}
	return nil
}

// extractJSONObjects finds all top-level {...} substrings in text.
func extractJSONObjects(text string) []string {
	var results []string
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		depth, inStr, escaped := 0, false, false
		for j := i; j < len(text); j++ {
			c := text[j]
			if inStr {
				if escaped {
					escaped = false
				} else if c == '\\' {
					escaped = true
				} else if c == '"' {
					inStr = false
				}
				continue
			}
			if c == '"' {
				inStr = true
			} else if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					results = append(results, text[i:j+1])
					break
				}
			}
		}
	}
	return results
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/tools/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/tools/compat.go
git commit -m "feat: add tool-call compatibility layer"
```

---

## Task 11: internal/taalas — chatjimmy.ai proxy

**Files:**
- Create: `internal/taalas/taalas.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p internal/taalas
touch internal/taalas/taalas.go
```

- [ ] **Step 2: Write taalas.go**

```go
package taalas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const baseURL   = "https://chatjimmy.ai/api"
const statsStart = "<|stats|>"
const statsEnd   = "<|/stats|>"
const userAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var modelMap = map[string]string{
	"taalas-llama3.1-8b": "llama3.1-8B",
}

// Complete sends a request to chatjimmy.ai and returns the response text.
func Complete(messages []map[string]interface{}, model string) (string, error) {
	taalasModel, ok := modelMap[model]
	if !ok {
		taalasModel = "llama3.1-8B"
	}

	// Build flat prompt from messages.
	var prompt string
	if len(messages) == 1 {
		prompt, _ = messages[0]["content"].(string)
	} else {
		var lines []string
		for _, m := range messages {
			role, _ := m["role"].(string)
			content, _ := m["content"].(string)
			lines = append(lines, strings.Title(role)+": "+content)
		}
		prompt = strings.Join(lines, "\n")
	}

	payload := map[string]interface{}{
		"messages": []map[string]interface{}{{"role": "user", "content": prompt}},
		"chatOptions": map[string]interface{}{
			"selectedModel": taalasModel,
			"systemPrompt":  "",
			"topK":          8,
		},
		"attachment": nil,
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/chat", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("taalas: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://chatjimmy.ai")
	req.Header.Set("Referer", "https://chatjimmy.ai/")
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("taalas: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("taalas: HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("taalas: read body: %w", err)
	}

	text, _ := parseResponse(string(raw))
	return text, nil
}

func parseResponse(raw string) (string, map[string]interface{}) {
	idx := strings.Index(raw, statsStart)
	if idx == -1 {
		return strings.TrimSpace(raw), nil
	}
	text := strings.TrimSpace(raw[:idx])
	statsRaw := raw[idx+len(statsStart):]
	end := strings.Index(statsRaw, statsEnd)
	if end != -1 {
		statsRaw = statsRaw[:end]
	}
	var stats map[string]interface{}
	json.Unmarshal([]byte(statsRaw), &stats)
	return text, stats
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./internal/taalas/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/taalas/taalas.go
git commit -m "feat: add Taalas chatjimmy.ai proxy"
```

---

## Task 12: cmd/proxy/main.go — entry point and router

**Files:**
- Create: `cmd/proxy/main.go`

- [ ] **Step 1: Create the file**

```bash
mkdir -p cmd/proxy
touch cmd/proxy/main.go
```

- [ ] **Step 2: Write main.go**

```go
package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/KTS-o7/freeseek-proxy/internal/auth"
	"github.com/KTS-o7/freeseek-proxy/internal/client"
	"github.com/KTS-o7/freeseek-proxy/internal/config"
	"github.com/KTS-o7/freeseek-proxy/internal/cookies"
	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
)

//go:embed ../../public/index.html
var static embed.FS

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	cookieMgr := cookies.New(cfg)
	authMgr := auth.New(cfg)
	deepseekClient := client.New(authMgr, cookieMgr)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := static.ReadFile("public/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Get("/v1/models", handleModels)

	r.Group(func(r chi.Router) {
		if cfg.APIKey != "" {
			r.Use(apiKeyMiddleware(cfg.APIKey))
		}
		r.Post("/v1/chat/completions", handleChatCompletions(deepseekClient))
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("freeseek-proxy listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer "+key {
				proxyerr.WriteJSON(w, proxyerr.Auth("Invalid API key"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 3: Verify it compiles (will fail on missing handleModels/handleChatCompletions — that is expected)**

```bash
go build ./cmd/proxy/... 2>&1 | head -5
```
Expected: errors about undefined `handleModels` and `handleChatCompletions` — that is correct, they come in the next task.

- [ ] **Step 4: Commit what we have**

```bash
git add cmd/proxy/main.go
git commit -m "feat: add main entry point with chi router"
```

---

## Task 13: cmd/proxy/models.go — /v1/models route

**Files:**
- Create: `cmd/proxy/models.go`

- [ ] **Step 1: Create the file**

```bash
touch cmd/proxy/models.go
```

- [ ] **Step 2: Write models.go**

```go
package main

import (
	"encoding/json"
	"net/http"
)

func handleModels(w http.ResponseWriter, r *http.Request) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	resp := map[string]interface{}{
		"object": "list",
		"data": []model{
			{ID: "deepseek-v3",        Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "deepseek-r1",        Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "deepseek-chat",      Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "deepseek-coder",     Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "deepseek-reasoner",  Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "taalas-llama3.1-8b", Object: "model", Created: 1704067200, OwnedBy: "taalas"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/proxy/... 2>&1 | grep -v "handleChatCompletions"
```
Expected: only the `handleChatCompletions` undefined error remains.

- [ ] **Step 4: Commit**

```bash
git add cmd/proxy/models.go
git commit -m "feat: add /v1/models route"
```

---

## Task 14: cmd/proxy/deepseek.go — DeepSeek session + completion handler

**Files:**
- Create: `cmd/proxy/deepseek.go`

- [ ] **Step 1: Create the file**

```bash
touch cmd/proxy/deepseek.go
```

- [ ] **Step 2: Write deepseek.go**

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/KTS-o7/freeseek-proxy/internal/client"
	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
	"github.com/KTS-o7/freeseek-proxy/internal/proxy"
	"github.com/KTS-o7/freeseek-proxy/internal/taalas"
	"github.com/KTS-o7/freeseek-proxy/internal/tools"
)

var modelTypeMap = map[string]string{
	"deepseek-v3":       "default",
	"deepseek-r1":       "expert",
	"deepseek-chat":     "default",
	"deepseek-coder":    "default",
	"deepseek-reasoner": "expert",
}

type chatRequest struct {
	Model           string           `json:"model"`
	Messages        []tools.Message  `json:"messages"`
	Stream          bool             `json:"stream"`
	Tools           []tools.Tool     `json:"tools"`
	ToolChoice      interface{}      `json:"tool_choice"`
	ThinkingEnabled bool             `json:"thinking_enabled"`
	SearchEnabled   bool             `json:"search_enabled"`
	ParentMessageID interface{}      `json:"parent_message_id"`
	SessionID       string           `json:"sessionId"`
}

func handleChatCompletions(c *client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			proxyerr.WriteJSON(w, proxyerr.Invalid("could not parse request body as JSON"))
			return
		}
		if len(req.Messages) == 0 {
			proxyerr.WriteJSON(w, proxyerr.Invalid("messages is required"))
			return
		}
		model := req.Model
		if model == "" {
			model = "deepseek-chat"
		}

		if strings.HasPrefix(model, "taalas-") {
			handleTaalas(w, r, req, model)
			return
		}
		handleDeepSeek(w, r, req, model, c)
	}
}

// ─── DeepSeek ────────────────────────────────────────────────────────────────

func handleDeepSeek(w http.ResponseWriter, r *http.Request, req chatRequest, model string, c *client.Client) {
	// Create session if not provided
	sessionID := req.SessionID
	if sessionID == "" {
		var err error
		sessionID, err = createSession(r.Context(), c)
		if err != nil {
			if pe, ok := err.(*proxyerr.ProxyError); ok {
				proxyerr.WriteJSON(w, pe)
			} else {
				proxyerr.WriteJSON(w, proxyerr.Server(err.Error()))
			}
			return
		}
	}

	// Build prompt
	toolsProvided := tools.NeedsCompatMode(req.Messages, req.Tools, req.ToolChoice)
	var prompt string
	if toolsProvided {
		prompt = tools.BuildPrompt(req.Messages, req.Tools, req.ToolChoice)
	} else {
		prompt = extractLastMessage(req.Messages)
	}

	promptText := messagesText(req.Messages)

	modelType := modelTypeMap[model]
	if modelType == "" {
		modelType = "default"
	}

	body := map[string]interface{}{
		"chat_session_id":  sessionID,
		"parent_message_id": normalizeParentID(req.ParentMessageID),
		"model_type":       modelType,
		"prompt":           prompt,
		"ref_file_ids":     []interface{}{},
		"thinking_enabled": req.ThinkingEnabled,
		"search_enabled":   req.SearchEnabled,
		"preempt":          false,
	}

	resp, err := c.PostWithPow(r.Context(), "/chat/completion", body, true)
	if err != nil {
		if pe, ok := err.(*proxyerr.ProxyError); ok {
			proxyerr.WriteJSON(w, pe)
		} else {
			proxyerr.WriteJSON(w, proxyerr.Server(err.Error()))
		}
		return
	}
	defer resp.Body.Close()

	if toolsProvided || !req.Stream {
		// Collect full response, then build OpenAI object
		fullText, msgID := proxy.CollectFull(resp.Body, req.ThinkingEnabled)

		// Try tool envelope parse
		parsed := tools.ParseEnvelope(fullText, req.Tools)

		if req.Stream {
			// Tool compat stream: re-emit as minimal SSE
			streamToolCompat(w, model, sessionID, msgID, fullText, parsed, promptText)
		} else {
			if parsed != nil {
				writeToolCallResponse(w, model, sessionID, msgID, parsed, promptText, fullText)
			} else {
				proxy.WriteNonStreaming(w, model, promptText, fullText, msgID, sessionID)
			}
		}
		return
	}

	// Pure streaming: proxy SSE in real time
	streamDeepSeek(w, resp.Body, model, sessionID, req.ThinkingEnabled)
}

func createSession(ctx interface{ Done() <-chan struct{} }, c *client.Client) (string, error) {
	r := &http.Request{}
	resp, err := c.Post(nil, "/chat_session/create", map[string]interface{}{"character_id": nil}, false)
	_ = r
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Data struct {
			BizData struct {
				ChatSession struct{ ID string `json:"id"` } `json:"chat_session"`
				ID          string `json:"id"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", proxyerr.Server("invalid session response")
	}
	id := result.Data.BizData.ChatSession.ID
	if id == "" {
		id = result.Data.BizData.ID
	}
	if id == "" {
		return "", proxyerr.Server("session ID not found in response")
	}
	return id, nil
}

func streamDeepSeek(w http.ResponseWriter, body io.Reader, model, sessionID string, thinking bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		proxyerr.WriteJSON(w, proxyerr.Server("streaming not supported"))
		return
	}

	id := proxy.ChatCmplID()
	created := time.Now().Unix()
	var msgID interface{}

	proxy.SetStreamHeaders(w, sessionID, nil)

	proxy.StreamChunk(w, flusher, proxy.RoleChunk(id, created, model))

	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		evt := proxy.ParseLine(scanner.Text(), thinking)
		if evt == nil {
			continue
		}
		if evt.ResponseMessageID != nil {
			msgID = evt.ResponseMessageID
		}
		if evt.Content != "" {
			proxy.StreamChunk(w, flusher, proxy.ContentChunk(id, created, model, evt.Content))
		}
	}

	proxy.StreamChunk(w, flusher, proxy.StopChunk(id, created, model))
	proxy.StreamDone(w, flusher)

	if msgID != nil {
		w.Header().Set("x-parent-message-id", fmt.Sprintf("%v", msgID))
	}
}

func streamToolCompat(w http.ResponseWriter, model, sessionID string, msgID interface{}, fullText string, toolCalls []tools.ToolCall, promptText string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		proxyerr.WriteJSON(w, proxyerr.Server("streaming not supported"))
		return
	}
	id := proxy.ChatCmplID()
	created := time.Now().Unix()
	proxy.SetStreamHeaders(w, sessionID, msgID)
	proxy.StreamChunk(w, flusher, proxy.RoleChunk(id, created, model))

	if len(toolCalls) > 0 {
		tcDelta := make([]map[string]interface{}, len(toolCalls))
		for i, tc := range toolCalls {
			tcDelta[i] = map[string]interface{}{
				"index": i, "id": tc.ID, "type": tc.Type,
				"function": map[string]interface{}{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
			}
		}
		proxy.StreamChunk(w, flusher, proxy.ToolCallsChunk(id, created, model, tcDelta))
		proxy.StreamChunk(w, flusher, proxy.ToolCallsStopChunk(id, created, model))
	} else {
		proxy.StreamChunk(w, flusher, proxy.ContentChunk(id, created, model, fullText))
		proxy.StreamChunk(w, flusher, proxy.StopChunk(id, created, model))
	}
	proxy.StreamDone(w, flusher)
}

func writeToolCallResponse(w http.ResponseWriter, model, sessionID string, msgID interface{}, toolCalls []tools.ToolCall, promptText, fullText string) {
	id := proxy.ChatCmplID()
	created := time.Now().Unix()
	tcList := make([]map[string]interface{}, len(toolCalls))
	for i, tc := range toolCalls {
		tcList[i] = map[string]interface{}{
			"id": tc.ID, "type": tc.Type,
			"function": map[string]interface{}{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
		}
	}
	body := map[string]interface{}{
		"id": id, "object": "chat.completion", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": nil, "tool_calls": tcList}, "finish_reason": "tool_calls", "logprobs": nil},
		},
		"usage": map[string]int{
			"prompt_tokens":     (len(promptText) + 3) / 4,
			"completion_tokens": (len(fullText) + 3) / 4,
			"total_tokens":      (len(promptText)+len(fullText)+3) / 4,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("x-chat-session-id", sessionID)
	if msgID != nil {
		w.Header().Set("x-parent-message-id", fmt.Sprintf("%v", msgID))
	}
	json.NewEncoder(w).Encode(body)
}

// ─── Taalas ──────────────────────────────────────────────────────────────────

func handleTaalas(w http.ResponseWriter, r *http.Request, req chatRequest, model string) {
	msgs := make([]map[string]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		c := ""
		switch v := m.Content.(type) {
		case string:
			c = v
		}
		msgs[i] = map[string]interface{}{"role": m.Role, "content": c}
	}

	text, err := taalas.Complete(msgs, model)
	if err != nil {
		proxyerr.WriteJSON(w, proxyerr.API(err.Error(), 502))
		return
	}

	promptText := messagesText(req.Messages)
	id := proxy.ChatCmplID()
	created := time.Now().Unix()

	if req.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			proxyerr.WriteJSON(w, proxyerr.Server("streaming not supported"))
			return
		}
		proxy.SetStreamHeaders(w, "", nil)
		proxy.StreamChunk(w, flusher, proxy.RoleChunk(id, created, model))
		proxy.StreamChunk(w, flusher, proxy.ContentChunk(id, created, model, text))
		proxy.StreamChunk(w, flusher, proxy.StopChunk(id, created, model))
		proxy.StreamDone(w, flusher)
		return
	}

	resp := map[string]interface{}{
		"id": id, "object": "chat.completion", "created": created, "model": model,
		"choices": []map[string]interface{}{
			{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": text}, "finish_reason": "stop", "logprobs": nil},
		},
		"usage": map[string]int{
			"prompt_tokens":     (len(promptText) + 3) / 4,
			"completion_tokens": (len(text) + 3) / 4,
			"total_tokens":      (len(promptText)+len(text)+3) / 4,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func extractLastMessage(messages []tools.Message) string {
	if len(messages) == 0 {
		return ""
	}
	m := messages[len(messages)-1]
	switch v := m.Content.(type) {
	case string:
		return v
	}
	return ""
}

func messagesText(messages []tools.Message) string {
	var parts []string
	for _, m := range messages {
		if s, ok := m.Content.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func normalizeParentID(v interface{}) interface{} {
	if s, ok := v.(string); ok {
		allDigits := len(s) > 0
		for _, c := range s {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			var n int64
			fmt.Sscanf(s, "%d", &n)
			return n
		}
	}
	return v
}
```

- [ ] **Step 3: Verify it compiles**

```bash
go build ./cmd/proxy/...
```
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add cmd/proxy/deepseek.go
git commit -m "feat: add DeepSeek and Taalas request handlers"
```

---

## Task 15: Fix createSession context arg + full build check

The `createSession` function in Task 14 has a placeholder `ctx` type. Fix it before the full build.

**Files:**
- Modify: `cmd/proxy/deepseek.go`

- [ ] **Step 1: Fix the createSession signature**

Replace the function signature and internal usage:

```go
func createSession(c *client.Client) (string, error) {
	resp, err := c.Post(context.Background(), "/chat_session/create", map[string]interface{}{"character_id": nil}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Data struct {
			BizData struct {
				ChatSession struct{ ID string `json:"id"` } `json:"chat_session"`
				ID          string `json:"id"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", proxyerr.Server("invalid session response")
	}
	id := result.Data.BizData.ChatSession.ID
	if id == "" {
		id = result.Data.BizData.ID
	}
	if id == "" {
		return "", proxyerr.Server("session ID not found in response")
	}
	return id, nil
}
```

And update the call site inside `handleDeepSeek`:

```go
sessionID, err = createSession(c)
```

Add `"context"` to the import block in `deepseek.go`.

- [ ] **Step 2: Full build**

```bash
go build ./...
```
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/proxy/deepseek.go
git commit -m "fix: correct createSession context argument"
```

---

## Task 16: Update .env with provided cookies

**Files:**
- Modify: `.env` (create from `.env.example` if not present)

- [ ] **Step 1: Create .env if missing**

```bash
[ -f .env ] || cp .env.example .env
```

- [ ] **Step 2: Set the DeepSeek cookies**

Open `.env` and set:

```
DEEPSEEK_COOKIES=aws-waf-token=8d97f9eb-db7b-45bf-b57e-a65222a9e7bf:BQoAfHdwoAMJAAAA:r6O0yBwt644eW8XCvk3+xOibbcIKETGF6Mvrp8Vb0TZdVKQLaNdl2flhVACT7NdWinhrXhuIBP9J1DQrw6JP/VDrvBZcYE2TPBTYsFNlyCQ9mqZebuF5w2uzjw8EA8PcZguFuwnwGxi8zRgZa5RdeQ8TrDL7sARMeyhPAWkv5XkPPccXQBgbriL601Aenok=; ds_session_id=3ab27c80079c4032bf9f38af133354a7
```

Note: `.env` is already in `.gitignore` — do not commit it.

- [ ] **Step 3: Verify .env is gitignored**

```bash
git check-ignore -v .env
```
Expected: `.gitignore:... .env`

---

## Task 17: Smoke test — build and run

- [ ] **Step 1: Build the binary**

```bash
go build -o freeseek-proxy ./cmd/proxy/
```
Expected: produces `./freeseek-proxy` binary with no errors.

- [ ] **Step 2: Check binary size**

```bash
ls -lh freeseek-proxy
```
Expected: 10–20 MB.

- [ ] **Step 3: Start the server**

```bash
./freeseek-proxy &
sleep 1
```

- [ ] **Step 4: Health check**

```bash
curl -s http://localhost:9123/health
```
Expected: `{"status":"ok"}`

- [ ] **Step 5: Models check**

```bash
curl -s http://localhost:9123/v1/models | python3 -m json.tool | head -20
```
Expected: JSON list with deepseek-v3, deepseek-r1, taalas-llama3.1-8b, etc.

- [ ] **Step 6: Chat completion (non-streaming)**

```bash
curl -s http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v3","messages":[{"role":"user","content":"Say hello in one word"}],"stream":false}' \
  | python3 -m json.tool
```
Expected: JSON with `choices[0].message.content` containing a greeting.

- [ ] **Step 7: Chat completion (streaming)**

```bash
curl -s http://localhost:9123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v3","messages":[{"role":"user","content":"Count to 3"}],"stream":true}'
```
Expected: multiple `data: {...}` SSE lines followed by `data: [DONE]`.

- [ ] **Step 8: Stop server and commit binary gitignore**

```bash
kill %1
```

Make sure `freeseek-proxy` binary is gitignored:

```bash
grep "freeseek-proxy" .gitignore || echo "freeseek-proxy" >> .gitignore
git add .gitignore
git commit -m "chore: gitignore compiled binary"
```

---

## Task 18: Cross-compile for Linux VPS

- [ ] **Step 1: Build Linux amd64 binary**

```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o freeseek-proxy-linux ./cmd/proxy/
```
Expected: produces `freeseek-proxy-linux`, no errors.

- [ ] **Step 2: Check binary size**

```bash
ls -lh freeseek-proxy-linux
```
Expected: 8–18 MB (smaller with `-s -w` strip flags).

- [ ] **Step 3: Commit**

```bash
echo "freeseek-proxy-linux" >> .gitignore
git add .gitignore
git commit -m "chore: gitignore linux cross-compiled binary"
```

- [ ] **Step 4: Deploy instructions**

Copy to VPS and run:

```bash
scp freeseek-proxy-linux user@your-vps:/opt/freeseek/freeseek-proxy
scp .env user@your-vps:/opt/freeseek/.env
ssh user@your-vps "cd /opt/freeseek && chmod +x freeseek-proxy && ./freeseek-proxy"
```

---
