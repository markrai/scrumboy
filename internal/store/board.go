package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// todoWithLaneTotal carries a Todo and its lane's total count (from window function).
type todoWithLaneTotal struct {
	Todo      Todo
	LaneTotal int
}

// flushLane writes the first limitPerLane items to cols[key], and meta for hasMore/cursor/totalCount.
func flushLane(key string, page []Todo, laneTotal, limitPerLane int, cols map[string][]Todo, meta map[string]LaneMeta) {
	hasMore := len(page) > limitPerLane
	var items []Todo
	var nextCursor string
	if hasMore {
		items = page[:limitPerLane]
		last := items[len(items)-1]
		nextCursor = fmt.Sprintf("%d:%d", last.Rank, last.ID)
	} else {
		items = page
	}
	cols[key] = items
	meta[key] = LaneMeta{HasMore: hasMore, NextCursor: nextCursor, TotalCount: laneTotal}
}

// getBoardPagedPerLane is the fallback when totalTodos exceeds boardTodoSoftCap.
func (s *Store) getBoardPagedPerLane(ctx context.Context, pc *ProjectContext, projectID int64, workflow []WorkflowColumn, tagFilter, searchFilter string, sprintFilter SprintFilter, limitPerLane int, tags []TagCount) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, map[string]LaneMeta, error) {
	cols := make(map[string][]Todo, len(workflow))
	meta := make(map[string]LaneMeta, len(workflow))
	for _, col := range workflow {
		cols[col.Key] = []Todo{}
		meta[col.Key] = LaneMeta{}
		items, nextCursor, hasMore, err := s.ListTodosForBoardLane(ctx, projectID, col.Key, limitPerLane, math.MinInt64, 0, tagFilter, searchFilter, sprintFilter)
		if err != nil {
			return Project{}, nil, nil, nil, nil, err
		}
		total, err := s.CountTodosForBoardLane(ctx, projectID, col.Key, tagFilter, searchFilter, sprintFilter)
		if err != nil {
			return Project{}, nil, nil, nil, nil, err
		}
		cols[col.Key] = items
		meta[col.Key] = LaneMeta{HasMore: hasMore, NextCursor: nextCursor, TotalCount: total}
	}
	if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
		log.Printf("failed to update board activity for project %d: %v", projectID, err)
	}
	return pc.Project, tags, workflow, cols, meta, nil
}

// sprintFilterArgs returns the SQL condition and args for a SprintFilter.
// Used by listAllTodosForBoard, ListTodosForBoardLane, CountTodosForBoardLane.
// Callers must pass args in order: prefix (e.g. projectID), sprintArgs..., suffix (e.g. searchFilter×3)
// so the single optional ? in cond (when Mode=="sprint") lines up with sprintArgs.
func sprintFilterArgs(sf SprintFilter) (cond string, args []any) {
	switch sf.Mode {
	case "sprint":
		return " AND t.sprint_id = ?", []any{sf.SprintID}
	case "sprint_number":
		// Resolve project-local sprint number inline to avoid a separate pre-query.
		// The EXISTS clause keeps filtering scoped to the same project as t.project_id.
		return " AND EXISTS (SELECT 1 FROM sprints sp WHERE sp.id = t.sprint_id AND sp.project_id = t.project_id AND sp.number = ?)", []any{sf.SprintNumber}
	case "scheduled":
		return " AND t.sprint_id IS NOT NULL", nil
	case "unscheduled":
		return " AND t.sprint_id IS NULL", nil
	default:
		return "", nil
	}
}

