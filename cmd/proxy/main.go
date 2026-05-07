package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/KTS-o7/freeseek-proxy/internal/assets"
	"github.com/KTS-o7/freeseek-proxy/internal/auth"
	"github.com/KTS-o7/freeseek-proxy/internal/client"
	"github.com/KTS-o7/freeseek-proxy/internal/config"
	"github.com/KTS-o7/freeseek-proxy/internal/cookies"
	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	cookieMgr := cookies.New(cfg)
	authMgr := auth.New(cfg)
	deepseekClient := client.New(authMgr, cookieMgr)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		data, _ := assets.StaticFiles.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.Get("/v1/models", handleModels)

	r.Group(func(r chi.Router) {
		if cfg.APIKey != "" {
			r.Use(apiKeyMiddleware(cfg.APIKey))
		}
		r.Post("/v1/chat/completions", handleChatCompletions(deepseekClient))
	})

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("freeseek-proxy listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader != "Bearer "+key {
				proxyerr.WriteJSON(w, proxyerr.Auth("Invalid API key"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
