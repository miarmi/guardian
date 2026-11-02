package guardian

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase 3: telling people (criteria 22–29).

const (
	mailGraph    = "graph"
	mailSMTP     = "smtp"
	mailPractice = "practice"
	kidLinkLife  = 30 * 24 * time.Hour
)

// mailSettings is the e-mail part of Nastavení; secrets live in secrets/.
type mailSettings struct {
	Type         string
	ChildEmail   string
	ParentEmails string // comma separated
	// Microsoft 365 through Microsoft Graph (client credentials).
	Tenant, AppID, AppSecret string
	// SMTP.
	Host, Port, User, Password string
	Sender                     string
}

func (m mailSettings) parents() []string { return splitAddresses(m.ParentEmails) }

func splitAddresses(s string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' }) {
		if strings.Contains(a, "@") {
			out = append(out, strings.TrimSpace(a))
		}
	}
	return out
}

// outboxItem is something "sent" in practice mode (criterion 29).
type outboxItem struct {
	Kind    string    `json:"kind"` // email or push
	To      string    `json:"to"`
	Subject string    `json:"subject"`
	Text    string    `json:"text"`
	Links   []string  `json:"links"`
	At      time.Time `json:"at"`
	HTML    string    `json:"html,omitempty"` // e-mails: how they look
}

// notifier sends pushes and e-mails, makes the child's links and the digest.
type notifier struct {
	st         *store
	lang       *lang
	zone       *time.Location
	secretsDir string
	baseURL    string
	practice   bool

	mu          sync.Mutex
	outbox      []outboxItem
	mailDown    bool // practice: sending fails
	graphToken  string
	graphExpiry time.Time
	vapid       *ecdsa.PrivateKey
}

// --- settings ---

var mailSecretFiles = map[string]string{"app_secret": "mail-app-secret", "password": "mail-password"}

func (n *notifier) mailSettings(ctx context.Context) mailSettings {
	get := func(k string) string { v, _ := n.st.Setting(ctx, k); return v }
	m := mailSettings{
		Type: get("mail_type"), ChildEmail: get("mail_child"), ParentEmails: get("mail_parents"),
		Tenant: get("mail_tenant"), AppID: get("mail_app_id"),
		Host: get("mail_host"), Port: get("mail_port"), User: get("mail_user"), Sender: get("mail_sender"),
	}
	if m.Type == "" {
		m.Type = mailGraph
	}
	if m.Type == mailPractice && !n.practice {
		m.Type = mailGraph
	}
	m.AppSecret = n.secret("app_secret")
	m.Password = n.secret("password")
	return m
}

