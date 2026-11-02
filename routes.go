package guardian

import (
	"log"
	"net/http"
	"time"
)

// Handler serves the pages. Mount it at the root of a host.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()

	// Overview and the WhatsApp connector (pages_overview.go, pages_whatsapp.go).
	mux.HandleFunc("GET /{$}", a.indexPage)
	mux.HandleFunc("POST /whatsapp/connect", a.startConnect)
	mux.HandleFunc("GET /whatsapp/connect", a.connectPage)
	mux.HandleFunc("GET /whatsapp/qr.png", a.qrImage)
	mux.HandleFunc("GET /whatsapp/status.json", a.statusJSON)
	mux.HandleFunc("GET /whatsapp/disconnect", a.disconnectPage)
	mux.HandleFunc("POST /whatsapp/disconnect", a.disconnect)
	mux.HandleFunc("GET /whatsapp/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })

	// Files and the home-screen app (assets.go).
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /manifest.webmanifest", a.manifest)
	mux.HandleFunc("GET /apple-touch-icon.png", a.icon("apple-touch-icon.png"))
	mux.HandleFunc("GET /icon-192.png", a.icon("icon-192.png"))
	mux.HandleFunc("GET /icon-512.png", a.icon("icon-512.png"))
	mux.Handle("GET /static/style.css", a.static)
	mux.Handle("GET /static/connect.js", a.static)
	mux.Handle("GET /static/settings.js", a.static)
	mux.Handle("GET /static/push.js", a.static)
	mux.HandleFunc("GET /sw.js", a.serviceWorker)

	// Nastavení: the model, e-mail and the child's help link
	// (pages_settings.go, pages_mail.go).
	mux.HandleFunc("GET /settings", a.settingsPage)
	mux.HandleFunc("POST /settings", a.saveSettings)
	mux.HandleFunc("POST /settings/test", a.testModel)
	mux.HandleFunc("POST /settings/mail", a.saveMail)
	mux.HandleFunc("POST /settings/mail/test", a.testMailHandler)
	mux.HandleFunc("POST /settings/helplink", a.sendHelpLinkHandler)

	// Situace and Nálezy (pages_situations.go, pages_findings.go).
	mux.HandleFunc("GET /situations", a.situationsPage)
	mux.HandleFunc("POST /situations", a.addSituation)
	mux.HandleFunc("POST /situations/{id}", a.updateSituation)
	mux.HandleFunc("GET /situations/{id}/delete", a.deleteSituationPage)
	mux.HandleFunc("GET /findings", a.findingsPage)
	mux.HandleFunc("POST /findings/{id}/{status}", a.markFinding)
	mux.HandleFunc("POST /requests/{id}/{status}", a.markHelpRequest)

	// The child's pages, outside the lab login (pages_kid.go).
	mux.HandleFunc("GET /kid/style.css", a.kidStyle)
	mux.HandleFunc("GET /kid/{token}", a.kidPage)
	mux.HandleFunc("POST /kid/{token}", a.kidAsk)
	mux.HandleFunc("GET /kid/help/{token}", a.helpPage)
	mux.HandleFunc("POST /kid/help/{token}", a.helpAsk)

	// Push devices (pages_push.go).
	mux.HandleFunc("GET /push/key", a.pushKey)
	mux.HandleFunc("POST /push/subscribe", a.pushSubscribe)
	mux.HandleFunc("POST /push/devices/{id}/delete", a.pushRemove)
	mux.HandleFunc("POST /push/test", a.pushTest)

	// Practice mode only (practice.go, pages_dev.go).
	if a.dev != nil {
		a.dev.routes(mux)
		if a.an != nil && a.an.practice != nil {
			mux.HandleFunc("POST /dev/analysis/run", a.devRunAnalysis)
			mux.HandleFunc("POST /dev/model/unreachable", a.devModelUnreachable)
			mux.HandleFunc("GET /dev/outbox", a.devOutbox)
			mux.HandleFunc("POST /dev/digest/run", a.devDigest)
			mux.HandleFunc("POST /dev/mail/unreachable", a.devMailDown)
		}
	}

	mux.HandleFunc("/", a.notFound)
	return logRequests(securityHeaders(http.NewCrossOriginProtection().Handler(mux)))
}

// contentSecurityPolicy allows only Guardian's own files, plus data: URIs
// for the font and mark embedded in style.css.
const contentSecurityPolicy = "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; " +
	"img-src 'self' data:; font-src 'self' data:; manifest-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// statusWriter remembers the status code for the request log.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// logRequests logs every request except the frequent polls.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path != "/healthz" && r.URL.Path != "/whatsapp/status.json" {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.code, time.Since(start).Round(time.Millisecond))
		}
	})
}
