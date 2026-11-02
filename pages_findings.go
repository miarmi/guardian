package guardian

import (
	"errors"
	"log"
	"net/http"
)

// Nálezy: what the analysis found and what the child asked for
// (criteria 16–18, 26, 30).

type findingsData struct {
	page
	Findings []Finding
	Requests []HelpRequest // the child's requests through the help link (criterion 30)
}

func (a *App) findingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fs, err := a.store.Findings(ctx)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	reqs, err := a.store.HelpRequests(ctx)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	a.render(w, "findings", http.StatusOK, findingsData{page: a.page(), Findings: fs, Requests: reqs})
	// Opening the page counts as opening the findings (criteria 18, 26, 30).
	if err := errors.Join(a.store.MarkFindingsSeen(ctx), a.store.MarkHelpSeen(ctx), a.store.MarkHelpRequestsSeen(ctx)); err != nil {
		log.Printf("findings: marking seen: %v", err)
	}
}

var (
	findingStatuses = map[string]bool{"resolved": true, "false_alarm": true, "open": true}
	requestStatuses = map[string]bool{"resolved": true, "open": true}
)

func (a *App) markFinding(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	status := r.PathValue("status")
	if !findingStatuses[status] {
		http.Error(w, "bad status", http.StatusBadRequest)
		return
	}
	if err := a.store.MarkFinding(r.Context(), id, status); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/findings", http.StatusSeeOther)
}

func (a *App) markHelpRequest(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	status := r.PathValue("status")
	if err != nil || !requestStatuses[status] {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.store.MarkHelpRequest(r.Context(), id, status); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/findings", http.StatusSeeOther)
}
