package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const DurableWebhookDeliveryRetention = 30 * 24 * time.Hour

const (
	DurableWebhookAdmissionInserted   = "inserted"
	DurableWebhookAdmissionDuplicate  = "duplicate"
	DurableWebhookAdmissionIneligible = "ineligible"
)

const (
	WebhookErrorNetwork           = "network"
	WebhookErrorHTTP4xx           = "http_4xx"
	WebhookErrorHTTP5xx           = "http_5xx"
	WebhookErrorHTTPOther         = "http_other"
	WebhookErrorSSRF              = "ssrf"
	WebhookErrorRedirect          = "redirect"
	WebhookErrorUnauthorized      = "unauthorized"
	WebhookErrorAttemptsExhausted = "attempts_exhausted"
	WebhookErrorInternal          = "internal"
)

type AdmitDurableWebhookDeliveryInput struct {
	WebhookID               int64
	ProjectID               int64
	AuthorizationGeneration int64
	EventID                 string
	EventType               string
	EventTimestamp          time.Time
	Body                    []byte
	AdmittedAt              time.Time
}

// AdmitDurableWebhookDelivery inserts one event only if the exact subscription
// generation observed by the dispatcher is still durable and currently
// authorized. The unique key makes repeated admission explicit and idempotent.
func (s *Store) AdmitDurableWebhookDelivery(ctx context.Context, in AdmitDurableWebhookDeliveryInput) (string, error) {
	if in.WebhookID <= 0 || in.ProjectID <= 0 || in.AuthorizationGeneration <= 0 || in.EventID == "" || in.EventType != TodoEventCreated || len(in.Body) == 0 || in.EventTimestamp.IsZero() {
		return "", fmt.Errorf("%w: invalid durable webhook delivery", ErrValidation)
	}
	admittedAt := in.AdmittedAt.UTC()
	if admittedAt.IsZero() {
		admittedAt = time.Now().UTC()
	}
	admittedMs := admittedAt.UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin durable webhook admission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `
INSERT INTO webhook_deliveries(
  webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, next_attempt_at,
  created_at, expires_at
)
SELECT w.id, w.project_id, ?, ?, ?, ?, w.authorization_generation,
       'pending', 0, ?, ?, ?
FROM webhooks w
JOIN users u ON u.id = w.user_id
JOIN projects p ON p.id = w.project_id
JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
WHERE w.id = ?
  AND w.project_id = ?
  AND w.delivery_mode = ?
  AND w.authorization_generation = ?
  AND (
    (u.is_service = 0 AND pm.role IN (?, ?)) OR
    (u.is_service = 1 AND pm.role IN (?, ?, ?))
  )
ON CONFLICT(webhook_id, event_id) DO NOTHING`,
		in.EventID, in.EventType, in.EventTimestamp.UTC().UnixMilli(), in.Body,
		admittedMs, admittedMs, admittedAt.Add(DurableWebhookDeliveryRetention).UnixMilli(),
		in.WebhookID, in.ProjectID, WebhookDeliveryDurable, in.AuthorizationGeneration,
		RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer,
	)
	if err != nil {
		return "", fmt.Errorf("insert durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", fmt.Errorf("read durable webhook admission result: %w", err)
	}
	result := DurableWebhookAdmissionInserted
	if affected == 0 {
		var duplicate int
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM webhook_deliveries WHERE webhook_id = ? AND event_id = ?
)`, in.WebhookID, in.EventID).Scan(&duplicate); err != nil {
			return "", fmt.Errorf("classify durable webhook admission: %w", err)
		}
		if duplicate != 0 {
			result = DurableWebhookAdmissionDuplicate
		} else {
			result = DurableWebhookAdmissionIneligible
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit durable webhook admission: %w", err)
	}
	return result, nil
}

type DurableWebhookDelivery struct {
	ID                      int64
	WebhookID               int64
	ProjectID               int64
	EventID                 string
	EventType               string
	EventTimestamp          time.Time
	Body                    []byte
	AuthorizationGeneration int64
	AttemptCount            int
	LeaseToken              string
	LeaseExpiresAt          time.Time
}

// ClaimDueDurableWebhookDeliveries leases a bounded batch in one short
// transaction. Expired leases are reclaimed with a new token; network I/O is
// performed only after this method commits.
func (s *Store) ClaimDueDurableWebhookDeliveries(ctx context.Context, now time.Time, limit int, leaseDuration time.Duration) ([]DurableWebhookDelivery, error) {
	if limit <= 0 || leaseDuration <= 0 {
		return nil, fmt.Errorf("%w: invalid durable webhook claim", ErrValidation)
	}
	now = now.UTC()
	leaseToken := uuid.NewString()
	leaseExpiresAt := now.Add(leaseDuration)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin durable webhook claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
UPDATE webhook_deliveries
SET status = 'leased',
    attempt_count = attempt_count + 1,
    lease_token = ?,
    lease_expires_at = ?,
    completed_at = NULL
WHERE id IN (
  SELECT id
  FROM webhook_deliveries
  WHERE (status = 'pending' AND next_attempt_at <= ?)
     OR (status = 'leased' AND lease_expires_at <= ?)
  ORDER BY CASE status WHEN 'pending' THEN next_attempt_at ELSE lease_expires_at END, id
  LIMIT ?
)
RETURNING id, webhook_id, project_id, event_id, event_type, event_timestamp,
          body, authorization_generation, attempt_count, lease_token, lease_expires_at`,
		leaseToken, leaseExpiresAt.UnixMilli(), now.UnixMilli(), now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("claim durable webhook deliveries: %w", err)
	}
	claims := make([]DurableWebhookDelivery, 0, limit)
	for rows.Next() {
		var claim DurableWebhookDelivery
		var eventTimestampMs, leaseExpiresMs int64
		if err := rows.Scan(
			&claim.ID, &claim.WebhookID, &claim.ProjectID, &claim.EventID,
			&claim.EventType, &eventTimestampMs, &claim.Body,
			&claim.AuthorizationGeneration, &claim.AttemptCount,
			&claim.LeaseToken, &leaseExpiresMs,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan durable webhook claim: %w", err)
		}
		claim.EventTimestamp = time.UnixMilli(eventTimestampMs).UTC()
		claim.LeaseExpiresAt = time.UnixMilli(leaseExpiresMs).UTC()
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate durable webhook claims: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close durable webhook claims: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit durable webhook claim: %w", err)
	}
	return claims, nil
}

