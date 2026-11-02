package guardian

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// practiceApp is a Czech app in practice mode, connected via the practice phone.
func practiceApp(t *testing.T) (*App, func(method, path string, form url.Values) *httptest.ResponseRecorder) {
	t.Helper()
	st := testStore(t)
	p := newPractice(st)
	a := newApp(st, p, Config{Language: "cs", Practice: true, SecretsDir: t.TempDir()})
	a.dev = p
	h := a.Handler()
	do := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		req := httptest.NewRequest(method, path, body)
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	do("POST", "/whatsapp/connect", url.Values{})
	do("POST", "/dev/whatsapp/scan", nil)
	return a, do
}

func TestAnalysisWithPracticeModel(t *testing.T) {
	_, do := practiceApp(t)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Analýza neběží: není připojený žádný AI model.") {
		t.Fatal("no-model notice missing")
	}
	// Six default situations, seeded once.
	sits := do("GET", "/situations", nil).Body.String()
	for _, name := range []string{"Urážky a ponižování", "Vylučování ze skupiny", "Výhrůžky", "Vydírání nebo nátlak na fotky", "Kontakt od neznámého dospělého", "Dítě je v nouzi"} {
		if !strings.Contains(sits, name) {
			t.Errorf("default situation %q missing", name)
		}
	}
	do("POST", "/settings", url.Values{"type": {"practice"}, "limit": {"50"}})
	if !strings.Contains(do("POST", "/settings/test", url.Values{}).Body.String(), "Model odpovídá.") {
		t.Fatal("practice model should answer")
	}
	page := do("GET", "/", nil).Body.String()
	if !strings.Contains(page, "Text zpráv zůstává na tvém serveru.") || strings.Contains(page, "Analýza neběží") {
		t.Fatal("overview should show a working practice model that keeps data home")
	}
	msg := func(chat, name, text string) {
		do("POST", "/dev/whatsapp/message", url.Values{"chat": {chat}, "chat_name": {name}, "sender_name": {"Kuba"}, "text": {text}})
	}
	msg("c1", "Kuba", "ahoj")
	msg("c1", "Kuba", "zítra tě zmlátím [Výhrůžky]")
	msg("c1", "Kuba", "fakt [Výhrůžky]")
	msg("c1", "Kuba", "a ještě [Výhrůžky]")
	do("POST", "/dev/analysis/run", nil)
	page = do("GET", "/", nil).Body.String()
	if !strings.Contains(page, "Nový vážný nález: Výhrůžky v chatu Kuba") {
		t.Fatal("alert banner missing")
	}
	fin := do("GET", "/findings", nil).Body.String()
	if strings.Count(fin, "Zkušební nález.") != 1 || strings.Count(fin, "<blockquote>") != 2 || strings.Contains(fin, "ahoj") {
		t.Fatalf("findings page wrong:\n%s", fin)
	}
	if strings.Contains(do("GET", "/", nil).Body.String(), "Nový vážný nález") {
		t.Fatal("banner should go once the findings were opened")
	}
	// Same situation, same chat, while open: adds to the finding.
	msg("c1", "Kuba", "znova [Výhrůžky]")
	do("POST", "/dev/analysis/run", nil)
	if n := strings.Count(do("GET", "/findings", nil).Body.String(), "Zkušební nález."); n != 1 {
		t.Fatalf("expected one finding, got %d", n)
	}
	// Model down: says so, messages wait, nothing lost.
	do("POST", "/dev/model/unreachable", url.Values{"on": {"1"}})
	msg("c2", "Eva", "[Výhrůžky]")
	do("POST", "/dev/analysis/run", nil)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Analýza se nepovedla: zkušební model je nedostupný") {
		t.Fatal("failure not shown")
	}
	do("POST", "/dev/model/unreachable", url.Values{"on": {"0"}})
	do("POST", "/dev/analysis/run", nil)
	if !strings.Contains(do("GET", "/findings", nil).Body.String(), "v chatu Eva") {
		t.Fatal("waiting message was lost")
	}
}

func TestDailyLimit(t *testing.T) {
	_, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}, "limit": {"1"}})
	for _, c := range []string{"a", "b"} {
		do("POST", "/dev/whatsapp/message", url.Values{"chat": {c}, "chat_name": {c}, "text": {"[Výhrůžky]"}})
	}
	do("POST", "/dev/analysis/run", nil)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Denní limit dotazů je vyčerpaný, analýza pokračuje zítra.") {
		t.Fatal("limit notice missing")
	}
}

