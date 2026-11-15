package main

import (
	"crypto/subtle"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"

	"github.com/KTS-o7/freeseek-proxy/internal/assets"
	"github.com/KTS-o7/freeseek-proxy/internal/auth"
	"github.com/KTS-o7/freeseek-proxy/internal/client"
	"github.com/KTS-o7/freeseek-proxy/internal/config"
	"github.com/KTS-o7/freeseek-proxy/internal/cookies"
	proxyerr "github.com/KTS-o7/freeseek-proxy/internal/errors"
	"github.com/KTS-o7/freeseek-proxy/internal/minimax"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	cookieMgr := cookies.New(cfg)
	authMgr := auth.New(cfg)
	deepseekClient := client.New(authMgr, cookieMgr)
	minimaxClient := minimax.New(cfg.MiniMaxBaseURL, cfg.MiniMaxAPIKey)
	if minimaxClient.Enabled() && cfg.APIKey == "" {
		log.Printf("WARNING: MINIMAX_API_KEY is set but API_KEY is empty; anyone who can reach this proxy can spend your MiniMax quota")
	}

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

	r.Get("/v1/models", handleModels(minimaxClient))

	r.Group(func(r chi.Router) {
		if cfg.APIKey != "" {
			r.Use(apiKeyMiddleware(cfg.APIKey))
		}
		r.Post("/v1/chat/completions", handleChatCompletions(deepseekClient, minimaxClient, cfg.KeepSessions))
	})

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	log.Printf("freeseek-proxy listening on http://%s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	want := []byte("Bearer " + key)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Constant-time compare so response timing does not leak the key.
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
				proxyerr.WriteJSON(w, proxyerr.Auth("Invalid API key"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
