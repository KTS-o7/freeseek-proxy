package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
	Model           string          `json:"model"`
	Messages        []tools.Message `json:"messages"`
	Stream          bool            `json:"stream"`
	Tools           []tools.Tool    `json:"tools"`
	ToolChoice      interface{}     `json:"tool_choice"`
	ThinkingEnabled bool            `json:"thinking_enabled"`
	SearchEnabled   bool            `json:"search_enabled"`
	ParentMessageID interface{}     `json:"parent_message_id"`
	SessionID       string          `json:"sessionId"`
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

func handleDeepSeek(w http.ResponseWriter, r *http.Request, req chatRequest, model string, c *client.Client) {
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
		"chat_session_id":   sessionID,
		"parent_message_id": normalizeParentID(req.ParentMessageID),
		"model_type":        modelType,
		"prompt":            prompt,
		"ref_file_ids":      []interface{}{},
		"thinking_enabled":  req.ThinkingEnabled,
		"search_enabled":    req.SearchEnabled,
		"preempt":           false,
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
		fullText, msgID := proxy.CollectFull(resp.Body, req.ThinkingEnabled)
		parsed := tools.ParseEnvelope(fullText, req.Tools)

		if req.Stream {
			streamToolCompat(w, model, sessionID, msgID, fullText, parsed)
		} else {
			if parsed != nil {
				writeToolCallResponse(w, model, sessionID, msgID, parsed, promptText, fullText)
			} else {
				proxy.WriteNonStreaming(w, model, promptText, fullText, msgID, sessionID)
			}
		}
		return
	}

	streamDeepSeek(w, resp, model, sessionID, req.ThinkingEnabled)
}

func createSession(ctx context.Context, c *client.Client) (string, error) {
	resp, err := c.Post(ctx, "/chat_session/create", map[string]interface{}{"character_id": nil}, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Data struct {
			BizData struct {
				ChatSession struct {
					ID string `json:"id"`
				} `json:"chat_session"`
				ID string `json:"id"`
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

func streamDeepSeek(w http.ResponseWriter, resp *http.Response, model, sessionID string, thinking bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		proxyerr.WriteJSON(w, proxyerr.Server("streaming not supported"))
		return
	}

	id := proxy.ChatCmplID()
	created := time.Now().Unix()

	proxy.SetStreamHeaders(w, sessionID, nil)
	proxy.StreamChunk(w, flusher, proxy.RoleChunk(id, created, model))

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		evt := proxy.ParseLine(scanner.Text(), thinking)
		if evt == nil {
			continue
		}
		if evt.Content != "" {
			proxy.StreamChunk(w, flusher, proxy.ContentChunk(id, created, model, evt.Content))
		}
	}

	proxy.StreamChunk(w, flusher, proxy.StopChunk(id, created, model))
	proxy.StreamDone(w, flusher)
}

func streamToolCompat(w http.ResponseWriter, model, sessionID string, msgID interface{}, fullText string, toolCalls []tools.ToolCall) {
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
			"total_tokens":      (len(promptText) + len(fullText) + 3) / 4,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("x-chat-session-id", sessionID)
	if msgID != nil {
		w.Header().Set("x-parent-message-id", fmt.Sprintf("%v", msgID))
	}
	json.NewEncoder(w).Encode(body)
}

func handleTaalas(w http.ResponseWriter, r *http.Request, req chatRequest, model string) {
	msgs := make([]map[string]interface{}, len(req.Messages))
	for i, m := range req.Messages {
		c := ""
		if s, ok := m.Content.(string); ok {
			c = s
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
			"total_tokens":      (len(promptText) + len(text) + 3) / 4,
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}

func extractLastMessage(messages []tools.Message) string {
	if len(messages) == 0 {
		return ""
	}
	m := messages[len(messages)-1]
	if s, ok := m.Content.(string); ok {
		return s
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
