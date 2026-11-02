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
	body, err := encryptPush([]byte(`{"title":"Guardian: Výhrůžky násilím"}`), b64.EncodeToString(ua.PublicKey().Bytes()), b64.EncodeToString(auth))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptPush(body, ua, auth)
	if err != nil || string(plain) != `{"title":"Guardian: Výhrůžky násilím"}` {
		t.Fatalf("got %q, %v", plain, err)
	}
}

func TestVAPIDTokenAndPushRequest(t *testing.T) {
	key, err := loadVAPIDKey(t.TempDir())
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
	tok, err := n.sign(tokenFinding, 42, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := n.checkFindingToken(tok); !ok || id != 42 {
		t.Fatal("valid token refused")
	}
	bad := []byte(tok)
	bad[3] ^= 1
	if _, ok := n.checkFindingToken(string(bad)); ok {
		t.Fatal("altered token accepted")
	}
	// The last character of each part has spare bits; every other spelling
	// of the same bytes must be refused.
	for _, i := range []int{strings.Index(tok, ".") - 1, len(tok) - 1} {
		for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_" {
			alt := []byte(tok)
			if byte(c) == alt[i] {
				continue
			}
			alt[i] = byte(c)
			if _, ok := n.checkFindingToken(string(alt)); ok {
				t.Fatalf("altered spelling %q accepted", alt)
			}
		}
	}
	old, _ := n.sign(tokenFinding, 42, time.Now().Add(-time.Minute))
	if _, ok := n.checkFindingToken(old); ok {
		t.Fatal("expired token accepted")
	}
	other := &notifier{secretsDir: t.TempDir()}
	if _, ok := other.checkFindingToken(tok); ok {
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

// childMails returns the e-mails to the child, newest first.
func childMails(t *testing.T, do func(string, string, url.Values) *httptest.ResponseRecorder) []outboxItem {
	t.Helper()
	var out []outboxItem
	for _, b := range outbox(t, do) {
		if b.Kind == "email" && b.To == "dite@example.cz" {
			out = append(out, b)
		}
	}
	return out
}

func TestParentsFirstThenChildCanAskForHelp(t *testing.T) {
	a, do := practiceApp(t)
	a.nt.baseURL = "https://guardian.example"
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz, tata@example.cz"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"c"}, "chat_name": {"Kuba"}, "sender_name": {"Kuba"}, "text": {"tajemství zpráva [Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	box := outbox(t, do)
	if len(box) != 1 || box[0].Kind != "push" || box[0].Subject != "Guardian: Výhrůžky násilím" || box[0].Text != "v chatu Kuba" {
		t.Fatalf("after the first look only the parents' push: %+v", box)
	}
	// The next look sends the child's e-mail.
	do("POST", "/dev/analysis/run", nil)
	mails := childMails(t, do)
	if len(mails) != 1 {
		t.Fatalf("child mails after the second look: %d", len(mails))
	}
	mail := mails[0]
	if mail.Subject != "Guardian něco našel" || !strings.Contains(mail.Text, "Rodiče o tom už vědí.") ||
		!strings.Contains(mail.Text, "Linka bezpečí: 116 111") || strings.Contains(mail.Text, "tajemství") || len(mail.Links) != 2 ||
		!strings.Contains(mail.Links[1], "/kid/help/") || !strings.Contains(mail.Text, "Potřebuju pomoc s něčím jiným") {
		t.Fatalf("mail: %+v", mail)
	}
	do("POST", "/dev/analysis/run", nil)
	if n := len(childMails(t, do)); n != 1 {
		t.Fatalf("child got %d e-mails, want 1", n)
	}
	link := strings.TrimPrefix(mail.Links[0], "https://guardian.example")
	before := len(outbox(t, do))
	// Opening the link changes nothing.
	page := do("GET", link, nil).Body.String()
	if !strings.Contains(page, "Ano, potřebuju pomoc") || len(outbox(t, do)) != before || strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc") {
		t.Fatal("opening the link acted, or the button is missing")
	}
	// Pressing the button does.
	page = do("POST", link, url.Values{"message": {"je to fakt zlý"}}).Body.String()
	if !strings.Contains(page, "Díky. Rodiče teď vědí, že potřebuješ pomoc.") {
		t.Fatal("no thanks")
	}
	if b := outbox(t, do)[0]; b.Kind != "push" || b.Text != "Dítě žádá o pomoc: Výhrůžky násilím v chatu Kuba" {
		t.Fatalf("help push: %+v", b)
	}
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc: Výhrůžky násilím v chatu Kuba") {
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

func TestFalseAlarmBeforeTheChildHears(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"x"}, "chat_name": {"X"}, "text": {"[Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	fs, _ := a.store.Findings(context.Background())
	do("POST", "/findings/"+itoa(fs[0].ID)+"/false_alarm", nil)
	do("POST", "/dev/analysis/run", nil)
	if n := len(childMails(t, do)); n != 0 {
		t.Fatalf("child got %d e-mails about a false alarm", n)
	}
}

func TestDigestGoesToParentsThenTheChildHears(t *testing.T) {
	_, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"p"}, "chat_name": {"P"}, "text": {"[Opakované ponižování]"}})
	do("POST", "/dev/analysis/run", nil)
	do("POST", "/dev/analysis/run", nil)
	if len(outbox(t, do)) != 0 {
		t.Fatalf("a digest situation told someone before the digest: %+v", outbox(t, do))
	}
	do("POST", "/dev/digest/run", nil)
	box := outbox(t, do)
	var digests, child int
	for _, b := range box {
		if b.Kind != "email" {
			continue
		}
		if strings.HasPrefix(b.Subject, "Guardian: týden ") {
			digests++
			if b.To != "mama@example.cz" || !strings.Contains(b.Text, "Opakované ponižování") {
				t.Fatalf("digest: %+v", b)
			}
		}
		if b.To == "dite@example.cz" {
			child++
			if b.Subject != "Guardian něco našel" {
				t.Fatalf("child got %q", b.Subject)
			}
		}
	}
	if digests != 1 || child != 1 {
		t.Fatalf("digests %d (want 1, parents only), child mails %d (want 1)", digests, child)
	}
	// The child's e-mail came after the digest.
	if box[0].To != "dite@example.cz" {
		t.Fatal("the child heard before the parents' digest")
	}
}

