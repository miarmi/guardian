package guardian

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// overviewAnalysis is what the overview says about phase 2 (criteria 12, 13,
// 17–20).
type overviewAnalysis struct {
	Available   bool // false in previews: no analysis runs there
	State       analysisState
	Destination string // host the text goes to, "" when it stays home
	OpenCount   int
	Alert       string // banner text, "" when none
	Reason      string // why there is no model, when it is just the key
}

func (a *App) overviewAnalysis(ctx context.Context) overviewAnalysis {
	var o overviewAnalysis
	o.OpenCount, _ = a.store.OpenFindingCount(ctx)
	if sit, name, chat, ok, _ := a.store.UnseenAlert(ctx); ok {
		if name == "" {
			name = chat
		}
		o.Alert = a.lang.T("alert_banner", sit, name)
	}
	if a.an == nil {
		o.State = analysisState{NoModel: true}
		return o
	}
	o.Available = true
	o.State = a.an.state(ctx)
	m := a.an.settings(ctx)
	if !o.State.NoModel {
		o.Destination = m.destination()
	} else if m.Type == modelAnthropic && m.Key == "" {
		o.Reason = a.lang.T("no_key_reason")
	}
	return o
}

// --- Nastavení ---

type settingsData struct {
	Lang        string
	Version     string
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
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := settingsData{Saved: q.Get("saved") == "1", Forgot: q.Get("forgot") == "1", NoKey: q.Get("nokey") == "1"}
	if a.an != nil {
		d.S = a.an.settings(r.Context())
	}
	a.renderSettings(w, r, d)
}

func keyHint(k string) string {
	if k == "" {
		return ""
	}
	if n := utf8.RuneCountInString(k); n > 4 {
		k = string([]rune(k)[n-4:])
	}
	return "…" + k
}

