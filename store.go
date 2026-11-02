package guardian

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Message is one chat message as kept by the app. Media is never kept, only
// its kind and caption.
type Message struct {
	ID         string
	Chat       string
	Sender     string
	SenderName string
	FromMe     bool
	At         time.Time
	Kind       string // text, image, video, audio, sticker, document, reaction, other
	Text       string
}

// ChatSummary is one row of the "Chats" list: no content, only counts.
type ChatSummary struct {
	JID     string
	Name    string
	Suffix  string // set when another chat has the same name
	IsGroup bool
	Count   int
	Last    time.Time
}

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	schema := []string{
		`CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT NOT NULL DEFAULT '',
			is_group INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			chat TEXT NOT NULL,
			id TEXT NOT NULL,
			sender TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			from_me INTEGER NOT NULL,
			at INTEGER NOT NULL,
			kind TEXT NOT NULL,
			text TEXT NOT NULL,
			PRIMARY KEY (chat, id)
		)`,
		`CREATE INDEX IF NOT EXISTS messages_chat_at ON messages (chat, at)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS situations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			description TEXT NOT NULL,
			sensitivity TEXT NOT NULL,
			action TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			builtin INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat TEXT NOT NULL,
			situation_id INTEGER NOT NULL REFERENCES situations(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			reason TEXT NOT NULL,
			quotes TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'open',
			seen INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS findings_open ON findings (status, chat, situation_id)`,
		`CREATE TABLE IF NOT EXISTS analysis_cursor (
			chat TEXT PRIMARY KEY,
			upto INTEGER NOT NULL
		)`,
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	// Added later: deleted situations are hidden, their findings stay.
	if _, err := db.Exec(`ALTER TABLE situations ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// SaveMessage keeps a message; a message seen twice (live and in a history
// sync) is kept once.
func (s *store) SaveMessage(ctx context.Context, m Message) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO messages
		(chat, id, sender, sender_name, from_me, at, kind, text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Chat, m.ID, m.Sender, m.SenderName, m.FromMe, m.At.Unix(), m.Kind, m.Text)
	return err
}

// EnsureChat records a chat; a name is only set when one is known and the
// chat has none yet, or when the new name is given as authoritative.
func (s *store) EnsureChat(ctx context.Context, jid, name string, isGroup, authoritative bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO chats (jid, name, is_group) VALUES (?, ?, ?)
		ON CONFLICT (jid) DO UPDATE SET
			name = CASE WHEN ? OR chats.name = '' THEN excluded.name ELSE chats.name END,
			is_group = excluded.is_group`,
		jid, name, isGroup, authoritative && name != "")
	return err
}

// ChatName returns the stored name of a chat, or "" if none is known.
func (s *store) ChatName(ctx context.Context, jid string) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

