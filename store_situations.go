package guardian

import (
	"context"
	"strconv"
)

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
// defaultsVersion numbers the default set of situations. Raising it makes
// SeedSituations replace an older default set (criterion 14).
const defaultsVersion = 2

// SeedSituations puts the default situations in place: on a new database
// it adds them; when the stored set is older, it replaces the old defaults
// and adds the new ones. Replaced defaults go away with their findings
// (criterion 14); added situations stay.
func (s *store) SeedSituations(ctx context.Context, defaults []Situation) error {
	if err := s.removeRetiredDefaults(ctx); err != nil {
		return err
	}
	n, err := s.count(ctx, "SELECT COUNT(*) FROM situations")
	if err != nil {
		return err
	}
	have := 1 // databases from before the version was kept hold set 1
	if v, err := s.Setting(ctx, "situations_version"); err != nil {
		return err
	} else if v != "" {
		have, _ = strconv.Atoi(v)
	}
	if n > 0 && have >= defaultsVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE situations SET deleted = 1, enabled = 0 WHERE builtin = 1 AND deleted = 0"); err != nil {
		return err
	}
	for _, d := range defaults {
		if _, err := tx.ExecContext(ctx, `INSERT INTO situations (name, description, sensitivity, action, enabled, builtin)
			VALUES (?, ?, ?, ?, 1, 1)`, d.Name, d.Description, d.Sensitivity, d.Action); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('situations_version', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, strconv.Itoa(defaultsVersion)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.removeRetiredDefaults(ctx)
}

// removeRetiredDefaults deletes default situations that a newer set
// replaced, and their findings. Only a replacement marks a default as
// deleted (defaults can't be deleted by hand), so nothing else is touched.
// It also cleans up databases that were upgraded before findings went along.
func (s *store) removeRetiredDefaults(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const retired = "SELECT id FROM situations WHERE builtin = 1 AND deleted = 1"
	if _, err := tx.ExecContext(ctx, "DELETE FROM findings WHERE situation_id IN ("+retired+")"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM situations WHERE builtin = 1 AND deleted = 1"); err != nil {
		return err
	}
	return tx.Commit()
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
