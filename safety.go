package guardian

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// Phase 6 (criteria 37–40): strangers, nude photos, WhatsApp's privacy
// settings and the settings only the child's phone can make. Unsolicited
// nude photos usually come the same way: a stranger writes first, or adds
// the child to a group. Guardian tells the parents at once, without any AI.

// safetyEvents is what a connector, real or practice, reports as it happens.
type safetyEvents interface {
	strangerWrote(ctx context.Context, chat, who string)
	addedToGroup(ctx context.Context, group, groupName, by string)
	photoReceived(ctx context.Context, chat, chatName, who string, known, nude bool)
	privacyChanged(ctx context.Context)
}

// photoJudge decides whether a photo looks like nudity. It runs inside
// Guardian; the photo is held only in memory while it looks (nudity.go).
type photoJudge interface {
	LooksNude(img []byte) (bool, error)
}

// --- criterion 37: strangers ---

const strangersTable = `CREATE TABLE IF NOT EXISTS strangers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	key TEXT NOT NULL UNIQUE,
	kind TEXT NOT NULL,
	who TEXT NOT NULL,
	chat TEXT NOT NULL,
	chat_name TEXT NOT NULL DEFAULT '',
	at INTEGER NOT NULL,
	status TEXT NOT NULL DEFAULT 'open',
	seen INTEGER NOT NULL DEFAULT 0
)`

// Stranger is someone not in the child's contacts who wrote first or added
// the child to a group.
type Stranger struct {
	ID       int64
	Kind     string // writes or group
	Who      string
	Chat     string
	ChatName string
	At       time.Time
	OK       bool // the parents marked it "V pořádku"
}

