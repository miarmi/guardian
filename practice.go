package guardian

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// practice is a pretend WhatsApp for tests: it behaves like the real
// connector towards the pages, and the tests play the child's phone through
// /dev/whatsapp/* addresses. It never touches the network.
type practice struct {
	st *store

	mu          sync.Mutex
	state       waState
	number      string
	qr          string
	qrExp       time.Time
	codes       int // how many codes this pairing has offered
	unreachable bool
}

const (
	practiceNumber   = "420777000111"
	practiceCodes    = 3
	practiceCodeLife = time.Minute
)

func newPractice(st *store) *practice {
	return &practice{st: st, state: waNotConnected}
}

func (p *practice) Status() waStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := waStatus{State: p.state, QR: p.qr, QRExpires: p.qrExp}
	if p.state == waConnected {
		s.Number = p.number
	}
	return s
}

func (p *practice) StartPairing(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.unreachable:
		return errors.New("practice: WhatsApp unreachable")
	case p.state == waConnected:
		return errors.New("already connected")
	case p.state == waPairing:
		return nil
	}
	p.state, p.codes = waPairing, 0
	p.nextCodeLocked()
	return nil
}

func (p *practice) nextCodeLocked() {
	p.codes++
	if p.codes > practiceCodes {
		p.state, p.qr, p.qrExp = waNotConnected, "", time.Time{}
		return
	}
	p.qr = "practice-" + randomID()
	p.qrExp = time.Now().Add(practiceCodeLife)
}

func (p *practice) Disconnect(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state, p.qr, p.qrExp, p.number = waNotConnected, "", time.Time{}, ""
	return nil
}

func (p *practice) Close() {}

func (p *practice) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /dev/whatsapp/scan", p.scan)
	mux.HandleFunc("POST /dev/whatsapp/next", p.next)
	mux.HandleFunc("POST /dev/whatsapp/message", p.message)
	mux.HandleFunc("POST /dev/whatsapp/remove", p.remove)
	mux.HandleFunc("POST /dev/whatsapp/unreachable", p.setUnreachable)
	mux.HandleFunc("GET /dev/whatsapp/messages", p.messages)
}

func (p *practice) scan(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != waPairing || p.qr == "" {
		http.Error(w, "no code on offer", http.StatusConflict)
		return
	}
	p.state, p.qr, p.qrExp, p.number = waConnected, "", time.Time{}, practiceNumber
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) next(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != waPairing {
		http.Error(w, "not pairing", http.StatusConflict)
		return
	}
	p.nextCodeLocked()
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) remove(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == waConnected {
		p.state, p.number = waPhoneRemoved, ""
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) setUnreachable(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.unreachable = r.PostForm.Get("on") == "1"
	p.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

var practiceKinds = map[string]bool{"text": true, "image": true, "video": true, "audio": true, "sticker": true, "document": true}

func (p *practice) message(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	p.mu.Lock()
	connected := p.state == waConnected
	p.mu.Unlock()
	if !connected {
		http.Error(w, "not connected", http.StatusConflict)
		return
	}
	chat := strings.TrimSpace(f.Get("chat"))
	kind := f.Get("kind")
	if kind == "" {
		kind = "text"
	}
	if chat == "" || !practiceKinds[kind] {
		http.Error(w, "chat and a known kind are required", http.StatusBadRequest)
		return
	}
	text := f.Get("text")
	if kind == "audio" || kind == "sticker" {
		text = "" // these carry no caption; the file itself is never kept
	}
	isGroup := f.Get("group") == "1"
	fromMe := f.Get("from_me") == "1"
	sender := chat
	if isGroup || fromMe {
		sender = "practice-sender-" + strings.ToLower(strings.ReplaceAll(f.Get("sender_name"), " ", "-"))
	}
	ctx := r.Context()
	if err := p.st.SaveMessage(ctx, Message{
		ID: randomID(), Chat: chat, Sender: sender, SenderName: f.Get("sender_name"),
		FromMe: fromMe, At: time.Now(), Kind: kind, Text: text,
	}); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	if err := p.st.EnsureChat(ctx, chat, f.Get("chat_name"), isGroup, true); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) messages(w http.ResponseWriter, r *http.Request) {
	msgs, err := p.st.Messages(r.Context(), r.URL.Query().Get("chat"))
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	type kept struct {
		SenderName string `json:"sender_name"`
		FromMe     bool   `json:"from_me"`
		Kind       string `json:"kind"`
		Text       string `json:"text"`
		At         string `json:"at"`
	}
	out := make([]kept, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, kept{m.SenderName, m.FromMe, m.Kind, m.Text, m.At.UTC().Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