func (n *notifier) secret(name string) string {
	b, err := os.ReadFile(filepath.Join(n.secretsDir, mailSecretFiles[name]))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (n *notifier) saveMailSettings(ctx context.Context, m mailSettings, newSecret, newPassword string) error {
	for k, v := range map[string]string{
		"mail_type": m.Type, "mail_child": m.ChildEmail, "mail_parents": m.ParentEmails,
		"mail_tenant": m.Tenant, "mail_app_id": m.AppID,
		"mail_host": m.Host, "mail_port": m.Port, "mail_user": m.User, "mail_sender": m.Sender,
	} {
		if err := n.st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	for name, v := range map[string]string{"app_secret": newSecret, "password": newPassword} {
		if v == "" {
			continue
		}
		if err := ensureDir(n.secretsDir); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(n.secretsDir, mailSecretFiles[name]), []byte(v), 0o600); err != nil {
			return err
		}
	}
	n.mu.Lock()
	n.graphToken = ""
	n.mu.Unlock()
	return nil
}

// --- sending mail ---

type email struct {
	To      []string
	Subject string
	Text    string
	HTML    string
	Links   []string
}

var mailHTTP = &http.Client{Timeout: 30 * time.Second}

func (n *notifier) send(ctx context.Context, e email) error {
	if len(e.To) == 0 {
		return nil
	}
	m := n.mailSettings(ctx)
	switch m.Type {
	case mailPractice:
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.mailDown {
			return errors.New(n.lang.T("practice_mail_down"))
		}
		n.outbox = append(n.outbox, outboxItem{Kind: "email", To: strings.Join(e.To, ", "), Subject: e.Subject, Text: e.Text, Links: e.Links, At: time.Now(), HTML: e.HTML})
		return nil
	case mailSMTP:
		return sendSMTP(m, e)
	default:
		return n.sendGraph(ctx, m, e)
	}
}

// sendGraph sends through Microsoft Graph with an app registration that has
// Mail.Send (ideally limited to the sender mailbox).
func (n *notifier) sendGraph(ctx context.Context, m mailSettings, e email) error {
	if m.Tenant == "" || m.AppID == "" || m.AppSecret == "" || m.Sender == "" {
		return errors.New(n.lang.T("mail_incomplete"))
	}
	token, err := n.graphAccessToken(ctx, m)
	if err != nil {
		return err
	}
	var to []map[string]any
	for _, a := range e.To {
		to = append(to, map[string]any{"emailAddress": map[string]string{"address": a}})
	}
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{
			"subject":      e.Subject,
			"body":         map[string]string{"contentType": "HTML", "content": e.HTML},
			"toRecipients": to,
		},
		"saveToSentItems": false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://graph.microsoft.com/v1.0/users/"+url.PathEscape(m.Sender)+"/sendMail", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := mailHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode/100 != 2 {
		var out struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		if out.Error.Message != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, out.Error.Message)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (n *notifier) graphAccessToken(ctx context.Context, m mailSettings) (string, error) {
	n.mu.Lock()
	if n.graphToken != "" && time.Now().Before(n.graphExpiry) {
		t := n.graphToken
		n.mu.Unlock()
		return t, nil
	}
	n.mu.Unlock()
	form := url.Values{
		"client_id":     {m.AppID},
		"client_secret": {m.AppSecret},
		"scope":         {"https://graph.microsoft.com/.default"},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://login.microsoftonline.com/"+url.PathEscape(m.Tenant)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := mailHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error_description"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode/100 != 2 || out.AccessToken == "" {
		msg := out.Error
		if i := strings.IndexByte(msg, '\r'); i > 0 {
			msg = msg[:i]
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(msg))
	}
	n.mu.Lock()
	n.graphToken = out.AccessToken
	n.graphExpiry = time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second)
	n.mu.Unlock()
	return out.AccessToken, nil
}

// sendSMTP sends a text+HTML message; port 465 means TLS from the start,
// anything else STARTTLS (required before any password is sent).
func sendSMTP(m mailSettings, e email) error {
	if m.Host == "" || m.Sender == "" {
		return errors.New("SMTP server and sender are required")
	}
	port := m.Port
	if port == "" {
		port = "587"
	}
	addr := net.JoinHostPort(m.Host, port)
	boundary := "guardian-" + randomID()
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nDate: %s\r\n",
		m.Sender, strings.Join(e.To, ", "), mime.QEncoding.Encode("utf-8", e.Subject), time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, e.Text)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n--%s--\r\n", boundary, e.HTML, boundary)

	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: m.Host})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if port != "465" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server offers no STARTTLS")
		}
		if err := c.StartTLS(&tls.Config{ServerName: m.Host}); err != nil {
			return err
		}
	}
	if m.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.User, m.Password, m.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(m.Sender); err != nil {
		return err
	}
	for _, a := range e.To {
		if err := c.Rcpt(a); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg.Bytes()); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// --- push ---

func (n *notifier) vapidKey() (*ecdsa.PrivateKey, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.vapid != nil {
		return n.vapid, nil
	}
	k, err := vapidKey(n.secretsDir)
	if err == nil {
		n.vapid = k
	}
	return k, err
}

// pushParents tells every subscribed device; in practice mode the push goes
// to the outbox instead.
func (n *notifier) pushParents(ctx context.Context, m pushMessage) {
	if n.practice {
		n.mu.Lock()
		n.outbox = append(n.outbox, outboxItem{Kind: "push", To: "parents", Subject: m.Title, Text: m.Body, Links: []string{m.URL}, At: time.Now()})
		n.mu.Unlock()
		return
	}
	devices, err := n.st.PushDevices(ctx)
	if err != nil || len(devices) == 0 {
		return
	}
	key, err := n.vapidKey()
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	contact := "mailto:guardian@localhost"
	if ps := n.mailSettings(ctx).parents(); len(ps) > 0 {
		contact = "mailto:" + ps[0]
	}
	for _, d := range devices {
		err := sendPush(ctx, key, contact, d, m)
		switch {
		case errors.Is(err, errGone):
			n.st.RemovePushEndpoint(ctx, d.Endpoint)
		case err != nil:
			log.Printf("push to %s: %v", d.Label, err)
		}
	}
}

