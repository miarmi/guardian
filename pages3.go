package guardian

import (
	"context"
	"encoding/json"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// --- the child's page (criterion 26), reachable without the lab login ---

type kidData struct {
	Lang          string
	Valid         bool
	Finding       Finding
	Thanks        bool
	Already       string // when the request already went out, as words
	Resolved      bool
	Contacts      []string
	Token         string
	HelplineLabel string
	HelplineTel   string
	Personal      bool // the help link of criterion 30, not about a finding
}

func (a *App) kidPage(w http.ResponseWriter, r *http.Request) {
	a.renderKid(w, r, false)
}

func (a *App) renderKid(w http.ResponseWriter, r *http.Request, thanks bool) {
	d := kidData{Lang: a.lang.code, Contacts: a.lang.contacts, Thanks: thanks,
		HelplineLabel: a.lang.helplineLabel, HelplineTel: a.lang.helplineTel}
	token := r.PathValue("token")
	if id, ok := a.nt.checkKidToken(token); ok {
		if f, found, err := a.store.Finding(r.Context(), id); err == nil && found {
			d.Valid, d.Finding, d.Token = true, f, token
			d.Resolved = f.Status == "resolved"
			if f.ChildHelp && !thanks {
				when := a.lang.when(f.HelpAt.In(a.zone), time.Now().In(a.zone))
				d.Already = a.lang.T("kid_already", when)
			}
		}
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	status := http.StatusOK
	if !d.Valid {
		status = http.StatusNotFound
	}
	a.render(w, a.pages["kid"], status, d)
}

// kidAsk is the only thing that acts: the child presses the button.
func (a *App) kidAsk(w http.ResponseWriter, r *http.Request) {
	id, ok := a.nt.checkKidToken(r.PathValue("token"))
	if !ok {
		a.renderKid(w, r, false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.PostForm.Get("message"))
	if utf8.RuneCountInString(msg) > 1000 {
		msg = string([]rune(msg)[:1000])
	}
	f, found, err := a.store.Finding(r.Context(), id)
	if err != nil || !found {
		a.renderKid(w, r, false)
		return
	}
	if !f.ChildHelp {
		if err := a.store.AskForHelp(r.Context(), id, msg); err != nil {
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
		a.nt.onHelp(context.WithoutCancel(r.Context()), f)
	}
	a.renderKid(w, r, true)
}

// --- push devices (criterion 22) ---

func (a *App) pushKey(w http.ResponseWriter, r *http.Request) {
	key, err := a.nt.vapidKey()
	if err != nil {
		http.Error(w, "no key", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"key": vapidPublic(key), "practice": a.dev != nil})
}

func (a *App) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Error(w, a.lang.T("an_preview"), http.StatusConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
		Label    string `json:"label"`
		Practice bool   `json:"practice"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad subscription", http.StatusBadRequest)
		return
	}
	if in.Practice && a.dev != nil {
		in.Endpoint, in.Keys.P256dh, in.Keys.Auth = "practice:"+randomID(), "-", "-"
	} else if !strings.HasPrefix(in.Endpoint, "https://") || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		http.Error(w, "bad subscription", http.StatusBadRequest)
		return
	}
	label := strings.TrimSpace(in.Label)
	if label == "" || utf8.RuneCountInString(label) > 60 {
		label = "?"
	}
	if err := a.store.AddPushDevice(r.Context(), PushDevice{Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth, Label: label}); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	// The page remembers its own endpoint, to mark "this device".
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"endpoint": in.Endpoint})
}

func (a *App) pushTest(w http.ResponseWriter, r *http.Request) {
	if a.an != nil {
		a.nt.testPush(context.WithoutCancel(r.Context()))
	}
	http.Redirect(w, r, "/settings?pushtest=1#notifications", http.StatusSeeOther)
}

func (a *App) pushRemove(w http.ResponseWriter, r *http.Request) {
	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		a.store.RemovePushDevice(r.Context(), id)
	}
	http.Redirect(w, r, "/settings#notifications", http.StatusSeeOther)
}

// --- e-mail settings (criterion 23) ---

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

func (a *App) mailView(ctx context.Context, r *http.Request) mailView {
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

func mailFromForm(r *http.Request, practiceAllowed bool) (mailSettings, string, string) {
	f := r.PostForm
	m := mailSettings{
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

func (a *App) saveMail(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	m, secret, pass := mailFromForm(r, a.dev != nil)
	bad := badAddress(r.PostForm.Get("child_email"))
	if bad == "" {
		bad = badAddress(r.PostForm.Get("parent_emails"))
	}
	if bad != "" { // keep what was typed, say which address is wrong
		d := settingsData{S: a.an.settings(r.Context())}
		d.MailResult = a.lang.T("bad_address", bad)
		d.MailTyped = &mailSettings{Type: m.Type, ChildEmail: r.PostForm.Get("child_email"), ParentEmails: r.PostForm.Get("parent_emails"),
			Tenant: m.Tenant, AppID: m.AppID, Host: m.Host, Port: m.Port, User: m.User, Sender: m.Sender}
		a.renderSettings(w, r, d)
		return
	}
	if err := a.nt.saveMailSettings(r.Context(), m, secret, pass); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?mailsaved=1#mail", http.StatusSeeOther)
}

func (a *App) testMailHandler(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	err := a.nt.testMail(r.Context())
	d := settingsData{S: a.an.settings(r.Context())}
	if err != nil {
		d.MailResult = a.lang.T("mail_bad", a.lang.explain(err))
	} else {
		d.MailResult, d.MailOK = a.lang.T("mail_ok"), true
	}
	a.renderSettings(w, r, d)
}

// sendHelpLinkHandler is "Poslat dítěti odkaz pro pomoc" (criterion 30).
func (a *App) sendHelpLinkHandler(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	d := settingsData{S: a.an.settings(r.Context())}
	if err := a.nt.sendHelpLink(r.Context()); err != nil {
		d.HelpResult = a.lang.T("mail_bad", a.lang.explain(err))
	} else {
		d.HelpResult, d.HelpOK = a.lang.T("mail_ok"), true
	}
	a.renderSettings(w, r, d)
}

// --- the child's personal help page (criterion 30) ---

func (a *App) helpPage(w http.ResponseWriter, r *http.Request) {
	a.renderHelp(w, r, false)
}

func (a *App) renderHelp(w http.ResponseWriter, r *http.Request, thanks bool) {
	d := kidData{Lang: a.lang.code, Contacts: a.lang.contacts, Thanks: thanks, Personal: true,
		HelplineLabel: a.lang.helplineLabel, HelplineTel: a.lang.helplineTel}
	token := r.PathValue("token")
	if a.nt.checkHelpToken(r.Context(), token) {
		d.Valid, d.Token = true, token
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	status := http.StatusOK
	if !d.Valid {
		status = http.StatusNotFound
	}
	a.render(w, a.pages["kid"], status, d)
}

func (a *App) helpAsk(w http.ResponseWriter, r *http.Request) {
	if !a.nt.checkHelpToken(r.Context(), r.PathValue("token")) {
		a.renderHelp(w, r, false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	msg := strings.TrimSpace(r.PostForm.Get("message"))
	if utf8.RuneCountInString(msg) > 1000 {
		msg = string([]rune(msg)[:1000])
	}
	_, isNew, err := a.store.AddHelpRequest(r.Context(), msg, func(t time.Time) string { return t.In(a.zone).Format("15:04") })
	if err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	if isNew {
		a.nt.onHelpRequest(context.WithoutCancel(r.Context()))
	}
	a.renderHelp(w, r, true)
}
