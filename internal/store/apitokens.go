package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

// APITokenPrefix is the mandatory v1 wire prefix for minted access tokens (before random material).
const APITokenPrefix = "sb_"

// APITokenMeta is non-secret metadata for one of a user's API access tokens.
type APITokenMeta struct {
	ID                    int64
	UserID                int64
	UserPublicID          string
	Name                  *string
	CreatedAt             time.Time
	LastUsedAt            *time.Time
	RevokedAt             *time.Time
	IsService             bool
	CreatedByUserID       *int64
	CreatedByUserPublicID *string
}

const (
	DefaultServiceTokenPageSize = 20
	MaxServiceTokenPageSize     = 100
)

func generateAPIToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("rand token: %w", err)
	}
	plaintext := APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return plaintext, hashToken(plaintext), nil
}

func normalizeAPITokenName(name *string) any {
	if name == nil {
		return nil
	}
	normalized := strings.TrimSpace(*name)
	if normalized == "" {
		return nil
	}
	return normalized
}

// CreateUserAPIToken generates a new opaque token, stores SHA-256(token) only, and returns the new row id, plaintext once, and created time.
// isService marks the user-owned token for bot/automation use; it authenticates as that user like any
// other token. When the user is later deleted, DeleteUser copies its metadata and owner provenance
// into archived_service_api_tokens before the row cascade-deletes, so the metadata is kept for
// offboarding review while the secret stops working.
//
// This is the self-service path for humans, so it refuses a service account; provisioning a service
// account's credentials needs its own explicitly authorized operation.
func (s *Store) CreateUserAPIToken(ctx context.Context, userID int64, name *string, isService bool) (id int64, plaintext string, createdAt time.Time, err error) {
	if userID <= 0 {
		return 0, "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}

	plaintext, tokenHash, err := generateAPIToken()
	if err != nil {
		return 0, "", time.Time{}, err
	}

	nowMs := time.Now().UTC().UnixMilli()
	createdAt = time.UnixMilli(nowMs).UTC()

	nameArg := normalizeAPITokenName(name)

	res, err := s.db.ExecContext(ctx, `
INSERT INTO api_tokens(
  user_id, token_hash, name, created_at, last_used_at, revoked_at, is_service,
  created_by_user_id, created_by_user_public_id
)
SELECT u.id, ?, ?, ?, NULL, NULL, ?, u.id, u.public_id
FROM (SELECT ? AS id) requested
LEFT JOIN users u ON u.id = requested.id
WHERE NOT EXISTS (SELECT 1 FROM users WHERE id = requested.id AND is_service <> 0)
`, tokenHash, nameArg, nowMs, isService, userID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: api_tokens.token_hash") {
			return 0, "", time.Time{}, ErrConflict
		}
		return 0, "", time.Time{}, fmt.Errorf("insert api token: %w", err)
	}
	inserted, err := res.RowsAffected()
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("api token rows affected: %w", err)
	}
	if inserted != 1 {
		return 0, "", time.Time{}, ErrServiceAccountRestricted
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("api token last insert id: %w", err)
	}
	return newID, plaintext, createdAt, nil
}

