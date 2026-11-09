package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	DurableWebhookMaxBodyBytes            = 64 * 1024
	DurableWebhookMaxActivePerWebhook     = 500
	DurableWebhookMaxActiveRows           = 10_000
	DurableWebhookMaxActivePayloadBytes   = 128 * 1024 * 1024
	DurableWebhookMaxRetainedRows         = 50_000
	DurableWebhookMaxRetainedPayloadBytes = 256 * 1024 * 1024
	DurableWebhookMaxHTTPAttempts         = 8
	DurableWebhookJanitorBatch            = 200
	DurableWebhookActiveLifetime          = 24 * time.Hour
	DurableWebhookDeliveredRetention      = 7 * 24 * time.Hour
	DurableWebhookFailedRetention         = 30 * 24 * time.Hour
)

var (
	ErrDurableWebhookCapacity        = errors.New("durable webhook storage capacity exceeded")
	ErrDurableWebhookPayloadTooLarge = errors.New("durable webhook payload exceeds 64 KiB")
)

const (
	DurableWebhookAdmissionInserted   = "inserted"
	DurableWebhookAdmissionDuplicate  = "duplicate"
	DurableWebhookAdmissionIneligible = "ineligible"
)

const (
	DurableWebhookAttemptReserved  = "reserved"
	DurableWebhookAttemptExhausted = "exhausted"
	DurableWebhookAttemptExpired   = "expired"
	DurableWebhookAttemptStale     = "stale"
)