// --- the child's links (criterion 26) ---

func (n *notifier) kidKey() ([]byte, error) {
	path := filepath.Join(n.secretsDir, "kid-key")
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := ensureDir(n.secretsDir); err != nil {
		return nil, err
	}
	return b, os.WriteFile(path, b, 0o600)
}

// kidToken signs a finding id and an expiry.
func (n *notifier) kidToken(findingID int64, expires time.Time) (string, error) {
	key, err := n.kidKey()
	if err != nil {
		return "", err
	}
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(findingID))
	binary.BigEndian.PutUint64(payload[8:], uint64(expires.Unix()))
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return b64.EncodeToString(payload) + "." + b64.EncodeToString(mac.Sum(nil)[:16]), nil
}

// checkKidToken returns the finding id of a valid, unexpired token.
func (n *notifier) checkKidToken(token string) (int64, bool) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok {
		return 0, false
	}
	payload, err1 := b64.DecodeString(p)
	got, err2 := b64.DecodeString(sig)
	if err1 != nil || err2 != nil || len(payload) != 16 || len(got) != 16 {
		return 0, false
	}
	// Only the exact spelling we issued counts: the last character of each
	// part carries spare bits, and a changed one must not decode the same.
	if b64.EncodeToString(payload) != p || b64.EncodeToString(got) != sig {
		return 0, false
	}
	key, err := n.kidKey()
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil)[:16], got) {
		return 0, false
	}
	if time.Now().Unix() > int64(binary.BigEndian.Uint64(payload[8:])) {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), true
}

func (n *notifier) link(path string) string {
	return strings.TrimRight(n.baseURL, "/") + path
}

// --- what happens on a finding ---

// onFinding tells people about a finding the analysis just recorded.
func (n *notifier) onFinding(ctx context.Context, id int64, isNew bool) {
	if !isNew {
		return
	}
	f, ok, err := n.st.Finding(ctx, id)
	if err != nil || !ok {
		return
	}
	if f.Situation.tellsParents() && f.Situation.Action == "alert" {
		n.pushParents(ctx, pushMessage{
			Title: n.lang.T("push_title", f.Situation.Name),
			Body:  n.lang.T("push_body", f.ChatLabel()),
			URL:   n.link("/findings"),
		})
	}
	if f.Situation.tellsChild() {
		n.tryChildMail(ctx, f)
	}
}

// tryChildMail sends the child's e-mail; a failure is kept, shown on the
// overview and retried at the next hourly look (criterion 25).
func (n *notifier) tryChildMail(ctx context.Context, f Finding) {
	err := n.mailChild(ctx, f)
	if err != nil {
		log.Printf("mail to the child: %v", err)
		n.st.SetChildMailPending(ctx, f.ID, true)
		n.st.SetSetting(ctx, "mail_child_error", n.lang.explain(err))
		return
	}
	n.st.SetChildMailPending(ctx, f.ID, false)
}

// retryChildMails tries the failed e-mails to the child again.
func (n *notifier) retryChildMails(ctx context.Context) {
	ids, err := n.st.PendingChildMails(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		f, ok, err := n.st.Finding(ctx, id)
		if err != nil || !ok {
			n.st.SetChildMailPending(ctx, id, false)
			continue
		}
		n.tryChildMail(ctx, f)
	}
	if ids, _ := n.st.PendingChildMails(ctx); len(ids) == 0 {
		n.st.SetSetting(ctx, "mail_child_error", "")
	}
}

// childMailError is why the child's e-mail last failed, "" when all went out.
func (n *notifier) childMailError(ctx context.Context) string {
	v, _ := n.st.Setting(ctx, "mail_child_error")
	return v
}

// onHelp tells the parents the child asked for help (criterion 26).
func (n *notifier) onHelp(ctx context.Context, f Finding) {
	n.pushParents(ctx, pushMessage{Title: "Guardian", Body: n.lang.T("help_banner", f.Situation.Name, f.ChatLabel()), URL: n.link("/findings")})
}

var errNoChildEmail = errors.New("no child e-mail")