// renderSettings shows the form with d.S in it (saved or as typed).
func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, d settingsData) {
	d.Lang, d.Version = a.lang.code, a.version
	if a.an != nil {
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
	a.render(w, a.pages["settings"], http.StatusOK, d)
}

// sameEndpoint says whether a key saved for one model may be used for
// another: same type and same host (criterion 12).
func sameEndpoint(x, y modelSettings) bool {
	return x.Type == y.Type && hostOf(x.Address) == hostOf(y.Address)
}

func hostOf(addr string) string {
	if u, err := url.Parse(strings.TrimSpace(addr)); err == nil {
		return strings.ToLower(u.Host)
	}
	return addr
}

// settingsFromForm reads the form; Anthropic gets its defaults filled in.
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
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	m := a.settingsFromForm(r)
	newKey := strings.TrimSpace(r.PostForm.Get("key"))
	saved := a.an.settings(r.Context())
	forgot := newKey == "" && saved.Key != "" && !sameEndpoint(saved, m)
	tellForgot := forgot && m.Type != modelPractice // the practice model has no key to ask for
	if err := a.an.saveSettings(r.Context(), m, newKey, forgot); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	dest := "/settings?saved=1"
	if tellForgot {
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
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	m := a.settingsFromForm(r)
	saved := a.an.settings(r.Context())
	if k := strings.TrimSpace(r.PostForm.Get("key")); k != "" {
		m.Key = k
	} else if sameEndpoint(saved, m) {
		m.Key = saved.Key
	}
	d := settingsData{S: m}
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
	if k := strings.TrimSpace(r.PostForm.Get("key")); k != "" {
		d.TypedKey = true // a typed key is never echoed back; it must be typed again to save it
	}
	a.renderSettings(w, r, d)
}

// --- Situace ---

type situationsData struct {
	Lang       string
	Version    string
	Situations []Situation
	Error      string
	Saved      int64 // the situation just saved, kept open
	Added      int64 // the situation just added, opened
}

func (a *App) situationsPage(w http.ResponseWriter, r *http.Request) {
	a.renderSituations(w, r, http.StatusOK, "")
}

func (a *App) renderSituations(w http.ResponseWriter, r *http.Request, status int, errText string) {
	sits, err := a.store.Situations(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	saved, _ := strconv.ParseInt(r.URL.Query().Get("saved"), 10, 64)
	added, _ := strconv.ParseInt(r.URL.Query().Get("added"), 10, 64)
	a.render(w, a.pages["situations"], status, situationsData{Lang: a.lang.code, Version: a.version, Situations: sits, Error: errText, Saved: saved, Added: added})
}

var (
	sensitivities = map[string]bool{"low": true, "medium": true, "high": true}
	actions       = map[string]bool{"record": true, "digest": true, "alert": true}
)

func situationFromForm(r *http.Request) (Situation, bool) {
	f := r.PostForm
	x := Situation{
		Name:        strings.TrimSpace(f.Get("name")),
		Description: strings.TrimSpace(f.Get("description")),
		Sensitivity: f.Get("sensitivity"),
		Action:      f.Get("action"),
		Enabled:     f.Get("enabled") == "1",
	}
	ok := x.Name != "" && x.Description != "" && utf8.RuneCountInString(x.Name) <= 80 &&
		utf8.RuneCountInString(x.Description) <= 1000 && sensitivities[x.Sensitivity] && actions[x.Action]
	return x, ok
}

func (a *App) addSituation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	x, ok := situationFromForm(r)
	if !ok {
		a.renderSituations(w, r, http.StatusUnprocessableEntity, a.lang.T("sit_incomplete"))
		return
	}
	id, err := a.store.AddSituation(r.Context(), x)
	if err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	sid := strconv.FormatInt(id, 10)
	http.Redirect(w, r, "/situations?added="+sid+"#sit-"+sid, http.StatusSeeOther)
}

func (a *App) updateSituation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("delete") == "1" {
		if r.PostForm.Get("confirmed") != "1" { // ask first
			http.Redirect(w, r, "/situations/"+strconv.FormatInt(id, 10)+"/delete", http.StatusSeeOther)
			return
		}
		if err := a.store.DeleteSituation(r.Context(), id); err != nil {
			http.Error(w, "could not delete", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/situations", http.StatusSeeOther)
		return
	}
	x, ok := situationFromForm(r)
	if !ok {
		a.renderSituations(w, r, http.StatusUnprocessableEntity, a.lang.T("sit_incomplete"))
		return
	}
	x.ID = id
	if err := a.store.UpdateSituation(r.Context(), x); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/situations?saved="+strconv.FormatInt(id, 10)+"#sit-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// deleteSituationPage asks before deleting an added situation.
func (a *App) deleteSituationPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/situations", http.StatusSeeOther)
		return
	}
	x, err := a.store.Situation(r.Context(), id)
	if err != nil || x.Builtin {
		http.Redirect(w, r, "/situations", http.StatusSeeOther)
		return
	}
	a.render(w, a.pages["delete"], http.StatusOK, map[string]any{"Lang": a.lang.code, "Version": a.version, "S": x})
}

// --- Nálezy ---

type findingsData struct {
	Lang     string
	Version  string
	Findings []Finding
}

func (a *App) findingsPage(w http.ResponseWriter, r *http.Request) {
	fs, err := a.store.Findings(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	a.render(w, a.pages["findings"], http.StatusOK, findingsData{Lang: a.lang.code, Version: a.version, Findings: fs})
	// Opening the page counts as opening the findings (criterion 18).
	_ = a.store.MarkFindingsSeen(r.Context())
}

func (a *App) markFinding(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	status := map[string]string{"resolved": "resolved", "false_alarm": "false_alarm", "open": "open"}[r.PathValue("status")]
	if status == "" {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	if err := a.store.MarkFinding(r.Context(), id, status); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/findings", http.StatusSeeOther)
}

// --- practice addresses for phase 2 (criterion 21) ---

func (a *App) devRunAnalysis(w http.ResponseWriter, r *http.Request) {
	a.an.runOnce(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) devModelUnreachable(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p := a.an.practice
	p.mu.Lock()
	p.unreachable = r.PostForm.Get("on") == "1"
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
