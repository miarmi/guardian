package guardian

import (
	"context"
	"database/sql"
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
	}
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
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
