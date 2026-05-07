package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Port                 int
	APIKey               string
	AuthToken            string
	Email                string
	Password             string
	SaveLogin            bool
	LoginFile            string
	Cookies              string
	CookieFile           string
	CookieRefreshCommand string
}

func Load() *Config {
	return &Config{
		Port:                 envInt("PORT", 9123),
		APIKey:               os.Getenv("API_KEY"),
		AuthToken:            strings.TrimSpace(os.Getenv("DEEPSEEK_AUTH_TOKEN")),
		Email:                strings.TrimSpace(os.Getenv("DEEPSEEK_EMAIL")),
		Password:             os.Getenv("DEEPSEEK_PASSWORD"),
		SaveLogin:            envBool("DEEPSEEK_SAVE_LOGIN", false),
		LoginFile:            envString("DEEPSEEK_LOGIN_FILE", "login.json"),
		Cookies:              os.Getenv("DEEPSEEK_COOKIES"),
		CookieFile:           os.Getenv("DEEPSEEK_COOKIE_FILE"),
		CookieRefreshCommand: os.Getenv("DEEPSEEK_COOKIE_REFRESH_COMMAND"),
	}
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func envString(key, def string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return def
	}
	return filepath.Clean(v)
}
