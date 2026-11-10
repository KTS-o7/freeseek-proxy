package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/KTS-o7/freeseek-proxy/internal/config"
)

const loginURL = "https://chat.deepseek.com/api/v0/users/login"

// Manager hands out a DeepSeek bearer token. Sources, in order: DEEPSEEK_AUTH_TOKEN,
// the saved login file, then a fresh email+password login. A token that DeepSeek
// rejects is marked bad and skipped from then on, so the next source gets a turn.
type Manager struct {
	cfg   *config.Config
	mu    sync.Mutex
	token string
	bad   map[string]bool
}

func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, bad: map[string]bool{}}
}

// Token returns a DeepSeek bearer token, loading or fetching one as needed.
func (m *Manager) Token() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" {
		return m.token, nil
	}
	if t := m.cfg.AuthToken; t != "" && !m.bad[t] {
		m.token = t
		return t, nil
	}
	if t := m.tokenFromFile(); t != "" && !m.bad[t] {
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

// Invalidate marks a token that DeepSeek rejected, so Token() moves on to the next source.
func (m *Manager) Invalidate(token string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if token == "" {
		return
	}
	m.bad[token] = true
	if m.token == token {
		m.token = ""
	}
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
		if m.cfg.AuthToken != "" {
			return "", nil, fmt.Errorf("DeepSeek rejected DEEPSEEK_AUTH_TOKEN (expired or revoked). Put a fresh userToken from chat.deepseek.com in DEEPSEEK_AUTH_TOKEN, or set DEEPSEEK_EMAIL and DEEPSEEK_PASSWORD so the proxy can log in itself")
		}
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
		return "", nil, fmt.Errorf("DeepSeek login failed: no token in response (wrong password, or the account signs in with Google)")
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
			BizData struct {
				User struct {
					Token string `json:"token"`
				} `json:"user"`
			} `json:"biz_data"`
			User struct {
				Token string `json:"token"`
			} `json:"user"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &v) != nil {
		return ""
	}
	if t := v.Data.BizData.User.Token; t != "" {
		return t
	}
	return v.Data.User.Token
}