// Chats lists every chat with messages, newest activity first.
func (s *store) Chats(ctx context.Context) ([]ChatSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.chat, COALESCE(c.name, ''), COALESCE(c.is_group, 0),
			COUNT(*), MAX(m.at)
		FROM messages m LEFT JOIN chats c ON c.jid = m.chat
		GROUP BY m.chat ORDER BY MAX(m.at) DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var c ChatSummary
		var last int64
		if err := rows.Scan(&c.JID, &c.Name, &c.IsGroup, &c.Count, &last); err != nil {
			return nil, err
		}
		c.Last = time.Unix(last, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Messages returns what was kept for one chat, oldest first.
func (s *store) Messages(ctx context.Context, chat string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, chat, sender, sender_name, from_me, at, kind, text
		FROM messages WHERE chat = ? ORDER BY at, rowid`, chat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var at int64
		if err := rows.Scan(&m.ID, &m.Chat, &m.Sender, &m.SenderName, &m.FromMe, &at, &m.Kind, &m.Text); err != nil {
			return nil, err
		}
		m.At = time.Unix(at, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Situation is one thing the agent watches for (SPEC.md criterion 14).
type Situation struct {
	ID          int64
	Name        string
	Description string
	Sensitivity string // low, medium, high
	Action      string // record, digest, alert
	Enabled     bool
	Builtin     bool
}

// Quote is one message a finding quotes.
type Quote struct {
	SenderName string    `json:"sender_name"`
	FromMe     bool      `json:"from_me"`
	At         time.Time `json:"at"`
	Text       string    `json:"text"`
}

// Finding is what the analysis reported (criteria 16–18).
type Finding struct {
	ID        int64
	Chat      string
	ChatName  string
	Situation Situation
	Created   time.Time
	Updated   time.Time
	Reason    string
	Quotes    []Quote
	Status    string // open, resolved, false_alarm
	Seen      bool
}

const situationCols = "id, name, description, sensitivity, action, enabled, builtin"

func scanSituation(sc interface{ Scan(...any) error }) (Situation, error) {
	var x Situation
	err := sc.Scan(&x.ID, &x.Name, &x.Description, &x.Sensitivity, &x.Action, &x.Enabled, &x.Builtin)
	return x, err
}

// Situations lists every situation, built-in ones first, in creation order.
func (s *store) Situations(ctx context.Context) ([]Situation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+situationCols+" FROM situations WHERE deleted = 0 ORDER BY builtin DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Situation
	for rows.Next() {
		x, err := scanSituation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *store) Situation(ctx context.Context, id int64) (Situation, error) {
	return scanSituation(s.db.QueryRowContext(ctx, "SELECT "+situationCols+" FROM situations WHERE id = ? AND deleted = 0", id))
}

// SeedSituations adds the default set once, on a fresh database.
func (s *store) SeedSituations(ctx context.Context, defaults []Situation) error {
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM situations").Scan(&n); err != nil || n > 0 {
		return err
	}
	for _, d := range defaults {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO situations (name, description, sensitivity, action, enabled, builtin)
			VALUES (?, ?, ?, ?, 1, 1)`, d.Name, d.Description, d.Sensitivity, d.Action); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) AddSituation(ctx context.Context, x Situation) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO situations (name, description, sensitivity, action, enabled, builtin)
		VALUES (?, ?, ?, ?, ?, 0)`, x.Name, x.Description, x.Sensitivity, x.Action, x.Enabled)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *store) UpdateSituation(ctx context.Context, x Situation) error {
	_, err := s.db.ExecContext(ctx, `UPDATE situations SET name = ?, description = ?, sensitivity = ?, action = ?, enabled = ?
		WHERE id = ?`, x.Name, x.Description, x.Sensitivity, x.Action, x.Enabled, x.ID)
	return err
}

// DeleteSituation hides an added situation; the default set stays, and the
// findings it made stay too (criterion 14).
func (s *store) DeleteSituation(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE situations SET deleted = 1, enabled = 0 WHERE id = ? AND builtin = 0", id)
	return err
}

// PendingChats lists chats with messages the analysis has not looked at.
func (s *store) PendingChats(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.chat FROM messages m
		LEFT JOIN analysis_cursor c ON c.chat = m.chat
		GROUP BY m.chat HAVING MAX(m.rowid) > COALESCE(MAX(c.upto), 0)
		ORDER BY MAX(m.at)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// numbered is a message with its storage position, for the analysis cursor.
type numbered struct {
	Message
	Seq int64
}

// ChatWindow returns up to `context` earlier messages already looked at and
// every message not yet looked at, oldest first.
func (s *store) ChatWindow(ctx context.Context, chat string, context int) (old, fresh []numbered, err error) {
	var upto int64
	err = s.db.QueryRowContext(ctx, "SELECT upto FROM analysis_cursor WHERE chat = ?", chat).Scan(&upto)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, err
	}
	load := func(q string, args ...any) ([]numbered, error) {
		rows, err := s.db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []numbered
		for rows.Next() {
			var m numbered
			var at int64
			if err := rows.Scan(&m.Seq, &m.ID, &m.Sender, &m.SenderName, &m.FromMe, &at, &m.Kind, &m.Text); err != nil {
				return nil, err
			}
			m.Chat, m.At = chat, time.Unix(at, 0)
			out = append(out, m)
		}
		return out, rows.Err()
	}
	const cols = "rowid, id, sender, sender_name, from_me, at, kind, text"
	old, err = load(`SELECT * FROM (SELECT `+cols+` FROM messages WHERE chat = ? AND rowid <= ?
		ORDER BY rowid DESC LIMIT ?) ORDER BY rowid`, chat, upto, context)
	if err != nil {
		return nil, nil, err
	}
	fresh, err = load(`SELECT `+cols+` FROM messages WHERE chat = ? AND rowid > ? ORDER BY rowid`, chat, upto)
	return old, fresh, err
}

// MarkAnalyzed moves the chat's cursor past the messages just looked at.
func (s *store) MarkAnalyzed(ctx context.Context, chat string, upto int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO analysis_cursor (chat, upto) VALUES (?, ?)
		ON CONFLICT (chat) DO UPDATE SET upto = MAX(upto, excluded.upto)`, chat, upto)
	return err
}

