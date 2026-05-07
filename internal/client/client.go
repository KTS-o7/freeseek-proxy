package client

import (
	"bytes"
	"context"
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
	resp, err := c.doPost(ctx, path, body)
	if err != nil {
		return nil, err
	}
	if isCloudflareResp(resp) {
		resp.Body.Close()
		if _, refreshErr := c.cookies.Refresh(); refreshErr != nil {
			return nil, proxyerr.Cloudflare(fmt.Sprintf("Cloudflare block; cookie refresh failed: %v", refreshErr))
		}
		return c.doPost(ctx, path, body)
	}
	return resp, nil
}

func (c *Client) doPost(ctx context.Context, path string, body interface{}) (*http.Response, error) {
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

	setDeepSeekHeaders(req, token, "")
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

	setDeepSeekHeaders(req, token, powHeader)
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
		if _, refreshErr := c.cookies.Refresh(); refreshErr != nil {
			return nil, proxyerr.Cloudflare(fmt.Sprintf("Cloudflare block; cookie refresh failed: %v", refreshErr))
		}
		return c.PostWithPow(ctx, path, body, stream)
	}

	if err := checkResponse(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp, nil
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
	ct := resp.Header.Get("Content-Type")
	return ct != "" && (resp.Header.Get("Server") == "cloudflare" || resp.Header.Get("CF-RAY") != "")
}

// newUTLSTransport returns an http.RoundTripper using utls Chrome-120 fingerprint.
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

	conn, err := net.Dial("tcp", host+":"+port)
	if err != nil {
		return nil, err
	}

	uconn := utls.UClient(conn, &utls.Config{
		ServerName: host,
	}, utls.HelloChrome_120)

	if err := uconn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	// Use a fresh transport with the already-handshaked TLS conn
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return uconn, nil
		},
	}
	return transport.RoundTrip(req)
}
