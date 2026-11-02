package guardian

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"
)

// Push devices (criterion 22): the parents' browsers that get notifications.

func (a *App) pushKey(w http.ResponseWriter, r *http.Request) {
	key, err := a.nt.vapidKey()
	if err != nil {
		http.Error(w, "no key", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"key": vapidPublic(key), "practice": a.dev != nil})
}

// pushSubscribe saves a browser's subscription. In practice mode a pretend
// device is saved instead, which only feeds the outbox.
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
	if id, err := pathID(r); err == nil {
		a.store.RemovePushDevice(r.Context(), id)
	}
	http.Redirect(w, r, "/settings#notifications", http.StatusSeeOther)
}