func (s *Store) CreateServiceAccountToken(ctx context.Context, requesterID, serviceUserID int64, name *string) (id int64, plaintext string, createdAt time.Time, err error) {
	if requesterID <= 0 || serviceUserID <= 0 {
		return 0, "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	plaintext, tokenHash, err := generateAPIToken()
	if err != nil {
		return 0, "", time.Time{}, err
	}
	createdAt = time.Now().UTC()
	nowMs := createdAt.UnixMilli()
	createdAt = time.UnixMilli(nowMs).UTC()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("begin create service token tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireOwnerTx(ctx, tx, requesterID); err != nil {
		return 0, "", time.Time{}, err
	}
	actorPublicID, err := userPublicIDTx(ctx, tx, requesterID)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	var (
		targetPublicID string
		targetService  bool
	)
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(public_id, ''), is_service <> 0 FROM users WHERE id = ?`, serviceUserID).Scan(&targetPublicID, &targetService); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", time.Time{}, ErrNotFound
		}
		return 0, "", time.Time{}, fmt.Errorf("read service token target: %w", err)
	}
	if !targetService {
		return 0, "", time.Time{}, fmt.Errorf("%w: target must be a service account", ErrValidation)
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO api_tokens(
  user_id, token_hash, name, created_at, last_used_at, revoked_at, is_service,
  created_by_user_id, created_by_user_public_id
)
VALUES (?, ?, ?, ?, NULL, NULL, 1, ?, ?)`,
		serviceUserID, tokenHash, normalizeAPITokenName(name), nowMs, requesterID, actorPublicID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: api_tokens.token_hash") {
			return 0, "", time.Time{}, ErrConflict
		}
		return 0, "", time.Time{}, fmt.Errorf("insert service token: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("service token last insert id: %w", err)
	}
	if err := s.insertInstanceAuditEventTx(ctx, tx, requesterID, actorPublicID, instanceActionServiceTokenCreated, serviceUserID, targetPublicID, &id); err != nil {
		return 0, "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("commit create service token tx: %w", err)
	}
	return id, plaintext, createdAt, nil
}

