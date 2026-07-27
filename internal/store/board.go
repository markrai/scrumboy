package store

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

// todoWithLaneTotal carries a Todo and its lane's total count (from window function).
type todoWithLaneTotal struct {
	Todo      Todo
	LaneTotal int
}

const boardTodoSoftCap = 2000

// flushLane writes the first limitPerLane items to cols[key], and meta for hasMore/cursor/totalCount.
func flushLane(key string, page []Todo, laneTotal, limitPerLane int, cols map[string][]Todo, meta map[string]LaneMeta, sortOrder SortOrder) {
	hasMore := len(page) > limitPerLane
	var items []Todo
	var nextCursor string
	if hasMore {
		items = page[:limitPerLane]
		last := items[len(items)-1]
		nextCursor = laneCursor(last, sortOrder)
	} else {
		items = page
	}
	cols[key] = items
	meta[key] = LaneMeta{HasMore: hasMore, NextCursor: nextCursor, TotalCount: laneTotal}
}

// laneCursor encodes the pagination cursor for the last row of a page, using
// whichever fields the active sortOrder actually orders by so that
// ParseLaneCursor's generic "a:b" split lines up with the ORDER BY/predicate
// pair chosen by laneOrderBy/laneCursorPredicate below.
func laneCursor(t Todo, sortOrder SortOrder) string {
	switch sortOrder {
	case SortOrderNewest, SortOrderOldest:
		return fmt.Sprintf("%d:%d", t.CreatedAt.UnixMilli(), t.ID)
	default:
		return fmt.Sprintf("%d:%d", t.Rank, t.ID)
	}
}

// laneOrderBy returns the ORDER BY fragment (no leading "ORDER BY") for a
// single lane's todos under the given sortOrder. Default preserves the
// existing manual drag-rank order; newest/oldest order by created_at with id
// as a stable tiebreaker.
func laneOrderBy(sortOrder SortOrder) string {
	switch sortOrder {
	case SortOrderNewest:
		return "t.created_at DESC, t.id DESC"
	case SortOrderOldest:
		return "t.created_at ASC, t.id ASC"
	default:
		return "t.rank ASC, t.id ASC"
	}
}

// laneCursorPredicate returns the "AND (...) OP (?, ?)" fragment that
// continues pagination after the cursor's two values, matching laneOrderBy's
// ordering direction for the same sortOrder.
func laneCursorPredicate(sortOrder SortOrder) string {
	switch sortOrder {
	case SortOrderNewest:
		return " AND (t.created_at, t.id) < (?, ?)"
	case SortOrderOldest:
		return " AND (t.created_at, t.id) > (?, ?)"
	default:
		return " AND (t.rank, t.id) > (?, ?)"
	}
}

// laneCursorSentinel returns the (a, b) starting bound used when fetching a
// lane's first page (no real cursor yet), chosen so laneCursorPredicate's
// comparison is true for every row regardless of sortOrder's direction.
func laneCursorSentinel(sortOrder SortOrder) (a, b int64) {
	if sortOrder == SortOrderNewest {
		return math.MaxInt64, math.MaxInt64
	}
	return math.MinInt64, 0
}

