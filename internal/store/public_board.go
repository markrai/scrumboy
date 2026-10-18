package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// publicTagPalette supplies colors for public tags that have no valid shared
// color. Mid-tone hues read on both light and dark themes when rendered as a
// tinted chip. Order is part of the public contract: reordering or resizing it
// changes every fallback color.
var publicTagPalette = [...]string{
	"#ef4444", // red
	"#f97316", // orange
	"#d97706", // amber
	"#65a30d", // lime
	"#16a34a", // green
	"#0d9488", // teal
	"#0891b2", // cyan
	"#3b82f6", // blue
	"#6366f1", // indigo
	"#8b5cf6", // violet
	"#d946ef", // fuchsia
	"#ec4899", // pink
}

// resolvePublicTagColor returns the shared tag color when it is valid hex, and
// otherwise a deterministic palette color derived from the tag's canonical name
// (FNV-1a over TagGroupKey) so equivalent legacy spellings share a color. It only
// sees tags.color; personal user_tag_colors are never consulted.
func resolvePublicTagColor(name, sharedColor string) string {
	if colorHexRe.MatchString(sharedColor) {
		return sharedColor
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(TagGroupKey(name)))
	return publicTagPalette[h.Sum32()%uint32(len(publicTagPalette))]
}

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
	// tagIDGroupJSON contains one JSON array per logical tag filter. Keeping each
	// group in one bound parameter avoids SQLite's placeholder limit even when a
	// durable project has many collaborator-owned or historical equivalent rows.
	tagIDGroupJSON []string
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
SELECT g.id, g.name, COALESCE(g.color, ''), t.local_id
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
WHERE t.project_id = ? AND t.archived_at IS NULL
ORDER BY g.id, t.local_id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list public tags: %w", err)
	}
	defer rows.Close()
	type aggregate struct {
		color string
		todos map[int64]struct{}
	}
	byName := make(map[string]*aggregate)
	for rows.Next() {
		var tagID, localID int64
		var name, color string
		if err := rows.Scan(&tagID, &name, &color, &localID); err != nil {
			return nil, fmt.Errorf("scan public tag: %w", err)
		}
		key := TagGroupKey(name)
		agg := byName[key]
		if agg == nil {
			agg = &aggregate{todos: make(map[int64]struct{})}
			byName[key] = agg
		}
		if agg.color == "" && colorHexRe.MatchString(color) {
			agg.color = color
		}
		agg.todos[localID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public tags: %w", err)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	tags := make([]PublicTagProjection, 0, len(names))
	for _, name := range names {
		agg := byName[name]
		tags = append(tags, PublicTagProjection{
			Name: name, Color: resolvePublicTagColor(name, agg.color), ActiveCount: len(agg.todos),
		})
	}
	return tags, nil
}

// resolvePublicTagFiltersTx expands each canonical public filter into the same
// physical-row union used by durable authenticated boards. The comparison stays in
// Go because SQLite LOWER() is ASCII-only and cannot implement CanonicalizeTag.
func resolvePublicTagFiltersTx(ctx context.Context, tx *sql.Tx, projectID int64, query PublicBoardQuery) (PublicBoardQuery, error) {
	if len(query.Tags) == 0 {
		return query, nil
	}
	if query.tagIDGroupJSON != nil {
		return query, nil
	}
	groupByKey := make(map[string]int, len(query.Tags))
	tagIDGroups := make([][]int64, len(query.Tags))
	for i, key := range query.Tags {
		groupByKey[key] = i
	}
	rows, err := tx.QueryContext(ctx, `
SELECT DISTINCT g.id, g.name
FROM tags g
JOIN todo_tags tt ON tt.tag_id = g.id
JOIN todos t ON t.id = tt.todo_id AND t.project_id = ?
ORDER BY g.id`, projectID)
	if err != nil {
		return PublicBoardQuery{}, fmt.Errorf("resolve public tag filters: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return PublicBoardQuery{}, fmt.Errorf("scan public tag filter: %w", err)
		}
		if group, ok := groupByKey[TagGroupKey(name)]; ok {
			tagIDGroups[group] = append(tagIDGroups[group], id)
		}
	}
	if err := rows.Err(); err != nil {
		return PublicBoardQuery{}, fmt.Errorf("rows public tag filters: %w", err)
	}
	query.tagIDGroupJSON = make([]string, len(tagIDGroups))
	for i, tagIDs := range tagIDGroups {
		encoded, err := json.Marshal(tagIDs)
		if err != nil {
			return PublicBoardQuery{}, fmt.Errorf("encode public tag filter ids: %w", err)
		}
		query.tagIDGroupJSON[i] = string(encoded)
	}
	return query, nil
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
	for _, tagIDsJSON := range query.tagIDGroupJSON {
		if tagIDsJSON == "[]" {
			where.WriteString(" AND 0")
			continue
		}
		args = append(args, tagIDsJSON)
		where.WriteString(` AND EXISTS (
SELECT 1 FROM todo_tags filter_tt
	WHERE filter_tt.todo_id = t.id
	  AND filter_tt.tag_id IN (SELECT value FROM json_each(?)))`)
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
	args := make([]any, 0, len(localIDs)+1)
	args = append(args, projectID)
	for i, localID := range localIDs {
		placeholders[i] = "?"
		args = append(args, localID)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT t.local_id, g.id, g.name, COALESCE(g.color, '')
FROM todos t
JOIN todo_tags tt ON tt.todo_id = t.id
JOIN tags g ON g.id = tt.tag_id
WHERE t.project_id = ? AND t.archived_at IS NULL
  AND t.local_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY t.local_id, g.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list public todo tags: %w", err)
	}
	defer rows.Close()
	byTodo := make(map[int64]map[string]string, len(localIDs))
	for rows.Next() {
		var localID, tagID int64
		var name, color string
		if err := rows.Scan(&localID, &tagID, &name, &color); err != nil {
			return nil, fmt.Errorf("scan public todo tag: %w", err)
		}
		key := TagGroupKey(name)
		if byTodo[localID] == nil {
			byTodo[localID] = make(map[string]string)
		}
		existing, exists := byTodo[localID][key]
		if !exists || (!colorHexRe.MatchString(existing) && colorHexRe.MatchString(color)) {
			byTodo[localID][key] = color
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows public todo tags: %w", err)
	}
	for localID, colors := range byTodo {
		names := make([]string, 0, len(colors))
		for name := range colors {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out[localID] = append(out[localID], PublicTodoTagProjection{
				Name: name, Color: resolvePublicTagColor(name, colors[name]),
			})
		}
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
	query, err = resolvePublicTagFiltersTx(ctx, tx, projectID, query)
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
	query, err = resolvePublicTagFiltersTx(ctx, tx, projectID, query)
	if err != nil {
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