func (s *Store) ListServiceAccountTokens(ctx context.Context, requesterID, serviceUserID int64, limit int, beforeID int64) ([]APITokenMeta, *int64, error) {
	if requesterID <= 0 || serviceUserID <= 0 || beforeID < 0 {
		return nil, nil, fmt.Errorf("%w: invalid id", ErrValidation)
	}
	if limit <= 0 {
		limit = DefaultServiceTokenPageSize
	}
	if limit > MaxServiceTokenPageSize {
		return nil, nil, fmt.Errorf("%w: limit must not exceed %d", ErrValidation, MaxServiceTokenPageSize)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin list service tokens tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdminTx(ctx, tx, requesterID); err != nil {
		return nil, nil, err
	}
	var targetPublicID string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(public_id, '') FROM users WHERE id = ? AND is_service <> 0`, serviceUserID).Scan(&targetPublicID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("read service token owner: %w", err)
	}

	query := `
SELECT id, name, created_at, last_used_at, revoked_at,
       created_by_user_id, created_by_user_public_id
FROM api_tokens
WHERE user_id = ?`
	args := []any{serviceUserID}
	if beforeID > 0 {
		query += ` AND id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list service tokens: %w", err)
	}
	defer rows.Close()

	items := make([]APITokenMeta, 0, limit+1)
	for rows.Next() {
		var (
			item            APITokenMeta
			name            sql.NullString
			createdAtMs     int64
			lastUsedAtMs    sql.NullInt64
			revokedAtMs     sql.NullInt64
			createdByID     sql.NullInt64
			createdByPublic sql.NullString
		)
		if err := rows.Scan(&item.ID, &name, &createdAtMs, &lastUsedAtMs, &revokedAtMs, &createdByID, &createdByPublic); err != nil {
			return nil, nil, fmt.Errorf("scan service token: %w", err)
		}
		item.UserID = serviceUserID
		item.UserPublicID = targetPublicID
		item.IsService = true
		item.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		if name.Valid {
			value := name.String
			item.Name = &value
		}
		if lastUsedAtMs.Valid {
			value := time.UnixMilli(lastUsedAtMs.Int64).UTC()
			item.LastUsedAt = &value
		}
		if revokedAtMs.Valid {
			value := time.UnixMilli(revokedAtMs.Int64).UTC()
			item.RevokedAt = &value
		}
		if createdByID.Valid {
			value := createdByID.Int64
			item.CreatedByUserID = &value
		}
		if createdByPublic.Valid {
			value := createdByPublic.String
			item.CreatedByUserPublicID = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate service tokens: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, fmt.Errorf("close service tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit list service tokens tx: %w", err)
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	items = items[:limit]
	next := items[len(items)-1].ID
	return items, &next, nil
}

func (s *Store) RevokeServiceAccountToken(ctx context.Context, requesterID, tokenID int64) error {
	if requesterID <= 0 || tokenID <= 0 {
		return fmt.Errorf("%w: invalid id", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin revoke service token tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireAdminTx(ctx, tx, requesterID); err != nil {
		return err
	}
	var (
		targetUserID   int64
		targetPublicID string
		targetService  bool
		revokedAtMs    sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, `
SELECT u.id, COALESCE(u.public_id, ''), u.is_service <> 0, t.revoked_at
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.id = ?`, tokenID).Scan(&targetUserID, &targetPublicID, &targetService, &revokedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("read service token for revocation: %w", err)
	}
	if !targetService {
		return ErrNotFound
	}
	if revokedAtMs.Valid {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().UTC().UnixMilli(), tokenID); err != nil {
		return fmt.Errorf("revoke service token: %w", err)
	}
	if s.instanceAuditEnabled {
		actorPublicID, err := userPublicIDTx(ctx, tx, requesterID)
		if err != nil {
			return err
		}
		if err := s.insertInstanceAuditEventTx(ctx, tx, requesterID, actorPublicID, instanceActionServiceTokenRevoked, targetUserID, targetPublicID, &tokenID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke service token tx: %w", err)
	}
	return nil
}

// ListUserAPITokens returns all tokens for the user (including revoked), newest first.
func (s *Store) ListUserAPITokens(ctx context.Context, userID int64) ([]APITokenMeta, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, created_at, last_used_at, revoked_at, is_service
FROM api_tokens
WHERE user_id = ?
ORDER BY created_at DESC
`, userID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()

	var out []APITokenMeta
	for rows.Next() {
		var (
			id          int64
			name        sql.NullString
			createdAtMs int64
			lastUsedMs  sql.NullInt64
			revokedMs   sql.NullInt64
			isService   bool
		)
		if err := rows.Scan(&id, &name, &createdAtMs, &lastUsedMs, &revokedMs, &isService); err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		meta := APITokenMeta{
			ID:        id,
			CreatedAt: time.UnixMilli(createdAtMs).UTC(),
			IsService: isService,
		}
		if name.Valid {
			s := name.String
			meta.Name = &s
		}
		if lastUsedMs.Valid {
			t := time.UnixMilli(lastUsedMs.Int64).UTC()
			meta.LastUsedAt = &t
		}
		if revokedMs.Valid {
			t := time.UnixMilli(revokedMs.Int64).UTC()
			meta.RevokedAt = &t
		}
		out = append(out, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api tokens: %w", err)
	}
	return out, nil
}

// RevokeUserAPIToken sets revoked_at for the token row if it belongs to the user and is not already revoked.
func (s *Store) RevokeUserAPIToken(ctx context.Context, userID, tokenID int64) error {
	if userID <= 0 || tokenID <= 0 {
		return fmt.Errorf("%w: invalid id", ErrValidation)
	}
	nowMs := time.Now().UTC().UnixMilli()
	res, err := s.db.ExecContext(ctx, `
UPDATE api_tokens SET revoked_at = ?
WHERE id = ? AND user_id = ? AND revoked_at IS NULL
`, nowMs, tokenID, userID)
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke api token rows: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUserByAPIToken returns the user for an active (non-revoked) API token. The full presented secret
// (including sb_ prefix) is hashed and matched. last_used_at is updated best-effort and must not affect success.
// Unlike every interactive lookup, this one does not exclude service accounts: an API token is their
// machine-authentication channel. They come back with IsService set and the plain user role.
func (s *Store) GetUserByAPIToken(ctx context.Context, rawToken string) (User, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return User{}, ErrNotFound
	}
	if !strings.HasPrefix(rawToken, APITokenPrefix) {
		return User{}, ErrNotFound
	}

	tokenHash := hashToken(rawToken)
	nowMs := time.Now().UTC().UnixMilli()

	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
	)
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.email, u.name, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled, u.is_service <> 0
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = ?
  AND t.revoked_at IS NULL
`, tokenHash).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled, &u.IsService)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get api token user: %w", err)
	}
	u.IsBootstrap = isBootstrap
	u.SystemRole = effectiveSystemRole(systemRoleStr, u.IsService)
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled && !u.IsService

	go func() {
		_, _ = s.db.ExecContext(context.Background(),
			`UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
			nowMs, tokenHash)
	}()

	return u, nil
}