// GetDurableWebhookDeliveryTarget returns current destination/signing material
// only for the same authorization generation captured by the delivery.
func (s *Store) GetDurableWebhookDeliveryTarget(ctx context.Context, webhookID, authorizationGeneration int64) (WebhookDeliveryTarget, bool, error) {
	var target WebhookDeliveryTarget
	err := s.db.QueryRowContext(ctx, `
SELECT w.url, w.secret
FROM webhooks w
JOIN users u ON u.id = w.user_id
JOIN projects p ON p.id = w.project_id
JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
WHERE w.id = ?
  AND w.delivery_mode = ?
  AND w.authorization_generation = ?
  AND (
    (u.is_service = 0 AND pm.role IN (?, ?)) OR
    (u.is_service = 1 AND pm.role IN (?, ?, ?))
  )`, webhookID, WebhookDeliveryDurable, authorizationGeneration,
		RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer,
	).Scan(&target.URL, &target.Secret)
	if errors.Is(err, sql.ErrNoRows) {
		return WebhookDeliveryTarget{}, false, nil
	}
	if err != nil {
		return WebhookDeliveryTarget{}, false, fmt.Errorf("authorize durable webhook delivery: %w", err)
	}
	return target, true, nil
}

func validWebhookErrorClass(class string) bool {
	switch class {
	case WebhookErrorNetwork, WebhookErrorHTTP4xx, WebhookErrorHTTP5xx,
		WebhookErrorHTTPOther, WebhookErrorSSRF, WebhookErrorRedirect,
		WebhookErrorUnauthorized, WebhookErrorAttemptsExhausted, WebhookErrorInternal:
		return true
	default:
		return false
	}
}

func (s *Store) CompleteDurableWebhookDelivery(ctx context.Context, id int64, leaseToken string, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "delivered", "", nil, completedAt)
}

func (s *Store) SkipDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "skipped", class, nil, completedAt)
}

func (s *Store) FailDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, httpStatus *int, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "failed", class, httpStatus, completedAt)
}

func (s *Store) finishDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, status, class string, httpStatus *int, completedAt time.Time) (bool, error) {
	if id <= 0 || leaseToken == "" || (status != "delivered" && !validWebhookErrorClass(class)) {
		return false, fmt.Errorf("%w: invalid durable webhook completion", ErrValidation)
	}
	var storedClass any
	if class != "" {
		storedClass = class
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = ?, lease_token = NULL, lease_expires_at = NULL,
    last_error_class = ?, last_http_status = ?, completed_at = ?
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
		status, storedClass, httpStatus, completedAt.UTC().UnixMilli(), id, leaseToken)
	if err != nil {
		return false, fmt.Errorf("finish durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read durable webhook completion result: %w", err)
	}
	return affected == 1, nil
}

func (s *Store) RetryDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, httpStatus *int, nextAttemptAt time.Time) (bool, error) {
	if id <= 0 || leaseToken == "" || !validWebhookErrorClass(class) {
		return false, fmt.Errorf("%w: invalid durable webhook retry", ErrValidation)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = 'pending', lease_token = NULL, lease_expires_at = NULL,
    last_error_class = ?, last_http_status = ?, next_attempt_at = ?, completed_at = NULL
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
		class, httpStatus, nextAttemptAt.UTC().UnixMilli(), id, leaseToken)
	if err != nil {
		return false, fmt.Errorf("retry durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read durable webhook retry result: %w", err)
	}
	return affected == 1, nil
}