// getBoardPagedPerLane is the fallback when totalTodos exceeds boardTodoSoftCap.
// tagFilter must already be resolved for this request (no per-lane re-scan).
func (s *Store) getBoardPagedPerLane(ctx context.Context, pc *ProjectContext, projectID int64, workflow []WorkflowColumn, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder, limitPerLane int, tags []TagCount) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, map[string]LaneMeta, error) {
	cols := make(map[string][]Todo, len(workflow))
	meta := make(map[string]LaneMeta, len(workflow))
	for _, col := range workflow {
		cols[col.Key] = []Todo{}
		meta[col.Key] = LaneMeta{}
		startA, startB := laneCursorSentinel(sortOrder)
		items, nextCursor, hasMore, err := s.listTodosForBoardLaneResolved(ctx, projectID, col.Key, limitPerLane, startA, startB, tagFilter, searchFilter, assigneeFilter, sprintFilter, sortOrder)
		if err != nil {
			return Project{}, nil, nil, nil, nil, err
		}
		total, err := s.countTodosForBoardLaneResolved(ctx, projectID, col.Key, tagFilter, searchFilter, assigneeFilter, sprintFilter)
		if err != nil {
			return Project{}, nil, nil, nil, nil, err
		}
		cols[col.Key] = items
		meta[col.Key] = LaneMeta{HasMore: hasMore, NextCursor: nextCursor, TotalCount: total}
	}
	// Expiring boards only: durable reads skip activity (no SELECT/UPDATE for last_activity_at / expiry).
	if pc.Project.ExpiresAt != nil {
		if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
			log.Printf("failed to update board activity for project %d: %v", projectID, err)
		}
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

// assigneeFilterArgs returns the SQL condition and args for a validated board
// assignee filter. Unknown internal states fail closed instead of widening the
// query. Same placement contract as sprintFilterArgs.
func assigneeFilterArgs(af AssigneeFilter) (cond string, args []any) {
	switch af.mode {
	case assigneeFilterNone:
		return "", nil
	case assigneeFilterUnassigned:
		return " AND t.assignee_user_id IS NULL", nil
	case assigneeFilterUser:
		if af.userID > 0 {
			return " AND t.assignee_user_id = ?", []any{af.userID}
		}
		return " AND 1 = 0", nil
	default:
		return " AND 1 = 0", nil
	}
}

// GetBoardPaged returns board with optional per-lane pagination. When limitPerLane > 0,
// runs 5 lane queries and returns columnsMeta for each status. Otherwise same as GetBoard.
// pc must be non-nil; use GetProjectContextBySlug or GetProjectContextForRead to obtain it.
func (s *Store) GetBoardPaged(ctx context.Context, pc *ProjectContext, tagFilter string, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder, limitPerLane int) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, map[string]LaneMeta, error) {
	if limitPerLane <= 0 {
		project, tags, workflow, cols, err := s.GetBoard(ctx, pc, tagFilter, searchFilter, assigneeFilter, sprintFilter, sortOrder)
		return project, tags, workflow, cols, nil, err
	}

	projectID := pc.Project.ID
	var viewerUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		viewerUserID = &userID
	}

	durable := pc.Project.ExpiresAt == nil
	tags, err := s.listTagCounts(ctx, projectID, viewerUserID, &pc.Role, durable)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}
	workflow, err := s.GetProjectWorkflow(ctx, projectID)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}

	// Resolve the tag filter once for this request and reuse it for the soft-cap
	// count, window query, and (if needed) every per-lane list/count.
	resolvedFilter, err := s.resolveBoardTagFilter(ctx, projectID, durable, tagFilter)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}

	// Soft cap: if filtered count exceeds threshold, fall back to per-lane queries (bounded memory)
	totalTodos, err := s.countTodosForBoard(ctx, projectID, resolvedFilter, searchFilter, assigneeFilter, sprintFilter)
	if err != nil {
		return Project{}, nil, nil, nil, nil, err
	}
	if totalTodos > boardTodoSoftCap {
		return s.getBoardPagedPerLane(ctx, pc, projectID, workflow, resolvedFilter, searchFilter, assigneeFilter, sprintFilter, sortOrder, limitPerLane, tags)
	}

	// Fast path: single window-function query returns rows + lane totals
	allTodos, err := s.listAllTodosForBoardWithCounts(ctx, projectID, resolvedFilter, searchFilter, assigneeFilter, sprintFilter, sortOrder)
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
				flushLane(currentKey, page, laneTotal, limitPerLane, cols, meta, sortOrder)
			}
			currentKey = tw.Todo.ColumnKey
			page = nil
			laneTotal = tw.LaneTotal
		}
		page = append(page, tw.Todo)
	}
	if currentKey != "" {
		flushLane(currentKey, page, laneTotal, limitPerLane, cols, meta, sortOrder)
	}

	if pc.Project.ExpiresAt != nil {
		if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
			log.Printf("failed to update board activity for project %d: %v", projectID, err)
		}
	}

	return pc.Project, tags, workflow, cols, meta, nil
}

