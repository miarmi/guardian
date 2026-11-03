package guardian

import (
	"context"
	"time"
)

// Signal is what a parental e-mail said, in short (criterion 34).
type Signal struct {
	ID       int64
	Platform string
	Subject  string
	Summary  string
	Received time.Time
}

const signalsTable = `CREATE TABLE IF NOT EXISTS signals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	platform TEXT NOT NULL,
	subject TEXT NOT NULL,
	summary TEXT NOT NULL,
	received_at INTEGER NOT NULL,
	created_at INTEGER NOT NULL
)`

func (s *store) AddSignal(ctx context.Context, x Signal) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO signals (platform, subject, summary, received_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		x.Platform, x.Subject, x.Summary, x.Received.Unix(), time.Now().Unix())
	return err
}

func scanSignal(sc scanner) (Signal, error) {
	var x Signal
	var at int64
	err := sc.Scan(&x.ID, &x.Platform, &x.Subject, &x.Summary, &at)
	x.Received = time.Unix(at, 0)
	return x, err
}

// Signals lists the newest signals first.
func (s *store) Signals(ctx context.Context, limit int) ([]Signal, error) {
	return queryAll(ctx, s.db, scanSignal, "SELECT id, platform, subject, summary, received_at FROM signals ORDER BY received_at DESC, id DESC LIMIT ?", limit)
}

// SignalsSince lists signals that arrived since a time, oldest first.
func (s *store) SignalsSince(ctx context.Context, since time.Time) ([]Signal, error) {
	return queryAll(ctx, s.db, scanSignal, "SELECT id, platform, subject, summary, received_at FROM signals WHERE created_at >= ? ORDER BY received_at, id", since.Unix())
}
