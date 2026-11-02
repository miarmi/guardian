package guardian

import (
	"context"
	"database/sql"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// store is Guardian's SQLite database. The methods are split by table area:
// store_chats.go, store_situations.go, store_findings.go, store_help.go and
// store_push.go.
type store struct {
	db *sql.DB
	// helpMu makes adding to an open help request one step, so two quick
	// requests from the child can neither both start a new one nor lose a
	// message while merging.
	helpMu sync.Mutex
}

// schema creates every table on a fresh database.
var schema = []string{
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

// migrations add columns that came later; on a database that has them
// already they fail with "duplicate column", which is fine.
var migrations = []string{
	// Deleted situations are hidden, their findings stay.
	`ALTER TABLE situations ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0`,
	// Left from a removed feature; kept so old and new databases match.
	`ALTER TABLE situations ADD COLUMN recipients TEXT NOT NULL DEFAULT 'both'`,
	// The child's request for help with a finding (criterion 26).
	`ALTER TABLE findings ADD COLUMN child_help INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE findings ADD COLUMN child_message TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE findings ADD COLUMN help_seen INTEGER NOT NULL DEFAULT 1`,
	`ALTER TABLE findings ADD COLUMN child_mail_pending INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE findings ADD COLUMN help_at INTEGER NOT NULL DEFAULT 0`,
	// When the child's e-mail may go out (criterion 25): 0 waits for the
	// parents' digest, otherwise the time the parents were told (ns).
	`ALTER TABLE findings ADD COLUMN child_mail_due INTEGER NOT NULL DEFAULT 0`,
}

// laterTables came with phase 3 and are created after the migrations.
var laterTables = []string{
	`CREATE TABLE IF NOT EXISTS help_requests (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		message TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'open',
		seen INTEGER NOT NULL DEFAULT 0,
		merged INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS push_devices (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		endpoint TEXT NOT NULL UNIQUE,
		p256dh TEXT NOT NULL,
		auth TEXT NOT NULL,
		label TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
}

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func migrate(db *sql.DB) error {
	for _, q := range schema {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	for _, q := range migrations {
		if _, err := db.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	for _, q := range laterTables {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) Close() error { return s.db.Close() }

func (s *store) Ping(ctx context.Context) error {
	var one int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// queryAll runs a query and turns every row into a T with scan.
func queryAll[T any](ctx context.Context, db *sql.DB, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanInt64(sc scanner) (int64, error) {
	var n int64
	err := sc.Scan(&n)
	return n, err
}

func scanString(sc scanner) (string, error) {
	var v string
	err := sc.Scan(&v)
	return v, err
}

// count runs a SELECT COUNT(*) style query.
func (s *store) count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// --- settings: a key-value table for everything that isn't secret ---

// Setting returns a saved value, or "" when there is none.
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

// Settings reads several keys in one query; a missing key, or a key that
// can't be read, reads as "" (as Setting's callers treated it before).
func (s *store) Settings(ctx context.Context, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return out
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings WHERE key IN (?"+strings.Repeat(", ?", len(keys)-1)+")", args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	return out
}
