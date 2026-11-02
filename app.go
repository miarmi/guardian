package guardian

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

const (
	msgNoChats     = "No messages yet."
	textNoPreviews = "Not available in previews"
	msgUnreachable = "Could not reach WhatsApp. Check the server's internet connection and try again."
)

// Config says where Guardian keeps its data and whether it may connect.
type Config struct {
	// DataDir holds the app's database. Required.
	DataDir string
	// SecretsDir holds the WhatsApp login. It must never be copied to another
	// instance: two copies of one linked device log each other out. Defaults
	// to DataDir/secrets.
	SecretsDir string
	// Connect allows the WhatsApp connector to run. Leave it false for
	// previews, tests and other copies of the app.
	Connect bool
	// Env names the environment for the page: "prod", a preview name, or "".
	Env string
	// Version is shown in the footer (a commit, say).
	Version string
	// TimeZone for the times on the pages, e.g. "Europe/Prague". Defaults to UTC.
	TimeZone string
}

// App is one running Guardian: a store, optionally a WhatsApp connector, and
// the pages that show them.
type App struct {
	store    *store
	wa       *whatsapp // nil when Config.Connect is false
	preview  string
	version  string
	index    *template.Template
	connect  *template.Template
	notfound *template.Template
	static   http.Handler
	icons    map[string][]byte
	zone     *time.Location
}

// New opens the database and, when allowed, the WhatsApp connector.
func New(ctx context.Context, cfg Config) (*App, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("guardian: DataDir is required")
	}
	if cfg.SecretsDir == "" {
		cfg.SecretsDir = filepath.Join(cfg.DataDir, "secrets")
	}
	st, err := openStore(filepath.Join(cfg.DataDir, "guardian.db"))
	if err != nil {
		return nil, fmt.Errorf("guardian: opening database: %w", err)
	}
	var wa *whatsapp
	if cfg.Connect {
		wa, err = newWhatsApp(ctx, cfg.SecretsDir, st)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("guardian: whatsapp: %w", err)
		}
	}
	return newApp(st, wa, cfg), nil
}

// Close stops the connector and closes the database.
func (a *App) Close() error {
	if a.wa != nil {
		a.wa.Close()
	}
	return a.store.Close()
}

func newApp(st *store, wa *whatsapp, cfg Config) *App {
	preview := ""
	if cfg.Env != "" && cfg.Env != "prod" {
		preview = cfg.Env
	}
	version := "dev"
	if cfg.Version != "" {
		version = cfg.Version
		if len(version) > 7 {
			version = version[:7]
		}
	}
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	zone := time.UTC
	if cfg.TimeZone != "" {
		if z, err := time.LoadLocation(cfg.TimeZone); err == nil {
			zone = z
		}
	}
	funcs := template.FuncMap{
		"when":  func(t time.Time) string { return humanTime(t, zone, time.Now()) },
		"count": formatCount,
	}
	return &App{
		store:    st,
		wa:       wa,
		preview:  preview,
		version:  version,
		index:    template.Must(template.New("index.html").Funcs(funcs).ParseFS(webFS, "web/index.html")),
		connect:  template.Must(template.New("connect.html").Funcs(funcs).ParseFS(webFS, "web/connect.html")),
		notfound: template.Must(template.New("notfound.html").Funcs(funcs).ParseFS(webFS, "web/notfound.html")),
		static:   http.StripPrefix("/static/", http.FileServer(http.FS(sub))),
		icons:    makeIcons(),
		zone:     zone,
	}
}

// Handler serves the pages. Mount it at the root of a host.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.indexPage)
	mux.HandleFunc("POST /whatsapp/connect", a.startConnect)
	mux.HandleFunc("GET /whatsapp/connect", a.connectPage)
	mux.HandleFunc("GET /whatsapp/qr.png", a.qrImage)
	mux.HandleFunc("GET /whatsapp/status.json", a.statusJSON)
	mux.HandleFunc("POST /whatsapp/disconnect", a.disconnect)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /manifest.webmanifest", a.manifest)
	mux.HandleFunc("GET /apple-touch-icon.png", a.icon("apple-touch-icon.png"))
	mux.HandleFunc("GET /icon-192.png", a.icon("icon-192.png"))
	mux.HandleFunc("GET /icon-512.png", a.icon("icon-512.png"))
	mux.Handle("GET /static/style.css", a.static)
	mux.Handle("GET /static/connect.js", a.static)
	mux.HandleFunc("GET /whatsapp/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	mux.HandleFunc("/", a.notFound)
	return logRequests(securityHeaders(http.NewCrossOriginProtection().Handler(mux)))
}

