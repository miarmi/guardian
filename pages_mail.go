package guardian

import (
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
	case mailGraph, mailSMTP:
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
