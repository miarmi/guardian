package guardian

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Model types (SPEC.md criterion 12).
const (
	modelAnthropic = "anthropic"
	modelOpenAI    = "openai"
	modelPractice  = "practice"

	defaultAnthropicAddress = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-sonnet-5-5"

	modelKeyFile = "model-key" // in secrets/, never in the database
)

// modelSettings is what Nastavení holds; the key lives in secrets/ only.
type modelSettings struct {
	Type    string
	Address string
	Model   string
	Key     string
	Limit   int
}

// ready says whether enough is filled in to ask the model anything.
func (m modelSettings) ready() bool {
	switch m.Type {
	case modelAnthropic:
		return m.Key != "" && m.Address != "" && m.Model != ""
	case modelOpenAI:
		return m.Address != "" && m.Model != ""
	case modelPractice:
		return true
	}
	return false
}

// useAnthropicDefaults switches to Anthropic's address and model.
func (m *modelSettings) useAnthropicDefaults() {
	m.Type, m.Address, m.Model = modelAnthropic, defaultAnthropicAddress, defaultAnthropicModel
}

// destination says who receives the messages' text (criterion 13): a host,
// or "" when it stays on this server or a private network.
func (m modelSettings) destination() string {
	if m.Type == modelPractice {
		return ""
	}
	u, err := url.Parse(m.Address)
	if err != nil || u.Hostname() == "" {
		return m.Address
	}
	host := u.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return ""
	}
	return host
}

// sameEndpoint says whether a key saved for one model may be used for
// another: same type and same host (criterion 12).
func sameEndpoint(x, y modelSettings) bool {
	return x.Type == y.Type && hostOf(x.Address) == hostOf(y.Address)
}

func hostOf(addr string) string {
	if u, err := url.Parse(strings.TrimSpace(addr)); err == nil {
		return strings.ToLower(u.Host)
	}
	return addr
}

// keyHint shows the last four characters of a saved secret: "…abcd".
func keyHint(k string) string {
	if k == "" {
		return ""
	}
	if n := utf8.RuneCountInString(k); n > 4 {
		k = string([]rune(k)[n-4:])
	}
	return "…" + k
}

// readModelSettings reads the saved rows, without the key and without
// filling in defaults.
func readModelSettings(ctx context.Context, st *store) modelSettings {
	v := st.Settings(ctx, "model_type", "model_address", "model_model", "model_limit")
	m := modelSettings{Type: v["model_type"], Address: v["model_address"], Model: v["model_model"], Limit: defaultLimit}
	if n, err := strconv.Atoi(v["model_limit"]); err == nil && n >= 0 {
		m.Limit = n
	}
	return m
}

// previewSettings reads the saved model settings without any key, for the
// locked form in previews.
func previewSettings(ctx context.Context, st *store) modelSettings {
	m := readModelSettings(ctx, st)
	if m.Type == "" || m.Type == modelPractice {
		m.useAnthropicDefaults()
	}
	return m
}

// settings reads Nastavení; the key comes from secrets/ only.
func (z *analyzer) settings(ctx context.Context) modelSettings {
	m := readModelSettings(ctx, z.st)
	if m.Type == "" {
		m.useAnthropicDefaults()
	}
	if m.Type == modelPractice && z.practice == nil {
		m.Type = "" // the practice model exists only in practice mode
	}
	m.Key = readSecret(z.secretsDir, modelKeyFile)
	return m
}

// saveSettings saves the form. A new key replaces the saved one; with
// forgetKey the saved key is removed first (it belonged to another host).
func (z *analyzer) saveSettings(ctx context.Context, m modelSettings, newKey string, forgetKey bool) error {
	for k, v := range map[string]string{"model_type": m.Type, "model_address": m.Address, "model_model": m.Model, "model_limit": strconv.Itoa(m.Limit)} {
		if err := z.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	if forgetKey {
		if err := os.Remove(filepath.Join(z.secretsDir, modelKeyFile)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if newKey != "" {
		if err := writeSecret(z.secretsDir, modelKeyFile, newKey); err != nil {
			return err
		}
	}
	z.mu.Lock()
	z.lastErr, z.limited = "", ""
	z.mu.Unlock()
	return nil
}
