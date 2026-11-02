package guardian

import (
	"context"
	"net/http"
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
	if !o.State.NoModel {
		o.Destination = a.an.settings(ctx).destination()
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
	Saved       bool
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	a.renderSettings(w, r, "", false)
}

func (a *App) renderSettings(w http.ResponseWriter, r *http.Request, result string, ok bool) {
	d := settingsData{Lang: a.lang.code, Version: a.version, Result: result, ResultOK: ok, Saved: r.URL.Query().Get("saved") == "1"}
	if a.an != nil {
		d.Available = true
		d.S = a.an.settings(r.Context())
		if d.S.Type == "" {
			d.S.Type = modelAnthropic
		}
		if d.S.Key != "" {
			k := d.S.Key
			if utf8.RuneCountInString(k) > 4 {
				k = string([]rune(k)[utf8.RuneCountInString(k)-4:])
			}
			d.KeyHint = "…" + k
			d.S.Key = ""
		}
		d.Practice = a.dev != nil
		d.Ready = a.an.settings(r.Context()).ready()
		d.Destination = a.an.settings(r.Context()).destination()
	}
	a.render(w, a.pages["settings"], http.StatusOK, d)
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
	}
	if err := a.an.saveSettings(r.Context(), m, strings.TrimSpace(f.Get("key"))); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

func (a *App) testModel(w http.ResponseWriter, r *http.Request) {
	if a.an == nil {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if err := a.an.test(r.Context()); err != nil {
		a.renderSettings(w, r, a.lang.T("model_bad", err.Error()), false)
		return
	}
	a.renderSettings(w, r, a.lang.T("model_ok"), true)
}

// --- Situace ---

type situationsData struct {
	Lang       string
	Version    string
	Situations []Situation
	Error      string
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
	a.render(w, a.pages["situations"], status, situationsData{Lang: a.lang.code, Version: a.version, Situations: sits, Error: errText})
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
	if err := a.store.AddSituation(r.Context(), x); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/situations", http.StatusSeeOther)
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
	http.Redirect(w, r, "/situations", http.StatusSeeOther)
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
	status := map[string]string{"resolved": "resolved", "false_alarm": "false_alarm"}[r.PathValue("status")]
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
