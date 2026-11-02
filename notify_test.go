package guardian

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPushEncryptionRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	body, err := encryptPush([]byte(`{"title":"Guardian: Výhrůžky"}`), b64.EncodeToString(ua.PublicKey().Bytes()), b64.EncodeToString(auth))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptPush(body, ua, auth)
	if err != nil || string(plain) != `{"title":"Guardian: Výhrůžky"}` {
		t.Fatalf("got %q, %v", plain, err)
	}
}

func TestVAPIDTokenAndPushRequest(t *testing.T) {
	key, err := vapidKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	var gotAuth, gotEnc string
	var gotBody []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotEnc = r.Header.Get("Authorization"), r.Header.Get("Content-Encoding")
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		gotBody = buf[:n]
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	old := pushHTTP
	pushHTTP = srv.Client()
	defer func() { pushHTTP = old }()
	d := PushDevice{Endpoint: srv.URL + "/push/abc", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	if err := sendPush(context.Background(), key, "mailto:a@b.cz", d, pushMessage{Title: "T", Body: "B"}); err != nil {
		t.Fatal(err)
	}
	if gotEnc != "aes128gcm" || !strings.HasPrefix(gotAuth, "vapid t=") {
		t.Fatalf("headers: %q %q", gotEnc, gotAuth)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(gotAuth, "vapid t="), ", k="+vapidPublic(key))
	if !verifyES256(tok, &key.PublicKey) {
		t.Fatal("VAPID signature does not verify")
	}
	plain, err := decryptPush(gotBody, ua, auth)
	if err != nil || !strings.Contains(string(plain), `"body":"B"`) {
		t.Fatalf("payload %q %v", plain, err)
	}
}

func TestKidTokens(t *testing.T) {
	n := &notifier{secretsDir: t.TempDir()}
	tok, err := n.kidToken(42, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := n.checkKidToken(tok); !ok || id != 42 {
		t.Fatal("valid token refused")
	}
	bad := []byte(tok)
	bad[3] ^= 1
	if _, ok := n.checkKidToken(string(bad)); ok {
		t.Fatal("altered token accepted")
	}
	old, _ := n.kidToken(42, time.Now().Add(-time.Minute))
	if _, ok := n.checkKidToken(old); ok {
		t.Fatal("expired token accepted")
	}
	other := &notifier{secretsDir: t.TempDir()}
	if _, ok := other.checkKidToken(tok); ok {
		t.Fatal("token from another key accepted")
	}
}

func outbox(t *testing.T, do func(string, string, url.Values) *httptest.ResponseRecorder) []outboxItem {
	t.Helper()
	var out []outboxItem
	if err := json.Unmarshal(do("GET", "/dev/outbox", nil).Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestChildHearsFirstAndCanAskForHelp(t *testing.T) {
	a, do := practiceApp(t)
	a.nt.baseURL = "https://guardian.example"
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz, tata@example.cz"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"c"}, "chat_name": {"Kuba"}, "sender_name": {"Kuba"}, "text": {"tajemství zpráva [Výhrůžky]"}})
	do("POST", "/dev/analysis/run", nil)
	box := outbox(t, do)
	var mail, push *outboxItem
	for i := range box {
		switch box[i].Kind {
		case "email":
			mail = &box[i]
		case "push":
			push = &box[i]
		}
	}
	if push == nil || push.Subject != "Guardian: Výhrůžky" || push.Text != "v chatu Kuba" {
		t.Fatalf("push: %+v", push)
	}
	if mail == nil || mail.To != "dite@example.cz" || mail.Subject != "Guardian něco našel" ||
		!strings.Contains(mail.Text, "Linka bezpečí: 116 111") || strings.Contains(mail.Text, "tajemství") || len(mail.Links) != 1 {
		t.Fatalf("mail: %+v", mail)
	}
	link := strings.TrimPrefix(mail.Links[0], "https://guardian.example")
	// Opening the link changes nothing.
	page := do("GET", link, nil).Body.String()
	if !strings.Contains(page, "Ano, ať to řeší rodiče") || len(outbox(t, do)) != len(box) || strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc") {
		t.Fatal("opening the link acted, or the button is missing")
	}
	// Pressing the button does.
	page = do("POST", link, url.Values{"message": {"je to fakt zlý"}}).Body.String()
	if !strings.Contains(page, "Díky. Rodiče to teď uvidí.") {
		t.Fatal("no thanks")
	}
	if b := outbox(t, do)[0]; b.Kind != "push" || b.Text != "Dítě žádá o pomoc: Výhrůžky v chatu Kuba" {
		t.Fatalf("help push: %+v", b)
	}
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc: Výhrůžky v chatu Kuba") {
		t.Fatal("no help banner")
	}
	fin := do("GET", "/findings", nil).Body.String()
	if !strings.Contains(fin, "Dítě žádá o pomoc") || !strings.Contains(fin, "je to fakt zlý") {
		t.Fatal("finding not labelled")
	}
	if strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc:") {
		t.Fatal("help banner stays after opening the findings")
	}
	// An altered link shows only the expired text and the contacts.
	bad := do("GET", link[:len(link)-2]+"xx", nil)
	if bad.Code != http.StatusNotFound || !strings.Contains(bad.Body.String(), "Tenhle odkaz už neplatí.") ||
		strings.Contains(bad.Body.String(), "Kuba") || !strings.Contains(bad.Body.String(), "116 111") {
		t.Fatal("altered link reveals or lacks contacts")
	}
}

func TestRecipientsAndDigest(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	sits, _ := a.store.Situations(context.Background())
	var threats Situation
	for _, s := range sits {
		if s.Name == "Výhrůžky" {
			threats = s
		}
	}
	// Parents only: no mail to the child, push still.
	do("POST", "/situations/"+itoa(threats.ID), url.Values{"name": {threats.Name}, "description": {threats.Description},
		"sensitivity": {"medium"}, "action": {"alert"}, "enabled": {"1"}, "recipients": {"parents"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"p"}, "chat_name": {"P"}, "text": {"[Výhrůžky]"}})
	do("POST", "/dev/analysis/run", nil)
	for _, b := range outbox(t, do) {
		if b.Kind == "email" {
			t.Fatalf("child got mail for a parents-only situation: %+v", b)
		}
	}
	do("POST", "/dev/digest/run", nil)
	var toParents, toChild *outboxItem
	box := outbox(t, do)
	for i := range box {
		if box[i].Kind == "email" && box[i].To == "mama@example.cz" {
			toParents = &box[i]
		}
		if box[i].Kind == "email" && box[i].To == "dite@example.cz" {
			toChild = &box[i]
		}
	}
	if toParents == nil || !strings.HasPrefix(toParents.Subject, "Guardian: týden ") || !strings.Contains(toParents.Text, "Výhrůžky") {
		t.Fatalf("parents' digest: %+v", toParents)
	}
	if toChild == nil || strings.Contains(toChild.Text, "Výhrůžky") || !strings.Contains(toChild.Text, "Tento týden Guardian nic nenašel.") {
		t.Fatalf("child's digest: %+v", toChild)
	}
}

func TestMailSettingsAndFailure(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings/mail", url.Values{"mail_type": {"graph"}, "tenant": {"t"}, "app_id": {"id"}, "app_secret": {"s3cret-abcd"}, "sender": {"guardian@racoonwood.cz"}, "parent_emails": {"mama@example.cz"}})
	page := do("GET", "/settings", nil).Body.String()
	if strings.Contains(page, "s3cret") || !strings.Contains(page, "…abcd") {
		t.Fatal("secret leaked or hint missing")
	}
	if v, _ := a.store.Setting(context.Background(), "mail_app_secret"); v != "" {
		t.Fatal("secret in the database")
	}
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "parent_emails": {"mama@example.cz"}})
	if !strings.Contains(do("POST", "/settings/mail/test", url.Values{}).Body.String(), "E-mail odešel.") {
		t.Fatal("practice test mail")
	}
	do("POST", "/dev/mail/unreachable", url.Values{"on": {"1"}})
	if !strings.Contains(do("POST", "/settings/mail/test", url.Values{}).Body.String(), "E-mail neodešel: zkušební pošta je nedostupná") {
		t.Fatal("failure not shown")
	}
}

func TestNextDigest(t *testing.T) {
	z, _ := time.LoadLocation("Europe/Prague")
	for now, want := range map[time.Time]time.Time{
		time.Date(2026, 11, 2, 10, 0, 0, 0, z):  time.Date(2026, 11, 8, 19, 0, 0, 0, z),
		time.Date(2026, 11, 8, 18, 59, 0, 0, z): time.Date(2026, 11, 8, 19, 0, 0, 0, z),
		time.Date(2026, 11, 8, 19, 0, 0, 0, z):  time.Date(2026, 11, 15, 19, 0, 0, 0, z),
	} {
		if got := nextDigest(now); !got.Equal(want) {
			t.Errorf("nextDigest(%v) = %v, want %v", now, got, want)
		}
	}
}

func TestNoDevOutboxOutsidePractice(t *testing.T) {
	a := newApp(testStore(t), nil, Config{Env: "p"})
	for _, p := range []string{"/dev/outbox", "/dev/digest/run", "/dev/mail/unreachable"} {
		rec := httptest.NewRecorder()
		method := "POST"
		if p == "/dev/outbox" {
			method = "GET"
		}
		a.Handler().ServeHTTP(rec, httptest.NewRequest(method, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}