const (
	WebhookErrorNetwork           = "network"
	WebhookErrorHTTP3xx           = "http_3xx"
	WebhookErrorHTTP4xx           = "http_4xx"
	WebhookErrorHTTP5xx           = "http_5xx"
	WebhookErrorHTTPOther         = "http_other"
	WebhookErrorSSRF              = "ssrf"
	WebhookErrorRedirect          = "redirect"
	WebhookErrorUnauthorized      = "unauthorized"
	WebhookErrorAttemptsExhausted = "attempts_exhausted"
	WebhookErrorExpired           = "expired"
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

func (s *Store) AdmitDurableWebhookDelivery(ctx context.Context, in AdmitDurableWebhookDeliveryInput) (string, error) {
	if in.WebhookID <= 0 || in.ProjectID <= 0 || in.AuthorizationGeneration <= 0 || in.EventID == "" || in.EventType != TodoEventCreated || len(in.Body) == 0 || in.EventTimestamp.IsZero() {
		return "", fmt.Errorf("%w: invalid durable webhook delivery", ErrValidation)
	}
	if len(in.Body) > DurableWebhookMaxBodyBytes {
		return "", ErrDurableWebhookPayloadTooLarge
	}
	admittedAt := in.AdmittedAt.UTC()
	if admittedAt.IsZero() {
		admittedAt = time.Now().UTC()
	}
	admittedMs := admittedAt.UnixMilli()
	activeDeadlineMs := admittedAt.Add(DurableWebhookActiveLifetime).UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("begin durable webhook admission: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert := func() (int64, error) {
		res, execErr := tx.ExecContext(ctx, `
INSERT INTO webhook_deliveries(
  webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, http_attempt_count,
  next_attempt_at, created_at, expires_at
)
SELECT w.id, w.project_id, ?, ?, ?, ?, w.authorization_generation,
       'pending', 0, 0, ?, ?, ?
FROM webhooks w
JOIN users u ON u.id = w.user_id
JOIN projects p ON p.id = w.project_id
JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
JOIN webhook_delivery_usage usage ON usage.singleton = 1
WHERE w.id = ?
  AND w.project_id = ?
  AND w.delivery_mode = ?
  AND w.authorization_generation = ?
  AND usage.active_rows < ?
  AND usage.active_payload_bytes + length(?) <= ?
  AND usage.total_rows < ?
  AND usage.total_payload_bytes + length(?) <= ?
  AND (
    SELECT COUNT(*) FROM webhook_deliveries d
    WHERE d.webhook_id = w.id AND d.status IN ('pending', 'leased')
  ) < ?
  AND (
    (u.is_service = 0 AND pm.role IN (?, ?)) OR
    (u.is_service = 1 AND pm.role IN (?, ?, ?))
  )
ON CONFLICT(webhook_id, event_id) DO NOTHING`,
			in.EventID, in.EventType, in.EventTimestamp.UTC().UnixMilli(), in.Body,
			admittedMs, admittedMs, activeDeadlineMs,
			in.WebhookID, in.ProjectID, WebhookDeliveryDurable, in.AuthorizationGeneration,
			DurableWebhookMaxActiveRows, in.Body, DurableWebhookMaxActivePayloadBytes,
			DurableWebhookMaxRetainedRows, in.Body, DurableWebhookMaxRetainedPayloadBytes,
			DurableWebhookMaxActivePerWebhook,
			RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer,
		)
		if execErr != nil {
			return 0, execErr
		}
		return res.RowsAffected()
	}

	affected, err := insert()
	if err != nil {
		return "", fmt.Errorf("insert durable webhook delivery: %w", err)
	}
	if affected == 0 {
		var duplicate int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1 FROM webhook_deliveries WHERE webhook_id = ? AND event_id = ?
)`, in.WebhookID, in.EventID).Scan(&duplicate); err != nil {
			return "", fmt.Errorf("classify durable webhook duplicate: %w", err)
		}
		if duplicate != 0 {
			if err := tx.Commit(); err != nil {
				return "", fmt.Errorf("commit durable webhook duplicate: %w", err)
			}
			return DurableWebhookAdmissionDuplicate, nil
		}
		eligible, eligibilityErr := durableWebhookEligibleTx(ctx, tx, in.WebhookID, in.ProjectID, in.AuthorizationGeneration)
		if eligibilityErr != nil {
			return "", eligibilityErr
		}
		if !eligible {
			return DurableWebhookAdmissionIneligible, nil
		}
		if _, _, err := maintainDurableWebhookDeliveriesTx(ctx, tx, admittedAt, DurableWebhookJanitorBatch); err != nil {
			return "", err
		}
		affected, err = insert()
		if err != nil {
			return "", fmt.Errorf("retry durable webhook admission after cleanup: %w", err)
		}
		if affected == 0 {
			if err := tx.Commit(); err != nil {
				return "", fmt.Errorf("commit durable webhook capacity cleanup: %w", err)
			}
			return "", ErrDurableWebhookCapacity
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit durable webhook admission: %w", err)
	}
	return DurableWebhookAdmissionInserted, nil
}

func durableWebhookEligibleTx(ctx context.Context, tx *sql.Tx, webhookID, projectID, generation int64) (bool, error) {
	var eligible int
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
  SELECT 1
  FROM webhooks w
  JOIN users u ON u.id = w.user_id
  JOIN projects p ON p.id = w.project_id
  JOIN project_members pm ON pm.project_id = w.project_id AND pm.user_id = w.user_id
  WHERE w.id = ? AND w.project_id = ? AND w.delivery_mode = ?
    AND w.authorization_generation = ?
    AND ((u.is_service = 0 AND pm.role IN (?, ?))
      OR (u.is_service = 1 AND pm.role IN (?, ?, ?)))
)`, webhookID, projectID, WebhookDeliveryDurable, generation,
		RoleMaintainer, RoleOwner, RoleViewer, RoleContributor, RoleMaintainer).Scan(&eligible)
	if err != nil {
		return false, fmt.Errorf("authorize durable webhook admission: %w", err)
	}
	return eligible != 0, nil
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
	HTTPAttemptCount        int
	LeaseToken              string
	LeaseExpiresAt          time.Time
}

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
	if _, err := tx.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = 'failed', lease_token = NULL, lease_expires_at = NULL,
    last_error_class = ?, completed_at = ?, expires_at = ?
