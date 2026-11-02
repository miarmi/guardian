package guardian

import (
	"context"
	"errors"
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
	for _, name := range []string{"Opakované ponižování", "Vyřazování ze skupiny", "Výhrůžky násilím", "Vydírání intimními fotkami", "Kontakt od cizího dospělého", "Dítě v nouzi"} {
		if !strings.Contains(sits, name) {
			t.Errorf("default situation %q missing", name)
		}
	}
	do("POST", "/settings", url.Values{"type": {"practice"}, "limit": {"50"}})
	if !strings.Contains(do("POST", "/settings/test", url.Values{"type": {"practice"}}).Body.String(), "Model odpovídá.") {
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
	msg("c1", "Kuba", "zítra tě zmlátím [Výhrůžky násilím]")
	msg("c1", "Kuba", "fakt [Výhrůžky násilím]")
	msg("c1", "Kuba", "a ještě [Výhrůžky násilím]")
	do("POST", "/dev/analysis/run", nil)
	page = do("GET", "/", nil).Body.String()
	if !strings.Contains(page, "Nový vážný nález: Výhrůžky násilím v chatu Kuba") {
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
	msg("c1", "Kuba", "znova [Výhrůžky násilím]")
	do("POST", "/dev/analysis/run", nil)
	if n := strings.Count(do("GET", "/findings", nil).Body.String(), "Zkušební nález."); n != 1 {
		t.Fatalf("expected one finding, got %d", n)
	}
	// Model down: says so, messages wait, nothing lost.
	do("POST", "/dev/model/unreachable", url.Values{"on": {"1"}})
	msg("c2", "Eva", "[Výhrůžky násilím]")
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
		do("POST", "/dev/whatsapp/message", url.Values{"chat": {c}, "chat_name": {c}, "text": {"[Výhrůžky násilím]"}})
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
	do("POST", "/situations/"+itoa(builtin.ID), url.Values{"delete": {"1"}, "confirmed": {"1"}})
	do("POST", "/situations/"+itoa(added.ID), url.Values{"delete": {"1"}, "confirmed": {"1"}})
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
	sits := []Situation{{ID: 3, Name: "Výhrůžky násilím"}}
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

func TestTestUsesTheFormAndKeepsIt(t *testing.T) {
	_, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	// Anthropic without a key: tested as typed, not as saved; form kept.
	page := do("POST", "/settings/test", url.Values{"type": {"anthropic"}}).Body.String()
	if strings.Contains(page, "Model odpovídá.") || !strings.Contains(page, "Chybí klíč.") || strings.Contains(page, "Vyzkoušeno:") {
		t.Fatal("tested the saved model instead of the form, or claimed a try")
	}
	if !strings.Contains(page, `<option value="anthropic" selected>`) {
		t.Fatal("form not kept")
	}
}

func TestKeyIsForgottenForAnotherAddress(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"anthropic"}, "key": {"sk-ant-secret-wxyz"}})
	rec := do("POST", "/settings", url.Values{"type": {"openai"}, "address": {"http://192.168.1.50:11434/v1"}, "model": {"llama3"}})
	if !strings.Contains(rec.Header().Get("Location"), "forgot=1") {
		t.Fatal("forgetting not reported")
	}
	if a.an.settings(context.Background()).Key != "" {
		t.Fatal("key kept for another address")
	}
	if !strings.Contains(do("GET", "/settings?forgot=1", nil).Body.String(), "Klíč patří k jiné adrese, zadej nový.") {
		t.Fatal("message missing")
	}
	// Same type and host keeps the key.
	do("POST", "/settings", url.Values{"type": {"openai"}, "address": {"http://192.168.1.50:11434/v1"}, "model": {"llama3"}, "key": {"k-1234"}})
	do("POST", "/settings", url.Values{"type": {"openai"}, "address": {"http://192.168.1.50:11434/v1"}, "model": {"mistral"}})
	if a.an.settings(context.Background()).Key != "k-1234" {
		t.Fatal("key lost on a model change at the same address")
	}
}

