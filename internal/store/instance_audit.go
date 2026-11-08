package store

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	instanceActionServiceAccountCreated = "service_account_created"
	instanceActionServiceAccountDeleted = "service_account_deleted"
	instanceActionServiceTokenCreated   = "service_token_created"
	instanceActionServiceTokenRevoked   = "service_token_revoked"
)

func userPublicIDTx(ctx context.Context, tx *sql.Tx, userID int64) (string, error) {
	var publicID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(public_id, '') FROM users WHERE id = ?`, userID).Scan(&publicID); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read user public id: %w", err)
	}
	return publicID, nil
}

func (s *Store) insertInstanceAuditEventTx(
	ctx context.Context,
	tx *sql.Tx,
	actorUserID int64,
	actorPublicID string,
	action string,
	targetUserID int64,
	targetPublicID string,
	tokenID *int64,
) error {
	if !s.instanceAuditEnabled {
		return nil
	}
	var tokenArg any
	if tokenID != nil {
		tokenArg = *tokenID
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO instance_audit_events(
  actor_user_id, actor_public_id, action, target_type,
  target_user_id, target_public_id, token_id, metadata, created_at
)
VALUES (?, ?, ?, 'service_account', ?, ?, ?, ?, ?)`,
		actorUserID, actorPublicID, action, targetUserID, targetPublicID, tokenArg, nil, nowUnixMs(),
	); err != nil {
		return fmt.Errorf("insert instance audit event: %w", err)
	}
	return nil
}
