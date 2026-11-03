package guardian

import (
	"context"
	"crypto/tls"
	"errors"
	"html"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/base64"
)

// Parental e-mails (criteria 32–36): the parental tools of platforms whose
// chats Guardian can't read (Discord Family Center, Roblox, Epic) e-mail
// the parent. Guardian reads only those e-mails from the parent's Gmail,
// read-only, and turns each into a short signal.

const (
	gmailIMAP       = "imap.gmail.com:993"
	gmailSMTP       = "smtp.gmail.com"
	gmailPasswordF  = "gmail-app-password"
	gmailPractice   = "practice"
	firstLookBack   = 14 * 24 * time.Hour // how far back the first read goes
	mailsPerLook    = 20                  // parental e-mails read per hourly look
	maxSignalText   = 6000                // characters of an e-mail given to the model
	gmailMailboxTag = "INBOX"
)

// parentalSenders are the only senders Guardian reads, with the platform
// each speaks for.
var parentalSenders = []struct{ Domain, Platform string }{
	{"discord.com", "Discord"},
	{"roblox.com", "Roblox"},
	{"epicgames.com", "Epic Games"},
}

func parentalDomains() []string {
	out := make([]string, len(parentalSenders))
	for i, s := range parentalSenders {
		out[i] = s.Domain
	}
	return out
}

// platformOf says which platform an address belongs to, "" for any other
// sender (the address's domain must be the platform's or a subdomain).
func platformOf(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return ""
	}
	domain := strings.ToLower(strings.TrimSpace(address[at+1:]))
	for _, s := range parentalSenders {
		if domain == s.Domain || strings.HasSuffix(domain, "."+s.Domain) {
			return s.Platform
		}
	}
	return ""
}

// gmailSettings is the Gmail section of Nastavení.
type gmailSettings struct {
	Type     string // "" (real Gmail) or "practice"
	Address  string
	Password string // from secrets/, never the database
	Read     bool   // Číst rodičovské e-maily
}

func (g gmailSettings) ready() bool {
	return g.Type == gmailPractice || (g.Address != "" && g.Password != "")
}

// parentalMail is one e-mail from a parental tool, as given to the model.
type parentalMail struct {
	UID      uint32
	Platform string
	From     string
	Subject  string
	Date     time.Time
	Text     string
}

// mailbox is where parental e-mails come from: Gmail or the practice one.
type mailbox interface {
	// New returns parental e-mails above the given UID (a first read goes
	// back firstLookBack), oldest first, and the mailbox's UIDVALIDITY.
	New(ctx context.Context, g gmailSettings, after uint32) ([]parentalMail, uint32, error)
	// Check signs in, for Vyzkoušet Gmail.
	Check(ctx context.Context, g gmailSettings) error
}

// gmailBox reads Gmail over IMAP. addr and tlsConfig are for tests; empty
// means Gmail itself.
type gmailBox struct {
	addr      string
	tlsConfig *tls.Config
}

func (b gmailBox) dial(ctx context.Context, g gmailSettings) (*imapConn, error) {
	addr := b.addr
	if addr == "" {
		addr = gmailIMAP
	}
	return imapDial(ctx, addr, g.Address, g.Password, b.tlsConfig)
}

func (b gmailBox) Check(ctx context.Context, g gmailSettings) error {
	c, err := b.dial(ctx, g)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.examine(ctx, gmailMailboxTag)
	return err
}

func (b gmailBox) New(ctx context.Context, g gmailSettings, after uint32) ([]parentalMail, uint32, error) {
	c, err := b.dial(ctx, g)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close()
	validity, err := c.examine(ctx, gmailMailboxTag)
	if err != nil {
		return nil, 0, err
	}
	uids, err := c.searchFrom(ctx, parentalDomains(), after, time.Now().Add(-firstLookBack))
	if err != nil {
		return nil, validity, err
	}
	if len(uids) > mailsPerLook {
		uids = uids[:mailsPerLook]
	}
	var out []parentalMail
	for _, uid := range uids {
		// The server's FROM search matches text anywhere in From; read the
		// header first and open the e-mail only when the sender is a
		// parental tool.
		head, err := c.fetch(ctx, uid, "HEADER.FIELDS (FROM)")
		if err != nil {
			return out, validity, err
		}
		if platformOf(fromAddress(head)) == "" {
			out = append(out, parentalMail{UID: uid}) // skipped, but remembered as seen
			continue
		}
		raw, err := c.fetch(ctx, uid, "")
		if err != nil {
			return out, validity, err
		}
		m, err := parseMail(raw)
		if err != nil {
			log.Printf("gmail: e-mail %d could not be read: %v", uid, err)
			out = append(out, parentalMail{UID: uid})
			continue
		}
		m.UID = uid
		out = append(out, m)
	}
	return out, validity, nil
}

