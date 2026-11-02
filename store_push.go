package guardian

import (
	"context"
	"time"
)

// PushDevice is one browser subscribed to push (criterion 22).
type PushDevice struct {
	ID       int64
	Endpoint string
	P256dh   string
	Auth     string
	Label    string
	Created  time.Time
}

// AddPushDevice subscribes a browser; subscribing again updates its keys.
func (s *store) AddPushDevice(ctx context.Context, d PushDevice) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO push_devices (endpoint, p256dh, auth, label, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET p256dh = excluded.p256dh, auth = excluded.auth, label = excluded.label`,
		d.Endpoint, d.P256dh, d.Auth, d.Label, time.Now().Unix())
	return err
}

func (s *store) PushDevices(ctx context.Context) ([]PushDevice, error) {
	return queryAll(ctx, s.db, func(sc scanner) (PushDevice, error) {
		var d PushDevice
		var created int64
		err := sc.Scan(&d.ID, &d.Endpoint, &d.P256dh, &d.Auth, &d.Label, &created)
		d.Created = time.Unix(created, 0)
		return d, err
	}, "SELECT id, endpoint, p256dh, auth, label, created_at FROM push_devices ORDER BY id")
}

func (s *store) RemovePushDevice(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM push_devices WHERE id = ?", id)
	return err
}

// RemovePushEndpoint forgets a browser the push service says is gone.
func (s *store) RemovePushEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM push_devices WHERE endpoint = ?", endpoint)
	return err
}
