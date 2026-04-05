package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PushSubscription is a stored Web Push subscription (one row per browser/device endpoint).
type PushSubscription struct {
	ID        int64
	UserID    int64
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UpsertPushSubscription inserts or updates a row keyed by endpoint (rebinds to user_id on conflict).
func (s *Store) UpsertPushSubscription(ctx context.Context, userID int64, endpoint, p256dh, auth string, userAgent *string) error {
	if endpoint == "" || p256dh == "" || auth == "" {
		return errors.New("push subscription: endpoint, p256dh, and auth required")
	}
	now := time.Now().UTC().UnixMilli()
	ua := sql.NullString{}
	if userAgent != nil {
		ua.String = *userAgent
		ua.Valid = true
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(endpoint) DO UPDATE SET
  user_id = excluded.user_id,
  p256dh = excluded.p256dh,
  auth = excluded.auth,
  user_agent = excluded.user_agent,
  updated_at = excluded.updated_at
`, userID, endpoint, p256dh, auth, ua, now, now)
	return err
}

// DeletePushSubscription removes a subscription for this user and endpoint.
func (s *Store) DeletePushSubscription(ctx context.Context, userID int64, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id = ? AND endpoint = ?`, userID, endpoint)
	return err
}

// DeletePushSubscriptionByEndpoint removes a row by endpoint only (e.g. after 410 from push service).
func (s *Store) DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// ListPushSubscriptionsByUser returns all push endpoints for a user.
func (s *Store) ListPushSubscriptionsByUser(ctx context.Context, userID int64) ([]PushSubscription, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, user_id, endpoint, p256dh, auth, user_agent, created_at, updated_at
FROM push_subscriptions WHERE user_id = ?
ORDER BY id ASC
`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PushSubscription
	for rows.Next() {
		var r PushSubscription
		var ua sql.NullString
		var createdMs, updatedMs int64
		if err := rows.Scan(&r.ID, &r.UserID, &r.Endpoint, &r.P256dh, &r.Auth, &ua, &createdMs, &updatedMs); err != nil {
			return nil, err
		}
		if ua.Valid {
			s := ua.String
			r.UserAgent = &s
		}
		r.CreatedAt = time.UnixMilli(createdMs).UTC()
		r.UpdatedAt = time.UnixMilli(updatedMs).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}
