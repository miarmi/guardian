package guardian

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// --- the child's page (criterion 26), reachable without the lab login ---

type kidData struct {
	Lang     string
	Valid    bool
	Finding  Finding
	Thanks   bool
	Already  bool
	Contacts []string
	Token    string
}

func (a *App) kidPage(w http.ResponseWriter, r *http.Request) {
	a.renderKid(w, r, false)
}

func (a *App) renderKid(w http.ResponseWriter, r *http.Request, thanks bool) {
	d := kidData{Lang: a.lang.code, Contacts: a.lang.contacts, Thanks: thanks}
	token := r.PathValue("token")
	if id, ok := a.nt.checkKidToken(token); ok {
		if f, found, err := a.store.Finding(r.Context(), id); err == nil && found {
			d.Valid, d.Finding, d.Token, d.Already = true, f, token, f.ChildHelp && !thanks
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
	w.WriteHeader(http.StatusNoContent)
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
	SecretHint string
	PassHint   string
	Practice   bool
	Result     string
	ResultOK   bool
	Saved      bool
	Devices    []PushDevice
}

func (a *App) mailView(ctx context.Context, r *http.Request) mailView {
	m := a.nt.mailSettings(ctx)
	v := mailView{M: m, SecretHint: keyHint(m.AppSecret), PassHint: keyHint(m.Password), Practice: a.dev != nil,
		Saved: r.URL.Query().Get("mailsaved") == "1"}
	v.M.AppSecret, v.M.Password = "", ""
	v.Devices, _ = a.store.PushDevices(ctx)
	return v
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
	if m.ChildEmail != "" && !strings.Contains(m.ChildEmail, "@") {
		m.ChildEmail = ""
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