func (n *notifier) mailChild(ctx context.Context, f Finding) error {
	m := n.mailSettings(ctx)
	if m.ChildEmail == "" {
		return nil
	}
	token, err := n.kidToken(f.ID, time.Now().Add(kidLinkLife))
	if err != nil {
		return err
	}
	link := n.link("/kid/" + token)
	data := map[string]any{"F": f, "Link": link, "Contacts": n.lang.contacts, "ParentsKnow": f.Situation.tellsParents()}
	var html, text bytes.Buffer
	if err := mailTmpl.ExecuteTemplate(&html, "child.html", n.view(data)); err != nil {
		return err
	}
	if err := mailTmpl.ExecuteTemplate(&text, "child.txt", n.view(data)); err != nil {
		return err
	}
	err = n.send(ctx, email{To: []string{m.ChildEmail}, Subject: n.lang.T("child_subject"), HTML: html.String(), Text: text.String(), Links: []string{link}})
	if errors.Is(err, errNoChildEmail) {
		return nil
	}
	return err
}

// view hands mail templates the words of the page language.
func (n *notifier) view(data map[string]any) map[string]any {
	data["L"] = n.lang
	return data
}

// testPush sends a test notification to every device.
func (n *notifier) testPush(ctx context.Context) {
	n.pushParents(ctx, pushMessage{Title: "Guardian", Body: n.lang.T("push_test"), URL: n.link("/settings")})
}

// --- weekly digest (criterion 28) ---

// digestLoop sends the digest every Sunday at 19:00 local time.
func (n *notifier) digestLoop(ctx context.Context) {
	for {
		next := nextDigest(time.Now().In(n.zone))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			if err := n.sendDigest(ctx, next); err != nil {
				log.Printf("digest: %v", err)
			}
		}
	}
}

func nextDigest(now time.Time) time.Time {
	d := time.Date(now.Year(), now.Month(), now.Day(), 19, 0, 0, 0, now.Location())
	for d.Weekday() != time.Sunday || !d.After(now) {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// sendDigest sends the week up to `end` to the parents and the child; each
// copy leaves out situations that aren't for that person.
func (n *notifier) sendDigest(ctx context.Context, end time.Time) error {
	end = end.In(n.zone)
	start := end.AddDate(0, 0, -7)
	fs, err := n.st.FindingsSince(ctx, start)
	if err != nil {
		return err
	}
	chats, _ := n.st.CountChats(ctx)
	m := n.mailSettings(ctx)
	// The label names every day the digest covers, including today.
	subject := n.lang.T("digest_subject", n.lang.day(start), n.lang.day(end))
	var firstErr error
	for _, who := range []struct {
		to   []string
		keep func(Situation) bool
	}{
		{m.parents(), Situation.tellsParents},
		{splitAddresses(m.ChildEmail), Situation.tellsChild},
	} {
		if len(who.to) == 0 {
			continue
		}
		var mine []Finding
		for _, f := range fs {
			if who.keep(f.Situation) {
				mine = append(mine, f)
			}
		}
		data := n.view(map[string]any{"Findings": mine, "Chats": chats, "Contacts": n.lang.contacts, "Child": who.to[0] == m.ChildEmail})
		var html, text bytes.Buffer
		if err := mailTmpl.ExecuteTemplate(&html, "digest.html", data); err != nil {
			return err
		}
		if err := mailTmpl.ExecuteTemplate(&text, "digest.txt", data); err != nil {
			return err
		}
		if err := n.send(ctx, email{To: who.to, Subject: subject, HTML: html.String(), Text: text.String()}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// testMail sends a short test e-mail to the parents (Poslat zkušební e-mail).
func (n *notifier) testMail(ctx context.Context) error {
	m := n.mailSettings(ctx)
	if len(m.parents()) == 0 {
		return errors.New(n.lang.T("mail_no_parents"))
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	text := n.lang.T("test_mail_text")
	return n.send(ctx, email{To: m.parents(), Subject: n.lang.T("test_mail_subject"), Text: text,
		HTML: "<p>" + template.HTMLEscapeString(text) + "</p>"})
}

// --- practice addresses (criterion 29) ---

func (n *notifier) devOutbox(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	out := make([]outboxItem, len(n.outbox))
	for i := range n.outbox {
		out[len(out)-1-i] = n.outbox[i] // newest first
	}
	n.mu.Unlock()
	for i := range out {
		if out[i].Links == nil {
			out[i].Links = []string{}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func (n *notifier) devDigest(w http.ResponseWriter, r *http.Request) {
	if err := n.sendDigest(r.Context(), time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (n *notifier) devMailDown(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	n.mu.Lock()
	n.mailDown = r.PostForm.Get("on") == "1"
	n.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
