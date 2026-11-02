package guardian

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

func testStore(t *testing.T) *store {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "guardian.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMessagesAreKeptOnce(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	m := Message{ID: "A1", Chat: "420111@s.whatsapp.net", Sender: "420111@s.whatsapp.net", At: time.Unix(1000, 0), Kind: "text", Text: "hi"}
	for i := 0; i < 2; i++ { // live and again from a history sync
		if err := st.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	m2 := m
	m2.ID, m2.At, m2.FromMe = "A2", time.Unix(2000, 0), true
	if err := st.SaveMessage(ctx, m2); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureChat(ctx, m.Chat, "Kuba", false, false); err != nil {
		t.Fatal(err)
	}
	chats, err := st.Chats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Count != 2 || chats[0].Name != "Kuba" || !chats[0].Last.Equal(time.Unix(2000, 0)) {
		t.Fatalf("unexpected chats: %+v", chats)
	}
}

func TestChatNameIsNotOverwrittenByAGuess(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.EnsureChat(ctx, "g@g.us", "Class 6.B", true, true))
	must(st.EnsureChat(ctx, "g@g.us", "", true, false))
	must(st.EnsureChat(ctx, "g@g.us", "guess", true, false))
	name, err := st.ChatName(ctx, "g@g.us")
	must(err)
	if name != "Class 6.B" {
		t.Fatalf("name = %q", name)
	}
	must(st.EnsureChat(ctx, "g@g.us", "Class 6.C", true, true))
	name, _ = st.ChatName(ctx, "g@g.us")
	if name != "Class 6.C" {
		t.Fatalf("name = %q", name)
	}
}

func TestDescribeKeepsCaptionsNotFiles(t *testing.T) {
	text := "see this"
	caption := "lol"
	cases := []struct {
		msg  *waE2E.Message
		kind string
		text string
	}{
		{&waE2E.Message{Conversation: &text}, "text", "see this"},
		{&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: &caption}}, "image", "lol"},
		{&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, "audio", ""},
		{&waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{}}, "", ""},
		{nil, "", ""},
	}
	for _, c := range cases {
		kind, got := describe(c.msg)
		if kind != c.kind || got != c.text {
			t.Errorf("describe(%v) = %q %q, want %q %q", c.msg, kind, got, c.kind, c.text)
		}
	}
}

func TestPreviewShowsNoConnectButton(t *testing.T) {
	st := testStore(t)
	a := newApp(st, nil, Config{Env: "some-preview", Version: "abcdef1234"})
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"<title>Guardian</title>", "Connectors", "WhatsApp", "Not available in previews", "Chats", "No messages yet.", "Preview: some-preview"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "Connect WhatsApp") {
		t.Error("preview offers a connect button")
	}
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", rec.Code, rec.Body.String())
	}
}

func TestWordsInBothLanguages(t *testing.T) {
	zone := time.UTC
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, zone)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, zone) }
	cases := []struct {
		lang, want string
		at         time.Time
	}{
		{"en", "today 09:05", at(2026, 11, 2, 9, 5)},
		{"en", "yesterday 23:59", at(2026, 11, 1, 23, 59)},
		{"en", "Fri 30 Oct", at(2026, 10, 30, 9, 0)},
		{"en", "3 Jan", at(2026, 1, 3, 9, 0)},
		{"en", "27 Oct 2025", at(2025, 10, 27, 9, 0)},
		{"cs", "dnes 9:05", at(2026, 11, 2, 9, 5)},
		{"cs", "včera 23:59", at(2026, 11, 1, 23, 59)},
		{"cs", "pá 30. 10.", at(2026, 10, 30, 9, 0)},
		{"cs", "3. 1.", at(2026, 1, 3, 9, 0)},
		{"cs", "27. 10. 2025", at(2025, 10, 27, 9, 0)},
	}
	for _, c := range cases {
		if got := pick(c.lang).when(c.at, now); got != c.want {
			t.Errorf("%s when(%v) = %q, want %q", c.lang, c.at, got, c.want)
		}
	}
	counts := []struct {
		lang string
		n    int
		want string
	}{
		{"en", 1, "1 message"}, {"en", 12345, "12\u202f345 messages"},
		{"cs", 1, "1 zpráva"}, {"cs", 3, "3 zprávy"}, {"cs", 5, "5 zpráv"}, {"cs", 1234, "1\u00a0234 zpráv"},
	}
	for _, c := range counts {
		if got := pick(c.lang).count(c.n); got != c.want {
			t.Errorf("%s count(%d) = %q, want %q", c.lang, c.n, got, c.want)
		}
	}
	if got := formatNumber("420777123456"); got != "+420 777 123 456" {
		t.Errorf("formatNumber = %q", got)
	}
	if pick("xx") != pick("en") {
		t.Error("unknown language should fall back to English")
	}
}