WHERE id IN (
  SELECT id FROM webhook_deliveries
  WHERE http_attempt_count >= ?
    AND (status = 'pending' OR (status = 'leased' AND lease_expires_at <= ?))
  ORDER BY id
  LIMIT ?
)`, WebhookErrorAttemptsExhausted, now.UnixMilli(), now.Add(DurableWebhookFailedRetention).UnixMilli(),
		DurableWebhookMaxHTTPAttempts, now.UnixMilli(), limit); err != nil {
		return nil, fmt.Errorf("terminalize exhausted durable webhook deliveries: %w", err)
	}
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
  WHERE expires_at > ?
    AND http_attempt_count < ?
    AND ((status = 'pending' AND next_attempt_at <= ?)
      OR (status = 'leased' AND lease_expires_at <= ?))
  ORDER BY CASE status WHEN 'pending' THEN next_attempt_at ELSE lease_expires_at END, id
  LIMIT ?
)
RETURNING id, webhook_id, project_id, event_id, event_type, event_timestamp,
          body, authorization_generation, attempt_count, http_attempt_count,
          lease_token, lease_expires_at`,
		leaseToken, leaseExpiresAt.UnixMilli(), now.UnixMilli(), DurableWebhookMaxHTTPAttempts,
		now.UnixMilli(), now.UnixMilli(), limit)
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
			&claim.AuthorizationGeneration, &claim.AttemptCount, &claim.HTTPAttemptCount,
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
  AND ((u.is_service = 0 AND pm.role IN (?, ?))
    OR (u.is_service = 1 AND pm.role IN (?, ?, ?)))`, webhookID, WebhookDeliveryDurable, authorizationGeneration,
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

type DurableWebhookAttemptReservation struct {
	Status           string
	HTTPAttemptCount int
}

func (s *Store) ReserveDurableWebhookAttempt(ctx context.Context, id int64, leaseToken string, now time.Time) (DurableWebhookAttemptReservation, error) {
	if id <= 0 || leaseToken == "" || now.IsZero() {
		return DurableWebhookAttemptReservation{}, fmt.Errorf("%w: invalid durable webhook attempt reservation", ErrValidation)
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return DurableWebhookAttemptReservation{}, fmt.Errorf("begin durable webhook attempt reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	err = tx.QueryRowContext(ctx, `
UPDATE webhook_deliveries
SET http_attempt_count = http_attempt_count + 1
WHERE id = ? AND status = 'leased' AND lease_token = ?
	AND lease_expires_at > ? AND expires_at > ? AND http_attempt_count < ?
RETURNING http_attempt_count`, id, leaseToken, now.UnixMilli(), now.UnixMilli(), DurableWebhookMaxHTTPAttempts).Scan(&count)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return DurableWebhookAttemptReservation{}, fmt.Errorf("commit durable webhook attempt reservation: %w", err)
		}
		return DurableWebhookAttemptReservation{Status: DurableWebhookAttemptReserved, HTTPAttemptCount: count}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DurableWebhookAttemptReservation{}, fmt.Errorf("reserve durable webhook attempt: %w", err)
	}
	var status string
	var currentToken sql.NullString
	var deadlineMs int64
	var leaseExpiresMs sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
SELECT status, lease_token, lease_expires_at, expires_at, http_attempt_count
FROM webhook_deliveries WHERE id = ?`, id).Scan(&status, &currentToken, &leaseExpiresMs, &deadlineMs, &count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DurableWebhookAttemptReservation{Status: DurableWebhookAttemptStale}, nil
		}
		return DurableWebhookAttemptReservation{}, fmt.Errorf("classify durable webhook attempt reservation: %w", err)
	}
	result := DurableWebhookAttemptStale
	if status == "leased" && currentToken.Valid && currentToken.String == leaseToken && leaseExpiresMs.Valid && leaseExpiresMs.Int64 > now.UnixMilli() {
		class := ""
		if count >= DurableWebhookMaxHTTPAttempts {
			result = DurableWebhookAttemptExhausted
			class = WebhookErrorAttemptsExhausted
		} else if deadlineMs <= now.UnixMilli() {
			result = DurableWebhookAttemptExpired
			class = WebhookErrorExpired
		}
		if class != "" {
			if _, err := tx.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = 'failed', lease_token = NULL, lease_expires_at = NULL,
    last_error_class = ?, completed_at = ?, expires_at = ?
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
				class, now.UnixMilli(), now.Add(DurableWebhookFailedRetention).UnixMilli(), id, leaseToken); err != nil {
				return DurableWebhookAttemptReservation{}, fmt.Errorf("terminalize durable webhook attempt reservation: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return DurableWebhookAttemptReservation{}, fmt.Errorf("commit durable webhook attempt classification: %w", err)
	}
	return DurableWebhookAttemptReservation{Status: result, HTTPAttemptCount: count}, nil
}

func validWebhookErrorClass(class string) bool {
	switch class {
	case WebhookErrorNetwork, WebhookErrorHTTP3xx, WebhookErrorHTTP4xx, WebhookErrorHTTP5xx,
		WebhookErrorHTTPOther, WebhookErrorSSRF, WebhookErrorRedirect,
		WebhookErrorUnauthorized, WebhookErrorAttemptsExhausted, WebhookErrorExpired, WebhookErrorInternal:
		return true
	default:
		return false
	}
}

func (s *Store) CompleteDurableWebhookDelivery(ctx context.Context, id int64, leaseToken string, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "delivered", "", nil, completedAt, DurableWebhookDeliveredRetention)
}

