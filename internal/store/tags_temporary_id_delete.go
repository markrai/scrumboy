package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) DeleteTagForTemporaryProjectByID(ctx context.Context, projectID, userID, tagID int64, isAnonymousBoard bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var expiresAt, creatorUserID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT expires_at, creator_user_id FROM projects WHERE id = ?`, projectID).Scan(&expiresAt, &creatorUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: project not found", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get project: %w", err)
	}
	if !expiresAt.Valid || expiresAt.Int64 <= time.Now().UTC().UnixMilli() {
		return fmt.Errorf("%w: temporary project unavailable", ErrNotFound)
	}
	if isAnonymousBoard && creatorUserID.Valid {
		return fmt.Errorf("%w: project is not anonymous", ErrUnauthorized)
	}

	var tagUserID, tagProjectID sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT user_id, project_id FROM tags WHERE id = ?`, tagID).Scan(&tagUserID, &tagProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get tag: %w", err)
	}

	if tagUserID.Valid {
		if tagUserID.Int64 != userID {
			return fmt.Errorf("%w: tag not owned by user", ErrUnauthorized)
		}
		var linked int
		err = tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todo_tags tt
JOIN todos t ON t.id = tt.todo_id
WHERE tt.tag_id = ? AND t.project_id = ?`, tagID, projectID).Scan(&linked)
		if err != nil {
			return fmt.Errorf("check tag on project: %w", err)
		}
		if linked == 0 {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
	} else if tagProjectID.Valid {
		if tagProjectID.Int64 != projectID {
			return fmt.Errorf("%w: tag not found", ErrNotFound)
		}
		if !isAnonymousBoard {
			if userID == 0 {
				return fmt.Errorf("%w: unauthorized", ErrUnauthorized)
			}
			role, err := s.getProjectRoleTx(ctx, tx, projectID, userID)
			if err != nil || !role.HasMinimumRole(RoleMaintainer) {
				return fmt.Errorf("%w: project maintainer required", ErrUnauthorized)
			}
		}
	} else {
		return fmt.Errorf("%w: tag has neither user_id nor project_id", ErrConflict)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, tagID); err != nil {
		return fmt.Errorf("delete todo_tags: %w", err)
	}
	var result sql.Result
	if tagUserID.Valid {
		result, err = tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND user_id = ?`, tagID, userID)
	} else {
		result, err = tx.ExecContext(ctx, `DELETE FROM tags WHERE id = ? AND project_id = ?`, tagID, projectID)
	}
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted tags: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: tag not found", ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete: %w", err)
	}
	return nil
}
