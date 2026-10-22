package store

import (
	"context"
	"database/sql"
	"fmt"
)

// ReapStaleImportBatches deletes Replace-import staging projects (import_batch_id set) whose
// staging began before startedBeforeMs, and returns their asset files for post-commit removal.
//
// Staging rows keep the exported created_at but get last_activity_at = now when staged
// (insertProjectWithBatchID), so last_activity_at is the staging time. The janitor passes the
// process start time: a batch staged before this process started cannot belong to a running
// import, because imports do not survive restarts.
func (s *Store) ReapStaleImportBatches(ctx context.Context, startedBeforeMs int64) (int64, []AssetFileRef, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, nil, fmt.Errorf("begin reap import batches: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id FROM projects WHERE import_batch_id IS NOT NULL AND last_activity_at < ?`, startedBeforeMs)
	if err != nil {
		return 0, nil, fmt.Errorf("list stale import batches: %w", err)
	}
	idSet := map[int64]struct{}{}
	if err := collectIDs(rows, idSet); err != nil {
		return 0, nil, fmt.Errorf("list stale import batches: %w", err)
	}
	if len(idSet) == 0 {
		return 0, nil, nil
	}
	ids := make([]int64, 0, len(idSet))
	args := make([]any, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
		args = append(args, id)
	}
	refs, err := listProjectAssetUUIDsTx(ctx, tx, ids)
	if err != nil {
		return 0, nil, err
	}
	var n int64
	for start := 0; start < len(args); start += sqlInChunk {
		chunk := args[start:min(start+sqlInChunk, len(args))]
		res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE import_batch_id IS NOT NULL AND id IN (`+sqlPlaceholders(len(chunk))+`)`, chunk...)
		if err != nil {
			return 0, nil, fmt.Errorf("delete stale import batches: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, nil, fmt.Errorf("delete stale import batches: %w", err)
		}
		n += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("commit reap import batches: %w", err)
	}
	return n, refs, nil
}
