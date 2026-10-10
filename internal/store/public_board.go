package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const publicTagDefaultColor = "#64748b"

type PublicProjectProjection struct {
	Slug           string
	Name           string
	DominantColor  string
	EstimationMode string
	SprintsEnabled bool
}

type PublicWorkflowProjection struct {
	Key      string
	Name     string
	Color    string
	IsDone   bool
	Position int
}

type PublicPriorityProjection struct {
	Key      string
	Name     string
	Color    string
	Position int
}

type PublicTagProjection struct {
	Name        string
	Color       string
	ActiveCount int
}

type PublicTodoTagProjection struct {
	Name  string
	Color string
}

type PublicTodoProjection struct {
	LocalID          int64
	Title            string
	Body             string
	ColumnKey        string
	EstimationPoints *int
	PriorityKey      *string
	SprintNumber     *int64
	Tags             []PublicTodoTagProjection
}

type PublicTodoOrder struct {
	Rank    int64
	LocalID int64
}

type PublicBoardQuery struct {
	Search       string
	Tags         []string
	SprintNumber *int64
	PriorityKey  *string
	Limit        int
	After        *PublicTodoOrder
}

type PublicLaneProjection struct {
	Items      []PublicTodoProjection
	HasMore    bool
	NextOrder  *PublicTodoOrder
	TotalCount int
}

type PublicBoardSnapshotProjection struct {
	Project     PublicProjectProjection
	Workflow    []PublicWorkflowProjection
	Priorities  []PublicPriorityProjection
	Tags        []PublicTagProjection
	Columns     map[string][]PublicTodoProjection
	ColumnsMeta map[string]PublicLaneProjection
}

type PublicSprintProjection struct {
	Number int64
	Name   string
	State  string
}

type PublicTodoLinkProjection struct {
	Direction string
	LocalID   int64
	Title     string
}

// ResolveEligiblePublicProject returns only the internal key required by the
// dedicated public projection queries. It does not apply member or temporary
// board authorization and does not return a rich Project model.
func (s *Store) ResolveEligiblePublicProject(ctx context.Context, slug string) (int64, error) {
	if !IsValidProjectSlug(slug) || IsReservedProjectSlug(slug) {
		return 0, ErrNotFound
	}
	var projectID int64
	err := s.db.QueryRowContext(ctx, `
SELECT id
FROM projects
WHERE slug = ?
  AND import_batch_id IS NULL
  AND expires_at IS NULL
  AND public_view_enabled = 1`, slug).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("resolve eligible public project: %w", err)
	}
	return projectID, nil
}

func loadEligiblePublicProjectTx(ctx context.Context, tx *sql.Tx, projectID int64, expectedSlug string) (PublicProjectProjection, error) {
	var (
		project        PublicProjectProjection
		sprintsEnabled int
	)
	err := tx.QueryRowContext(ctx, `
SELECT slug, name, dominant_color, estimation_mode, sprints_enabled
FROM projects
WHERE id = ?
  AND slug = ?
  AND import_batch_id IS NULL
  AND expires_at IS NULL
  AND public_view_enabled = 1`, projectID, expectedSlug).Scan(
		&project.Slug,
		&project.Name,
		&project.DominantColor,
		&project.EstimationMode,
		&sprintsEnabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicProjectProjection{}, ErrNotFound
	}
	if err != nil {
		return PublicProjectProjection{}, fmt.Errorf("load public project projection: %w", err)
	}
	if !IsValidProjectSlug(project.Slug) || IsReservedProjectSlug(project.Slug) {
		return PublicProjectProjection{}, ErrNotFound
	}
	project.SprintsEnabled = sprintsEnabled == 1
	return project, nil
}

func listPublicWorkflowTx(ctx context.Context, tx *sql.Tx, projectID int64) ([]PublicWorkflowProjection, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT key, name, color, is_done, position
FROM project_workflow_columns
WHERE project_id = ?
ORDER BY position, key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list public workflow: %w", err)
	}
	defer rows.Close()
	workflow := make([]PublicWorkflowProjection, 0, 8)
	for rows.Next() {
		var column PublicWorkflowProjection
		var isDone int
		if err := rows.Scan(&column.Key, &column.Name, &column.Color, &isDone, &column.Position); err != nil {
			return nil, fmt.Errorf("scan public workflow: %w", err)
		}
		column.IsDone = isDone == 1
		workflow = append(workflow, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public workflow: %w", err)
	}
	return workflow, nil
}

