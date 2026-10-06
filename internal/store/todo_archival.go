package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	ReasonTodoArchived = "todo_archived"
	// MaxTodoArchiveBatch is the authoritative upper bound on a single archive or
	// restore batch. Transports must derive their own validation and advertised
	// schema limits from this constant rather than repeating the literal.
	MaxTodoArchiveBatch = 500
)

func todoArchivedError() error {
	return &reasonedStoreError{base: ErrConflict, reason: ReasonTodoArchived, msg: "conflict: todo is archived; restore it before modifying"}
}

type TodoArchiveBatchResult struct {
	TargetState          string     `json:"targetState"`
	RequestedCount       int        `json:"requestedCount"`
	TransitionedCount    int        `json:"transitionedCount"`
	UnchangedCount       int        `json:"unchangedCount"`
	TransitionedLocalIDs []int64    `json:"transitionedLocalIds"`
	UnchangedLocalIDs    []int64    `json:"unchangedLocalIds"`
	TransitionedAt       *time.Time `json:"transitionedAt"`
}

func validateArchiveIDs(ids []int64) error {
	if len(ids) < 1 || len(ids) > MaxTodoArchiveBatch {
		return fmt.Errorf("%w: localIds must contain between 1 and %d items", ErrValidation, MaxTodoArchiveBatch)
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("%w: localIds must be positive", ErrValidation)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("%w: localIds must be unique", ErrValidation)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (s *Store) ArchiveTodoByLocalID(ctx context.Context, projectID, localID int64, mode Mode) (TodoArchiveBatchResult, error) {
	return s.ArchiveTodosByLocalID(ctx, projectID, []int64{localID}, mode)
}

func (s *Store) RestoreTodoByLocalID(ctx context.Context, projectID, localID int64, mode Mode) (TodoArchiveBatchResult, error) {
	return s.RestoreTodosByLocalID(ctx, projectID, []int64{localID}, mode)
}

// ArchiveTodosByLocalID archives 1..MaxTodoArchiveBatch stories atomically. Archival is
// orthogonal to workflow state: column_key, rank, done_at, the story's updated_at, tags,
// links, sprint, priority and assignment are all left exactly as they were.
func (s *Store) ArchiveTodosByLocalID(ctx context.Context, projectID int64, ids []int64, mode Mode) (TodoArchiveBatchResult, error) {
	return s.archiveTodosByLocalID(ctx, projectID, ids, mode, true)
}

// RestoreTodosByLocalID clears archival on 1..MaxTodoArchiveBatch stories atomically and
// rewrites no history.
//
// Rank note: restore returns a story with the rank it was archived at. Lane rebalancing
// (rebalanceColumn) renumbers only active rows, so a rebalance that happens while a story
// is archived can leave its preserved rank away from its original slot, or tied with an
// active row. That is accepted: ranks are preserved rather than recomputed, and lane
// ordering stays total because lanes sort by (rank, id). See
// TestRestoreAfterRebalancePreservesStoredRank.
func (s *Store) RestoreTodosByLocalID(ctx context.Context, projectID int64, ids []int64, mode Mode) (TodoArchiveBatchResult, error) {
	return s.archiveTodosByLocalID(ctx, projectID, ids, mode, false)
}

func (s *Store) archiveTodosByLocalID(ctx context.Context, projectID int64, ids []int64, mode Mode, archive bool) (TodoArchiveBatchResult, error) {
	state := "restored"
	if archive {
		state = "archived"
	}
	result := TodoArchiveBatchResult{TargetState: state, RequestedCount: len(ids)}
	if err := validateArchiveIDs(ids); err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return result, fmt.Errorf("begin todo archival: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return result, err
	}
	// Reuse the ordinary todo write boundary (including temporary-board expiry
	// and capability semantics), then apply archival's stricter durable-project
	// maintainer rule.
	p, err := s.getProjectForWriteTx(ctx, tx, projectID, mode)
	if err != nil {
		return result, err
	}
	if p.ExpiresAt == nil {
		enabled, e := authEnabledTx(ctx, tx)
		if e != nil {
			return result, e
		}
		if enabled {
			uid, ok := UserIDFromContext(ctx)
			if !ok {
				return result, ErrUnauthorized
			}
			role, e := s.getProjectRoleTx(ctx, tx, projectID, uid)
			if e != nil {
				return result, e
			}
			if !CanArchiveTodo(role) {
				return result, ErrUnauthorized
			}
		}
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, projectID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, local_id, column_key, archived_at FROM todos WHERE project_id = ? AND local_id IN ("+ph+")", args...)
	if err != nil {
		return result, fmt.Errorf("load todos for archival: %w", err)
	}
	type rowState struct {
		id, local int64
		col       string
		archived  sql.NullInt64
	}
	byLocal := make(map[int64]rowState, len(ids))
	for rows.Next() {
		var r rowState
		if err := rows.Scan(&r.id, &r.local, &r.col, &r.archived); err != nil {
			rows.Close()
			return result, err
		}
		byLocal[r.local] = r
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, err
	}
	rows.Close()
	if len(byLocal) != len(ids) {
		return result, ErrNotFound
	}
	var nowMs int64
	for _, local := range ids {
		r := byLocal[local]
		isArchived := r.archived.Valid
		if isArchived == archive {
			result.UnchangedLocalIDs = append(result.UnchangedLocalIDs, local)
			continue
		}
		if nowMs == 0 {
			nowMs = time.Now().UTC().UnixMilli()
		}
		var archiveValue any
		if archive {
			archiveValue = nowMs
		}
		res, e := tx.ExecContext(ctx, "UPDATE todos SET archived_at = ? WHERE id = ? AND archived_at "+map[bool]string{true: "IS NULL", false: "IS NOT NULL"}[archive], archiveValue, r.id)
		if e != nil {
			return result, fmt.Errorf("update todo archive state: %w", e)
		}
		n, e := res.RowsAffected()
		if e != nil {
			return result, e
		}
		if n == 0 {
			result.UnchangedLocalIDs = append(result.UnchangedLocalIDs, local)
			continue
		}
		result.TransitionedLocalIDs = append(result.TransitionedLocalIDs, local)
		before, after := any(nil), any(nowMs)
		if !archive {
			before, after = nowMs, nil
			if r.archived.Valid {
				before = r.archived.Int64
			}
		}
		meta := map[string]any{"local_id": local, "column_key": r.col, "before_archived_at": before, "after_archived_at": after}
		actor, _ := UserIDFromContext(ctx)
		var actorPtr *int64
		if actor != 0 {
			actorPtr = &actor
		}
		action := "todo_archived"
		if !archive {
			action = "todo_restored"
		}
		if e := insertAuditEventTx(ctx, tx, projectID, actorPtr, action, "todo", &r.id, meta); e != nil {
			return result, fmt.Errorf("audit %s: %w", action, e)
		}
	}
	result.TransitionedCount = len(result.TransitionedLocalIDs)
	result.UnchangedCount = len(result.UnchangedLocalIDs)
	if result.TransitionedCount > 0 {
		t := time.UnixMilli(nowMs).UTC()
		result.TransitionedAt = &t
		if err := touchProject(ctx, tx, projectID, nowMs); err != nil {
			return result, err
		}
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit todo archival: %w", err)
	}
	if result.TransitionedCount > 0 {
		_ = s.UpdateBoardActivity(ctx, projectID)
	}
	return result, nil
}

// ListArchivedTodos returns newest archived stories first. Cursor is archivedAtMs:globalTodoID.
func (s *Store) ListArchivedTodos(ctx context.Context, projectID int64, limit int, afterArchivedAtMs, afterID *int64, mode Mode) ([]Todo, string, bool, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if (afterArchivedAtMs == nil) != (afterID == nil) {
		return nil, "", false, fmt.Errorf("%w: archive cursor requires timestamp and id", ErrValidation)
	}
	if afterArchivedAtMs != nil && (*afterArchivedAtMs < 0 || *afterID <= 0) {
		return nil, "", false, fmt.Errorf("%w: invalid archive cursor", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", false, fmt.Errorf("begin archive page read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.getProjectForReadTx(ctx, tx, projectID, mode); err != nil {
		return nil, "", false, err
	}
	args := []any{projectID}
	cursor := ""
	if afterArchivedAtMs != nil && afterID != nil {
		cursor = " AND (archived_at < ? OR (archived_at = ? AND id < ?))"
		args = append(args, *afterArchivedAtMs, *afterArchivedAtMs, *afterID)
	}
	args = append(args, limit+1)
	rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, local_id, title, body, column_key, rank,
       estimation_points, assignee_user_id, created_by_user_id, sprint_id,
       priority_key, created_at, updated_at, done_at, archived_at
FROM todos
WHERE project_id = ? AND archived_at IS NOT NULL`+cursor+`
ORDER BY archived_at DESC, id DESC
LIMIT ?`, args...)
	if err != nil {
		return nil, "", false, err
	}
	out := make([]Todo, 0, limit+1)
	for rows.Next() {
		var t Todo
		var localID sql.NullInt64
		var estimationPoints, assigneeUserID, createdByUserID, sprintID sql.NullInt64
		var priorityKey sql.NullString
		var createdAtMs, updatedAtMs int64
		var doneAtMs, archivedAtMs sql.NullInt64
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &t.ColumnKey, &t.Rank,
			&estimationPoints, &assigneeUserID, &createdByUserID, &sprintID,
			&priorityKey, &createdAtMs, &updatedAtMs, &doneAtMs, &archivedAtMs,
		); err != nil {
			rows.Close()
			return nil, "", false, err
		}
		if !localID.Valid || !archivedAtMs.Valid {
			rows.Close()
			return nil, "", false, fmt.Errorf("%w: invalid archived todo projection", ErrConflict)
		}
		t.LocalID = localID.Int64
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		if createdByUserID.Valid {
			v := createdByUserID.Int64
			t.CreatedByUserID = &v
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		if priorityKey.Valid {
			v := priorityKey.String
			t.PriorityKey = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			v := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &v
		}
		v := time.UnixMilli(archivedAtMs.Int64).UTC()
		t.ArchivedAt = &v
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, "", false, err
	}
	rows.Close()
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	todoIDs := make([]int64, len(out))
	for i := range out {
		todoIDs[i] = out[i].ID
	}
	tagMap, err := listTagsForTodosQueryer(ctx, tx, todoIDs)
	if err != nil {
		return nil, "", false, err
	}
	for i := range out {
		out[i].Tags = tagMap[out[i].ID]
	}
	if err := tx.Commit(); err != nil {
		return nil, "", false, fmt.Errorf("commit archive page read: %w", err)
	}
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		return out, fmt.Sprintf("%d:%d", last.ArchivedAt.UnixMilli(), last.ID), true, nil
	}
	return out, "", false, nil
}

// ParseArchiveCursor parses the strict archivedAtMs:globalID format.
func ParseArchiveCursor(raw string) (int64, int64, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: invalid archive cursor", ErrValidation)
	}
	a, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || a < 0 {
		return 0, 0, fmt.Errorf("%w: invalid archive cursor", ErrValidation)
	}
	b, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil || b <= 0 {
		return 0, 0, fmt.Errorf("%w: invalid archive cursor", ErrValidation)
	}
	return a, b, nil
}
