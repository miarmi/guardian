package guardian

import (
	"net/http"
	"slices"
	"time"
)

// Practice addresses for the analysis and the notifications (criteria 21,
// 29), registered only in practice mode. The pretend phone's addresses are
// in practice.go.

func (a *App) devRunAnalysis(w http.ResponseWriter, r *http.Request) {
	a.an.runOnce(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) devModelUnreachable(w http.ResponseWriter, r *http.Request) {
	toggle(a.an.practice.setUnreachable)(w, r)
}

// devOutbox lists what was "sent", newest first.
func (a *App) devOutbox(w http.ResponseWriter, r *http.Request) {
	n := a.nt
	n.mu.Lock()
	out := make([]outboxItem, len(n.outbox)) // [] rather than null when empty
	copy(out, n.outbox)
	n.mu.Unlock()
	slices.Reverse(out)
	for i := range out {
		if out[i].Links == nil {
			out[i].Links = []string{}
		}
	}
	writeJSON(w, out)
}

func (a *App) devDigest(w http.ResponseWriter, r *http.Request) {
	if err := a.nt.sendDigest(r.Context(), time.Now()); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) devMailDown(w http.ResponseWriter, r *http.Request) {
	toggle(func(on bool) {
		a.nt.mu.Lock()
		a.nt.mailDown = on
		a.nt.mu.Unlock()
	})(w, r)
}

// devGmailMessage puts an e-mail into the practice mailbox (criterion 36).
func (a *App) devGmailMessage(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r, formLimit) {
		return
	}
	a.an.practiceBox.add(r.PostForm.Get("from"), r.PostForm.Get("subject"), r.PostForm.Get("text"))
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) devGmailDown(w http.ResponseWriter, r *http.Request) {
	toggle(func(on bool) {
		b := a.an.practiceBox
		b.mu.Lock()
		b.unreachable = on
		b.mu.Unlock()
	})(w, r)
}
