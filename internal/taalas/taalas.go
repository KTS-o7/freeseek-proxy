package taalas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const taalasBaseURL = "https://chatjimmy.ai/api"
const statsStart    = "<|stats|>"
const statsEnd      = "<|/stats|>"
const taalasUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

var modelMap = map[string]string{
	"taalas-llama3.1-8b": "llama3.1-8B",
}

// Complete sends a request to chatjimmy.ai and returns the response text.
func Complete(messages []map[string]interface{}, model string) (string, error) {
	taalasModel, ok := modelMap[model]
	if !ok {
		taalasModel = "llama3.1-8B"
	}

	var prompt string
	if len(messages) == 1 {
		prompt, _ = messages[0]["content"].(string)
	} else {
		var lines []string
		for _, m := range messages {
			role, _ := m["role"].(string)
			content, _ := m["content"].(string)
			r := strings.ToUpper(role[:1]) + role[1:]
			lines = append(lines, r+": "+content)
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
	req, err := http.NewRequest(http.MethodPost, taalasBaseURL+"/chat", bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("taalas: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://chatjimmy.ai")
	req.Header.Set("Referer", "https://chatjimmy.ai/")
	req.Header.Set("User-Agent", taalasUA)

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
