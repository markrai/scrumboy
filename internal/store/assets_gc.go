package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"scrumboy/internal/assetrefs"
)

// GCOptions bounds one CollectGarbageAssets run.
type GCOptions struct {
	// Retention is how long an asset must stay unreferenced before it may be deleted.
	Retention time.Duration
	// Limit caps the number of candidates considered in one run.
	Limit int
	// BodyBudget caps the note bodies examined by the canonical re-check per run. A candidate in
	// progress always completes; remaining candidates wait for the next run.
	BodyBudget int
}

// GCResult reports one CollectGarbageAssets run.
type GCResult struct {
	// Deleted lists files whose rows were deleted by the committed transaction.
	Deleted []AssetFileRef
	// Repaired counts missing references restored for canonically embedded assets.
	Repaired int
	// Considered counts candidates evaluated.
	Considered int
}

// gcBeforeDeleteHook lets tests interleave a write between a candidate's re-check and its
// conditional delete (same transaction, so it models a save that committed first).
var gcBeforeDeleteHook func(ctx context.Context, tx *sql.Tx, assetID int64)

type gcCandidate struct {
	id        int64
	projectID int64
	uuid      string
}

// CollectGarbageAssets deletes asset rows that have been unreferenced for at least
// opts.Retention and that no story in their project embeds canonically. A candidate that is
// canonically embedded but lacks its todo_assets row (for example after an older binary edited the
// note) has the row repaired instead. Mentions in prose, code or links never extend retention.
// Deleted file refs are returned only after the transaction commits.
func (s *Store) CollectGarbageAssets(ctx context.Context, nowMs int64, opts GCOptions) (GCResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	if opts.BodyBudget <= 0 {
		opts.BodyBudget = 2000
	}
	cutoff := nowMs - opts.Retention.Milliseconds()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return GCResult{}, fmt.Errorf("begin asset gc: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, uuid FROM project_assets
WHERE unreferenced_at IS NOT NULL AND unreferenced_at < ?
ORDER BY unreferenced_at, id
LIMIT ?`, cutoff, opts.Limit)
	if err != nil {
		return GCResult{}, fmt.Errorf("list gc candidates: %w", err)
	}
	var cands []gcCandidate
	for rows.Next() {
		var c gcCandidate
		if err := rows.Scan(&c.id, &c.projectID, &c.uuid); err != nil {
			_ = rows.Close()
			return GCResult{}, fmt.Errorf("scan gc candidate: %w", err)
		}
		cands = append(cands, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return GCResult{}, fmt.Errorf("iterate gc candidates: %w", err)
	}
	_ = rows.Close()

	var result GCResult
	var deleted []AssetFileRef
	examined := 0
	for _, c := range cands {
		if examined >= opts.BodyBudget {
			break
		}
		result.Considered++
		repairedTodo, n, err := repairCanonicalEmbedTx(ctx, tx, c, nowMs)
		examined += n
		if err != nil {
			return GCResult{}, err
		}
		if repairedTodo {
			result.Repaired++
			continue
		}
		if gcBeforeDeleteHook != nil {
			gcBeforeDeleteHook(ctx, tx, c.id)
		}
		var ref AssetFileRef
		err = tx.QueryRowContext(ctx, `
DELETE FROM project_assets
WHERE id = ? AND unreferenced_at IS NOT NULL AND unreferenced_at < ?
  AND NOT EXISTS (SELECT 1 FROM todo_assets WHERE asset_id = project_assets.id)
RETURNING uuid, mime_type`, c.id, cutoff).Scan(&ref.UUID, &ref.MimeType)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return GCResult{}, fmt.Errorf("delete gc candidate: %w", err)
		}
		deleted = append(deleted, ref)
	}
	if err := tx.Commit(); err != nil {
		return GCResult{}, fmt.Errorf("commit asset gc: %w", err)
	}
	result.Deleted = deleted
	return result, nil
}

// repairCanonicalEmbedTx re-checks every note in the candidate's project that mentions the asset
// path. If any canonically embeds it, that note's references are re-synced (restoring the missing
// row). It returns whether a repair happened and how many bodies were examined.
func repairCanonicalEmbedTx(ctx context.Context, tx *sql.Tx, c gcCandidate, nowMs int64) (bool, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, body FROM todos WHERE project_id = ? AND instr(body, ?) > 0 ORDER BY id`,
		c.projectID, assetrefs.CanonicalPathPrefix+c.uuid)
	if err != nil {
		return false, 0, fmt.Errorf("gc re-check: %w", err)
	}
	type note struct {
		id   int64
		body string
	}
	var notes []note
	for rows.Next() {
		var n note
		if err := rows.Scan(&n.id, &n.body); err != nil {
			_ = rows.Close()
			return false, 0, fmt.Errorf("gc re-check scan: %w", err)
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, 0, fmt.Errorf("gc re-check: %w", err)
	}
	_ = rows.Close()
	repaired := false
	for _, n := range notes {
		if !slices.Contains(assetrefs.Extract(n.body), c.uuid) {
			continue
		}
		if err := syncTodoAssetsTx(ctx, tx, c.projectID, n.id, n.body, nowMs); err != nil {
			return false, len(notes), err
		}
		repaired = true
	}
	return repaired, len(notes), nil
}

// ReconcileTodoAssets re-syncs todo_assets for up to limit stories with id > afterTodoID, in one
// transaction. It repairs both missing and extra rows (for example after an older binary edited
// notes). It returns the cursor for the next batch, the number of rows changed, and whether the
// pass reached the end.
func (s *Store) ReconcileTodoAssets(ctx context.Context, afterTodoID int64, limit int) (int64, int, bool, error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return afterTodoID, 0, false, fmt.Errorf("begin reconcile: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id, project_id, body FROM todos WHERE id > ? ORDER BY id LIMIT ?`, afterTodoID, limit)
	if err != nil {
		return afterTodoID, 0, false, fmt.Errorf("reconcile list: %w", err)
	}
	type item struct {
		id, projectID int64
		body          string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.projectID, &it.body); err != nil {
			_ = rows.Close()
			return afterTodoID, 0, false, fmt.Errorf("reconcile scan: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return afterTodoID, 0, false, fmt.Errorf("reconcile list: %w", err)
	}
	_ = rows.Close()
	if len(items) == 0 {
		return afterTodoID, 0, true, nil
	}
	var before int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_assets WHERE todo_id > ? AND todo_id <= ?`, afterTodoID, items[len(items)-1].id).Scan(&before); err != nil {
		return afterTodoID, 0, false, fmt.Errorf("reconcile count: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	changed := 0
	for _, it := range items {
		have, err := todoAssetIDsTx(ctx, tx, it.id)
		if err != nil {
			return afterTodoID, 0, false, fmt.Errorf("reconcile: %w", err)
		}
		if err := syncTodoAssetsTx(ctx, tx, it.projectID, it.id, it.body, nowMs); err != nil {
			return afterTodoID, 0, false, err
		}
		after, err := todoAssetIDsTx(ctx, tx, it.id)
		if err != nil {
			return afterTodoID, 0, false, fmt.Errorf("reconcile: %w", err)
		}
		changed += symmetricDiff(have, after)
	}
	if err := tx.Commit(); err != nil {
		return afterTodoID, 0, false, fmt.Errorf("commit reconcile: %w", err)
	}
	return items[len(items)-1].id, changed, len(items) < limit, nil
}

func symmetricDiff(a, b map[int64]struct{}) int {
	n := 0
	for id := range a {
		if _, ok := b[id]; !ok {
			n++
		}
	}
	for id := range b {
		if _, ok := a[id]; !ok {
			n++
		}
	}
	return n
}

// ExistingAssetUUIDs returns the subset of uuids that have a project_assets row.
func (s *Store) ExistingAssetUUIDs(ctx context.Context, uuids []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(uuids))
	for start := 0; start < len(uuids); start += sqlInChunk {
		chunk := uuids[start:min(start+sqlInChunk, len(uuids))]
		args := make([]any, len(chunk))
		for i, u := range chunk {
			args[i] = u
		}
		rows, err := s.db.QueryContext(ctx, `SELECT uuid FROM project_assets WHERE uuid IN (`+sqlPlaceholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("existing asset uuids: %w", err)
		}
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("existing asset uuids: %w", err)
			}
			out[u] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("existing asset uuids: %w", err)
		}
		_ = rows.Close()
	}
	return out, nil
}

// HasAnyAssets reports whether any asset row exists.
func (s *Store) HasAnyAssets(ctx context.Context) (bool, error) {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_assets)`).Scan(&ok); err != nil {
		return false, fmt.Errorf("has any assets: %w", err)
	}
	return ok, nil
}
