package guardian

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Model types (SPEC.md criterion 12).
const (
	modelAnthropic = "anthropic"
	modelOpenAI    = "openai"
	modelPractice  = "practice"

	defaultAnthropicAddress = "https://api.anthropic.com"
	defaultAnthropicModel   = "claude-sonnet-5-5"
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

// chatModel asks a model one question with a system prompt and returns its
// text answer.
type chatModel interface {
	Ask(ctx context.Context, system, question string) (string, error)
}

var modelHTTP = &http.Client{Timeout: 3 * time.Minute}

func newChatModel(m modelSettings) (chatModel, error) {
	switch m.Type {
	case modelAnthropic:
		return anthropicModel{m}, nil
	case modelOpenAI:
		return openAIModel{m}, nil
	}
	return nil, fmt.Errorf("unknown model type %q", m.Type)
}

type anthropicModel struct{ m modelSettings }

func (a anthropicModel) Ask(ctx context.Context, system, question string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      a.m.Model,
		"max_tokens": 4000,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": question}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.m.Address, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.m.Key)
	req.Header.Set("anthropic-version", "2023-06-01")
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New(out.Error.Message)
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

type openAIModel struct{ m modelSettings }

func (o openAIModel) Ask(ctx context.Context, system, question string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": o.m.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": question},
		},
	})
	addr := strings.TrimRight(o.m.Address, "/")
	if !strings.HasSuffix(addr, "/chat/completions") {
		addr += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.m.Key != "" {
		req.Header.Set("Authorization", "Bearer "+o.m.Key)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := doJSON(req, &out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", errors.New(out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("the answer was empty")
	}
	return out.Choices[0].Message.Content, nil
}

// doJSON sends a request and decodes a JSON answer, turning HTTP errors into
// short readable reasons.
func doJSON(req *http.Request, out any) error {
	resp, err := modelHTTP.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return uerr.Err
		}
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, e.Error.Message)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return errors.New("the answer was not JSON")
	}
	return nil
}