func TestSituationsEditable(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/situations", url.Values{"name": {"Hry o peníze"}, "description": {"Sázky a hazard."}, "sensitivity": {"high"}, "action": {"record"}, "enabled": {"1"}})
	sits, _ := a.store.Situations(context.Background())
	var added, builtin Situation
	for _, s := range sits {
		if s.Name == "Hry o peníze" {
			added = s
		}
		if s.Builtin && builtin.ID == 0 {
			builtin = s
		}
	}
	if added.ID == 0 || added.Builtin || !added.Enabled {
		t.Fatalf("added: %+v", added)
	}
	do("POST", "/situations/"+itoa(builtin.ID), url.Values{"delete": {"1"}})
	do("POST", "/situations/"+itoa(added.ID), url.Values{"delete": {"1"}})
	sits2, _ := a.store.Situations(context.Background())
	if len(sits2) != len(sits)-1 {
		t.Fatalf("builtin must stay, added must go: %d -> %d", len(sits), len(sits2))
	}
	b := builtin
	do("POST", "/situations/"+itoa(b.ID), url.Values{"name": {b.Name}, "description": {b.Description}, "sensitivity": {"low"}, "action": {"record"}})
	got, _ := a.store.Situation(context.Background(), b.ID)
	if got.Enabled || got.Sensitivity != "low" {
		t.Fatalf("switch off failed: %+v", got)
	}
}

func TestAnthropicSettingsKeepKeySecret(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"anthropic"}, "key": {"sk-ant-secret-wxyz"}})
	page := do("GET", "/settings", nil).Body.String()
	if strings.Contains(page, "sk-ant-secret") || !strings.Contains(page, "…wxyz") {
		t.Fatal("key leaked or hint missing")
	}
	if !strings.Contains(page, "https://api.anthropic.com") || !strings.Contains(page, "claude-sonnet-5-5") ||
		!strings.Contains(page, "Text zpráv se k vyhodnocení posílá do: api.anthropic.com") {
		t.Fatal("anthropic defaults or destination missing")
	}
	if v, _ := a.store.Setting(context.Background(), "model_key"); v != "" {
		t.Fatal("key must not be in the database")
	}
}

func TestDestination(t *testing.T) {
	for addr, want := range map[string]string{
		"https://api.anthropic.com":    "api.anthropic.com",
		"http://localhost:11434/v1":    "",
		"http://192.168.1.5:1234/v1":   "",
		"https://openrouter.ai/api/v1": "openrouter.ai",
	} {
		if got := (modelSettings{Type: modelOpenAI, Address: addr}).destination(); got != want {
			t.Errorf("%s: %q, want %q", addr, got, want)
		}
	}
}

func TestParseFindings(t *testing.T) {
	sits := []Situation{{ID: 3, Name: "Výhrůžky"}}
	fresh := []numbered{{Message: Message{Text: "a"}}, {Message: Message{Text: "b"}}}
	got, err := parseFindings("Here you go:\n```json\n{\"findings\":[{\"situation\":3,\"reason\":\"Někdo vyhrožuje.\",\"quotes\":[4,5,9,1]},{\"situation\":99,\"reason\":\"x\",\"quotes\":[]}]}\n```", sits, fresh, 4)
	if err != nil || len(got) != 1 || len(got[0].Quotes) != 2 || got[0].Quotes[0].Text != "a" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := parseFindings("I cannot help", sits, fresh, 1); err == nil {
		t.Fatal("nonsense should be an error")
	}
}

func TestRealModelsSpeakTheirAPIs(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-api-key")+r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			io.WriteString(w, `{"content":[{"type":"text","text":"{\"findings\":[]}"}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ans, err := anthropicModel{modelSettings{Address: srv.URL, Model: "m", Key: "k1"}}.Ask(ctx, "s", "q")
	if err != nil || ans != `{"findings":[]}` || gotPath != "/v1/messages" || gotKey != "k1" {
		t.Fatalf("anthropic: %q %v %s %s", ans, err, gotPath, gotKey)
	}
	ans, err = openAIModel{modelSettings{Address: srv.URL + "/v1", Model: "m", Key: "k2"}}.Ask(ctx, "s", "q")
	if err != nil || ans != "OK" || gotPath != "/v1/chat/completions" || gotKey != "Bearer k2" {
		t.Fatalf("openai: %q %v %s %s", ans, err, gotPath, gotKey)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