// GetBoard returns full board (all todos, no pagination).
// pc must be non-nil; use GetProjectContextBySlug or GetProjectContextForRead to obtain it.
func (s *Store) GetBoard(ctx context.Context, pc *ProjectContext, tagFilter string, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder) (Project, []TagCount, []WorkflowColumn, map[string][]Todo, error) {
	projectID := pc.Project.ID
	var viewerUserID *int64
	if userID, ok := UserIDFromContext(ctx); ok {
		viewerUserID = &userID
	}

	durable := pc.Project.ExpiresAt == nil
	tags, err := s.listTagCounts(ctx, projectID, viewerUserID, &pc.Role, durable)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}
	workflow, err := s.GetProjectWorkflow(ctx, projectID)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}

	resolvedFilter, err := s.resolveBoardTagFilter(ctx, projectID, durable, tagFilter)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}

	cols := make(map[string][]Todo, len(workflow))
	for _, col := range workflow {
		cols[col.Key] = []Todo{}
	}

	// OPTIMIZED: Fetch all todos in a single query instead of 5 separate queries (one per status)
	// This reduces query overhead and is more efficient on low-power hardware
	todos, err := s.listAllTodosForBoard(ctx, projectID, resolvedFilter, searchFilter, assigneeFilter, sprintFilter, sortOrder)
	if err != nil {
		return Project{}, nil, nil, nil, err
	}

	// Group todos by status in Go
	for _, todo := range todos {
		cols[todo.ColumnKey] = append(cols[todo.ColumnKey], todo)
	}

	// Expiring boards: throttled read extends rolling expiry. Durable: skip (no activity DB work on read).
	if pc.Project.ExpiresAt != nil {
		if err := s.UpdateBoardActivity(ctx, projectID); err != nil {
			log.Printf("failed to update board activity for project %d: %v", projectID, err)
		}
	}

	return pc.Project, tags, workflow, cols, nil
}

