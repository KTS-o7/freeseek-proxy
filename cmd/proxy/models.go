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
			// chat.deepseek.com picks the model version; these name its two modes.
			// Old names (deepseek-v3, -r1, -coder, -reasoner) still work as aliases.
			{ID: "deepseek-chat", Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "deepseek-expert", Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
			{ID: "taalas-llama3.1-8b", Object: "model", Created: 1704067200, OwnedBy: "taalas"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
