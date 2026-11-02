package guardian

import (
	"context"
	"database/sql"
	"time"
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
	First   int64  // when the chat first appeared, for stable numbering
	IsGroup bool
	Count   int
	Last    time.Time
}

// chatInfo is what the analysis tells the model about a chat.
type chatInfo struct {
	Name    string
	IsGroup bool
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
	c, err := s.ChatInfo(ctx, jid)
	return c.Name, err
}

// ChatInfo returns a chat's stored name and kind; an unknown chat has none.
func (s *store) ChatInfo(ctx context.Context, jid string) (chatInfo, error) {
	var c chatInfo
	err := s.db.QueryRowContext(ctx, "SELECT name, is_group FROM chats WHERE jid = ?", jid).Scan(&c.Name, &c.IsGroup)
	if err == sql.ErrNoRows {
		return chatInfo{}, nil
	}
	return c, err
}

// Chats lists every chat with messages, newest activity first.
func (s *store) Chats(ctx context.Context) ([]ChatSummary, error) {
	return queryAll(ctx, s.db, func(sc scanner) (ChatSummary, error) {
		var c ChatSummary
		var last int64
		err := sc.Scan(&c.JID, &c.Name, &c.IsGroup, &c.Count, &last, &c.First)
		c.Last = time.Unix(last, 0)
		return c, err
	}, `SELECT m.chat, COALESCE(c.name, ''), COALESCE(c.is_group, 0),
			COUNT(*), MAX(m.at), MIN(m.rowid)
		FROM messages m LEFT JOIN chats c ON c.jid = m.chat
		GROUP BY m.chat ORDER BY MAX(m.at) DESC LIMIT 500`)
}

// CountChats counts the chats with any message.
func (s *store) CountChats(ctx context.Context) (int, error) {
	return s.count(ctx, "SELECT COUNT(DISTINCT chat) FROM messages")
}

// Messages returns what was kept for one chat, oldest first.
func (s *store) Messages(ctx context.Context, chat string) ([]Message, error) {
	return queryAll(ctx, s.db, func(sc scanner) (Message, error) {
		var m Message
		var at int64
		err := sc.Scan(&m.ID, &m.Chat, &m.Sender, &m.SenderName, &m.FromMe, &at, &m.Kind, &m.Text)
		m.At = time.Unix(at, 0)
		return m, err
	}, `SELECT id, chat, sender, sender_name, from_me, at, kind, text
		FROM messages WHERE chat = ? ORDER BY at, rowid`, chat)
}

// --- the analysis cursor: how far each chat has been looked at ---

// numbered is a message with its storage position, for the analysis cursor.
type numbered struct {
	Message
	Seq int64
}

// PendingChats lists chats with messages the analysis has not looked at.
func (s *store) PendingChats(ctx context.Context) ([]string, error) {
	return queryAll(ctx, s.db, scanString, `SELECT m.chat FROM messages m
		LEFT JOIN analysis_cursor c ON c.chat = m.chat
		GROUP BY m.chat HAVING MAX(m.rowid) > COALESCE(MAX(c.upto), 0)
		ORDER BY MAX(m.at)`)
}

// ChatWindow returns up to contextSize earlier messages already looked at
// and every message not yet looked at, oldest first.
func (s *store) ChatWindow(ctx context.Context, chat string, contextSize int) (old, fresh []numbered, err error) {
	var upto int64
	err = s.db.QueryRowContext(ctx, "SELECT upto FROM analysis_cursor WHERE chat = ?", chat).Scan(&upto)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, err
	}
	scan := func(sc scanner) (numbered, error) {
		var m numbered
		var at int64
		err := sc.Scan(&m.Seq, &m.ID, &m.Sender, &m.SenderName, &m.FromMe, &at, &m.Kind, &m.Text)
		m.Chat, m.At = chat, time.Unix(at, 0)
		return m, err
	}
	const cols = "rowid, id, sender, sender_name, from_me, at, kind, text"
	old, err = queryAll(ctx, s.db, scan, `SELECT * FROM (SELECT `+cols+` FROM messages WHERE chat = ? AND rowid <= ?
		ORDER BY rowid DESC LIMIT ?) ORDER BY rowid`, chat, upto, contextSize)
	if err != nil {
		return nil, nil, err
	}
	fresh, err = queryAll(ctx, s.db, scan, `SELECT `+cols+` FROM messages WHERE chat = ? AND rowid > ? ORDER BY rowid`, chat, upto)
	return old, fresh, err
}

// MarkAnalyzed moves the chat's cursor past the messages just looked at.
func (s *store) MarkAnalyzed(ctx context.Context, chat string, upto int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO analysis_cursor (chat, upto) VALUES (?, ?)
		ON CONFLICT (chat) DO UPDATE SET upto = MAX(upto, excluded.upto)`, chat, upto)
	return err
}
