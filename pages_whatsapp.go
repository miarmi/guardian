package guardian

import (
	"log"
	"net/http"
	"time"

	"github.com/skip2/go-qrcode"
)

// Linking and unlinking the child's WhatsApp.

func (a *App) startConnect(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil {
		http.Error(w, a.lang.T("not_previews"), http.StatusConflict)
		return
	}
	if err := a.wa.StartPairing(r.Context()); err != nil {
		log.Printf("whatsapp: pairing: %v", err)
		http.Redirect(w, r, "/whatsapp/connect?error=unreachable", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/whatsapp/connect", http.StatusSeeOther)
}

type connectData struct {
	page
	State waState
	HasQR bool
	Error string
}

func (a *App) connectPage(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s := a.wa.Status()
	if s.State == waConnected {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	errText := ""
	if r.URL.Query().Get("error") == "unreachable" {
		errText = a.lang.T("unreachable")
	}
	a.render(w, "connect", http.StatusOK, connectData{page: a.page(), State: s.State, HasQR: s.QR != "", Error: errText})
}

// qrImage draws the current QR code; 204 while there is none yet.
func (a *App) qrImage(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil {
		http.NotFound(w, r)
		return
	}
	s := a.wa.Status()
	if s.QR == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	png, err := qrcode.Encode(s.QR, qrcode.Medium, 512)
	if err != nil {
		http.Error(w, "could not draw the code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// statusJSON is polled by the connect page: {"state": "...", "qr": "<hash>"}.
// The hash changes with every fresh code so the page knows when to reload the image.
func (a *App) statusJSON(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil {
		writeJSON(w, map[string]any{"state": "unavailable"})
		return
	}
	s := a.wa.Status()
	left := 0
	if !s.QRExpires.IsZero() {
		left = max(0, int(time.Until(s.QRExpires).Seconds()))
	}
	writeJSON(w, map[string]any{"state": string(s.State), "qr": shortHash(s.QR), "seconds": left,
		"countdown": a.lang.T("new_code_in", left)})
}

// disconnectPage asks before logging the device out: one tap should not undo
// a pairing that needs the phone in hand to redo.
func (a *App) disconnectPage(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil || a.wa.Status().State != waConnected {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, "disconnect", http.StatusOK, a.page())
}

func (a *App) disconnect(w http.ResponseWriter, r *http.Request) {
	if a.wa != nil {
		if err := a.wa.Disconnect(r.Context()); err != nil {
			log.Printf("whatsapp: disconnect: %v", err)
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