// GetBoardPaged returns board with optional per-lane pagination. When limitPerLane > 0,
// runs 5 lane queries and returns columnsMeta for each status. Otherwise same as GetBoard.
// pc must be non-nil; use GetProjectContextBySlug or GetProjectContextForRead to obtain it.
func (s *Store) GetBoardPaged(ctx context.Context, pc *ProjectContext, tagFilter string, searchFilter string, sprintFilter SprintFilter, limitPerLane int) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, map[string]LaneMeta, error) {
	if limitPerLane <= 0 {
		project, tags, workflow, cols, err := s.GetBoard(ctx, pc, tagFilter, searchFilter, sprintFilter)
		return project, tags, workflow, cols, nil, err
	}

	projectID := pc.Project.ID
	var viewerUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		viewerUserID = &userID
	}

	tags, err := s.listTagCounts(ctx, projectID, viewerUserID, &pc.Role)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}
	workflow, err := s.GetProjectWorkflow(ctx, projectID)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}

	tagFilter = normalizeTagFilter(tagFilter)

	// Soft cap: if filtered count exceeds threshold, fall back to per-lane queries (bounded memory)
	const boardTodoSoftCap = 2000
	totalTodos, err := s.countTodosForBoard(ctx, projectID, tagFilter, searchFilter, sprintFilter)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}
	if totalTodos > boardTodoSoftCap {
		return s.getBoardPagedPerLane(ctx, pc, projectID, workflow, tagFilter, searchFilter, sprintFilter, limitPerLane, tags)
	}

	// Fast path: single window-function query returns rows + lane totals
	allTodos, err := s.listAllTodosForBoardWithCounts(ctx, projectID, tagFilter, searchFilter, sprintFilter)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}

	cols := make(map[string][]Todo, len(workflow))
	meta := make(map[string]LaneMeta, len(workflow))
	for _, col := range workflow {
		cols[col.Key] = []Todo{}
		meta[col.Key] = LaneMeta{}
	}

	// Partition into lanes; results are ordered by column_key, rank, id
	currentKey := ""
	var page []Todo
	var laneTotal int
	for _, tw := range allTodos {
		if tw.Todo.ColumnKey != currentKey {
			if currentKey != "" {
				flushLane(currentKey, page, laneTotal, limitPerLane, cols, meta)
			}
			currentKey = tw.Todo.ColumnKey
			page = nil
			laneTotal = tw.LaneTotal
		}
		page = append(page, tw.Todo)
	}
	if currentKey != "" {
		flushLane(currentKey, page, laneTotal, limitPerLane, cols, meta)
	}

	if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
		log.Printf("failed to update board activity for project %d: %v", projectID, err)
	}

	return pc.Project, tags, workflow, cols, meta, nil
}

// GetBoard returns full board (all todos, no pagination).
// pc must be non-nil; use GetProjectContextBySlug or GetProjectContextForRead to obtain it.
func (s *Store) GetBoard(ctx context.Context, pc *ProjectContext, tagFilter string, searchFilter string, sprintFilter SprintFilter) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, error) {
	projectID := pc.Project.ID
	var viewerUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		viewerUserID = &userID
	}

	tags, err := s.listTagCounts(ctx, projectID, viewerUserID, &pc.Role)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}
	workflow, err := s.GetProjectWorkflow(ctx, projectID)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}

	tagFilter = normalizeTagFilter(tagFilter)

	cols := make(map[string][]Todo, len(workflow))
	for _, col := range workflow {
		cols[col.Key] = []Todo{}
	}

	// OPTIMIZED: Fetch all todos in a single query instead of 5 separate queries (one per status)
	// This reduces query overhead and is more efficient on low-power hardware
	todos, err := s.listAllTodosForBoard(ctx, projectID, tagFilter, searchFilter, sprintFilter)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}

	// Group todos by status in Go
	for _, todo := range todos {
		cols[todo.ColumnKey] = append(cols[todo.ColumnKey], todo)
	}

	// Track activity: any board access (read) resets expiration
	// Best-effort: log errors but don't fail the request
	if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
		log.Printf("failed to update board activity for project %d: %v", projectID, err)
	}

	return pc.Project, tags, workflow, cols, nil
}

// listAllTodosForBoard fetches all todos for a board in a single query
// OPTIMIZED: Single query instead of 5 separate queries (one per status)
func (s *Store) listAllTodosForBoard(ctx context.Context, projectID int64, tagFilter string, searchFilter string, sprintFilter SprintFilter) ([]Todo, error) {
	// Show ALL tags on todos (no user filter - collaboration-friendly)
	// Tag filter matches by name across all owners

	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)

	var rows *sql.Rows
	var err error

	if tagFilter == "" {
		// No tag filter - simple query without CTE
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
WHERE
  t.project_id = ?
  `+sprintCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.column_key ASC, t.rank ASC, t.id ASC
`,
			args...,
		)
	} else {
		// Tag filter active - use CTE to pre-filter (avoids correlated subquery)
		// Placeholder order: CTE g.name=? (1), main project_id=? (2), sprintCond (0–1), search ?,?,? (3).
		args := []any{tagFilter, projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ?
  `+sprintCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.column_key ASC, t.rank ASC, t.id ASC
`,
			args...,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	var out []Todo
	var todoIDs []int64
	for rows.Next() {
		var t Todo
		var columnKey string
		var createdAtMs, updatedAtMs int64
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var assigneeUserID sql.NullInt64
		var sprintID sql.NullInt64
		var doneAtMs sql.NullInt64
		if err := rows.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &columnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		if !localID.Valid {
			return nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = columnKey
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &dt
		}
		todoIDs = append(todoIDs, t.ID)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}

	tagMap, err := s.listTagsForTodos(ctx, todoIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Tags = tagMap[out[i].ID]
	}
	return out, nil
}

// countTodosForBoard returns the count of todos matching board filters (tag, search, sprint).
// Used for soft cap check; must mirror filters used by listAllTodosForBoardWithCounts.
func (s *Store) countTodosForBoard(ctx context.Context, projectID int64, tagFilter, searchFilter string, sprintFilter SprintFilter) (int, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	var count int
	if tagFilter == "" {
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos t
WHERE t.project_id = ?
`+sprintCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`, args...).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos: %w", err)
		}
	} else {
		args := []any{tagFilter, projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT COUNT(*) FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ?
`+sprintCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`, args...).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos: %w", err)
		}
	}
	return count, nil
}

