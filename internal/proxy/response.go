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
	if sessionID != "" {
		w.Header().Set("x-chat-session-id", sessionID)
	}
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