// fromAddress reads the address out of a From header block.
func fromAddress(head []byte) string {
	msg, err := mail.ReadMessage(strings.NewReader(string(head) + "\r\n"))
	if err != nil {
		return ""
	}
	a, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return ""
	}
	return a.Address
}

// parseMail turns a raw e-mail into its sender, subject, date and plain text.
func parseMail(raw []byte) (parentalMail, error) {
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		return parentalMail{}, err
	}
	var m parentalMail
	if a, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
		m.From = a.Address
	}
	m.Platform = platformOf(m.From)
	dec := new(mime.WordDecoder)
	m.Subject, _ = dec.DecodeHeader(msg.Header.Get("Subject"))
	if m.Subject == "" {
		m.Subject = msg.Header.Get("Subject")
	}
	m.Date, _ = msg.Header.Date()
	plain, htmlText := bodyText(msg.Header.Get("Content-Type"), msg.Header.Get("Content-Transfer-Encoding"), msg.Body, 0)
	m.Text = plain
	if strings.TrimSpace(m.Text) == "" {
		m.Text = htmlToText(htmlText)
	}
	m.Text = clip(strings.TrimSpace(collapseBlank(m.Text)), maxSignalText)
	return m, nil
}

// bodyText walks the MIME parts and returns the plain and HTML text.
func bodyText(contentType, encoding string, body io.Reader, depth int) (plain, htmlText string) {
	media, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		media = "text/plain"
	}
	if strings.HasPrefix(media, "multipart/") && depth < 5 {
		r := multipart.NewReader(body, params["boundary"])
		for {
			p, err := r.NextPart()
			if err != nil {
				break
			}
			pl, ht := bodyText(p.Header.Get("Content-Type"), p.Header.Get("Content-Transfer-Encoding"), p, depth+1)
			if plain == "" {
				plain = pl
			}
			if htmlText == "" {
				htmlText = ht
			}
		}
		return plain, htmlText
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		body = quotedprintable.NewReader(body)
	case "base64":
		body = base64.NewDecoder(base64.StdEncoding, &stripNewlines{r: body})
	}
	b, _ := io.ReadAll(io.LimitReader(body, maxLiteral))
	switch media {
	case "text/plain":
		return string(b), ""
	case "text/html":
		return "", string(b)
	}
	return "", ""
}

// stripNewlines drops CR and LF, which base64 bodies are wrapped with.
type stripNewlines struct{ r io.Reader }

func (s *stripNewlines) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	j := 0
	for _, c := range p[:n] {
		if c != '\r' && c != '\n' {
			p[j] = c
			j++
		}
	}
	return j, err
}