func (s *Store) SkipDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "skipped", class, nil, completedAt, DurableWebhookFailedRetention)
}

func (s *Store) FailDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, httpStatus *int, completedAt time.Time) (bool, error) {
	return s.finishDurableWebhookDelivery(ctx, id, leaseToken, "failed", class, httpStatus, completedAt, DurableWebhookFailedRetention)
}

func (s *Store) finishDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, status, class string, httpStatus *int, completedAt time.Time, retention time.Duration) (bool, error) {
	if id <= 0 || leaseToken == "" || completedAt.IsZero() || (status != "delivered" && !validWebhookErrorClass(class)) {
		return false, fmt.Errorf("%w: invalid durable webhook completion", ErrValidation)
	}
	var storedClass any
	if class != "" {
		storedClass = class
	}
	completedAt = completedAt.UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = ?, lease_token = NULL, lease_expires_at = NULL,
    last_error_class = ?, last_http_status = ?, completed_at = ?, expires_at = ?
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
		status, storedClass, httpStatus, completedAt.UnixMilli(), completedAt.Add(retention).UnixMilli(), id, leaseToken)
	if err != nil {
		return false, fmt.Errorf("finish durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read durable webhook completion result: %w", err)
	}
	return affected == 1, nil
}

func (s *Store) RetryDurableWebhookDelivery(ctx context.Context, id int64, leaseToken, class string, httpStatus *int, nextAttemptAt, updatedAt time.Time) (bool, error) {
	if id <= 0 || leaseToken == "" || !validWebhookErrorClass(class) || nextAttemptAt.IsZero() || updatedAt.IsZero() {
		return false, fmt.Errorf("%w: invalid durable webhook retry", ErrValidation)
	}
	updatedAt = updatedAt.UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = CASE
      WHEN http_attempt_count >= ? OR expires_at <= ? THEN 'failed'
      ELSE 'pending'
    END,
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error_class = CASE
      WHEN http_attempt_count >= ? THEN ?
      WHEN expires_at <= ? THEN ?
      ELSE ?
    END,
    last_http_status = ?,
    next_attempt_at = ?,
    completed_at = CASE WHEN http_attempt_count >= ? OR expires_at <= ? THEN ? ELSE NULL END,
    expires_at = CASE
      WHEN http_attempt_count >= ? OR expires_at <= ? THEN ?
      ELSE expires_at
    END
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
		DurableWebhookMaxHTTPAttempts, updatedAt.UnixMilli(),
		DurableWebhookMaxHTTPAttempts, WebhookErrorAttemptsExhausted, updatedAt.UnixMilli(), WebhookErrorExpired, class,
		httpStatus, nextAttemptAt.UTC().UnixMilli(),
		DurableWebhookMaxHTTPAttempts, updatedAt.UnixMilli(), updatedAt.UnixMilli(),
		DurableWebhookMaxHTTPAttempts, updatedAt.UnixMilli(), updatedAt.Add(DurableWebhookFailedRetention).UnixMilli(),
		id, leaseToken)
	if err != nil {
		return false, fmt.Errorf("retry durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read durable webhook retry result: %w", err)
	}
	return affected == 1, nil
}

