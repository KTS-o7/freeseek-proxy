package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/KTS-o7/freeseek-proxy/internal/config"
)

const loginURL = "https://coder.deepseek.com/api/v0/users/login"

type Manager struct {
	cfg   *config.Config
	token string
}

func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Token returns a valid DeepSeek bearer token, loading or fetching as needed.
func (m *Manager) Token() (string, error) {
	if m.cfg.AuthToken != "" {
		return m.cfg.AuthToken, nil
	}
	if m.token != "" {
		return m.token, nil
	}
	if t := m.tokenFromFile(); t != "" {
		m.token = t
		return t, nil
	}
	t, payload, err := m.login()
	if err != nil {
		return "", err
	}
	if m.cfg.SaveLogin {
		m.saveLogin(payload)
	}
	m.token = t
	return t, nil
}

func (m *Manager) tokenFromFile() string {
	data, err := os.ReadFile(m.cfg.LoginFile)
	if err != nil {
		return ""
	}
	return extractToken(data)
}

func (m *Manager) login() (string, []byte, error) {
	if m.cfg.Email == "" || m.cfg.Password == "" {
		return "", nil, fmt.Errorf("DeepSeek auth: set DEEPSEEK_AUTH_TOKEN or DEEPSEEK_EMAIL+DEEPSEEK_PASSWORD")
	}
	body, _ := json.Marshal(map[string]string{
		"email":     m.cfg.Email,
		"mobile":    "",
		"password":  m.cfg.Password,
		"area_code": "",
	})
	resp, err := http.Post(loginURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("DeepSeek login request failed: %w", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body)
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("DeepSeek login failed: HTTP %d", resp.StatusCode)
	}
	t := extractToken(buf.Bytes())
	if t == "" {
		return "", nil, fmt.Errorf("DeepSeek login: no token in response")
	}
	return t, buf.Bytes(), nil
}

func (m *Manager) saveLogin(payload []byte) {
	_ = os.MkdirAll(filepath.Dir(m.cfg.LoginFile), 0o755)
	_ = os.WriteFile(m.cfg.LoginFile, payload, 0o600)
}

func extractToken(data []byte) string {
	var v struct {
		Data struct {
			User struct {
				Token string `json:"token"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &v) == nil {
		return v.Data.User.Token
	}
	return ""
}