func listPublicPrioritiesTx(ctx context.Context, tx *sql.Tx, projectID int64) ([]PublicPriorityProjection, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT key, name, color, position
FROM project_priorities
WHERE project_id = ?
ORDER BY position, key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list public priorities: %w", err)
	}
	defer rows.Close()
	priorities := make([]PublicPriorityProjection, 0, 4)
	for rows.Next() {
		var priority PublicPriorityProjection
		if err := rows.Scan(&priority.Key, &priority.Name, &priority.Color, &priority.Position); err != nil {
			return nil, fmt.Errorf("scan public priority: %w", err)
		}
		priorities = append(priorities, priority)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public priorities: %w", err)
	}
	return priorities, nil
}

func listPublicTagsTx(ctx context.Context, tx *sql.Tx, projectID int64) ([]PublicTagProjection, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT g.name, COALESCE(NULLIF(g.color, ''), ?), COUNT(DISTINCT t.local_id)
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
WHERE t.project_id = ? AND t.archived_at IS NULL
GROUP BY g.name, COALESCE(NULLIF(g.color, ''), ?)
ORDER BY g.name`, publicTagDefaultColor, projectID, publicTagDefaultColor)
	if err != nil {
		return nil, fmt.Errorf("list public tags: %w", err)
	}
	defer rows.Close()
	tags := make([]PublicTagProjection, 0, 8)
	for rows.Next() {
		var tag PublicTagProjection
		if err := rows.Scan(&tag.Name, &tag.Color, &tag.ActiveCount); err != nil {
			return nil, fmt.Errorf("scan public tag: %w", err)
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public tags: %w", err)
	}
	return tags, nil
}

func publicLikePattern(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(value))
	return "%" + escaped + "%"
}

func publicTodoWhere(projectID int64, columnKey string, query PublicBoardQuery, includeAfter bool) (string, []any) {
	var where strings.Builder
	where.WriteString("t.project_id = ? AND t.column_key = ? AND t.archived_at IS NULL")
	args := []any{projectID, columnKey}
	if query.Search != "" {
		where.WriteString(` AND (LOWER(t.title) LIKE ? ESCAPE '\' OR LOWER(t.body) LIKE ? ESCAPE '\')`)
		pattern := publicLikePattern(query.Search)
		args = append(args, pattern, pattern)
	}
	for _, tag := range query.Tags {
		where.WriteString(` AND EXISTS (
SELECT 1 FROM todo_tags filter_tt
JOIN tags filter_g ON filter_g.id = filter_tt.tag_id
WHERE filter_tt.todo_id = t.id AND LOWER(TRIM(filter_g.name)) = ?)`)
		args = append(args, tag)
	}
	if query.SprintNumber != nil {
		where.WriteString(` AND EXISTS (
SELECT 1 FROM sprints filter_s
WHERE filter_s.id = t.sprint_id AND filter_s.project_id = t.project_id AND filter_s.number = ?)`)
		args = append(args, *query.SprintNumber)
	}
	if query.PriorityKey != nil {
		where.WriteString(" AND t.priority_key = ?")
		args = append(args, *query.PriorityKey)
	}
	if includeAfter && query.After != nil {
		where.WriteString(" AND (t.rank > ? OR (t.rank = ? AND t.local_id > ?))")
		args = append(args, query.After.Rank, query.After.Rank, query.After.LocalID)
	}
	return where.String(), args
}

