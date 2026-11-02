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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

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
	// Practice runs a pretend WhatsApp instead of the real one, driven
	// through /dev/whatsapp/* addresses, so tests can play the child's phone.
	// Never set it together with Connect, and never where real people look.
	Practice bool
	// Env names the environment for the page: "prod", a preview name, or "".
	Env string
	// Version is shown in the footer (a commit, say).
	Version string
	// TimeZone for the times on the pages, e.g. "Europe/Prague". Defaults to UTC.
	TimeZone string
	// Language of the pages: "en" (default) or "cs".
	Language string
	// BaseURL is the app's own address, for links in e-mails and pushes
	// (e.g. https://guardian.lab.example.cz).
	BaseURL string
}

// App is one running Guardian: a store, optionally a WhatsApp connector, and
// the pages that show them.
type App struct {
	store    *store
	wa       connector // nil when neither Connect nor Practice is set
	dev      *practice // non-nil only in practice mode
	an       *analyzer // nil when wa is nil (previews): nothing is analysed there
	nt       *notifier // always: the child's links must answer everywhere
	pages    map[string]*template.Template
	stop     context.CancelFunc
	preview  string
	version  string
	index    *template.Template
	connect  *template.Template
	notfound *template.Template
	disc     *template.Template
	static   http.Handler
	lang     *lang
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
	if cfg.Connect && cfg.Practice {
		st.Close()
		return nil, errors.New("guardian: Connect and Practice exclude each other")
	}
	switch {
	case cfg.Connect:
		wa, err := newWhatsApp(ctx, cfg.SecretsDir, st)
		if err != nil {
			st.Close()
			return nil, fmt.Errorf("guardian: whatsapp: %w", err)
		}
		a := newApp(st, wa, cfg)
		loopCtx, cancel := context.WithCancel(context.Background())
		a.stop = cancel
		go a.an.loop(loopCtx)
		go a.nt.digestLoop(loopCtx)
		return a, nil
	case cfg.Practice:
		p := newPractice(st)
		a := newApp(st, p, cfg)
		a.dev = p
		return a, nil
	}
	return newApp(st, nil, cfg), nil
}

// Close stops the connector and closes the database.
func (a *App) Close() error {
	if a.stop != nil {
		a.stop()
	}
	if a.wa != nil {
		a.wa.Close()
	}
	return a.store.Close()
}

func newApp(st *store, wa connector, cfg Config) *App {
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
	l := pick(cfg.Language)
	funcs := template.FuncMap{
		"when":  func(t time.Time) string { return l.when(t.In(zone), time.Now().In(zone)) },
		"count": l.count,
		"t":     l.T,
		"list":  func(v ...string) []string { return v },
		"clock": func(t time.Time) string { return t.In(zone).Format("15:04") },
		"newSituation": func() Situation {
			return Situation{Sensitivity: "medium", Action: "digest", Enabled: true, Recipients: "both"}
		},
		"linkify": linkify,
	}
	a := &App{
		store:    st,
		wa:       wa,
		preview:  preview,
		version:  version,
		index:    template.Must(template.New("index.html").Funcs(funcs).ParseFS(webFS, "web/index.html")),
		connect:  template.Must(template.New("connect.html").Funcs(funcs).ParseFS(webFS, "web/connect.html")),
		notfound: template.Must(template.New("notfound.html").Funcs(funcs).ParseFS(webFS, "web/notfound.html")),
		disc:     template.Must(template.New("disconnect.html").Funcs(funcs).ParseFS(webFS, "web/disconnect.html")),
		lang:     l,
		pages: map[string]*template.Template{
			"settings":   template.Must(template.New("settings.html").Funcs(funcs).ParseFS(webFS, "web/settings.html")),
			"situations": template.Must(template.New("situations.html").Funcs(funcs).ParseFS(webFS, "web/situations.html")),
			"findings":   template.Must(template.New("findings.html").Funcs(funcs).ParseFS(webFS, "web/findings.html")),
			"delete":     template.Must(template.New("delsituation.html").Funcs(funcs).ParseFS(webFS, "web/delsituation.html")),
			"kid":        template.Must(template.New("kid.html").Funcs(funcs).ParseFS(webFS, "web/kid.html")),
		},
		static: http.StripPrefix("/static/", http.FileServer(http.FS(sub))),
		icons:  makeIcons(),
		zone:   zone,
	}
	a.nt = &notifier{st: st, lang: l, zone: zone, secretsDir: cfg.SecretsDir, baseURL: cfg.BaseURL, practice: cfg.Practice}
	if wa != nil {
		a.an = &analyzer{st: st, lang: l, zone: zone, secretsDir: cfg.SecretsDir, notify: a.nt.onFinding, afterLook: a.nt.retryChildMails}
		if cfg.Practice {
			a.an.practice = &practiceModel{reason: l.T("practice_reason"), errText: l.T("practice_unreachable")}
		}
		if err := st.SeedSituations(context.Background(), l.situations); err != nil {
			log.Printf("situations: %v", err)
		}
	}
	return a
}

