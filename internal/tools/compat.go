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
	Role       string      `json:"role"`
	Content    interface{} `json:"content"` // string or []ContentPart
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
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
			argsStr := ""
			switch a := m["arguments"].(type) {
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
