package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

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

// Post sends a JSON POST to path. See send for retries.
func (c *Client) Post(ctx context.Context, path string, body interface{}, stream bool) (*http.Response, error) {
	return c.send(ctx, path, body, false)
}

// PostWithPow solves a fresh proof-of-work challenge and sends it with the POST.
func (c *Client) PostWithPow(ctx context.Context, path string, body map[string]interface{}, stream bool) (*http.Response, error) {
	return c.send(ctx, path, body, true)
}

// DeleteSession deletes a chat from the DeepSeek account. Best effort: errors are returned for logging only.
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	resp, err := c.Post(ctx, "/chat_session/delete", map[string]string{"chat_session_id": sessionID}, false)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// send makes the request. If DeepSeek rejects the token it marks the token bad and
// tries once more with the next token source. If Cloudflare blocks it, it refreshes
// cookies and tries once more.
func (c *Client) send(ctx context.Context, path string, body interface{}, withPow bool) (*http.Response, error) {
	authRetried, cfRetried := false, false
	for {
		resp, token, err := c.once(ctx, path, body, withPow)
		if pe, ok := err.(*proxyerr.ProxyError); ok {
			if pe.IsAuth() && token != "" && !authRetried {
				authRetried = true
				log.Printf("DeepSeek rejected the current token; trying the next token source")
				c.auth.Invalidate(token)
				continue
			}
			if pe.IsCloudflare() && !cfRetried {
				cfRetried = true
				if _, refreshErr := c.cookies.Refresh(); refreshErr != nil {
					return nil, proxyerr.Cloudflare(fmt.Sprintf("Cloudflare block; cookie refresh failed: %v", refreshErr))
				}
				continue
			}
		}
		return resp, err
	}
}

// once makes one request and returns the token it used.
func (c *Client) once(ctx context.Context, path string, body interface{}, withPow bool) (*http.Response, string, error) {
	powHeader := ""
	if withPow {
		h, err := c.powHeader(ctx)
		if err != nil {
			return nil, "", err
		}
		powHeader = h
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, "", proxyerr.Server("failed to marshal request body")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, "", proxyerr.Server("failed to build request")
	}

	token, err := c.auth.Token()
	if err != nil {
		return nil, "", proxyerr.UpstreamAuth(err.Error())
	}

	setDeepSeekHeaders(req, token, powHeader)
	if h := cookies.Header(c.cookies.Load()); h != "" {
		req.Header.Set("Cookie", h)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, token, proxyerr.Network(fmt.Sprintf("DeepSeek request failed: %v", err))
	}
	c.cookies.UpdateFromResponse(resp)

	if isCloudflareResp(resp) {
		resp.Body.Close()
		return nil, token, proxyerr.Cloudflare("DeepSeek request blocked by Cloudflare")
	}
	if err := checkResponse(resp); err != nil {
		resp.Body.Close()
		return nil, token, err
	}
	if err := checkEnvelope(resp); err != nil {
		return nil, token, err
	}
	return resp, token, nil
}

// powHeader fetches a PoW challenge and returns the solved base64 header value.
func (c *Client) powHeader(ctx context.Context) (string, error) {
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

// checkEnvelope catches errors that DeepSeek sends as HTTP 200 with a JSON body
// such as {"code":40003,"msg":"INVALID_TOKEN"}. Streams (text/event-stream) are
// left alone. For JSON it reads the body and puts it back for the caller.
func checkEnvelope(resp *http.Response) error {
	if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if err != nil {
		return proxyerr.Network(fmt.Sprintf("reading DeepSeek response: %v", err))
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))

	var env struct {
		Code *int   `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			BizCode int    `json:"biz_code"`
			BizMsg  string `json:"biz_msg"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &env) != nil || env.Code == nil {
		return nil
	}
	if *env.Code == 40001 || *env.Code == 40002 || *env.Code == 40003 || strings.Contains(strings.ToUpper(env.Msg), "TOKEN") {
		return proxyerr.UpstreamAuth(fmt.Sprintf("DeepSeek rejected the token (%d %s)", *env.Code, env.Msg))
	}
	if *env.Code != 0 {
		return proxyerr.API(fmt.Sprintf("DeepSeek error %d: %s", *env.Code, env.Msg), 502)
	}
	if env.Data.BizCode != 0 {
		return proxyerr.API(fmt.Sprintf("DeepSeek error %d: %s", env.Data.BizCode, env.Data.BizMsg), 502)
	}
	return nil
}

func setDeepSeekHeaders(req *http.Request, token, powHeader string) {
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
	if powHeader != "" {
		req.Header.Set("x-ds-pow-response", powHeader)
	}
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
		return proxyerr.UpstreamAuth("DeepSeek rejected the token (HTTP 401)")
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
	ct := resp.Header.Get("Content-Type")
	return ct != "" && (resp.Header.Get("Server") == "cloudflare" || resp.Header.Get("CF-RAY") != "")
}

// newUTLSTransport returns an http.RoundTripper using utls Chrome-120 fingerprint.
// It negotiates ALPN and speaks HTTP/2 or HTTP/1.1 accordingly.
func newUTLSTransport() http.RoundTripper {
	return &utlsTransport{}
}

type utlsTransport struct{}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}

	tcpConn, err := net.Dial("tcp", host+":"+port)
	if err != nil {
		return nil, err
	}

	uconn := utls.UClient(tcpConn, &utls.Config{
		ServerName: host,
		// Allow the server to send h2 in ALPN; utls Chrome_120 includes it.
		InsecureSkipVerify: false,
	}, utls.HelloChrome_120)

	if err := uconn.Handshake(); err != nil {
		tcpConn.Close()
		return nil, err
	}

	switch uconn.ConnectionState().NegotiatedProtocol {
	case "h2":
		// HTTP/2 over utls connection
		t2 := &http2.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				// Return the already-handshaked utls conn on first call.
				// Subsequent calls (keep-alive reuse) get a fresh utls conn.
				c, err2 := net.Dial("tcp", addr)
				if err2 != nil {
					return nil, err2
				}
				uc := utls.UClient(c, &utls.Config{ServerName: host}, utls.HelloChrome_120)
				if err2 := uc.Handshake(); err2 != nil {
					c.Close()
					return nil, err2
				}
				return uc, nil
			},
		}
		// Use the pre-handshaked conn directly by wrapping it.
		// http2.Transport.NewClientConn lets us inject an existing conn.
		cc, err := t2.NewClientConn(uconn)
		if err != nil {
			uconn.Close()
			return nil, err
		}
		return cc.RoundTrip(req)

	default:
		// HTTP/1.1 over utls connection
		t1 := &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return uconn, nil
			},
		}
		return t1.RoundTrip(req)
	}
}
