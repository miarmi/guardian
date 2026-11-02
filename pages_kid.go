package guardian

import (
	"context"
	"net/http"
	"time"
)

// The child's pages, reachable without the lab login: the page about one
// finding (criterion 26) and the personal help page (criterion 30). Opening
// either changes nothing; only pressing the button acts.

const childMessageMax = 1000 // characters the child may write

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

func (a *App) newKidData(thanks, personal bool) kidData {
	return kidData{Lang: a.lang.code, Contacts: a.lang.contacts, Thanks: thanks, Personal: personal,
		HelplineLabel: a.lang.helplineLabel, HelplineTel: a.lang.helplineTel}
}

// renderKid sends a child's page; an invalid link answers 404 with only the
// expired text and the contacts. Nothing of it leaks to other sites.
func (a *App) renderKid(w http.ResponseWriter, d kidData) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
	status := http.StatusOK
	if !d.Valid {
		status = http.StatusNotFound
	}
	a.render(w, "kid", status, d)
}

// --- a finding's page ---

func (a *App) kidPage(w http.ResponseWriter, r *http.Request) {
	a.renderFindingForKid(w, r, false)
}

func (a *App) renderFindingForKid(w http.ResponseWriter, r *http.Request, thanks bool) {
	d := a.newKidData(thanks, false)
	token := r.PathValue("token")
	if id, ok := a.nt.checkFindingToken(token); ok {
		if f, found, err := a.store.Finding(r.Context(), id); err == nil && found {
			d.Valid, d.Finding, d.Token = true, f, token
			d.Resolved = f.Status == "resolved"
			if f.ChildHelp && !thanks {
				when := a.lang.when(f.HelpAt.In(a.zone), time.Now().In(a.zone))
				d.Already = a.lang.T("kid_already", when)
			}
		}
	}
	a.renderKid(w, d)
}

// kidAsk is the child pressing "yes, I need help" on a finding's page.
func (a *App) kidAsk(w http.ResponseWriter, r *http.Request) {
	id, ok := a.nt.checkFindingToken(r.PathValue("token"))
	if !ok {
		a.renderFindingForKid(w, r, false)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	msg := formText(r, "message", childMessageMax)
	f, found, err := a.store.Finding(r.Context(), id)
	if err != nil || !found {
		a.renderFindingForKid(w, r, false)
		return
	}
	if !f.ChildHelp {
		asked, err := a.store.AskForHelp(r.Context(), id, msg)
		if err != nil {
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
		if asked {
			a.nt.onHelp(context.WithoutCancel(r.Context()), f)
		}
	}
	a.renderFindingForKid(w, r, true)
}

// --- the personal help page ---

func (a *App) helpPage(w http.ResponseWriter, r *http.Request) {
	a.renderHelpForKid(w, r, false)
}

func (a *App) renderHelpForKid(w http.ResponseWriter, r *http.Request, thanks bool) {
	d := a.newKidData(thanks, true)
	if token := r.PathValue("token"); a.nt.checkHelpToken(r.Context(), token) {
		d.Valid, d.Token = true, token
	}
	a.renderKid(w, d)
}

// helpAsk is the child asking for help with anything; a second request
// soon after adds to the first and tells the parents only once.
func (a *App) helpAsk(w http.ResponseWriter, r *http.Request) {
	if !a.nt.checkHelpToken(r.Context(), r.PathValue("token")) {
		a.renderHelpForKid(w, r, false)
		return
	}
	if !parseForm(w, r, formLimit) {
		return
	}
	msg := formText(r, "message", childMessageMax)
	_, isNew, err := a.store.AddHelpRequest(r.Context(), msg, func(t time.Time) string { return t.In(a.zone).Format("15:04") })
	if err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	if isNew {
		a.nt.onHelpRequest(context.WithoutCancel(r.Context()))
	}
	a.renderHelpForKid(w, r, true)
}