// connectorView is the WhatsApp row of the "Connectors" list.
type connectorView struct {
	Available  bool // false in previews: nothing to press
	StateText  string
	Note       string // one line of help under the state, if any
	CanConnect bool
	Connected  bool
}

func (a *App) connector() connectorView {
	if a.wa == nil {
		return connectorView{StateText: textNoPreviews}
	}
	s := a.wa.Status()
	switch s.State {
	case waConnected:
		return connectorView{Available: true, StateText: "Connected as " + formatNumber(s.Number), Connected: true}
	case waPairing:
		return connectorView{Available: true, StateText: "Not connected", CanConnect: true}
	case waPhoneRemoved:
		return connectorView{Available: true, StateText: "Disconnected by the phone", Note: "The device was removed on the phone. Connect again to continue.", CanConnect: true}
	default:
		return connectorView{Available: true, StateText: "Not connected", CanConnect: true}
	}
}

type indexData struct {
	Preview   string // preview name, "" in production and development
	Version   string
	Connector connectorView
	Chats     []ChatSummary
	NoChats   string
	Error     string
}

func (a *App) indexPage(w http.ResponseWriter, r *http.Request) {
	chats, err := a.store.Chats(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	a.render(w, a.index, http.StatusOK, indexData{
		Preview: a.preview, Version: a.version, Connector: a.connector(), Chats: chats, NoChats: msgNoChats,
	})
}

func (a *App) startConnect(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil {
		http.Error(w, textNoPreviews, http.StatusConflict)
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
	Version string
	State   waState
	HasQR   bool
	Error   string
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
		errText = msgUnreachable
	}
	a.render(w, a.connect, http.StatusOK, connectData{Version: a.version, State: s.State, HasQR: s.QR != "", Error: errText})
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
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if a.wa == nil {
		json.NewEncoder(w).Encode(map[string]any{"state": "unavailable"})
		return
	}
	s := a.wa.Status()
	left := 0
	if !s.QRExpires.IsZero() {
		left = max(0, int(time.Until(s.QRExpires).Seconds()))
	}
	json.NewEncoder(w).Encode(map[string]any{"state": string(s.State), "qr": shortHash(s.QR), "seconds": left})
}

func (a *App) disconnect(w http.ResponseWriter, r *http.Request) {
	if a.wa != nil {
		if err := a.wa.Disconnect(r.Context()); err != nil {
			log.Printf("whatsapp: disconnect: %v", err)
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) render(w http.ResponseWriter, t *template.Template, status int, data any) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Printf("template: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.render(w, a.notfound, http.StatusNotFound, map[string]string{"Version": a.version})
}

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func (a *App) manifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/manifest+json")
	json.NewEncoder(w).Encode(map[string]any{
		"name":             "Guardian",
		"short_name":       "Guardian",
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#f4f3f0",
		"theme_color":      iconColor,
		"icons": []map[string]string{
			{"src": "/icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
	})
}

func (a *App) icon(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(a.icons[name])
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self'; manifest-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

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

func ensureDir(path string) error { return os.MkdirAll(path, 0o700) }

// shortHash is a cheap fingerprint for change detection, not security.
func shortHash(s string) string {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	if s == "" {
		return ""
	}
	return hex8(h)
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	var b [8]byte
	for i := 7; i >= 0; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}

// humanTime says when something happened the way a person would:
// "today 09:05", "yesterday 07:45", "Fri 31 Oct", "27 Oct 2025".
func humanTime(t time.Time, zone *time.Location, now time.Time) string {
	t, now = t.In(zone), now.In(zone)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, zone) }
	switch d := day(now).Sub(day(t)); {
	case d == 0:
		return "today " + t.Format("15:04")
	case d == 24*time.Hour:
		return "yesterday " + t.Format("15:04")
	case d > 0 && d < 7*24*time.Hour:
		return t.Format("Mon 2 Jan")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// formatCount writes "1 message", "58 messages", "12 345 messages".
func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u202f" + s[i:]
	}
	if n == 1 {
		return s + " message"
	}
	return s + " messages"
}

// formatNumber groups a phone number the way people write it. Czech and
// Slovak numbers get the usual 3-3-3 grouping; others stay as they are.
func formatNumber(digits string) string {
	if (strings.HasPrefix(digits, "420") || strings.HasPrefix(digits, "421")) && len(digits) == 12 {
		return "+" + digits[:3] + " " + digits[3:6] + " " + digits[6:9] + " " + digits[9:]
	}
	return "+" + digits
}
