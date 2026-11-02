package guardian

import (
	"context"
	"database/sql"
	"time"
)

// HelpRequest is the child asking for help through the personal link
// (criterion 30).
type HelpRequest struct {
	ID      int64
	Created time.Time
	Updated time.Time
	Message string
	Status  string // open, resolved
}

// helpMerge is how long a second request adds to the first.
const helpMerge = 10 * time.Minute

// AddHelpRequest records a request; within helpMerge of the last open one
// it adds to that one instead (isNew false). Merged parts each start with
// their time, written by stamp.
func (s *store) AddHelpRequest(ctx context.Context, message string, stamp func(time.Time) string) (id int64, isNew bool, err error) {
	s.helpMu.Lock()
	defer s.helpMu.Unlock()
	now := time.Now()
	var old string
	var created int64
	var merged bool
	err = s.db.QueryRowContext(ctx, `SELECT id, message, created_at, merged FROM help_requests WHERE status = 'open' AND created_at >= ?
		ORDER BY id DESC LIMIT 1`, now.Add(-helpMerge).Unix()).Scan(&id, &old, &created, &merged)
	switch {
	case err == nil:
		if message != "" {
			old, merged = mergeHelpMessage(old, merged, time.Unix(created, 0), message, now, stamp), true
		}
		_, err = s.db.ExecContext(ctx, "UPDATE help_requests SET message = ?, merged = ?, updated_at = ?, seen = 0 WHERE id = ?", old, merged, now.Unix(), id)
		return id, false, err
	case err != sql.ErrNoRows:
		return 0, false, err
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO help_requests (created_at, updated_at, message) VALUES (?, ?, ?)", now.Unix(), now.Unix(), message)
	if err != nil {
		return 0, false, err
	}
	id, err = res.LastInsertId()
	return id, true, err
}

// mergeHelpMessage adds a new message to a request, one part per line, each
// starting with its time. The first message gets its time only once a
// second one joins it.
func mergeHelpMessage(old string, merged bool, created time.Time, message string, now time.Time, stamp func(time.Time) string) string {
	if old != "" && !merged {
		old = stamp(created) + " " + old
	}
	if old != "" {
		old += "\n"
	}
	return old + stamp(now) + " " + message
}

func scanHelpRequest(sc scanner) (HelpRequest, error) {
	var h HelpRequest
	var c, u int64
	err := sc.Scan(&h.ID, &c, &u, &h.Message, &h.Status)
	h.Created, h.Updated = time.Unix(c, 0), time.Unix(u, 0)
	return h, err
}

const helpRequestCols = "SELECT id, created_at, updated_at, message, status FROM help_requests "

// HelpRequests lists requests: open ones first, newest first.
func (s *store) HelpRequests(ctx context.Context) ([]HelpRequest, error) {
	return queryAll(ctx, s.db, scanHelpRequest, helpRequestCols+"ORDER BY status = 'open' DESC, updated_at DESC, id DESC LIMIT 200")
}

// HelpRequestsSince lists requests made or changed since a time, oldest first.
func (s *store) HelpRequestsSince(ctx context.Context, since time.Time) ([]HelpRequest, error) {
	return queryAll(ctx, s.db, scanHelpRequest, helpRequestCols+"WHERE updated_at >= ? ORDER BY created_at, id", since.Unix())
}

// MarkHelpRequest resolves or reopens a request.
func (s *store) MarkHelpRequest(ctx context.Context, id int64, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE help_requests SET status = ?, seen = 1 WHERE id = ?", status, id)
	return err
}

// UnseenHelpRequest says whether a request waits for the parents to open
// the findings page.
func (s *store) UnseenHelpRequest(ctx context.Context) (bool, error) {
	n, err := s.count(ctx, "SELECT COUNT(*) FROM help_requests WHERE seen = 0")
	return n > 0, err
}

// MarkHelpRequestsSeen is called when the findings page is opened.
func (s *store) MarkHelpRequestsSeen(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE help_requests SET seen = 1 WHERE seen = 0")
	return err
}