// listAllTodosForBoardWithCounts fetches all todos with per-lane totals via window function.
// Each row carries lane_total; no separate count query needed.
func (s *Store) listAllTodosForBoardWithCounts(ctx context.Context, projectID int64, tagFilter, searchFilter string, sprintFilter SprintFilter) ([]todoWithLaneTotal, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)

	var rows *sql.Rows
	var err error

	if tagFilter == "" {
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
  COUNT(*) OVER (PARTITION BY t.column_key) AS lane_total
FROM todos t
WHERE
  t.project_id = ?
`+sprintCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.column_key ASC, t.rank ASC, t.id ASC
`, args...)
	} else {
		args := []any{tagFilter, projectID}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
  COUNT(*) OVER (PARTITION BY t.column_key) AS lane_total
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ?
`+sprintCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.column_key ASC, t.rank ASC, t.id ASC
`, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("list todos with counts: %w", err)
	}
	defer rows.Close()

	var out []todoWithLaneTotal
	var todoIDs []int64
	for rows.Next() {
		var t Todo
		var columnKey string
		var createdAtMs, updatedAtMs int64
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var assigneeUserID sql.NullInt64
		var sprintID sql.NullInt64
		var doneAtMs sql.NullInt64
		var laneTotal int
		if err := rows.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &columnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs, &laneTotal); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		if !localID.Valid {
			return nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = columnKey
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &dt
		}
		todoIDs = append(todoIDs, t.ID)
		out = append(out, todoWithLaneTotal{Todo: t, LaneTotal: laneTotal})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}

	tagMap, err := s.listTagsForTodos(ctx, todoIDs)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Todo.Tags = tagMap[out[i].Todo.ID]
	}
	return out, nil
}

// ListTodosForBoardLane returns todos for one status with cursor-based pagination.
// Cursor format "rank:id" uses DB id (not localId). Returns (items, nextCursor, hasMore).
// nextCursor is empty when hasMore is false.
func (s *Store) ListTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, limit int, afterRank, afterID int64, tagFilter, searchFilter string, sprintFilter SprintFilter) ([]Todo, string, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)

	var rows *sql.Rows
	var err error

	if tagFilter == "" {
		args := []any{projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, afterRank, afterID, searchFilter, searchFilter, searchFilter, fetchLimit)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
WHERE
  t.project_id = ? AND t.column_key = ?
  `+sprintCond+`
  AND (t.rank, t.id) > (?, ?)
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.rank ASC, t.id ASC
LIMIT ?
`,
			args...,
		)
	} else {
		// Placeholder order: CTE g.name=? (1), main project_id=? (2), status=? (3), sprintCond (0–1), (rank,id) (4,5), search ?,?,? (6), LIMIT ? (7).
		args := []any{tagFilter, projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, afterRank, afterID, searchFilter, searchFilter, searchFilter, fetchLimit)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ? AND t.column_key = ?
  `+sprintCond+`
  AND (t.rank, t.id) > (?, ?)
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY t.rank ASC, t.id ASC
LIMIT ?
`,
			args...,
		)
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("list todos lane: %w", err)
	}
	defer rows.Close()

	var out []Todo
	var todoIDs []int64
	var lastRank, lastID int64
	for rows.Next() {
		var t Todo
		var rowColumnKey string
		var createdAtMs, updatedAtMs int64
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var assigneeUserID sql.NullInt64
		var sprintID sql.NullInt64
		var doneAtMs sql.NullInt64
		if err := rows.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &rowColumnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs); err != nil {
			return nil, "", false, fmt.Errorf("scan todo: %w", err)
		}
		if !localID.Valid {
			return nil, "", false, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = rowColumnKey
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &dt
		}
		todoIDs = append(todoIDs, t.ID)
		lastRank, lastID = t.Rank, t.ID
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", false, fmt.Errorf("rows todos: %w", err)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
		todoIDs = todoIDs[:limit]
	}
	tagMap, err := s.listTagsForTodos(ctx, todoIDs)
	if err != nil {
		return nil, "", false, err
	}
	for i := range out {
		out[i].Tags = tagMap[out[i].ID]
	}
	if hasMore {
		return out, fmt.Sprintf("%d:%d", lastRank, lastID), true, nil
	}
	return out, "", false, nil
}