var (
	reInvisible = regexp.MustCompile(`(?is)<(style|script|head)[^>]*>.*?</(style|script|head)>`)
	reBreak     = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/h[1-6]|/li)[^>]*>`)
	reTag       = regexp.MustCompile(`<[^>]+>`)
	reBlank     = regexp.MustCompile(`\n\s*\n\s*\n+`)
	reSpaces    = regexp.MustCompile(`[ \t\x{00a0}]+`)
)

func htmlToText(s string) string {
	s = reInvisible.ReplaceAllString(s, "")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

func collapseBlank(s string) string {
	s = reSpaces.ReplaceAllString(s, " ")
	return reBlank.ReplaceAllString(s, "\n\n")
}

// practiceBox is the practice mailbox of criterion 36.
type practiceBox struct {
	mu          sync.Mutex
	mails       []parentalMail // every e-mail, any sender
	unreachable bool
	errText     string
}

func (p *practiceBox) add(from, subject, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mails = append(p.mails, parentalMail{UID: uint32(len(p.mails) + 1), From: from, Platform: platformOf(from),
		Subject: subject, Text: text, Date: time.Now()})
}

func (p *practiceBox) Check(ctx context.Context, g gmailSettings) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unreachable {
		return errors.New(p.errText)
	}
	return nil
}

func (p *practiceBox) New(ctx context.Context, g gmailSettings, after uint32) ([]parentalMail, uint32, error) {
	if err := p.Check(ctx, g); err != nil {
		return nil, 0, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []parentalMail
	for _, m := range p.mails {
		if m.UID > after && m.Platform != "" { // other senders are never read
			out = append(out, m)
		}
	}
	if len(out) > mailsPerLook {
		out = out[:mailsPerLook]
	}
	return out, 1, nil
}

// --- reading during the hourly look ---

// readGmailSettings reads the Gmail section; practice is offered only in
// practice mode.
func readGmailSettings(ctx context.Context, st *store, secretsDir string, practiceAllowed bool) gmailSettings {
	v := st.Settings(ctx, "gmail_type", "gmail_address", "gmail_read")
	g := gmailSettings{Type: v["gmail_type"], Address: v["gmail_address"], Read: v["gmail_read"] == "1"}
	if g.Type == gmailPractice && !practiceAllowed {
		g.Type = ""
	}
	g.Password = readSecret(secretsDir, gmailPasswordF)
	return g
}

// saveGmailSettings saves the Gmail section; an empty password keeps the
// saved one.
func saveGmailSettings(ctx context.Context, st *store, secretsDir string, g gmailSettings, newPassword string) error {
	read := ""
	if g.Read {
		read = "1"
	}
	for k, v := range map[string]string{"gmail_type": g.Type, "gmail_address": g.Address, "gmail_read": read} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			return err
		}
	}
	if newPassword != "" {
		return writeSecret(secretsDir, gmailPasswordF, strings.ReplaceAll(newPassword, " ", ""))
	}
	return nil
}

func (z *analyzer) gmailSettings(ctx context.Context) gmailSettings {
	return readGmailSettings(ctx, z.st, z.secretsDir, z.practice != nil)
}

// readParentalMail reads new parental e-mails and makes signals (and
// findings) from them. A mailbox failure is kept for the overview
// (criterion 35) and tried again next hour; a model failure or the daily
// limit is returned like in the chat analysis.
func (z *analyzer) readParentalMail(ctx context.Context, a analyst, m modelSettings, on []Situation) error {
	g := z.gmailSettings(ctx)
	if !g.Read || !g.ready() || z.box == nil {
		z.setGmailError("")
		return nil
	}
	box := z.box
	if g.Type == gmailPractice {
		box = z.practiceBox
	}
	v := z.st.Settings(ctx, "gmail_last_uid", "gmail_validity")
	last, _ := strconv.ParseUint(v["gmail_last_uid"], 10, 32)
	validity, _ := strconv.ParseUint(v["gmail_validity"], 10, 32)
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	mails, newValidity, err := box.New(readCtx, g, uint32(last))
	if err == nil && validity != 0 && uint64(newValidity) != validity {
		// The mailbox was rebuilt: UIDs mean something else now; start over.
		mails, _, err = box.New(readCtx, g, 0)
	}
	if err != nil {
		log.Printf("gmail: %v", err)
		z.setGmailError(z.lang.explain(err))
		return nil
	}
	z.setGmailError("")
	z.st.SetSetting(ctx, "gmail_validity", strconv.FormatUint(uint64(newValidity), 10))
	for _, pm := range mails {
		if pm.Platform != "" {
			if err := z.spend(ctx, m.Limit); err != nil {
				return err // the rest waits until the limit allows
			}
			askCtx, cancel := context.WithTimeout(ctx, askTimeout)
			sig, found, err := a.Signal(askCtx, pm, on)
			cancel()
			if err != nil {
				return err // asked again next hour
			}
			if err := z.recordSignal(ctx, pm, sig, found); err != nil {
				return err
			}
		}
		if err := z.st.SetSetting(ctx, "gmail_last_uid", strconv.FormatUint(uint64(pm.UID), 10)); err != nil {
			return err
		}
	}
	return nil
}

// recordSignal stores the signal and any findings it carries.
func (z *analyzer) recordSignal(ctx context.Context, pm parentalMail, summary string, found []rawFinding) error {
	if strings.TrimSpace(summary) != "" {
		at := pm.Date
		if at.IsZero() {
			at = time.Now()
		}
		if err := z.st.AddSignal(ctx, Signal{Platform: pm.Platform, Subject: pm.Subject, Summary: summary, Received: at}); err != nil {
			return err
		}
	}
	if len(found) == 0 {
		return nil
	}
	chat := "email:" + strings.ToLower(strings.ReplaceAll(pm.Platform, " ", ""))
	if err := z.st.EnsureChat(ctx, chat, z.lang.T("email_chat", pm.Platform), false, true); err != nil {
		return err
	}
	return z.record(ctx, chat, found)
}

func (z *analyzer) setGmailError(s string) {
	z.mu.Lock()
	z.gmailErr = s
	z.mu.Unlock()
}

// gmailError is why Gmail couldn't be read at the last look, "" when fine.
func (z *analyzer) gmailError() string {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.gmailErr
}