// listAllTodosForBoard fetches all todos for a board in a single query
// OPTIMIZED: Single query instead of 5 separate queries (one per status)
func (s *Store) listAllTodosForBoard(ctx context.Context, projectID int64, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder) ([]Todo, error) {
	// Show ALL tags on todos (no user filter - collaboration-friendly)
	// Tag filter matches by resolved backing tag IDs

	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	assigneeCond, assigneeArgs := assigneeFilterArgs(assigneeFilter)
	orderBy := "t.column_key ASC, " + laneOrderBy(sortOrder)

	var rows *sql.Rows
	var err error

	if tagFilter.NoFilter {
		// No tag filter - simple query without CTE
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
WHERE
  t.project_id = ?
  `+sprintCond+assigneeCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
`,
			args...,
		)
	} else {
		if len(tagFilter.TagIDs) == 0 {
			return nil, nil
		}
		idPH, idArgs := tagFilterPlaceholders(tagFilter.TagIDs)
		// Placeholder order: CTE tag_id IN (…) (N), main project_id=? (1), sprintCond (0–1), assigneeCond (0–1), search ?,?,? (3).
		args := make([]any, 0, len(idArgs)+6)
		args = append(args, idArgs...)
		args = append(args, projectID)
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  WHERE tt.tag_id IN (`+idPH+`)
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ?
  `+sprintCond+assigneeCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
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

// countTodosForBoard returns the count of todos matching board filters (tag, search, assignee, sprint).
// Used for soft cap check; must mirror filters used by listAllTodosForBoardWithCounts.
func (s *Store) countTodosForBoard(ctx context.Context, projectID int64, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter) (int, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	assigneeCond, assigneeArgs := assigneeFilterArgs(assigneeFilter)
	var count int
	if tagFilter.NoFilter {
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos t
WHERE t.project_id = ?
`+sprintCond+assigneeCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`, args...).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos: %w", err)
		}
	} else {
		if len(tagFilter.TagIDs) == 0 {
			return 0, nil
		}
		idPH, idArgs := tagFilterPlaceholders(tagFilter.TagIDs)
		args := make([]any, 0, len(idArgs)+6)
		args = append(args, idArgs...)
		args = append(args, projectID)
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  WHERE tt.tag_id IN (`+idPH+`)
)
SELECT COUNT(*) FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ?
`+sprintCond+assigneeCond+`
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
func (s *Store) listAllTodosForBoardWithCounts(ctx context.Context, projectID int64, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder) ([]todoWithLaneTotal, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	assigneeCond, assigneeArgs := assigneeFilterArgs(assigneeFilter)
	orderBy := "t.column_key ASC, " + laneOrderBy(sortOrder)

	var rows *sql.Rows
	var err error

	if tagFilter.NoFilter {
		args := []any{projectID}
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
  COUNT(*) OVER (PARTITION BY t.column_key) AS lane_total
FROM todos t
WHERE
  t.project_id = ?
`+sprintCond+assigneeCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
`, args...)
	} else {
		if len(tagFilter.TagIDs) == 0 {
			return nil, nil
		}
		idPH, idArgs := tagFilterPlaceholders(tagFilter.TagIDs)
		args := make([]any, 0, len(idArgs)+6)
		args = append(args, idArgs...)
		args = append(args, projectID)
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  WHERE tt.tag_id IN (`+idPH+`)
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at,
  COUNT(*) OVER (PARTITION BY t.column_key) AS lane_total
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ?
`+sprintCond+assigneeCond+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
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
// Cursor format is "a:b" where (a, b) depends on sortOrder: for the default manual
// order it's "rank:id" (DB id, not localId); for sortOrder=newest/oldest it's
// "createdAtMs:id". Returns (items, nextCursor, hasMore). nextCursor is empty when
// hasMore is false.
//
// Ordering contract: the query's ORDER BY and cursor predicate are chosen together by
// laneOrderBy/laneCursorPredicate for the given sortOrder, and laneCursor encodes the
// matching two fields for the "last row" cursor. If either helper changes independently,
// pagination tests and cursor semantics must be updated together.
func (s *Store) ListTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, limit int, afterA, afterB int64, tagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder) ([]Todo, string, bool, error) {
	// Exported entry point (lane pagination, MCP board tools): resolve once so the
	// filter agrees with GetBoard for the same project scope.
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return nil, "", false, err
	}
	resolved, err := s.resolveBoardTagFilter(ctx, projectID, p.ExpiresAt == nil, tagFilter)
	if err != nil {
		return nil, "", false, err
	}
	return s.listTodosForBoardLaneResolved(ctx, projectID, columnKey, limit, afterA, afterB, resolved, searchFilter, assigneeFilter, sprintFilter, sortOrder)
}

