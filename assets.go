package guardian

import (
	"encoding/json"
	"net/http"
)

// Files that aren't pages: the health check, the 404 page, the home-screen
// manifest and icons, the service worker and the child's stylesheet.

func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	if err := a.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok"))
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.render(w, "notfound", http.StatusNotFound, a.page())
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

// serviceWorker shows pushes and opens Guardian when one is tapped. It is
// served from the root so its scope covers every page.
func (a *App) serviceWorker(w http.ResponseWriter, r *http.Request) {
	a.embedded(w, r, "web/sw.js", "text/javascript; charset=utf-8", "no-cache")
}

// kidStyle serves the stylesheet under /kid/, the only path the child can
// reach without the lab login.
func (a *App) kidStyle(w http.ResponseWriter, r *http.Request) {
	a.embedded(w, r, "web/style.css", "text/css; charset=utf-8", "public, max-age=3600")
}

func (a *App) embedded(w http.ResponseWriter, r *http.Request, path, contentType, cache string) {
	b, err := webFS.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cache)
	w.Write(b)
}
