package guardian

import "context"

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

const situationCols = "id, name, description, sensitivity, action, enabled, builtin"

func scanSituation(sc scanner) (Situation, error) {
	var x Situation
	err := sc.Scan(&x.ID, &x.Name, &x.Description, &x.Sensitivity, &x.Action, &x.Enabled, &x.Builtin)
	return x, err
}

// Situations lists every situation, built-in ones first, in creation order.
func (s *store) Situations(ctx context.Context) ([]Situation, error) {
	return queryAll(ctx, s.db, scanSituation, "SELECT "+situationCols+" FROM situations WHERE deleted = 0 ORDER BY builtin DESC, id")
}

func (s *store) Situation(ctx context.Context, id int64) (Situation, error) {
	return scanSituation(s.db.QueryRowContext(ctx, "SELECT "+situationCols+" FROM situations WHERE id = ? AND deleted = 0", id))
}

// SeedSituations adds the default set once, on a fresh database.
func (s *store) SeedSituations(ctx context.Context, defaults []Situation) error {
	if n, err := s.count(ctx, "SELECT COUNT(*) FROM situations"); err != nil || n > 0 {
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