func validatePublicLaneQueryTx(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string, query PublicBoardQuery) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM project_workflow_columns WHERE project_id = ? AND key = ?)`, projectID, columnKey).Scan(&exists); err != nil {
		return fmt.Errorf("validate public lane: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: unknown public lane", ErrValidation)
	}
	if query.PriorityKey != nil {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM project_priorities WHERE project_id = ? AND key = ?)`, projectID, *query.PriorityKey).Scan(&exists); err != nil {
			return fmt.Errorf("validate public priority: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: unknown public priority", ErrValidation)
		}
	}
	if query.SprintNumber != nil {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM sprints WHERE project_id = ? AND number = ?)`, projectID, *query.SprintNumber).Scan(&exists); err != nil {
			return fmt.Errorf("validate public sprint: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: unknown public sprint", ErrValidation)
		}
	}
	return nil
}

func listPublicTodoTagsTx(ctx context.Context, tx *sql.Tx, projectID int64, localIDs []int64) (map[int64][]PublicTodoTagProjection, error) {
	out := make(map[int64][]PublicTodoTagProjection, len(localIDs))
	if len(localIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(localIDs))
	args := make([]any, 0, len(localIDs)+2)
	args = append(args, publicTagDefaultColor, projectID)
	for i, localID := range localIDs {
		placeholders[i] = "?"
		args = append(args, localID)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT t.local_id, g.name, COALESCE(NULLIF(g.color, ''), ?)
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
WHERE t.project_id = ? AND t.archived_at IS NULL
  AND t.local_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY t.local_id, g.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("list public todo tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var localID int64
		var tag PublicTodoTagProjection
		if err := rows.Scan(&localID, &tag.Name, &tag.Color); err != nil {
			return nil, fmt.Errorf("scan public todo tag: %w", err)
		}
		out[localID] = append(out[localID], tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public todo tags: %w", err)
	}
	return out, nil
}

func listPublicLaneTx(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string, query PublicBoardQuery) (PublicLaneProjection, error) {
	if query.Limit < 1 || query.Limit > 50 {
		return PublicLaneProjection{}, fmt.Errorf("%w: invalid public page limit", ErrValidation)
	}
	if err := validatePublicLaneQueryTx(ctx, tx, projectID, columnKey, query); err != nil {
		return PublicLaneProjection{}, err
	}
	where, args := publicTodoWhere(projectID, columnKey, query, true)
	args = append(args, query.Limit+1)
	rows, err := tx.QueryContext(ctx, `
SELECT t.local_id, t.title, t.body, t.column_key, t.rank,
       t.estimation_points, t.priority_key, s.number
FROM todos t
LEFT JOIN sprints s ON s.id = t.sprint_id AND s.project_id = t.project_id
WHERE `+where+`
ORDER BY t.rank, t.local_id
LIMIT ?`, args...)
	if err != nil {
		return PublicLaneProjection{}, fmt.Errorf("list public lane: %w", err)
	}
	type rowProjection struct {
		todo PublicTodoProjection
		rank int64
	}
	fetched := make([]rowProjection, 0, query.Limit+1)
	for rows.Next() {
		var (
			row              rowProjection
			estimationPoints sql.NullInt64
			priorityKey      sql.NullString
			sprintNumber     sql.NullInt64
		)
		if err := rows.Scan(&row.todo.LocalID, &row.todo.Title, &row.todo.Body, &row.todo.ColumnKey, &row.rank, &estimationPoints, &priorityKey, &sprintNumber); err != nil {
			_ = rows.Close()
			return PublicLaneProjection{}, fmt.Errorf("scan public todo: %w", err)
		}
		if estimationPoints.Valid {
			value := int(estimationPoints.Int64)
			row.todo.EstimationPoints = &value
		}
		if priorityKey.Valid {
			value := priorityKey.String
			row.todo.PriorityKey = &value
		}
		if sprintNumber.Valid {
			value := sprintNumber.Int64
			row.todo.SprintNumber = &value
		}
		row.todo.Tags = []PublicTodoTagProjection{}
		fetched = append(fetched, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return PublicLaneProjection{}, fmt.Errorf("rows public lane: %w", err)
	}
	_ = rows.Close()

	hasMore := len(fetched) > query.Limit
	if hasMore {
		fetched = fetched[:query.Limit]
	}
	localIDs := make([]int64, len(fetched))
	for i := range fetched {
		localIDs[i] = fetched[i].todo.LocalID
	}
	tagsByTodo, err := listPublicTodoTagsTx(ctx, tx, projectID, localIDs)
	if err != nil {
		return PublicLaneProjection{}, err
	}
	items := make([]PublicTodoProjection, len(fetched))
	for i := range fetched {
		items[i] = fetched[i].todo
		if tags := tagsByTodo[items[i].LocalID]; tags != nil {
			items[i].Tags = tags
		}
	}

	countWhere, countArgs := publicTodoWhere(projectID, columnKey, query, false)
	var totalCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos t WHERE `+countWhere, countArgs...).Scan(&totalCount); err != nil {
		return PublicLaneProjection{}, fmt.Errorf("count public lane: %w", err)
	}
	page := PublicLaneProjection{Items: items, HasMore: hasMore, TotalCount: totalCount}
	if hasMore && len(fetched) > 0 {
		last := fetched[len(fetched)-1]
		page.NextOrder = &PublicTodoOrder{Rank: last.rank, LocalID: last.todo.LocalID}
	}
	return page, nil
}

