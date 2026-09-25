package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MaxArchivedServiceAPITokensPageSize bounds one page of ListArchivedServiceAPITokens.
const MaxArchivedServiceAPITokensPageSize = 200

// ArchivedServiceAPIToken is the immutable (until purged) record of a service API token whose
// owning user was deleted. TokenID is the historical api_tokens.id, not a credential. Origin and
// archiving-owner identity are snapshots, not references: either user may no longer exist, and
// users.id values can be reused after deletion, so the ids must not be resolved against users.
type ArchivedServiceAPIToken struct {
	ID               int64
	TokenID          int64
	Name             *string
	CreatedAt        time.Time
	LastUsedAt       *time.Time
	RevokedAt        time.Time
	RevokedOnArchive bool

	OriginUserID    int64
	OriginUserEmail string
	OriginUserName  string

	ArchivedAt          time.Time
	ArchivedByUserID    int64
	ArchivedByUserEmail string
}

// archiveServiceAPITokensTx snapshots every service token owned by targetUserID (active or already
// revoked) into archived_service_api_tokens, recording who originally owned it and which owner
// deleted that user. For a token still active, the archive record gets revoked_at = now and
// revoked_on_archive = 1; the live row is never updated. token_hash is never copied, and the
// api_tokens rows themselves cascade-delete with the user in the caller's transaction, so the
// secret stops authenticating atomically with the deletion.
// Runs within the caller's transaction; must be called before the user row is deleted.
func archiveServiceAPITokensTx(ctx context.Context, tx *sql.Tx, targetUserID, requesterID int64, now time.Time) error {
	nowMs := now.UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO archived_service_api_tokens(
  token_id, name, created_at, last_used_at, revoked_at, revoked_on_archive,
  origin_user_id, origin_user_email, origin_user_name,
  archived_at, archived_by_user_id, archived_by_user_email
)
SELECT t.id, t.name, t.created_at, t.last_used_at, COALESCE(t.revoked_at, ?), t.revoked_at IS NULL,
       o.id, o.email, o.name,
       ?, a.id, a.email
FROM api_tokens t
JOIN users o ON o.id = t.user_id
JOIN users a ON a.id = ?
WHERE t.user_id = ? AND t.is_service = 1
ORDER BY t.id
`, nowMs, nowMs, requesterID, targetUserID); err != nil {
		return fmt.Errorf("archive service api tokens: %w", err)
	}
	return nil
}

// ListArchivedServiceAPITokens returns archived service-token records, newest archive first, one
// page at a time. beforeID (0 for the first page) is the exclusive cursor; nextBeforeID is nil when
// there are no further records. Requires owner role.
func (s *Store) ListArchivedServiceAPITokens(ctx context.Context, requesterID int64, limit int, beforeID int64) (items []ArchivedServiceAPIToken, nextBeforeID *int64, err error) {
	if limit < 1 || limit > MaxArchivedServiceAPITokensPageSize {
		return nil, nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrValidation, MaxArchivedServiceAPITokensPageSize)
	}
	if beforeID < 0 {
		return nil, nil, fmt.Errorf("%w: invalid cursor", ErrValidation)
	}
	if err := s.requireOwner(ctx, requesterID); err != nil {
		return nil, nil, err
	}

	query := `
SELECT id, token_id, name, created_at, last_used_at, revoked_at, revoked_on_archive,
       origin_user_id, origin_user_email, origin_user_name,
       archived_at, archived_by_user_id, archived_by_user_email
FROM archived_service_api_tokens`
	args := []any{}
	if beforeID > 0 {
		query += ` WHERE id < ?`
		args = append(args, beforeID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list archived service api tokens: %w", err)
	}
	defer rows.Close()

	items = []ArchivedServiceAPIToken{}
	for rows.Next() {
		var (
			item        ArchivedServiceAPIToken
			name        sql.NullString
			createdAtMs int64
			lastUsedMs  sql.NullInt64
			revokedMs   int64
			archivedMs  int64
		)
		if err := rows.Scan(
			&item.ID, &item.TokenID, &name, &createdAtMs, &lastUsedMs, &revokedMs, &item.RevokedOnArchive,
			&item.OriginUserID, &item.OriginUserEmail, &item.OriginUserName,
			&archivedMs, &item.ArchivedByUserID, &item.ArchivedByUserEmail,
		); err != nil {
			return nil, nil, fmt.Errorf("scan archived service api token: %w", err)
		}
		if name.Valid {
			n := name.String
			item.Name = &n
		}
		item.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		if lastUsedMs.Valid {
			t := time.UnixMilli(lastUsedMs.Int64).UTC()
			item.LastUsedAt = &t
		}
		item.RevokedAt = time.UnixMilli(revokedMs).UTC()
		item.ArchivedAt = time.UnixMilli(archivedMs).UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate archived service api tokens: %w", err)
	}

	if len(items) > limit {
		items = items[:limit]
		next := items[limit-1].ID
		nextBeforeID = &next
	}
	return items, nextBeforeID, nil
}

// PurgeArchivedServiceAPITokens permanently deletes archived service-token records archived strictly
// before archivedBefore and returns how many were removed. This is the retention control for the
// archive; records are otherwise immutable. Requires owner role.
func (s *Store) PurgeArchivedServiceAPITokens(ctx context.Context, requesterID int64, archivedBefore time.Time) (int64, error) {
	if archivedBefore.IsZero() {
		return 0, fmt.Errorf("%w: archivedBefore is required", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin purge archived service api tokens tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := requireOwnerTx(ctx, tx, requesterID); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM archived_service_api_tokens WHERE archived_at < ?`, archivedBefore.UTC().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("purge archived service api tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge archived service api tokens rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit purge archived service api tokens tx: %w", err)
	}
	return n, nil
}
