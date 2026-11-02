package guardian

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Situace: what the analysis watches for (criterion 14).

type situationsData struct {
	page
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
	a.render(w, "situations", status, situationsData{page: a.page(), Situations: sits, Error: errText, Saved: saved, Added: added})
}

var (
	sensitivities = map[string]bool{"low": true, "medium": true, "high": true}
	actions       = map[string]bool{"record": true, "digest": true, "alert": true}
)

// situationFromForm reads a situation; ok is false when something is
// missing, too long or not one of the choices.
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

// situationAddress opens the situation on the page after a change.
func situationAddress(param string, id int64) string {
	sid := strconv.FormatInt(id, 10)
	return "/situations?" + param + "=" + sid + "#sit-" + sid
}

func (a *App) addSituation(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r, formLimit) {
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
	http.Redirect(w, r, situationAddress("added", id), http.StatusSeeOther)
}

// updateSituation saves a situation, or deletes it once confirmed.
func (a *App) updateSituation(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	if r.PostForm.Get("delete") == "1" {
		a.deleteSituation(w, r, id)
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
	http.Redirect(w, r, situationAddress("saved", id), http.StatusSeeOther)
}

func (a *App) deleteSituation(w http.ResponseWriter, r *http.Request, id int64) {
	if r.PostForm.Get("confirmed") != "1" { // ask first
		http.Redirect(w, r, "/situations/"+strconv.FormatInt(id, 10)+"/delete", http.StatusSeeOther)
		return
	}
	if err := a.store.DeleteSituation(r.Context(), id); err != nil {
		http.Error(w, "could not delete", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/situations", http.StatusSeeOther)
}

// deleteSituationPage asks before deleting an added situation.
func (a *App) deleteSituationPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Redirect(w, r, "/situations", http.StatusSeeOther)
		return
	}
	x, err := a.store.Situation(r.Context(), id)
	if err != nil || x.Builtin {
		http.Redirect(w, r, "/situations", http.StatusSeeOther)
		return
	}
	a.render(w, "delete", http.StatusOK, struct {
		page
		S Situation
	}{a.page(), x})
}
