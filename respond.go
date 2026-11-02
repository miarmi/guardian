package guardian

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Small helpers the handlers share.

const (
	formLimit   = 16 << 10 // a page's form
	toggleLimit = 4 << 10  // a practice switch: on=1 or on=0
)

// render fills the named page and sends it; a template error sends a short
// 500 instead of half a page.
func (a *App) render(w http.ResponseWriter, name string, status int, data any) {
	var buf bytes.Buffer
	if err := a.pages[name].Execute(&buf, data); err != nil {
		log.Printf("template: %v", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// parseForm reads a posted form of at most limit bytes. On failure it has
// answered 400 and returns false.
func parseForm(w http.ResponseWriter, r *http.Request, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	return true
}

// toggle is a practice switch: POST on=1 turns it on, anything else off.
func toggle(set func(on bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !parseForm(w, r, toggleLimit) {
			return
		}
		set(r.PostForm.Get("on") == "1")
		w.WriteHeader(http.StatusNoContent)
	}
}

// pathID reads the {id} part of the address.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// writeJSON sends v as JSON that is never cached.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// formText is a trimmed form field cut to at most max characters.
func formText(r *http.Request, field string, max int) string {
	s := strings.TrimSpace(r.PostForm.Get(field))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}