func (s *Store) GetPublicBoardSnapshot(ctx context.Context, projectID int64, expectedSlug string, query PublicBoardQuery) (PublicBoardSnapshotProjection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PublicBoardSnapshotProjection{}, fmt.Errorf("begin public snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	project, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug)
	if err != nil {
		return PublicBoardSnapshotProjection{}, err
	}
	workflow, err := listPublicWorkflowTx(ctx, tx, projectID)
	if err != nil {
		return PublicBoardSnapshotProjection{}, err
	}
	priorities, err := listPublicPrioritiesTx(ctx, tx, projectID)
	if err != nil {
		return PublicBoardSnapshotProjection{}, err
	}
	tags, err := listPublicTagsTx(ctx, tx, projectID)
	if err != nil {
		return PublicBoardSnapshotProjection{}, err
	}
	columns := make(map[string][]PublicTodoProjection, len(workflow))
	columnsMeta := make(map[string]PublicLaneProjection, len(workflow))
	for _, column := range workflow {
		page, err := listPublicLaneTx(ctx, tx, projectID, column.Key, query)
		if err != nil {
			return PublicBoardSnapshotProjection{}, err
		}
		columns[column.Key] = page.Items
		page.Items = nil
		columnsMeta[column.Key] = page
	}
	if err := tx.Commit(); err != nil {
		return PublicBoardSnapshotProjection{}, fmt.Errorf("commit public snapshot read: %w", err)
	}
	return PublicBoardSnapshotProjection{
		Project: project, Workflow: workflow, Priorities: priorities, Tags: tags,
		Columns: columns, ColumnsMeta: columnsMeta,
	}, nil
}

func (s *Store) GetPublicBoardLane(ctx context.Context, projectID int64, expectedSlug, columnKey string, query PublicBoardQuery) (PublicLaneProjection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PublicLaneProjection{}, fmt.Errorf("begin public lane: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return PublicLaneProjection{}, err
	}
	page, err := listPublicLaneTx(ctx, tx, projectID, columnKey, query)
	if err != nil {
		return PublicLaneProjection{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublicLaneProjection{}, fmt.Errorf("commit public lane read: %w", err)
	}
	return page, nil
}

func scanPublicTodo(row *sql.Row) (PublicTodoProjection, error) {
	var (
		todo             PublicTodoProjection
		estimationPoints sql.NullInt64
		priorityKey      sql.NullString
		sprintNumber     sql.NullInt64
	)
	err := row.Scan(&todo.LocalID, &todo.Title, &todo.Body, &todo.ColumnKey, &estimationPoints, &priorityKey, &sprintNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicTodoProjection{}, ErrNotFound
	}
	if err != nil {
		return PublicTodoProjection{}, fmt.Errorf("scan public todo detail: %w", err)
	}
	if estimationPoints.Valid {
		value := int(estimationPoints.Int64)
		todo.EstimationPoints = &value
	}
	if priorityKey.Valid {
		value := priorityKey.String
		todo.PriorityKey = &value
	}
	if sprintNumber.Valid {
		value := sprintNumber.Int64
		todo.SprintNumber = &value
	}
	todo.Tags = []PublicTodoTagProjection{}
	return todo, nil
}

func (s *Store) GetPublicTodoDetail(ctx context.Context, projectID int64, expectedSlug string, localID int64) (PublicTodoProjection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return PublicTodoProjection{}, fmt.Errorf("begin public todo detail: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return PublicTodoProjection{}, err
	}
	todo, err := scanPublicTodo(tx.QueryRowContext(ctx, `
SELECT t.local_id, t.title, t.body, t.column_key,
       t.estimation_points, t.priority_key, s.number
FROM todos t
LEFT JOIN sprints s ON s.id = t.sprint_id AND s.project_id = t.project_id
WHERE t.project_id = ? AND t.local_id = ? AND t.archived_at IS NULL`, projectID, localID))
	if err != nil {
		return PublicTodoProjection{}, err
	}
	tags, err := listPublicTodoTagsTx(ctx, tx, projectID, []int64{localID})
	if err != nil {
		return PublicTodoProjection{}, err
	}
	if tags[localID] != nil {
		todo.Tags = tags[localID]
	}
	if err := tx.Commit(); err != nil {
		return PublicTodoProjection{}, fmt.Errorf("commit public todo detail read: %w", err)
	}
	return todo, nil
}

func (s *Store) ListPublicTodoLinks(ctx context.Context, projectID int64, expectedSlug string, localID int64) ([]PublicTodoLinkProjection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin public todo links: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return nil, err
	}
	var sourceExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM todos WHERE project_id = ? AND local_id = ? AND archived_at IS NULL)`, projectID, localID).Scan(&sourceExists); err != nil {
		return nil, fmt.Errorf("validate public link source: %w", err)
	}
	if !sourceExists {
		return nil, ErrNotFound
	}
	rows, err := tx.QueryContext(ctx, `
SELECT 'outbound', target.local_id, target.title
FROM todo_links link
JOIN todos source ON source.project_id = link.project_id AND source.local_id = link.from_local_id AND source.archived_at IS NULL
JOIN todos target ON target.project_id = link.project_id AND target.local_id = link.to_local_id AND target.archived_at IS NULL
WHERE link.project_id = ? AND source.local_id = ?
UNION ALL
SELECT 'inbound', source.local_id, source.title
FROM todo_links link
JOIN todos source ON source.project_id = link.project_id AND source.local_id = link.from_local_id AND source.archived_at IS NULL
JOIN todos target ON target.project_id = link.project_id AND target.local_id = link.to_local_id AND target.archived_at IS NULL
WHERE link.project_id = ? AND target.local_id = ?
ORDER BY 1, 2`, projectID, localID, projectID, localID)
	if err != nil {
		return nil, fmt.Errorf("list public todo links: %w", err)
	}
	defer rows.Close()
	links := make([]PublicTodoLinkProjection, 0, 4)
	for rows.Next() {
		var link PublicTodoLinkProjection
		if err := rows.Scan(&link.Direction, &link.LocalID, &link.Title); err != nil {
			return nil, fmt.Errorf("scan public todo link: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public todo links: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit public todo links read: %w", err)
	}
	return links, nil
}

func (s *Store) ListPublicSprints(ctx context.Context, projectID int64, expectedSlug string) ([]PublicSprintProjection, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin public sprints: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	project, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug)
	if err != nil {
		return nil, err
	}
	sprints := make([]PublicSprintProjection, 0, 4)
	if project.SprintsEnabled {
		rows, err := tx.QueryContext(ctx, `
SELECT number, name, state
FROM sprints
WHERE project_id = ?
ORDER BY number`, projectID)
		if err != nil {
			return nil, fmt.Errorf("list public sprints: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sprint PublicSprintProjection
			if err := rows.Scan(&sprint.Number, &sprint.Name, &sprint.State); err != nil {
				return nil, fmt.Errorf("scan public sprint: %w", err)
			}
			sprints = append(sprints, sprint)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows public sprints: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit public sprints read: %w", err)
	}
	return sprints, nil
}
