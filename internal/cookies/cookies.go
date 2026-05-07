package cookies

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KTS-o7/freeseek-proxy/internal/config"
)

type Manager struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Load merges cookies from file and from DEEPSEEK_COOKIES env var.
func (m *Manager) Load() map[string]string {
	cookies := m.loadFile()
	for k, v := range parseString(m.cfg.Cookies) {
		cookies[k] = v
	}
	return cookies
}

// UpdateFromResponse merges Set-Cookie headers into the saved cookie file.
func (m *Manager) UpdateFromResponse(resp *http.Response) {
	if resp == nil {
		return
	}
	extra := map[string]string{}
	for _, c := range resp.Cookies() {
		if c.Name != "" {
			extra[c.Name] = c.Value
		}
	}
	if len(extra) == 0 {
		return
	}
	merged := m.Load()
	for k, v := range extra {
		merged[k] = v
	}
	m.saveFile(merged)
}

// Refresh runs the configured refresh command and reloads cookies.
func (m *Manager) Refresh() (map[string]string, error) {
	if m.cfg.CookieRefreshCommand == "" {
		return m.Load(), nil
	}
	cmd := exec.Command("sh", "-c", m.cfg.CookieRefreshCommand)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return m.Load(), nil
}

func (m *Manager) loadFile() map[string]string {
	path := m.cookieFilePath()
	if path == "" {
		return map[string]string{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var flat map[string]string
	if json.Unmarshal(data, &flat) == nil {
		return flat
	}
	var list []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.Unmarshal(data, &list) == nil {
		out := map[string]string{}
		for _, item := range list {
			if item.Name != "" {
				out[item.Name] = item.Value
			}
		}
		return out
	}
	return map[string]string{}
}

func (m *Manager) saveFile(cookies map[string]string) {
	path := m.cookieFilePath()
	if path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.MarshalIndent(cookies, "", "  ")
	_ = os.WriteFile(path, b, 0o600)
}

func (m *Manager) cookieFilePath() string {
	if m.cfg.CookieFile != "" {
		return m.cfg.CookieFile
	}
	if _, err := os.Stat("cookies.json"); err == nil {
		return "cookies.json"
	}
	return ""
}

// parseString parses "key=value; key2=value2" cookie strings.
func parseString(raw string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		idx := strings.Index(part, "=")
		k := strings.TrimSpace(part[:idx])
		v := strings.TrimSpace(part[idx+1:])
		if k != "" {
			out[k] = v
		}
	}
	return out
}

// Header returns cookies formatted as a Cookie header value.
func Header(cookies map[string]string) string {
	parts := make([]string, 0, len(cookies))
	for k, v := range cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}
