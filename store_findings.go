package guardian

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

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
	// The child asked for the parents' help with it (criterion 26).
	ChildHelp    bool
	ChildMessage string
	HelpAt       time.Time
}

// ChatLabel is the chat's name, or its id when it has none.
func (f Finding) ChatLabel() string {
	if f.ChatName != "" {
		return f.ChatName
	}
	return f.Chat
}

// RecordFinding adds a finding, or adds to the open one for the same
// situation in the same chat (criterion 17). It returns the finding's id and
// whether it is new.
func (s *store) RecordFinding(ctx context.Context, chat string, situationID int64, reason string, quotes []Quote, now time.Time) (int64, bool, error) {
	q, err := json.Marshal(quotes)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM findings WHERE chat = ? AND situation_id = ? AND status = 'open'`, chat, situationID).Scan(&id)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE findings SET updated_at = ?, reason = ?, quotes = ?, seen = 0 WHERE id = ?`,
			now.Unix(), reason, string(q), id)
		return id, false, err
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO findings (chat, situation_id, created_at, updated_at, reason, quotes)
		VALUES (?, ?, ?, ?, ?, ?)`, chat, situationID, now.Unix(), now.Unix(), reason, string(q))
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	return id, true, err
}

// Findings lists findings: open ones first, then newest first.
func (s *store) Findings(ctx context.Context) ([]Finding, error) {
	return s.findingsWhere(ctx, "1 = 1", "ORDER BY f.status = 'open' DESC, f.updated_at DESC, f.id DESC LIMIT 500")
}

// Finding returns one finding, if it exists.
func (s *store) Finding(ctx context.Context, id int64) (Finding, bool, error) {
	fs, err := s.findingsWhere(ctx, "f.id = ?", "", id)
	if err != nil || len(fs) == 0 {
		return Finding{}, false, err
	}
	return fs[0], true, nil
}

// FindingsSince lists findings created or changed since a time, oldest first.
func (s *store) FindingsSince(ctx context.Context, since time.Time) ([]Finding, error) {
	return s.findingsWhere(ctx, "f.updated_at >= ?", "ORDER BY f.created_at, f.id", since.Unix())
}

func (s *store) findingsWhere(ctx context.Context, where, order string, args ...any) ([]Finding, error) {
	return queryAll(ctx, s.db, scanFinding, `SELECT f.id, f.chat, COALESCE(c.name, ''), f.created_at, f.updated_at,
			f.reason, f.quotes, f.status, f.seen, f.child_help, f.child_message, f.help_at, `+prefixed("s.", situationCols)+`
		FROM findings f JOIN situations s ON s.id = f.situation_id LEFT JOIN chats c ON c.jid = f.chat
		WHERE `+where+` `+order, args...)
}

func scanFinding(sc scanner) (Finding, error) {
	var f Finding
	var created, updated, helpAt int64
	var quotes string
	x := &f.Situation
	if err := sc.Scan(&f.ID, &f.Chat, &f.ChatName, &created, &updated, &f.Reason, &quotes, &f.Status, &f.Seen,
		&f.ChildHelp, &f.ChildMessage, &helpAt,
		&x.ID, &x.Name, &x.Description, &x.Sensitivity, &x.Action, &x.Enabled, &x.Builtin, &x.Kind); err != nil {
		return f, err
	}
	f.Created, f.Updated = time.Unix(created, 0), time.Unix(updated, 0)
	if helpAt > 0 {
		f.HelpAt = time.Unix(helpAt, 0)
	}
	_ = json.Unmarshal([]byte(quotes), &f.Quotes) // a damaged row still shows, without quotes
	return f, nil
}

// prefixed puts a table alias before every column of a list.
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
	return s.count(ctx, "SELECT COUNT(*) FROM findings WHERE status = 'open'")
}

// UnseenAlert returns the newest open, unseen finding whose situation says
// "alert at once", if any: its situation's name and the chat's label.
func (s *store) UnseenAlert(ctx context.Context) (situation, chat string, ok bool, err error) {
	return s.newestFinding(ctx, "f.status = 'open' AND f.seen = 0 AND s.action = 'alert'")
}

// UnseenHelp returns the newest request for help with a finding that the
// parents haven't opened.
func (s *store) UnseenHelp(ctx context.Context) (situation, chat string, ok bool, err error) {
	return s.newestFinding(ctx, "f.child_help = 1 AND f.help_seen = 0")
}

// newestFinding is the banner's source: the situation's name and the chat's
// name (or id) of the most recently changed finding that matches.
func (s *store) newestFinding(ctx context.Context, where string) (situation, chat string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT s.name, COALESCE(NULLIF(c.name, ''), f.chat) FROM findings f
		JOIN situations s ON s.id = f.situation_id LEFT JOIN chats c ON c.jid = f.chat
		WHERE `+where+` ORDER BY f.updated_at DESC LIMIT 1`).Scan(&situation, &chat)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return situation, chat, err == nil, err
}

// AskForHelp records the child's request for help with a finding. It
// reports false when the child had already asked, so a double press tells
// the parents only once.
func (s *store) AskForHelp(ctx context.Context, id int64, message string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE findings SET child_help = 1, child_message = ?, help_seen = 0, help_at = ?
		WHERE id = ? AND child_help = 0`, message, time.Now().Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkHelpSeen is called when the findings page is opened.
func (s *store) MarkHelpSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE findings SET help_seen = 1 WHERE help_seen = 0")
	return err
}

// --- the queue of e-mails to the child, one per finding (criterion 25) ---

// QueueChildMail says the child's e-mail about a finding is to go out:
// due is when the parents were told, or zero to wait for their digest.
func (s *store) QueueChildMail(ctx context.Context, id int64, due time.Time) error {
	var at int64
	if !due.IsZero() {
		at = due.UnixNano()
	}
	_, err := s.db.ExecContext(ctx, "UPDATE findings SET child_mail_pending = 1, child_mail_due = ? WHERE id = ?", at, id)
	return err
}

// ChildMailSent clears the queued e-mail.
func (s *store) ChildMailSent(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE findings SET child_mail_pending = 0 WHERE id = ?", id)
	return err
}

// ChildMailsDue lists findings whose e-mail to the child may go out: the
// parents were told before `before`.
func (s *store) ChildMailsDue(ctx context.Context, before time.Time) ([]int64, error) {
	return queryAll(ctx, s.db, scanInt64, `SELECT id FROM findings
		WHERE child_mail_pending = 1 AND child_mail_due > 0 AND child_mail_due < ? ORDER BY id`, before.UnixNano())
}

// ReleaseDigestMails marks the e-mails that waited for the parents' digest
// as due at upto, and returns them in id order. It is one statement, so an
// e-mail queued meanwhile is either released and returned, or left waiting.
func (s *store) ReleaseDigestMails(ctx context.Context, upto time.Time) ([]int64, error) {
	ids, err := queryAll(ctx, s.db, scanInt64, `UPDATE findings SET child_mail_due = ?
		WHERE child_mail_pending = 1 AND child_mail_due = 0 AND created_at <= ? RETURNING id`, upto.UnixNano(), upto.Unix())
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	return ids, nil
}