func TestCzechPage(t *testing.T) {
	st := testStore(t)
	a := newApp(st, nil, Config{Env: "p", Language: "cs"})
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	for _, want := range []string{`<html lang="cs">`, "Konektory", "V náhledu není k dispozici", "Chaty", "Zatím žádné zprávy.", "Náhled: p"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	rec = httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Taková stránka tu není.") {
		t.Errorf("404 page: %d", rec.Code)
	}
}

func TestPracticePlaysThePhone(t *testing.T) {
	st := testStore(t)
	p := newPractice(st)
	a := newApp(st, p, Config{Language: "cs"})
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
	if rec := do("POST", "/dev/whatsapp/scan", nil); rec.Code != http.StatusConflict {
		t.Fatalf("scan with no code: %d", rec.Code)
	}
	do("POST", "/whatsapp/connect", url.Values{})
	if rec := do("POST", "/dev/whatsapp/scan", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("scan: %d", rec.Code)
	}
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Připojeno jako &#43;420 777 000 111") {
		t.Fatal("not shown as connected")
	}
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"c1"}, "chat_name": {"6.B"}, "group": {"1"}, "sender_name": {"Kuba"}, "kind": {"image"}, "text": {"lol"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"c1"}, "chat_name": {"6.B"}, "group": {"1"}, "sender_name": {"Kuba"}, "kind": {"audio"}, "text": {"ignored"}})
	var kept []map[string]any
	json.Unmarshal(do("GET", "/dev/whatsapp/messages?chat=c1", nil).Body.Bytes(), &kept)
	if len(kept) != 2 || kept[0]["kind"] != "image" || kept[0]["text"] != "lol" || kept[1]["text"] != "" {
		t.Fatalf("kept: %v", kept)
	}
	page := do("GET", "/", nil).Body.String()
	if !strings.Contains(page, "6.B") || !strings.Contains(page, "2 zprávy") || strings.Contains(page, "lol") {
		t.Fatal("chat list wrong or shows content")
	}
	do("POST", "/dev/whatsapp/remove", nil)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Odpojeno telefonem") {
		t.Fatal("removal not shown")
	}
	// Three codes, then it expires.
	do("POST", "/whatsapp/connect", url.Values{})
	for i := 0; i < 3; i++ {
		do("POST", "/dev/whatsapp/next", nil)
	}
	if p.Status().State != waNotConnected {
		t.Fatal("code did not expire after the third")
	}
	do("POST", "/dev/whatsapp/unreachable", url.Values{"on": {"1"}})
	if rec := do("POST", "/whatsapp/connect", url.Values{}); !strings.Contains(rec.Header().Get("Location"), "error=unreachable") {
		t.Fatalf("unreachable: %v", rec.Header())
	}
}

func TestNoDevAddressesOutsidePractice(t *testing.T) {
	a := newApp(testStore(t), nil, Config{Env: "p"})
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/dev/whatsapp/scan", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}
