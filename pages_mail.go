package guardian

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
)

// Nastavení: e-mail, push devices and the child's help link (criteria 22,
// 23, 30). The model part of the page is in pages_settings.go.

type mailView struct {
	M          mailSettings
	Error      string // a typed address that isn't one
	PushSent   bool
	SecretHint string
	PassHint   string
	Practice   bool
	Result     string
	ResultOK   bool
	Saved      bool
	Devices    []PushDevice
	HelpUntil  string // until when the child's help link works, "" without one
	HelpResult string
	HelpOK     bool
}

// mailView is the saved e-mail settings without their secrets.
func (a *App) mailView(r *http.Request) mailView {
	ctx := r.Context()
	m := a.nt.mailSettings(ctx)
	v := mailView{M: m, SecretHint: keyHint(m.AppSecret), PassHint: keyHint(m.Password), Practice: a.dev != nil,
		Saved: r.URL.Query().Get("mailsaved") == "1", PushSent: r.URL.Query().Get("pushtest") == "1"}
	v.M.AppSecret, v.M.Password = "", ""
	v.Devices, _ = a.store.PushDevices(ctx)
	if link, until, _ := a.nt.helpLink(ctx, false); link != "" {
		v.HelpUntil = a.lang.date(until.In(a.zone))
	}
	return v
}

// badAddress returns the first entry that isn't an e-mail address.
func badAddress(list string) string {
	for _, a := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '\r' }) {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if addr, err := mail.ParseAddress(a); err != nil || addr.Address != a {
			return a
		}
	}
	return ""
}

// mailFromForm reads the e-mail form, plus any newly typed secret and
// password (empty keeps the saved ones).
func mailFromForm(r *http.Request, practiceAllowed bool) (m mailSettings, secret, password string) {
	f := r.PostForm
	m = mailSettings{
		Type:         f.Get("mail_type"),
		ChildEmail:   strings.TrimSpace(f.Get("child_email")),
		ParentEmails: strings.Join(splitAddresses(f.Get("parent_emails")), ", "),
		Tenant:       strings.TrimSpace(f.Get("tenant")),
		AppID:        strings.TrimSpace(f.Get("app_id")),
		Host:         strings.TrimSpace(f.Get("host")),
		Port:         strings.TrimSpace(f.Get("port")),
		User:         strings.TrimSpace(f.Get("user")),
		Sender:       strings.TrimSpace(f.Get("sender")),
	}
	switch m.Type {
	case mailGmail, mailGraph, mailSMTP:
	case mailPractice:
		if !practiceAllowed {
			m.Type = mailGraph
		}
	default:
		m.Type = mailGraph
	}
	return m, strings.TrimSpace(f.Get("app_secret")), strings.TrimSpace(f.Get("password"))
}

// mailResult is the line after an e-mail went out, or didn't.
func (a *App) mailResult(err error) (string, bool) {
	if err != nil {
		return a.lang.T("mail_bad", a.lang.explain(err)), false
	}
	return a.lang.T("mail_ok"), true
}

func (a *App) saveMail(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	m, secret, pass := mailFromForm(r, a.dev != nil)
	typedChild, typedParents := r.PostForm.Get("child_email"), r.PostForm.Get("parent_emails")
	bad := badAddress(typedChild)
	if bad == "" {
		bad = badAddress(typedParents)
	}
	if bad != "" { // keep what was typed, say which address is wrong
		typed := m
		typed.ChildEmail, typed.ParentEmails = typedChild, typedParents
		a.renderSettings(w, r, settingsData{S: a.an.settings(r.Context()), MailResult: a.lang.T("bad_address", bad), MailTyped: &typed})
		return
	}
	if err := a.nt.saveMailSettings(r.Context(), m, secret, pass); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?mailsaved=1#mail", http.StatusSeeOther)
}

// testMailHandler is "Poslat zkušební e-mail".
func (a *App) testMailHandler(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	err := a.nt.testMail(r.Context())
	d := settingsData{S: a.an.settings(r.Context())}
	d.MailResult, d.MailOK = a.mailResult(err)
	a.renderSettings(w, r, d)
}

// sendHelpLinkHandler is "Poslat dítěti odkaz pro pomoc" (criterion 30).
func (a *App) sendHelpLinkHandler(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	err := a.nt.sendHelpLink(r.Context())
	d := settingsData{S: a.an.settings(r.Context())}
	d.HelpResult, d.HelpOK = a.mailResult(err)
	a.renderSettings(w, r, d)
}

// --- Gmail (criteria 32, 35) ---

// gmailView is the Gmail section without its password.
type gmailView struct {
	G        gmailSettings
	PassHint string
	Practice bool
	Result   string
	ResultOK bool
	Saved    bool
	Error    string // why the last read failed (criterion 35)
}

func (a *App) gmailView(r *http.Request) gmailView {
	g := readGmailSettings(r.Context(), a.store, a.nt.secretsDir, a.dev != nil)
	v := gmailView{G: g, PassHint: keyHint(g.Password), Practice: a.dev != nil, Saved: r.URL.Query().Get("gmailsaved") == "1"}
	v.G.Password = ""
	if a.an != nil {
		v.Error = a.an.gmailError()
	}
	return v
}

func gmailFromForm(r *http.Request, practiceAllowed bool) (gmailSettings, string) {
	f := r.PostForm
	g := gmailSettings{Type: f.Get("gmail_type"), Address: strings.TrimSpace(f.Get("gmail_address")), Read: f.Get("gmail_read") == "1"}
	if g.Type != gmailPractice || !practiceAllowed {
		g.Type = ""
	}
	return g, strings.TrimSpace(f.Get("gmail_password"))
}

func (a *App) saveGmail(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	g, pass := gmailFromForm(r, a.dev != nil)
	if g.Address != "" {
		if bad := badAddress(g.Address); bad != "" {
			a.renderSettings(w, r, settingsData{S: a.an.settings(r.Context()), GmailResult: a.lang.T("bad_address", bad)})
			return
		}
	}
	if err := saveGmailSettings(r.Context(), a.store, a.nt.secretsDir, g, pass); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	a.an.setGmailError("") // new settings: the next look tells
	http.Redirect(w, r, "/settings?gmailsaved=1#gmail", http.StatusSeeOther)
}

// testGmail is "Vyzkoušet Gmail": it signs in with what the form says (a
// password left empty uses the saved one) and saves nothing.
func (a *App) testGmail(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	g, pass := gmailFromForm(r, a.dev != nil)
	g.Password = strings.ReplaceAll(pass, " ", "")
	if g.Password == "" {
		g.Password = readSecret(a.nt.secretsDir, gmailPasswordF)
	}
	d := settingsData{S: a.an.settings(r.Context())}
	box := a.an.box
	if g.Type == gmailPractice {
		box = a.an.practiceBox
	}
	var err error
	if !g.ready() {
		err = errors.New(a.lang.T("gmail_incomplete"))
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), testTimeout)
		err = box.Check(ctx, g)
		cancel()
	}
	if err != nil {
		d.GmailResult = a.lang.T("gmail_bad", a.lang.explain(err))
	} else {
		d.GmailResult, d.GmailOK = a.lang.T("gmail_ok"), true
		a.an.setGmailError("") // it works now; the next look confirms
	}
	d.GmailTyped = &g
	a.renderSettings(w, r, d)
}