// Handler serves the pages. Mount it at the root of a host.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.indexPage)
	mux.HandleFunc("POST /whatsapp/connect", a.startConnect)
	mux.HandleFunc("GET /whatsapp/connect", a.connectPage)
	mux.HandleFunc("GET /whatsapp/qr.png", a.qrImage)
	mux.HandleFunc("GET /whatsapp/status.json", a.statusJSON)
	mux.HandleFunc("GET /whatsapp/disconnect", a.disconnectPage)
	mux.HandleFunc("POST /whatsapp/disconnect", a.disconnect)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /manifest.webmanifest", a.manifest)
	mux.HandleFunc("GET /apple-touch-icon.png", a.icon("apple-touch-icon.png"))
	mux.HandleFunc("GET /icon-192.png", a.icon("icon-192.png"))
	mux.HandleFunc("GET /icon-512.png", a.icon("icon-512.png"))
	mux.Handle("GET /static/style.css", a.static)
	mux.Handle("GET /static/connect.js", a.static)
	mux.HandleFunc("GET /whatsapp/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	mux.HandleFunc("GET /settings", a.settingsPage)
	mux.HandleFunc("POST /settings", a.saveSettings)
	mux.HandleFunc("POST /settings/test", a.testModel)
	mux.HandleFunc("GET /situations", a.situationsPage)
	mux.HandleFunc("POST /situations", a.addSituation)
	mux.HandleFunc("POST /situations/{id}", a.updateSituation)
	mux.HandleFunc("GET /situations/{id}/delete", a.deleteSituationPage)
	mux.HandleFunc("GET /findings", a.findingsPage)
	mux.HandleFunc("POST /findings/{id}/{status}", a.markFinding)
	mux.Handle("GET /static/settings.js", a.static)
	mux.Handle("GET /static/push.js", a.static)
	mux.HandleFunc("GET /sw.js", a.serviceWorker)
	mux.HandleFunc("GET /kid/style.css", a.kidStyle)
	mux.HandleFunc("GET /kid/{token}", a.kidPage)
	mux.HandleFunc("POST /kid/{token}", a.kidAsk)
	mux.HandleFunc("GET /push/key", a.pushKey)
	mux.HandleFunc("POST /push/subscribe", a.pushSubscribe)
	mux.HandleFunc("POST /push/devices/{id}/delete", a.pushRemove)
	mux.HandleFunc("POST /push/test", a.pushTest)
	mux.HandleFunc("POST /settings/mail", a.saveMail)
	mux.HandleFunc("POST /settings/mail/test", a.testMailHandler)
	if a.dev != nil {
		a.dev.routes(mux)
		if a.an != nil && a.an.practice != nil {
			mux.HandleFunc("POST /dev/analysis/run", a.devRunAnalysis)
			mux.HandleFunc("POST /dev/model/unreachable", a.devModelUnreachable)
			mux.HandleFunc("GET /dev/outbox", a.nt.devOutbox)
			mux.HandleFunc("POST /dev/digest/run", a.nt.devDigest)
			mux.HandleFunc("POST /dev/mail/unreachable", a.nt.devMailDown)
		}
	}
	mux.HandleFunc("/", a.notFound)
	return logRequests(securityHeaders(http.NewCrossOriginProtection().Handler(mux)))
}