// listTodosForBoardLaneResolved is the lane list helper that consumes a pre-resolved
// tag filter (no project lookup, no tag-row scan).
func (s *Store) listTodosForBoardLaneResolved(ctx context.Context, projectID int64, columnKey string, limit int, afterA, afterB int64, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter, sortOrder SortOrder) ([]Todo, string, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	fetchLimit := limit + 1

	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	assigneeCond, assigneeArgs := assigneeFilterArgs(assigneeFilter)
	orderBy := laneOrderBy(sortOrder)
	cursorPredicate := laneCursorPredicate(sortOrder)

	var rows *sql.Rows
	var err error

	if tagFilter.NoFilter {
		args := []any{projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, afterA, afterB, searchFilter, searchFilter, searchFilter, fetchLimit)
		rows, err = s.db.QueryContext(ctx, `
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
WHERE
  t.project_id = ? AND t.column_key = ?
  `+sprintCond+assigneeCond+`
  `+cursorPredicate+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
LIMIT ?
`,
			args...,
		)
	} else {
		if len(tagFilter.TagIDs) == 0 {
			return nil, "", false, nil
		}
		idPH, idArgs := tagFilterPlaceholders(tagFilter.TagIDs)
		// Placeholder order: CTE tag_id IN (…) (N), main project_id=? (1), status=? (2), sprintCond (0–1), assigneeCond (0–1), cursor (3,4), search ?,?,? (5), LIMIT ? (6).
		args := make([]any, 0, len(idArgs)+10)
		args = append(args, idArgs...)
		args = append(args, projectID, columnKey)
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, afterA, afterB, searchFilter, searchFilter, searchFilter, fetchLimit)
		rows, err = s.db.QueryContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  WHERE tt.tag_id IN (`+idPH+`)
)
SELECT
  t.id, t.project_id, t.local_id, t.title, t.body, t.column_key, t.rank, t.estimation_points, t.assignee_user_id, t.sprint_id, t.created_at, t.updated_at, t.done_at
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE
  t.project_id = ? AND t.column_key = ?
  `+sprintCond+assigneeCond+`
  `+cursorPredicate+`
  AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
ORDER BY `+orderBy+`
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
		last := out[len(out)-1]
		return out, laneCursor(last, sortOrder), true, nil
	}
	return out, "", false, nil
}

// CountTodosForBoardLane returns the total number of todos in the lane with the
// same tag, search, assignee, and sprint filters as ListTodosForBoardLane.
func (s *Store) CountTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, tagFilter string, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter) (int, error) {
	p, err := s.getProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	resolved, err := s.resolveBoardTagFilter(ctx, projectID, p.ExpiresAt == nil, tagFilter)
	if err != nil {
		return 0, err
	}
	return s.countTodosForBoardLaneResolved(ctx, projectID, columnKey, resolved, searchFilter, assigneeFilter, sprintFilter)
}

// countTodosForBoardLaneResolved is the lane count helper that consumes a pre-resolved
// tag filter (no project lookup, no tag-row scan).
func (s *Store) countTodosForBoardLaneResolved(ctx context.Context, projectID int64, columnKey string, tagFilter boardTagFilter, searchFilter string, assigneeFilter AssigneeFilter, sprintFilter SprintFilter) (int, error) {
	sprintCond, sprintArgs := sprintFilterArgs(sprintFilter)
	assigneeCond, assigneeArgs := assigneeFilterArgs(assigneeFilter)

	var count int
	if tagFilter.NoFilter {
		args := []any{projectID, columnKey}
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos t
WHERE t.project_id = ? AND t.column_key = ?
`+sprintCond+assigneeCond+`
AND (? = '' OR LOWER(t.title) LIKE '%' || LOWER(?) || '%' OR LOWER(t.body) LIKE '%' || LOWER(?) || '%')
`,
			args...,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("count todos lane: %w", err)
		}
	} else {
		if len(tagFilter.TagIDs) == 0 {
			return 0, nil
		}
		idPH, idArgs := tagFilterPlaceholders(tagFilter.TagIDs)
		// Placeholder order: CTE tag_id IN (…) (N), main project_id=? (1), status=? (2), sprintCond (0–1), assigneeCond (0–1), search ?,?,? (3).
		args := make([]any, 0, len(idArgs)+7)
		args = append(args, idArgs...)
		args = append(args, projectID, columnKey)
		args = append(args, sprintArgs...)
		args = append(args, assigneeArgs...)
		args = append(args, searchFilter, searchFilter, searchFilter)
		err := s.db.QueryRowContext(ctx, `
WITH tagged_todos AS (
  SELECT DISTINCT tt.todo_id
  FROM todo_tags tt
  WHERE tt.tag_id IN (`+idPH+`)
)
SELECT COUNT(*) FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ? AND t.column_key = ?
`+sprintCond+assigneeCond+`
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