// RecordFinding adds a finding, or adds to the open one for the same
// situation in the same chat (criterion 17).
func (s *store) RecordFinding(ctx context.Context, chat string, situationID int64, reason string, quotes []Quote, now time.Time) error {
	q, err := json.Marshal(quotes)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE findings SET updated_at = ?, reason = ?, quotes = ?, seen = 0
		WHERE chat = ? AND situation_id = ? AND status = 'open'`, now.Unix(), reason, string(q), chat, situationID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO findings (chat, situation_id, created_at, updated_at, reason, quotes)
		VALUES (?, ?, ?, ?, ?, ?)`, chat, situationID, now.Unix(), now.Unix(), reason, string(q))
	return err
}

// Findings lists findings: open ones first, then newest first.
func (s *store) Findings(ctx context.Context) ([]Finding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.chat, COALESCE(c.name, ''), f.created_at, f.updated_at,
			f.reason, f.quotes, f.status, f.seen, `+prefixed("s.", situationCols)+`
		FROM findings f JOIN situations s ON s.id = f.situation_id LEFT JOIN chats c ON c.jid = f.chat
		ORDER BY f.status = 'open' DESC, f.updated_at DESC, f.id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		var f Finding
		var created, updated int64
		var quotes string
		x := &f.Situation
		if err := rows.Scan(&f.ID, &f.Chat, &f.ChatName, &created, &updated, &f.Reason, &quotes, &f.Status, &f.Seen,
			&x.ID, &x.Name, &x.Description, &x.Sensitivity, &x.Action, &x.Enabled, &x.Builtin); err != nil {
			return nil, err
		}
		f.Created, f.Updated = time.Unix(created, 0), time.Unix(updated, 0)
		_ = json.Unmarshal([]byte(quotes), &f.Quotes)
		out = append(out, f)
	}
	return out, rows.Err()
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = p + parts[i]
	}
	return strings.Join(parts, ", ")
}

// MarkFinding resolves a finding, calls it a false alarm, or reopens it.
// Reopening is refused when the same situation is already open in that chat.
func (s *store) MarkFinding(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE findings SET status = ?, seen = 1 WHERE id = ?
		AND NOT (? = 'open' AND EXISTS (SELECT 1 FROM findings o WHERE o.status = 'open' AND o.id != findings.id
			AND o.chat = findings.chat AND o.situation_id = findings.situation_id))`, status, id, status)
	return err
}

// MarkFindingsSeen is called when the findings page is opened.
func (s *store) MarkFindingsSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE findings SET seen = 1 WHERE seen = 0")
	return err
}

// OpenFindingCount counts findings still open.
func (s *store) OpenFindingCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM findings WHERE status = 'open'").Scan(&n)
	return n, err
}

// UnseenAlert returns the newest open, unseen finding whose situation says
// "alert at once", if any.
func (s *store) UnseenAlert(ctx context.Context) (situation, chatName, chat string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT s.name, COALESCE(c.name, ''), f.chat FROM findings f
		JOIN situations s ON s.id = f.situation_id LEFT JOIN chats c ON c.jid = f.chat
		WHERE f.status = 'open' AND f.seen = 0 AND s.action = 'alert'
		ORDER BY f.updated_at DESC LIMIT 1`).Scan(&situation, &chatName, &chat)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	return situation, chatName, chat, err == nil, err
}