// connector is what the pages need from a WhatsApp connection, real or practice.
type connector interface {
	Status() waStatus
	StartPairing(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Close()
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
		return connectorView{StateText: a.lang.T("not_previews")}
	}
	s := a.wa.Status()
	switch s.State {
	case waConnected:
		return connectorView{Available: true, StateText: a.lang.T("st_as", formatNumber(s.Number)), Connected: true}
	case waPairing:
		return connectorView{Available: true, StateText: a.lang.T("st_not"), CanConnect: true}
	case waPhoneRemoved:
		return connectorView{Available: true, StateText: a.lang.T("st_removed"), Note: a.lang.T("removed_next"), CanConnect: true}
	default:
		return connectorView{Available: true, StateText: a.lang.T("st_not"), CanConnect: true}
	}
}

type indexData struct {
	Preview   string // preview name, "" in production and development
	Version   string
	Connector connectorView
	Chats     []ChatSummary
	Lang      string
	Analysis  overviewAnalysis
}

func (a *App) indexPage(w http.ResponseWriter, r *http.Request) {
	chats, err := a.store.Chats(r.Context())
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	// Two chats with the same name get the end of the number to tell them apart.
	seen := map[string]int{}
	for _, c := range chats {
		seen[c.Name]++
	}
	// Number same-name chats by when they first appeared, so a label never
	// moves to another chat.
	order := map[string][]int64{}
	for _, c := range chats {
		if c.Name != "" && seen[c.Name] > 1 {
			order[c.Name] = append(order[c.Name], c.First)
		}
	}
	for _, firsts := range order {
		sort.Slice(firsts, func(i, j int) bool { return firsts[i] < firsts[j] })
	}
	for i, c := range chats {
		if c.Name == "" || seen[c.Name] < 2 {
			continue
		}
		user, _, _ := strings.Cut(c.JID, "@")
		if !c.IsGroup && len(user) > 3 && strings.Trim(user, "0123456789") == "" {
			chats[i].Suffix = "…" + user[len(user)-3:]
			continue
		}
		for n, f := range order[c.Name] {
			if f == c.First {
				chats[i].Suffix = "(" + strconv.Itoa(n+1) + ")"
			}
		}
	}
	a.render(w, a.index, http.StatusOK, indexData{
		Preview: a.preview, Version: a.version, Connector: a.connector(), Chats: chats, Lang: a.lang.code,
		Analysis: a.overviewAnalysis(r.Context()),
	})
}

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
	Version string
	State   waState
	HasQR   bool
	Error   string
	Lang    string
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
	a.render(w, a.connect, http.StatusOK, connectData{Version: a.version, State: s.State, HasQR: s.QR != "", Error: errText, Lang: a.lang.code})
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
	json.NewEncoder(w).Encode(map[string]any{"state": string(s.State), "qr": shortHash(s.QR), "seconds": left,
		"countdown": a.lang.T("new_code_in", left)})
}

// disconnectPage asks before logging the device out: one tap should not undo
// a pairing that needs the phone in hand to redo.
func (a *App) disconnectPage(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil || a.wa.Status().State != waConnected {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	a.render(w, a.disc, http.StatusOK, map[string]string{"Version": a.version, "Lang": a.lang.code})
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

// kidStyle serves the stylesheet under /kid/, the only path the child can
// reach without the lab login.
func (a *App) kidStyle(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/style.css")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(b)
}

// serviceWorker shows pushes and opens Guardian when one is tapped.
func (a *App) serviceWorker(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.render(w, a.notfound, http.StatusNotFound, map[string]string{"Version": a.version, "Lang": a.lang.code})
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

// formatNumber groups a phone number the way people write it. Czech and
// Slovak numbers get the usual 3-3-3 grouping; others stay as they are.
func formatNumber(digits string) string {
	if (strings.HasPrefix(digits, "420") || strings.HasPrefix(digits, "421")) && len(digits) == 12 {
		return "+" + digits[:3] + " " + digits[3:6] + " " + digits[6:9] + " " + digits[9:]
	}
	return "+" + digits
}
