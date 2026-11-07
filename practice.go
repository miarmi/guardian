package guardian

import (
	"context"
	"errors"
	"net/http"
	"slices"
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
	privacy     privacy // the pretend phone's settings, all "everyone" at first
	ev          safetyEvents
}

const (
	practiceNumber   = "420777000111"
	practiceCodes    = 3
	practiceCodeLife = time.Minute
)

func newPractice(st *store) *practice {
	all := privacy{GroupAdd: "all", Profile: "all", Status: "all", LastSeen: "all", CallAdd: "all", Messages: "all"}
	return &practice{st: st, state: waNotConnected, privacy: all}
}

func (p *practice) watch(ev safetyEvents, _ photoJudge) {
	p.mu.Lock()
	p.ev = ev
	p.mu.Unlock()
}

func (p *practice) events() safetyEvents {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ev
}

func (p *practice) Privacy(ctx context.Context) (privacy, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unreachable {
		return privacy{}, errors.New("practice: WhatsApp unreachable")
	}
	return p.privacy, nil
}

func (p *practice) SetPrivacySafe(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unreachable {
		return errors.New("practice: WhatsApp unreachable")
	}
	for k, v := range safeValue {
		p.privacy.set(k, v)
	}
	return nil
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

// nextCodeLocked offers the next code; after practiceCodes the pairing
// expires, as the real one does. Call it with p.mu held.
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
	mux.HandleFunc("POST /dev/whatsapp/unreachable", toggle(p.setUnreachable))
	mux.HandleFunc("GET /dev/whatsapp/messages", p.messages)
	mux.HandleFunc("POST /dev/whatsapp/added", p.added)
	mux.HandleFunc("GET /dev/whatsapp/privacy", p.getPrivacy)
	mux.HandleFunc("POST /dev/whatsapp/privacy", p.setPrivacy)
}

func (p *practice) scan(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	if p.state != waPairing || p.qr == "" {
		p.mu.Unlock()
		http.Error(w, "no code on offer", http.StatusConflict)
		return
	}
	p.state, p.qr, p.qrExp, p.number = waConnected, "", time.Time{}, practiceNumber
	ev := p.ev
	p.mu.Unlock()
	if ev != nil { // the first read, as on linking
		ev.privacyChanged(r.Context())
	}
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

func (p *practice) setUnreachable(on bool) {
	p.mu.Lock()
	p.unreachable = on
	p.mu.Unlock()
}

var practiceKinds = map[string]bool{"text": true, "image": true, "video": true, "audio": true, "sticker": true, "document": true}

func (p *practice) message(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r, 64<<10) {
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
	photo := f.Get("photo")
	if photo != "" && photo != "nude" && photo != "plain" {
		http.Error(w, "photo is nude or plain", http.StatusBadRequest)
		return
	}
	if photo != "" {
		kind = "image" // a received photo; no real image exists in practice
	}
	known := f.Get("known") != "0"
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
	earlier, err := p.st.Messages(ctx, chat)
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
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
	if ev := p.events(); ev != nil && !fromMe {
		who := firstNonEmpty(f.Get("sender_name"), f.Get("chat_name"), chat)
		if !known && !isGroup && len(earlier) == 0 {
			ev.strangerWrote(ctx, chat, who)
		}
		if photo != "" {
			ev.photoReceived(ctx, chat, f.Get("chat_name"), who, known, photo == "nude")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// added plays someone adding the child to a group (criterion 37).
func (p *practice) added(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r, 16<<10) {
		return
	}
	f := r.PostForm
	p.mu.Lock()
	connected := p.state == waConnected
	p.mu.Unlock()
	chat, by := strings.TrimSpace(f.Get("chat")), strings.TrimSpace(f.Get("by"))
	switch {
	case !connected:
		http.Error(w, "not connected", http.StatusConflict)
		return
	case chat == "" || by == "":
		http.Error(w, "chat and by are required", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if err := p.st.EnsureChat(ctx, chat, f.Get("chat_name"), true, true); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	if ev := p.events(); ev != nil && f.Get("known") == "0" {
		ev.addedToGroup(ctx, chat, f.Get("chat_name"), by)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) getPrivacy(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	v := p.privacy
	p.mu.Unlock()
	writeJSON(w, v)
}

// setPrivacy changes the pretend phone's settings, as the child could on
// the phone; Guardian notices as it would on WhatsApp's change event.
func (p *practice) setPrivacy(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r, 16<<10) {
		return
	}
	p.mu.Lock()
	next := p.privacy
	for _, k := range privacyKeys {
		v := r.PostForm.Get(k)
		if v == "" {
			continue
		}
		if !slices.Contains(privacyValues[k], v) {
			p.mu.Unlock()
			http.Error(w, "bad value for "+k, http.StatusBadRequest)
			return
		}
		next.set(k, v)
	}
	p.privacy = next
	ev := p.ev
	p.mu.Unlock()
	if ev != nil {
		ev.privacyChanged(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *practice) messages(w http.ResponseWriter, r *http.Request) {
	msgs, err := p.st.Messages(r.Context(), r.URL.Query().Get("chat"))
	if err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	type kept struct { // what the test sees of a kept message
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
	writeJSON(w, out)
}
