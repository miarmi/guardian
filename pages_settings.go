package guardian

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// Nastavení: the AI model (criteria 12, 13, 19). The e-mail part of the
// same page is in pages_mail.go.

type settingsData struct {
	page
	Available   bool
	S           modelSettings
	KeyHint     string // "…abcd", or "" when no key is saved
	Practice    bool   // the practice type may be offered
	Destination string
	Ready       bool
	Result      string
	ResultOK    bool
	Tried       string // "<model> na <host>" after Vyzkoušet
	Saved       bool
	Forgot      bool // the key was forgotten because type or host changed
	NoKey       bool // saved a type that needs a key, without one
	TypedKey    bool // Vyzkoušet used a typed key that was not saved
	Mail        mailView
	MailResult  string
	MailOK      bool
	MailTyped   *mailSettings // the form as typed, when it had a mistake
	HelpResult  string        // after "Poslat dítěti odkaz pro pomoc"
	HelpOK      bool
	Gmail       gmailView
	GmailResult string // after Vyzkoušet Gmail or a bad address
	GmailOK     bool
	GmailTyped  *gmailSettings // the Gmail form as typed
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := settingsData{Saved: q.Get("saved") == "1", Forgot: q.Get("forgot") == "1", NoKey: q.Get("nokey") == "1"}
	if a.an != nil {
		d.S = a.an.settings(r.Context())
	}
	a.renderSettings(w, r, d)
}

// renderSettings shows the form with d.S in it (saved or as typed).
func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, d settingsData) {
	d.page = a.page()
	d.Mail = a.mailView(r)
	d.Mail.Result, d.Mail.ResultOK = d.MailResult, d.MailOK
	d.Mail.HelpResult, d.Mail.HelpOK = d.HelpResult, d.HelpOK
	if d.MailTyped != nil {
		d.Mail.M = *d.MailTyped
	}
	d.Gmail = a.gmailView(r)
	d.Gmail.Result, d.Gmail.ResultOK = d.GmailResult, d.GmailOK
	if d.GmailTyped != nil {
		d.Gmail.G = *d.GmailTyped
		d.Gmail.G.Password = ""
	}
	if a.an == nil {
		// Previews show the form as saved, locked: nothing is analysed there.
		d.S = previewSettings(r.Context(), a.store)
		d.Destination = d.S.destination()
	} else {
		d.Available = true
		if d.S.Type == "" {
			d.S.Type = modelAnthropic
		}
		saved := a.an.settings(r.Context())
		// The saved key is offered only while type and host still match it.
		if sameEndpoint(saved, d.S) {
			d.KeyHint = keyHint(saved.Key)
		}
		d.Practice = a.dev != nil
		d.Ready = saved.ready()
		d.Destination = d.S.destination()
		d.S.Key = ""
	}
	a.render(w, "settings", http.StatusOK, d)
}

// settingsFromForm reads the form; Anthropic gets its defaults filled in.
// An unreadable limit keeps the saved one.
func (a *App) settingsFromForm(r *http.Request) modelSettings {
	f := r.PostForm
	m := modelSettings{Type: f.Get("type"), Address: strings.TrimSpace(f.Get("address")), Model: strings.TrimSpace(f.Get("model")), Limit: defaultLimit}
	switch m.Type {
	case modelAnthropic, modelOpenAI:
	case modelPractice:
		if a.dev == nil {
			m.Type = modelAnthropic
		}
	default:
		m.Type = modelAnthropic
	}
	if m.Type == modelAnthropic {
		if m.Address == "" {
			m.Address = defaultAnthropicAddress
		}
		if m.Model == "" {
			m.Model = defaultAnthropicModel
		}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(f.Get("limit"))); err == nil && n >= 0 {
		m.Limit = n
	} else if a.an != nil {
		m.Limit = a.an.settings(r.Context()).Limit
	}
	return m
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	m := a.settingsFromForm(r)
	newKey := strings.TrimSpace(r.PostForm.Get("key"))
	saved := a.an.settings(r.Context())
	forgot := newKey == "" && saved.Key != "" && !sameEndpoint(saved, m)
	if err := a.an.saveSettings(r.Context(), m, newKey, forgot); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	dest := "/settings?saved=1"
	if forgot && m.Type != modelPractice { // the practice model has no key to ask for
		dest += "&forgot=1"
	}
	if m.Type == modelAnthropic && !a.an.settings(r.Context()).ready() {
		dest += "&nokey=1"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// testModel tries the model as filled in on the page, without saving it
// (criterion 12). A typed key is used; otherwise the saved one, but only for
// the same type and host.
func (a *App) testModel(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	m := a.settingsFromForm(r)
	saved := a.an.settings(r.Context())
	typedKey := strings.TrimSpace(r.PostForm.Get("key"))
	if typedKey != "" {
		m.Key = typedKey
	} else if sameEndpoint(saved, m) {
		m.Key = saved.Key
	}
	// A typed key is never echoed back; it must be typed again to save it.
	d := settingsData{S: m, TypedKey: typedKey != ""}
	err := a.an.test(r.Context(), m)
	var miss errMissing
	switch {
	case errors.As(err, &miss):
		d.Result = miss.text // nothing was tried
	case err != nil:
		d.Result = a.lang.T("model_bad", a.lang.explain(err))
	default:
		d.Result, d.ResultOK = a.lang.T("model_ok"), true
	}
	if m.Type != modelPractice && !errors.As(err, &miss) {
		d.Tried = a.lang.T("tried", m.Model, hostOf(m.Address))
	}
	// Testing what is saved also tells the overview how it stands.
	if m.Type == saved.Type && m.Address == saved.Address && m.Model == saved.Model && m.Key == saved.Key {
		a.an.noteTest(err)
	}
	a.renderSettings(w, r, d)
}
