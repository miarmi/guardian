package guardian

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

// email is one message, in plain text and HTML. Links are what the
// practice outbox lists, so tests can follow them.
type email struct {
	To      []string
	Subject string
	Text    string
	HTML    string
	Links   []string
}

var mailHTTP = &http.Client{Timeout: 30 * time.Second}

// send delivers an e-mail the way Nastavení says.
func (n *notifier) send(ctx context.Context, e email) error {
	return n.sendWith(ctx, n.mailSettings(ctx), e)
}

// sendWith delivers an e-mail with settings already read, for callers that
// send several.
func (n *notifier) sendWith(ctx context.Context, m mailSettings, e email) error {
	if len(e.To) == 0 {
		return nil
	}
	if m.Type == mailGmail {
		g := readGmailSettings(ctx, n.st, n.secretsDir, n.practice)
		if g.Type == gmailPractice {
			m.Type = mailPractice
		} else {
			if !g.ready() {
				return errors.New(n.lang.T("gmail_incomplete"))
			}
			m.Type, m.Host, m.Port, m.User, m.Password, m.Sender = mailSMTP, gmailSMTP, "587", g.Address, g.Password, g.Address
		}
	}
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

// testMail sends a short test e-mail to the parents (Poslat zkušební e-mail).
func (n *notifier) testMail(ctx context.Context) error {
	m := n.mailSettings(ctx)
	if len(m.parents()) == 0 {
		return errors.New(n.lang.T("mail_no_parents"))
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	text := n.lang.T("test_mail_text")
	return n.sendWith(ctx, m, email{To: m.parents(), Subject: n.lang.T("test_mail_subject"), Text: text,
		HTML: "<p>" + template.HTMLEscapeString(text) + "</p>"})
}

// --- Microsoft 365 through Microsoft Graph ---

// graphToken is a cached access token and the credentials it was issued
// for, so a token fetched while the settings changed is never used for the
// new ones.
type graphToken struct {
	token   string
	expires time.Time
	creds   string
}

func graphCreds(m mailSettings) string { return m.Tenant + "\x00" + m.AppID + "\x00" + m.AppSecret }

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
	if resp.StatusCode/100 == 2 {
		return nil
	}
	var out struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if out.Error.Message != "" {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, out.Error.Message)
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}

// graphAccessToken returns a cached token for these credentials, or gets a
// new one (client credentials flow). The lock is not held while asking.
func (n *notifier) graphAccessToken(ctx context.Context, m mailSettings) (string, error) {
	creds := graphCreds(m)
	n.mu.Lock()
	cached := n.graph
	n.mu.Unlock()
	if cached.token != "" && cached.creds == creds && time.Now().Before(cached.expires) {
		return cached.token, nil
	}
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
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode/100 != 2 || out.AccessToken == "" {
		msg := out.Error
		if i := strings.IndexByte(msg, '\r'); i > 0 {
			msg = msg[:i]
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(msg))
	}
	n.mu.Lock()
	n.graph = graphToken{token: out.AccessToken, expires: time.Now().Add(time.Duration(out.ExpiresIn-60) * time.Second), creds: creds}
	n.mu.Unlock()
	return out.AccessToken, nil
}

// forgetGraphToken drops the cached token after the settings change.
func (n *notifier) forgetGraphToken() {
	n.mu.Lock()
	n.graph = graphToken{}
	n.mu.Unlock()
}

// --- SMTP ---

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
	implicitTLS := port == "465"
	conn, err := dialSMTP(net.JoinHostPort(m.Host, port), m.Host, implicitTLS)
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
	if !implicitTLS {
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
	if _, err := w.Write(mimeMessage(m.Sender, e)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func dialSMTP(addr, host string, implicitTLS bool) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	if implicitTLS {
		return tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	}
	return dialer.Dial("tcp", addr)
}

// mimeMessage writes the e-mail as multipart/alternative: text, then HTML.
func mimeMessage(from string, e email) []byte {
	boundary := "guardian-" + randomID()
	var msg bytes.Buffer
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nDate: %s\r\n",
		from, strings.Join(e.To, ", "), mime.QEncoding.Encode("utf-8", e.Subject), time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&msg, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", boundary, e.Text)
	fmt.Fprintf(&msg, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n--%s--\r\n", boundary, e.HTML, boundary)
	return msg.Bytes()
}
