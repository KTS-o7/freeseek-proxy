package main

import (
	"encoding/json"
	"net/http"

	"github.com/KTS-o7/freeseek-proxy/internal/minimax"
)

type model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func handleModels(mm *minimax.Client) http.HandlerFunc {
	models := []model{
		// chat.deepseek.com picks the model version. deepseek-reasoner turns thinking on.
		// Old names (deepseek-v3, -coder, -r1, -expert) still work as aliases.
		{ID: "deepseek-chat", Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
		{ID: "deepseek-reasoner", Object: "model", Created: 1704067200, OwnedBy: "deepseek"},
		{ID: "taalas-llama3.1-8b", Object: "model", Created: 1704067200, OwnedBy: "taalas"},
	}
	if mm.Enabled() {
		for _, id := range minimax.Models {
			models = append(models, model{ID: id, Object: "model", Created: 1704067200, OwnedBy: "minimax"})
		}
	}
	resp := map[string]interface{}{"object": "list", "data": models}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