// CountTodosForBoardLane returns the total number of todos in the lane (same tag/search filters as ListTodosForBoardLane).
func (s *Store) CountTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, tagFilter string, searchFilter string, sprintFilter SprintFilter) (int, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)

	var count int
	if tagFilter == "" {
		args := []any{projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos t
WHERE t.project_id = ? AND t.column_key = ?
`+sprintCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`,
			args...,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos lane: %w", err)
		}
	} else {
		// Placeholder order: CTE g.name=? (1), main project_id=? (2), status=? (3), sprintCond (0–1), search ?,?,? (4).
		args := []any{tagFilter, projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT COUNT(*) FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ? AND t.column_key = ?
`+sprintCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`,
			args...,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos lane: %w", err)
		}
	}
	return count, nil
}

// CountTodosByColumnKey returns unfiltered todo counts per column_key for a project
// (same notion as DeleteWorkflowColumn: all todos in that lane, no tag/search/sprint filter).
// Column keys with zero todos are omitted from the map; callers treat missing keys as 0.
// The query is satisfied by existing indexes with leading project_id and column_key
// (e.g. idx_todos_project_column_key_rank_id from migration 038).
func (s *Store) CountTodosByColumnKey(ctx context.Context, projectID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT column_key, COUNT(*) FROM todos WHERE project_id = ? GROUP BY column_key
`, projectID)
	if err != nil {
		return nil, fmt.Errorf("count todos by column key: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("scan count todos by column key: %w", err)
		}
		out[key] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows count todos by column key: %w", err)
	}
	return out, nil
}

// ParseLaneCursor parses "rank:id" cursor. Returns (0, 0) for empty or invalid.
func ParseLaneCursor(cursor string) (rank, id int64) {
	if cursor == "" {
		return 0, 0
	}
	parts := strings.SplitN(cursor, ":", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	r, err1 := strconv.ParseInt(parts[0], 10, 64)
	i, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0
	}
	return r, i
}

// listTodosByStatus is deprecated but kept for backward compatibility if needed elsewhere
// Use listAllTodosForBoard instead for better performance
func (s *Store) listTodosByColumnKey(ctx context.Context, projectID int64, columnKey string, tagFilter string, searchFilter string) ([]Todo, error) {
	// Show ALL tags on todos (no user filter - collaboration-friendly)
	// Tag filter matches by name across all owners
	// OPTIMIZED: Pre-filter tagged todos with CTE instead of correlated EXISTS subquery
	// This turns N subquery executions into ONE scan of todo_tags + tags
	rows, err := s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  JOIN tags g ON g.id = tt.tag_id
  WHERE g.name = ?
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
  COALESCE(GROUP_CONCAT(DISTINCT g.name, ','), '') AS tags_csv
FROM todos t
LEFT JOIN todo_tags tt ON tt.todo_id = t.id
LEFT JOIN tags g ON g.id = tt.tag_id
LEFT JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ? AND t.column_key = ?
  AND (? = '' OR ft.todo_id IS NOT NULL)
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
GROUP BY t.id
ORDER BY t.rank ASC, t.id ASC
`,
		tagFilter, projectID, columnKey, tagFilter, searchFilter, searchFilter, searchFilter,
	)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	var out []Todo
	for rows.Next() {
		var t Todo
		var rowColumnKey string
		var createdAtMs, updatedAtMs int64
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var assigneeUserID sql.NullInt64
		var sprintID sql.NullInt64
		var doneAtMs sql.NullInt64
		var tagsCSV string
		if err := rows.Scan(&t.ID, &t.ProjectID, &localID, &t.Title, &t.Body, &rowColumnKey, &t.Rank, &estimationPoints, &assigneeUserID, &sprintID, &createdAtMs, &updatedAtMs, &doneAtMs, &tagsCSV); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		if !localID.Valid {
			return nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = rowColumnKey
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if assigneeUserID.Valid {
			v := assigneeUserID.Int64
			t.AssigneeUserID = &v
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		t.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.DoneAt = &dt
		}

		if tagsCSV != "" {
			// Tags are already distinct (GROUP_CONCAT with DISTINCT) and sorted by SQLite
			// No need for deduplication in Go
			t.Tags = strings.Split(tagsCSV, ",")
			sort.Strings(t.Tags) // Still sort for consistency
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}
	return out, nil
}