func TestPersonalHelpLink(t *testing.T) {
	a, do := practiceApp(t)
	a.nt.baseURL = "https://guardian.example"
	do("POST", "/settings", url.Values{"type": {"practice"}})
	if !strings.Contains(do("GET", "/settings", nil).Body.String(), "Dítě zatím odkaz nemá.") {
		t.Fatal("no-link state missing")
	}
	if !strings.Contains(do("POST", "/settings/helplink", nil).Body.String(), "E-mail neodešel: nejdřív vyplň e-mail dítěte") {
		t.Fatal("missing child e-mail not said")
	}
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	page := do("POST", "/settings/helplink", nil).Body.String()
	if !strings.Contains(page, "E-mail odešel.") || !strings.Contains(page, "Odkaz platí do ") {
		t.Fatal("sending the link not confirmed")
	}
	mails := childMails(t, do)
	if len(mails) != 1 || mails[0].Subject != "Guardian: když budeš potřebovat pomoc" || !strings.Contains(mails[0].Text, "Linka bezpečí") {
		t.Fatalf("help link mail: %+v", mails)
	}
	link := strings.TrimPrefix(mails[0].Links[0], "https://guardian.example")
	before := len(outbox(t, do))
	if p := do("GET", link, nil).Body.String(); !strings.Contains(p, "Co se děje?") || !strings.Contains(p, "Potřebuju pomoc") || len(outbox(t, do)) != before {
		t.Fatal("help page missing or opening it acted")
	}
	if !strings.Contains(do("POST", link, url.Values{"message": {"ve škole mi berou svačinu"}}).Body.String(), "Díky. Rodiče teď vědí, že potřebuješ pomoc.") {
		t.Fatal("no thanks")
	}
	push := outbox(t, do)[0]
	if push.Kind != "push" || push.Text != "Dítě žádá o pomoc" || strings.Contains(push.Text+push.Subject, "svačin") {
		t.Fatalf("push: %+v", push)
	}
	if !strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc") {
		t.Fatal("no banner")
	}
	// A second request within 10 minutes adds to the first, without a push.
	do("POST", link, url.Values{"message": {"a dneska zase"}})
	if n := len(outbox(t, do)); n != before+1 {
		t.Fatalf("second request pushed again (%d items)", n)
	}
	fin := do("GET", "/findings", nil).Body.String()
	if !strings.Contains(fin, "ve škole mi berou svačinu") || !strings.Contains(fin, "a dneska zase") || strings.Count(fin, "/requests/") != 1 {
		t.Fatal("request not in Nálezy once with both messages")
	}
	if strings.Contains(do("GET", "/", nil).Body.String(), "Dítě žádá o pomoc") {
		t.Fatal("banner stays after Nálezy was opened")
	}
	reqs, _ := a.store.HelpRequests(context.Background())
	do("POST", "/requests/"+itoa(reqs[0].ID)+"/resolved", nil)
	if r, _ := a.store.HelpRequests(context.Background()); r[0].Status != "resolved" {
		t.Fatal("request not resolved")
	}
	// The digest lists the request.
	do("POST", "/dev/digest/run", nil)
	if d := outbox(t, do)[0]; !strings.Contains(d.Text, "ve škole mi berou svačinu") {
		t.Fatalf("digest without the request: %+v", d)
	}
	// A new link retires the old one.
	do("POST", "/settings/helplink", nil)
	if r := do("GET", link, nil); r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "Tenhle odkaz už neplatí.") ||
		!strings.Contains(r.Body.String(), "Platný odkaz najdeš v nejnovějším e-mailu od Guardiana s předmětem „Guardian: když budeš potřebovat pomoc“.") {
		t.Fatal("old link still works")
	}
	newLink := strings.TrimPrefix(childMails(t, do)[0].Links[0], "https://guardian.example")
	if do("GET", newLink, nil).Code != http.StatusOK {
		t.Fatal("new link refused")
	}
	// A finding's link is not a help link, and the other way round.
	if do("GET", strings.Replace(newLink, "/kid/help/", "/kid/", 1), nil).Code != http.StatusNotFound {
		t.Fatal("help link accepted as a finding link")
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

func TestChildMailFailureIsShownAndRetried(t *testing.T) {
	_, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	do("POST", "/dev/mail/unreachable", url.Values{"on": {"1"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"f"}, "chat_name": {"F"}, "text": {"[Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	do("POST", "/dev/analysis/run", nil)
	if !strings.Contains(do("GET", "/", nil).Body.String(), "E-mail dítěti neodešel: zkušební pošta je nedostupná") {
		t.Fatal("failure not on the overview")
	}
	do("POST", "/dev/mail/unreachable", url.Values{"on": {"0"}})
	do("POST", "/dev/analysis/run", nil)
	if strings.Contains(do("GET", "/", nil).Body.String(), "E-mail dítěti neodešel") {
		t.Fatal("notice stays after the retry went out")
	}
	n := 0
	for _, b := range outbox(t, do) {
		if b.Kind == "email" && b.To == "dite@example.cz" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("child got %d e-mails, want 1", n)
	}
}

func TestChildSeesResolvedAndBadAddressesAreSaid(t *testing.T) {
	a, do := practiceApp(t)
	do("POST", "/settings", url.Values{"type": {"practice"}})
	do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "child_email": {"dite@example.cz"}, "parent_emails": {"mama@example.cz"}})
	do("POST", "/dev/whatsapp/message", url.Values{"chat": {"r"}, "chat_name": {"R"}, "text": {"[Výhrůžky násilím]"}})
	do("POST", "/dev/analysis/run", nil)
	do("POST", "/dev/analysis/run", nil)
	var link string
	for _, b := range outbox(t, do) {
		if b.Kind == "email" {
			link = strings.TrimPrefix(b.Links[0], a.nt.baseURL)
		}
	}
	page := do("GET", link, nil).Body.String()
	if !strings.Contains(page, `href="tel:116111"`) || !strings.Contains(page, `href="https://e-bezpeci.cz"`) || strings.Contains(page, "Rodiče to označili jako vyřešené.") {
		t.Fatal("contacts not tappable, or resolved too early")
	}
	fs, _ := a.store.Findings(context.Background())
	do("POST", "/findings/"+itoa(fs[0].ID)+"/resolved", nil)
	if !strings.Contains(do("GET", link, nil).Body.String(), "Rodiče to označili jako vyřešené.") {
		t.Fatal("resolved not shown to the child")
	}
	page = do("POST", "/settings/mail", url.Values{"mail_type": {"practice"}, "parent_emails": {"mama@example.cz; tata"}}).Body.String()
	if !strings.Contains(page, "„tata“ není platná e-mailová adresa.") || !strings.Contains(page, "mama@example.cz; tata") {
		t.Fatal("bad address not said or form not kept")
	}
	if m := a.nt.mailSettings(context.Background()); m.ParentEmails != "mama@example.cz" {
		t.Fatalf("saved despite the mistake: %q", m.ParentEmails)
	}
}

func TestLinkify(t *testing.T) {
	got := string(linkify("Když jde o bezpečí hned: 158 (policie) nebo 112 <b>"))
	if !strings.Contains(got, `<a class="tel" href="tel:158" style="white-space:nowrap;color:#1e6b64">158</a>`) || !strings.Contains(got, `href="tel:112"`) || strings.Contains(got, "<b>") {
		t.Fatalf("got %s", got)
	}
}
