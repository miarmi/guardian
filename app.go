package guardian

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"time"
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
	store *store
	wa    connector // nil when neither Connect nor Practice is set
	dev   *practice // non-nil only in practice mode
	an    *analyzer // nil when wa is nil (previews): nothing is analysed there
	nt    *notifier // always: the child's links must answer everywhere
	stop  context.CancelFunc

	pages   map[string]*template.Template // by the names in pageFiles
	static  http.Handler
	icons   map[string][]byte
	lang    *lang
	zone    *time.Location
	preview string // preview name, "" in production and development
	version string // short, for the footer
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

// pageFiles are the page templates in web/, by the name render uses.
var pageFiles = map[string]string{
	"index":      "index.html",
	"connect":    "connect.html",
	"notfound":   "notfound.html",
	"disconnect": "disconnect.html",
	"settings":   "settings.html",
	"situations": "situations.html",
	"findings":   "findings.html",
	"platforms":  "platforms.html",
	"delete":     "delsituation.html",
	"kid":        "kid.html",
}

func newApp(st *store, wa connector, cfg Config) *App {
	zone := time.UTC
	if cfg.TimeZone != "" {
		if z, err := time.LoadLocation(cfg.TimeZone); err == nil {
			zone = z
		}
	}
	l := pick(cfg.Language)
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	a := &App{
		store:   st,
		wa:      wa,
		pages:   parsePages(l, zone),
		static:  http.StripPrefix("/static/", http.FileServer(http.FS(sub))),
		icons:   makeIcons(),
		lang:    l,
		zone:    zone,
		preview: previewName(cfg.Env),
		version: shortVersion(cfg.Version),
	}
	a.nt = &notifier{st: st, lang: l, zone: zone, secretsDir: cfg.SecretsDir, baseURL: cfg.BaseURL, practice: cfg.Practice}
	if wa != nil {
		a.an = &analyzer{st: st, lang: l, zone: zone, secretsDir: cfg.SecretsDir, notify: a.nt.onFinding, afterLook: a.nt.retryChildMails, box: gmailBox{}}
		if cfg.Practice {
			a.an.practice = &practiceModel{reason: l.T("practice_reason"), errText: l.T("practice_unreachable")}
			a.an.practiceBox = &practiceBox{errText: l.T("practice_gmail_down")}
		}
	}
	// Previews too, so a preview shows the default set production will get.
	if err := st.SeedSituations(context.Background(), l.situations); err != nil {
		log.Printf("situations: %v", err)
	}
	return a
}

// parsePages parses every page template with the functions they share.
func parsePages(l *lang, zone *time.Location) map[string]*template.Template {
	funcs := template.FuncMap{
		"when":  func(t time.Time) string { return l.when(t.In(zone), time.Now().In(zone)) },
		"count": l.count,
		"t":     l.T,
		"list":  func(v ...string) []string { return v },
		"clock": func(t time.Time) string { return t.In(zone).Format("15:04") },
		"newSituation": func() Situation {
			return Situation{Sensitivity: "medium", Action: "digest", Enabled: true}
		},
		"linkify": linkify,
	}
	pages := make(map[string]*template.Template, len(pageFiles))
	for name, file := range pageFiles {
		pages[name] = template.Must(template.New(file).Funcs(funcs).ParseFS(webFS, "web/"+file))
	}
	return pages
}

// previewName is the preview's name for the page, "" in production and
// development.
func previewName(env string) string {
	if env == "prod" {
		return ""
	}
	return env
}

// shortVersion keeps a commit short enough for the footer.
func shortVersion(v string) string {
	switch {
	case v == "":
		return "dev"
	case len(v) > 7:
		return v[:7]
	}
	return v
}

// page is what every parent's page needs for its frame.
type page struct {
	Lang    string
	Version string
}

func (a *App) page() page { return page{Lang: a.lang.code, Version: a.version} }