func TestDeleteAsksAndKeepsFindings(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/situations", url.Values{"name": {"Hazard"}, "description": {"Sázky."}, "sensitivity": {"high"}, "action": {"record"}, "enabled": {"1"}})
	var id int64
	sits, _ := a.store.Situations(context.Background())
	for _, s := range sits {
		if s.Name == "Hazard" {
			id = s.ID
		}
	}
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"h"}, "chat_name": {"H"}, "text": {"[Hazard]"}})
	do("POST", "/dev/analysis/run", nil)
	rec := do("POST", "/situations/"+itoa(id), url.Values{"delete": {"1"}})
	if !strings.HasSuffix(rec.Header().Get("Location"), "/delete") {
		t.Fatal("did not ask first")
	}
	if !strings.Contains(do("GET", "/situations/"+itoa(id)+"/delete", nil).Body.String(), "Smazat situaci „Hazard“? Popis se ztratí, nálezy z ní zůstanou.") {
		t.Fatal("question missing")
	}
	do("POST", "/situations/"+itoa(id), url.Values{"delete": {"1"}, "confirmed": {"1"}})
	if _, err := a.store.Situation(context.Background(), id); err == nil {
		t.Fatal("not deleted")
	}
	if !strings.Contains(do("GET", "/findings", nil).Body.String(), "Hazard") {
		t.Fatal("its finding was lost")
	}
}

func TestReopenFinding(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"r"}, "chat_name": {"R"}, "text": {"[Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	fs, _ := a.store.Findings(context.Background())
	id := itoa(fs[0].ID)
	do("POST", "/findings/"+id+"/false_alarm", nil)
	if !strings.Contains(do("GET", "/findings", nil).Body.String(), "Znovu otevřít") {
		t.Fatal("no reopen")
	}
	do("POST", "/findings/"+id+"/open", nil)
	if n, _ := a.store.OpenFindingCount(context.Background()); n != 1 {
		t.Fatalf("open count %d", n)
	}
}

func TestErrorsInPlainWords(t *testing.T) {
	cs := pick("cs")
	for in, want := range map[string]string{
		"HTTP 401: invalid x-api-key":          "klíč nebyl přijat, zkontroluj ho (401)",
		"HTTP 404":                             "model nebo adresa neexistuje (404)",
		"dial tcp: lookup x: no such host":     "adresa neodpovídá",
		"the model's answer could not be read": "odpověď modelu nešla přečíst",
	} {
		if got := cs.explain(errors.New(in)); got != want {
			t.Errorf("%q -> %q", in, got)
		}
	}
}

func TestSlowAddressAnswersThePage(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-hang }))
	defer srv.Close()
	defer close(hang)
	old := testTimeout
	testTimeout = 300 * time.Millisecond
	defer func() { testTimeout = old }()
	_, do := practiceApp(t)
	start := time.Now()
	page := do("POST", "/settings/test", url.Values{"type": {"openai"}, "address": {srv.URL + "/v1"}, "model": {"llama3"}}).Body.String()
	if time.Since(start) > 5*time.Second {
		t.Fatal("test did not give up in time")
	}
	if !strings.Contains(page, "Model neodpovídá: adresa neodpovídá (vypršel čas)") || !strings.Contains(page, `value="llama3"`) {
		t.Fatal("no timeout message or the form was lost")
	}
}

func TestNewDefaultSetReplacesTheOld(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	// A database from before: the old defaults, no version, one added situation.
	old := []Situation{{Name: "Výhrůžky", Description: "d", Sensitivity: "medium", Action: "alert"}}
	for _, o := range old {
		if _, err := st.db.ExecContext(ctx, `INSERT INTO situations (name, description, sensitivity, action, enabled, builtin) VALUES (?, ?, ?, ?, 1, 1)`,
			o.Name, o.Description, o.Sensitivity, o.Action); err != nil {
			t.Fatal(err)
		}
	}
	added, err := st.AddSituation(ctx, Situation{Name: "Moje", Description: "d", Sensitivity: "low", Action: "digest", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO findings (chat, situation_id, created_at, updated_at, reason, quotes) VALUES ('c', 1, 1, 1, 'r', '[]')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second run changes nothing
		if err := st.SeedSituations(ctx, defaultSituationsCS); err != nil {
			t.Fatal(err)
		}
	}
	sits, _ := st.Situations(ctx)
	names := map[string]int{}
	for _, s := range sits {
		names[s.Name]++
	}
	if names["Výhrůžky"] != 0 || names["Moje"] != 1 || len(sits) != len(defaultSituationsCS)+1 {
		t.Fatalf("after the upgrade: %v", names)
	}
	for _, d := range defaultSituationsCS {
		if names[d.Name] != 1 {
			t.Errorf("default %q listed %d times", d.Name, names[d.Name])
		}
	}
	if fs, _ := st.Findings(ctx); len(fs) != 1 || fs[0].Situation.Name != "Výhrůžky" {
		t.Fatal("the old default's finding is gone")
	}
	_ = added
}
