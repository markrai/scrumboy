package store

import (
	"context"
	"database/sql"
	"fmt"

	"scrumboy/internal/assetrefs"
)

// syncTodoAssetsTx makes todo_assets for todoID equal to the canonical image references in body
// that resolve to assets of projectID. Unknown UUIDs and other projects' assets are ignored.
// It is diff-based, so references that are kept never flap unreferenced_at; the todo_assets
// triggers maintain unreferenced_at for added and removed rows. It must run inside the
// transaction that writes the body, after the body write and before commit.
func syncTodoAssetsTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64, body string, nowMs int64) error {
	want, err := resolveProjectAssetIDsTx(ctx, tx, projectID, assetrefs.Extract(body))
	if err != nil {
		return fmt.Errorf("sync todo assets: %w", err)
	}
	have, err := todoAssetIDsTx(ctx, tx, todoID)
	if err != nil {
		return fmt.Errorf("sync todo assets: %w", err)
	}
	var remove, add []int64
	for id := range have {
		if _, ok := want[id]; !ok {
			remove = append(remove, id)
		}
	}
	for id := range want {
		if _, ok := have[id]; !ok {
			add = append(add, id)
		}
	}
	for start := 0; start < len(remove); start += sqlInChunk {
		chunk := remove[start:min(start+sqlInChunk, len(remove))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, todoID)
		for _, id := range chunk {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_assets WHERE todo_id = ? AND asset_id IN (`+sqlPlaceholders(len(chunk))+`)`, args...); err != nil {
			return fmt.Errorf("sync todo assets: delete: %w", err)
		}
	}
	for _, id := range add {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO todo_assets(todo_id, asset_id, created_at) VALUES (?, ?, ?)`, todoID, id, nowMs); err != nil {
			return fmt.Errorf("sync todo assets: insert: %w", err)
		}
	}
	return nil
}

func resolveProjectAssetIDsTx(ctx context.Context, tx *sql.Tx, projectID int64, uuids []string) (map[int64]struct{}, error) {
	out := make(map[int64]struct{}, len(uuids))
	for start := 0; start < len(uuids); start += sqlInChunk {
		chunk := uuids[start:min(start+sqlInChunk, len(uuids))]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, projectID)
		for _, u := range chunk {
			args = append(args, u)
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM project_assets WHERE project_id = ? AND uuid IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		if err := collectIDs(rows, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func todoAssetIDsTx(ctx context.Context, tx *sql.Tx, todoID int64) (map[int64]struct{}, error) {
	rows, err := tx.QueryContext(ctx, `SELECT asset_id FROM todo_assets WHERE todo_id = ?`, todoID)
	if err != nil {
		return nil, err
	}
	out := map[int64]struct{}{}
	if err := collectIDs(rows, out); err != nil {
		return nil, err
	}
	return out, nil
}

func collectIDs(rows *sql.Rows, out map[int64]struct{}) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		out[id] = struct{}{}
	}
	return rows.Err()
}
