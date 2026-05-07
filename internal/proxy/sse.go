package proxy

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// SSEEvent holds parsed content and optional message ID from one DeepSeek SSE line.
type SSEEvent struct {
	Content           string
	ResponseMessageID interface{} // number or string
}

type deepSeekSSE struct {
	V             interface{} `json:"v"` // string or object
	P             string      `json:"p"`
	ResponseMsgID interface{} `json:"response_message_id"`
	Content       string      `json:"content"`
}

// ParseLine parses one "data: ..." SSE line. Returns nil when the line should be skipped.
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

	if d.ResponseMsgID != nil {
		evt.ResponseMessageID = d.ResponseMsgID
	}

	// Skip status/usage events
	if d.P == "response/status" || d.P == "response/accumulated_token_usage" {
		return evt
	}
	// Skip thinking fragments when not requested
	if !includeThinking && strings.Contains(d.P, "thinking") {
		return evt
	}

	switch v := d.V.(type) {
	case string:
		evt.Content = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp struct {
			Response struct {
				MessageID interface{} `json:"message_id"`
				Fragments []struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				} `json:"fragments"`
			} `json:"response"`
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
