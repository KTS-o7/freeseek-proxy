// Package minimax forwards OpenAI-format chat requests to the MiniMax API.
//
// MiniMax already speaks the OpenAI chat format, so the request body goes through
// almost unchanged. The proxy swaps the client's key for the MiniMax key and copies
// the reply (JSON or SSE) back.
package minimax

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
)

// DefaultBaseURL is the international OpenAI-compatible endpoint.
const DefaultBaseURL = "https://api.minimax.io/v1"

// Models are the model IDs listed in /v1/models when MiniMax is configured.
var Models = []string{"MiniMax-M3", "MiniMax-M3.1-Flash"}

// Fields only this proxy understands. They are removed before forwarding.
var proxyOnlyFields = []string{"thinking_enabled", "search_enabled", "parent_message_id", "sessionId"}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{}, // no total timeout: streams and long thinking runs; the request context cancels it
	}
}

// Enabled reports whether a MiniMax key is configured.
func (c *Client) Enabled() bool { return c != nil && c.apiKey != "" }

// IsModel reports whether a model ID belongs to MiniMax.
func IsModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "minimax-")
}

// prepareBody removes proxy-only fields and asks MiniMax to return thinking in
// reasoning_content (not <think> tags in content) unless the client chose otherwise.
func prepareBody(raw []byte) ([]byte, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	for _, f := range proxyOnlyFields {
		delete(body, f)
	}
	if _, ok := body["reasoning_split"]; !ok {
		body["reasoning_split"] = json.RawMessage("true")
	}
	return json.Marshal(body)
}

// Forward sends the chat request to MiniMax and copies the reply to w.
func (c *Client) Forward(w http.ResponseWriter, r *http.Request, raw []byte) {
	body, err := prepareBody(raw)
	if err != nil {
		proxyerr.WriteJSON(w, proxyerr.Invalid("could not parse request body as JSON"))
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		proxyerr.WriteJSON(w, proxyerr.Server("minimax: build request: "+err.Error()))
		return
	}
	// Only these headers go upstream. The client's Authorization header (the proxy key) never does.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if a := r.Header.Get("Accept"); a != "" {
		req.Header.Set("Accept", a)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			return // client went away
		}
		proxyerr.WriteJSON(w, proxyerr.Network("minimax: request failed: "+err.Error()))
		return
	}
	defer resp.Body.Close()

	// A bad MiniMax key is a server problem. Report it as 503 so clients do not
	// think their proxy API key is wrong.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		proxyerr.WriteJSON(w, proxyerr.UpstreamAuth(fmt.Sprintf("minimax rejected the server's MiniMax key (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))))
		return
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			return
		}
	}
}
