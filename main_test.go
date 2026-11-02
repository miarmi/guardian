package guardian

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	for _, want := range []string{"<title>Guardian</title>", "Connectors", "WhatsApp", textNoPreviews, "Chats", msgNoChats, "Environment: preview: some-preview"} {
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