// AddStranger records a stranger once per key; isNew is false when the
// person or group was reported before.
func (s *store) AddStranger(ctx context.Context, key string, x Stranger) (bool, error) {
	res, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO strangers (key, kind, who, chat, chat_name, at)
		VALUES (?, ?, ?, ?, ?, ?)`, key, x.Kind, x.Who, x.Chat, x.ChatName, x.At.Unix())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func scanStranger(sc scanner) (Stranger, error) {
	var x Stranger
	var at int64
	var status string
	err := sc.Scan(&x.ID, &x.Kind, &x.Who, &x.Chat, &x.ChatName, &at, &status)
	x.At, x.OK = time.Unix(at, 0), status == "ok"
	return x, err
}

const strangerCols = "id, kind, who, chat, chat_name, at, status"

// Strangers lists them, open ones first, newest first.
func (s *store) Strangers(ctx context.Context) ([]Stranger, error) {
	return queryAll(ctx, s.db, scanStranger, "SELECT "+strangerCols+" FROM strangers ORDER BY status = 'open' DESC, at DESC, id DESC LIMIT 200")
}

func (s *store) StrangersSince(ctx context.Context, since time.Time) ([]Stranger, error) {
	return queryAll(ctx, s.db, scanStranger, "SELECT "+strangerCols+" FROM strangers WHERE at >= ? ORDER BY at", since.Unix())
}

func (s *store) MarkStranger(ctx context.Context, id int64, ok bool) error {
	status := "open"
	if ok {
		status = "ok"
	}
	_, err := s.db.ExecContext(ctx, "UPDATE strangers SET status = ? WHERE id = ?", status, id)
	return err
}

func (s *store) MarkStrangersSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE strangers SET seen = 1 WHERE seen = 0")
	return err
}

// UnseenStrangers names the strangers the parents haven't seen in Nálezy,
// newest first, at most limit of them, and how many more there are.
func (s *store) UnseenStrangers(ctx context.Context, limit int) (who []string, more int, err error) {
	who, err = queryAll(ctx, s.db, scanString, "SELECT who FROM strangers WHERE seen = 0 ORDER BY at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, 0, err
	}
	n, err := s.count(ctx, "SELECT COUNT(*) FROM strangers WHERE seen = 0")
	return who, max(0, n-len(who)), err
}

func (a *App) strangerWrote(ctx context.Context, chat, who string) {
	a.addStranger(ctx, "chat:"+chat, Stranger{Kind: "writes", Who: who, Chat: chat, At: time.Now()},
		a.lang.T("stranger_writes", who))
}

func (a *App) addedToGroup(ctx context.Context, group, groupName, by string) {
	if groupName == "" {
		groupName = group
	}
	a.addStranger(ctx, "group:"+group, Stranger{Kind: "group", Who: by, Chat: group, ChatName: groupName, At: time.Now()},
		a.lang.T("stranger_added", by, groupName))
}

func (a *App) addStranger(ctx context.Context, key string, x Stranger, body string) {
	isNew, err := a.store.AddStranger(ctx, key, x)
	if err != nil {
		log.Printf("strangers: %v", err)
		return
	}
	if isNew {
		a.nt.pushParents(ctx, pushMessage{Title: a.lang.T("stranger_push_title"), Body: body, URL: a.nt.link("/findings#strangers")})
	}
}

func (a *App) markStranger(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	status := r.PathValue("status")
	if err != nil || (status != "ok" && status != "open") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := a.store.MarkStranger(r.Context(), id, status == "ok"); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/findings#strangers", http.StatusSeeOther)
}

// --- criterion 38: photos that look like nudity ---

// photoReceived records a finding of the "Možná nahá fotka" situation. The
// photo itself never gets here: only the judgement.
func (a *App) photoReceived(ctx context.Context, chat, chatName, who string, known, nude bool) {
	if !nude {
		return
	}
	sit, ok, err := a.store.PhotoSituation(ctx)
	if err != nil || !ok || !sit.Enabled {
		return
	}
	if chatName != "" {
		_ = a.store.EnsureChat(ctx, chat, chatName, false, false)
	}
	reason := a.lang.T("photo_reason", who)
	if !known {
		reason += a.lang.T("photo_not_contact")
	}
	id, isNew, err := a.store.RecordFinding(ctx, chat, sit.ID, reason, nil, time.Now())
	if err != nil {
		log.Printf("photo finding: %v", err)
		return
	}
	a.nt.onFinding(ctx, id, isNew)
}

// --- criterion 39: WhatsApp's privacy settings ---

// privacy is the part of WhatsApp's privacy settings Guardian looks at,
// with WhatsApp's own values (all, contacts, contact_blacklist, none,
// known).
type privacy struct {
	GroupAdd string `json:"groupadd"`
	Profile  string `json:"profile"`
	Status   string `json:"status"`
	LastSeen string `json:"last"`
	CallAdd  string `json:"calladd"`
	Messages string `json:"messages"`
}

// privacyRow is one setting as the overview shows it.
type privacyRow struct {
	Key, Label, Value string
	Risky             bool
}

// safeValue is what "Nastavit bezpečně" sets.
var safeValue = map[string]string{
	"groupadd": "contacts", "profile": "contacts", "status": "contacts", "last": "contacts",
	"calladd": "known", "messages": "contacts",
}

// privacyValues are the values each setting can take.
var privacyValues = map[string][]string{
	"groupadd": {"all", "contacts", "contact_blacklist", "none"},
	"profile":  {"all", "contacts", "contact_blacklist", "none"},
	"status":   {"all", "contacts", "contact_blacklist", "none"},
	"last":     {"all", "contacts", "contact_blacklist", "none"},
	"calladd":  {"all", "known"},
	"messages": {"all", "contacts"},
}

func (p privacy) get(key string) string {
	switch key {
	case "groupadd":
		return p.GroupAdd
	case "profile":
		return p.Profile
	case "status":
		return p.Status
	case "last":
		return p.LastSeen
	case "calladd":
		return p.CallAdd
	case "messages":
		return p.Messages
	}
	return ""
}

func (p *privacy) set(key, v string) {
	switch key {
	case "groupadd":
		p.GroupAdd = v
	case "profile":
		p.Profile = v
	case "status":
		p.Status = v
	case "last":
		p.LastSeen = v
	case "calladd":
		p.CallAdd = v
	case "messages":
		p.Messages = v
	}
}

var privacyKeys = []string{"groupadd", "profile", "status", "last", "calladd", "messages"}

// riskyValue: only "everyone" is risky; contacts, contacts except some, and
// nobody are all fine. An unknown value isn't called risky.
func riskyValue(v string) bool { return v == "all" }

func (p privacy) risky() []string {
	var out []string
	for _, k := range privacyKeys {
		if riskyValue(p.get(k)) {
			out = append(out, k)
		}
	}
	return out
}

func (a *App) privacyRows(p privacy) []privacyRow {
	rows := make([]privacyRow, 0, len(privacyKeys))
	for _, k := range privacyKeys {
		v := p.get(k)
		label := a.lang.T("pv_val_" + v)
		if k == "calladd" || k == "messages" {
			label = a.lang.T("pv_off")
			if v == safeValue[k] {
				label = a.lang.T("pv_on")
			}
		}
		if v == "" {
			label = "?"
		}
		rows = append(rows, privacyRow{Key: k, Label: a.lang.T("pv_" + k), Value: label, Risky: riskyValue(v)})
	}
	return rows
}

const settingPrivacy = "wa_privacy" // the settings as last checked, JSON

// checkPrivacy compares the settings with the last check and tells the
// parents when one that was safe has become risky. It never changes them.
func (a *App) checkPrivacy(ctx context.Context) {
	if a.wa == nil || a.wa.Status().State != waConnected {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	now, err := a.wa.Privacy(cctx)
	if err != nil {
		log.Printf("privacy: %v", err)
		return
	}
	var before privacy
	known := false
	if v, _ := a.store.Setting(ctx, settingPrivacy); v != "" {
		known = json.Unmarshal([]byte(v), &before) == nil
	}
	if b, err := json.Marshal(now); err == nil {
		_ = a.store.SetSetting(ctx, settingPrivacy, string(b))
	}
	if !known {
		return
	}
	var changed []string
	for _, k := range now.risky() {
		if old := before.get(k); old != "" && !riskyValue(old) {
			changed = append(changed, a.lang.T("pv_"+k))
		}
	}
	if len(changed) > 0 {
		a.nt.pushParents(ctx, pushMessage{Title: a.lang.T("privacy_push"), Body: joinList(changed), URL: a.nt.link("/#privacy")})
	}
}

func (a *App) privacyChanged(ctx context.Context) { a.checkPrivacy(ctx) }

// privacyLoop checks the settings soon after the start and then once a day.
func (a *App) privacyLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
		a.checkPrivacy(ctx)
	}
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.checkPrivacy(ctx)
		}
	}
}

// fixPrivacy is "Nastavit bezpečně": the parents' tap, never Guardian alone.
func (a *App) fixPrivacy(w http.ResponseWriter, r *http.Request) {
	if a.wa == nil || a.wa.Status().State != waConnected {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if err := a.wa.SetPrivacySafe(ctx); err != nil {
		log.Printf("privacy: setting: %v", err)
		http.Redirect(w, r, "/?privacy=failed#privacy", http.StatusSeeOther)
		return
	}
	a.checkPrivacy(r.Context())
	http.Redirect(w, r, "/?privacy=done#privacy", http.StatusSeeOther)
}

// privacyView is the "Nastavení soukromí" part under the WhatsApp connector.
type privacyView struct {
	Shown   bool
	Rows    []privacyRow
	AllSafe bool
	Done    bool   // just set
	Error   string // couldn't read or set
}

func (a *App) privacyView(r *http.Request) privacyView {
	if a.wa == nil || a.wa.Status().State != waConnected {
		return privacyView{}
	}
	v := privacyView{Shown: true, Done: r.URL.Query().Get("privacy") == "done"}
	if r.URL.Query().Get("privacy") == "failed" {
		v.Error = a.lang.T("privacy_failed")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p, err := a.wa.Privacy(ctx)
	if err != nil {
		v.Error = a.lang.T("privacy_unreadable")
		return v
	}
	v.Rows = a.privacyRows(p)
	v.AllSafe = len(p.risky()) == 0
	return v
}

// --- criterion 40: settings on the child's phone ---

const (
	settingPhoneChecked = "phone_checked" // YYYY-MM-DD
	phoneCheckEvery     = 90 * 24 * time.Hour
)

func (a *App) phoneChecked(w http.ResponseWriter, r *http.Request) {
	if err := a.store.SetSetting(r.Context(), settingPhoneChecked, time.Now().In(a.zone).Format("2006-01-02")); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/platforms#phone", http.StatusSeeOther)
}

// lastPhoneCheck returns when the phone was last checked, zero if never.
func (a *App) lastPhoneCheck(ctx context.Context) time.Time {
	v, _ := a.store.Setting(ctx, settingPhoneChecked)
	t, err := time.ParseInLocation("2006-01-02", v, a.zone)
	if err != nil {
		return time.Time{}
	}
	return t
}

// phoneReminder is the overview's line, "" when no reminder is due.
func (a *App) phoneReminder(ctx context.Context) string {
	t := a.lastPhoneCheck(ctx)
	switch {
	case t.IsZero():
		return a.lang.T("phone_remind_never")
	case time.Since(t) >= phoneCheckEvery:
		return a.lang.T("phone_remind", a.lang.date(t))
	}
	return ""
}

// joinList writes "a, b and c" without the language's "and": a comma list.
func joinList(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