func (s *Store) ReleaseDurableWebhookDelivery(ctx context.Context, id int64, leaseToken string, now time.Time) (bool, error) {
	if id <= 0 || leaseToken == "" || now.IsZero() {
		return false, fmt.Errorf("%w: invalid durable webhook release", ErrValidation)
	}
	now = now.UTC()
	res, err := s.db.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = CASE WHEN expires_at <= ? THEN 'failed' ELSE 'pending' END,
    lease_token = NULL,
    lease_expires_at = NULL,
    next_attempt_at = ?,
    last_error_class = CASE WHEN expires_at <= ? THEN ? ELSE last_error_class END,
    completed_at = CASE WHEN expires_at <= ? THEN ? ELSE NULL END,
    expires_at = CASE WHEN expires_at <= ? THEN ? ELSE expires_at END
WHERE id = ? AND status = 'leased' AND lease_token = ?`,
		now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), WebhookErrorExpired,
		now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.Add(DurableWebhookFailedRetention).UnixMilli(), id, leaseToken)
	if err != nil {
		return false, fmt.Errorf("release durable webhook delivery: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read durable webhook release result: %w", err)
	}
	return affected == 1, nil
}

type DurableWebhookMaintenance struct {
	Terminalized int64
	Deleted      int64
}

func (s *Store) MaintainDurableWebhookDeliveries(ctx context.Context, now time.Time, batch int) (DurableWebhookMaintenance, error) {
	if now.IsZero() || batch <= 0 {
		return DurableWebhookMaintenance{}, fmt.Errorf("%w: invalid durable webhook maintenance", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return DurableWebhookMaintenance{}, fmt.Errorf("begin durable webhook maintenance: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	terminalized, deleted, err := maintainDurableWebhookDeliveriesTx(ctx, tx, now.UTC(), batch)
	if err != nil {
		return DurableWebhookMaintenance{}, err
	}
	if err := tx.Commit(); err != nil {
		return DurableWebhookMaintenance{}, fmt.Errorf("commit durable webhook maintenance: %w", err)
	}
	return DurableWebhookMaintenance{Terminalized: terminalized, Deleted: deleted}, nil
}

func maintainDurableWebhookDeliveriesTx(ctx context.Context, tx *sql.Tx, now time.Time, batch int) (int64, int64, error) {
	nowMs := now.UnixMilli()
	res, err := tx.ExecContext(ctx, `
UPDATE webhook_deliveries
SET status = 'failed',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_error_class = CASE WHEN http_attempt_count >= ? THEN ? ELSE ? END,
    completed_at = ?,
    expires_at = ?
WHERE id IN (
  SELECT id FROM webhook_deliveries
  WHERE (status = 'pending' OR (status = 'leased' AND lease_expires_at <= ?))
    AND (http_attempt_count >= ? OR expires_at <= ?)
  ORDER BY expires_at, id
  LIMIT ?
)`, DurableWebhookMaxHTTPAttempts, WebhookErrorAttemptsExhausted, WebhookErrorExpired,
		nowMs, now.Add(DurableWebhookFailedRetention).UnixMilli(), nowMs,
		DurableWebhookMaxHTTPAttempts, nowMs, batch)
	if err != nil {
		return 0, 0, fmt.Errorf("terminalize expired durable webhook deliveries: %w", err)
	}
	terminalized, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("read terminalized durable webhook deliveries: %w", err)
	}
	res, err = tx.ExecContext(ctx, `
DELETE FROM webhook_deliveries
WHERE id IN (
  SELECT id FROM webhook_deliveries
  WHERE status IN ('delivered', 'failed', 'skipped') AND expires_at <= ?
  ORDER BY expires_at, id
  LIMIT ?
)`, nowMs, batch)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired durable webhook deliveries: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("read deleted durable webhook deliveries: %w", err)
	}
	return terminalized, deleted, nil
}

type DurableWebhookUsage struct {
	ActiveRows         int64
	ActivePayloadBytes int64
	TotalRows          int64
	TotalPayloadBytes  int64
}

func (s *Store) DurableWebhookDeliveryUsage(ctx context.Context) (DurableWebhookUsage, error) {
	var usage DurableWebhookUsage
	err := s.db.QueryRowContext(ctx, `
SELECT active_rows, active_payload_bytes, total_rows, total_payload_bytes
FROM webhook_delivery_usage WHERE singleton = 1`).Scan(
		&usage.ActiveRows, &usage.ActivePayloadBytes, &usage.TotalRows, &usage.TotalPayloadBytes,
	)
	if err != nil {
		return DurableWebhookUsage{}, fmt.Errorf("read durable webhook delivery usage: %w", err)
	}
	return usage, nil
}
