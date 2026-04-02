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
	ID         int64
	Name       *string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

// CreateUserAPIToken generates a new opaque token, stores SHA-256(token) only, and returns the new row id, plaintext once, and created time.
func (s *Store) CreateUserAPIToken(ctx context.Context, userID int64, name *string) (id int64, plaintext string, createdAt time.Time, err error) {
	if userID <= 0 {
		return 0, "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return 0, "", time.Time{}, fmt.Errorf("rand token: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(b)
	plaintext = APITokenPrefix + secret
	tokenHash := hashToken(plaintext)

	nowMs := time.Now().UTC().UnixMilli()
	createdAt = time.UnixMilli(nowMs).UTC()

	var nameArg any
	if name != nil {
		n := strings.TrimSpace(*name)
		if n != "" {
			nameArg = n
		}
	}

	res, err := s.db.ExecContext(ctx, `
INSERT INTO api_tokens(user_id, token_hash, name, created_at, last_used_at, revoked_at)
VALUES (?, ?, ?, ?, NULL, NULL)
`, userID, tokenHash, nameArg, nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: api_tokens.token_hash") {
			return 0, "", time.Time{}, ErrConflict
		}
		return 0, "", time.Time{}, fmt.Errorf("insert api token: %w", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, "", time.Time{}, fmt.Errorf("api token last insert id: %w", err)
	}
	return newID, plaintext, createdAt, nil
}

// ListUserAPITokens returns all tokens for the user (including revoked), newest first.
func (s *Store) ListUserAPITokens(ctx context.Context, userID int64) ([]APITokenMeta, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, created_at, last_used_at, revoked_at
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
			id              int64
			name            sql.NullString
			createdAtMs     int64
			lastUsedMs      sql.NullInt64
			revokedMs       sql.NullInt64
		)
		if err := rows.Scan(&id, &name, &createdAtMs, &lastUsedMs, &revokedMs); err != nil {
			return nil, fmt.Errorf("scan api token: %w", err)
		}
		meta := APITokenMeta{
			ID:        id,
			CreatedAt: time.UnixMilli(createdAtMs).UTC(),
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
SELECT u.id, u.email, u.name, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = ?
  AND t.revoked_at IS NULL
`, tokenHash).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get api token user: %w", err)
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled

	go func() {
		_, _ = s.db.ExecContext(context.Background(),
			`UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
			nowMs, tokenHash)
	}()

	return u, nil
}
