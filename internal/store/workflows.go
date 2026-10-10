package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var columnKeyRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_]*[a-z0-9])?$`)
var colorHexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidWorkflowColumnColor reports whether s is a non-empty #RRGGBB hex string.
func ValidWorkflowColumnColor(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && colorHexRe.MatchString(s)
}

const (
	maxWorkflowColumns     = 12
	defaultWorkflowColor   = "#64748b"
	maxWorkflowNameLength  = 200
	maxWorkflowKeyAttempts = 1000
)

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type sqlRowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func isValidColumnKey(key string) bool {
	key = strings.TrimSpace(key)
	if len(key) == 0 || len(key) > maxSlugLen {
		return false
	}
	return columnKeyRe.MatchString(key)
}

// IsValidWorkflowColumnKey exposes the persisted project-local key grammar to
// the public application validator without exposing workflow mutation policy.
func IsValidWorkflowColumnKey(key string) bool {
	return isValidColumnKey(key)
}

// HumanizeColumnKey converts a snake_case column key to Title Case.
// Example: "in_progress" → "In Progress", "custom_review" → "Custom Review"
func HumanizeColumnKey(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func workflowKeyFromName(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.Join(strings.Fields(key), "_")
	var b strings.Builder
	b.Grow(len(key))
	lastUnderscore := false
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r == '_':
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	key = strings.Trim(b.String(), "_")
	if len(key) > maxSlugLen {
		key = strings.Trim(key[:maxSlugLen], "_")
	}
	if key == "" {
		return "lane"
	}
	return key
}

func uniqueWorkflowKey(baseKey string, used map[string]struct{}) (string, error) {
	if _, exists := used[baseKey]; !exists && isValidColumnKey(baseKey) {
		return baseKey, nil
	}
	for i := 2; i <= maxWorkflowKeyAttempts; i++ {
		suffix := fmt.Sprintf("_%d", i)
		candidate := baseKey
		if len(candidate)+len(suffix) > maxSlugLen {
			candidate = strings.Trim(candidate[:maxSlugLen-len(suffix)], "_")
		}
		if candidate == "" {
			candidate = "lane"
			if len(candidate)+len(suffix) > maxSlugLen {
				candidate = candidate[:maxSlugLen-len(suffix)]
			}
		}
		candidate += suffix
		if !isValidColumnKey(candidate) {
			continue
		}
		if _, exists := used[candidate]; exists {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("%w: could not generate unique workflow column key", ErrConflict)
}

func defaultWorkflowColumns() []WorkflowColumn {
	return []WorkflowColumn{
		{Key: DefaultColumnBacklog, Name: "Backlog", Color: "#9CA3AF", Position: 0, IsDone: false, System: true},
		{Key: DefaultColumnNotStarted, Name: "Not Started", Color: "#F59E0B", Position: 1, IsDone: false, System: true},
		{Key: DefaultColumnDoing, Name: "In Progress", Color: "#10B981", Position: 2, IsDone: false, System: true},
		{Key: DefaultColumnTesting, Name: "Testing", Color: "#3B82F6", Position: 3, IsDone: false, System: true},
		{Key: DefaultColumnDone, Name: "Done", Color: "#EF4444", Position: 4, IsDone: true, System: true},
	}
}

func (s *Store) EnsureDefaultWorkflowColumns(ctx context.Context, projectID int64) error {
	return s.ensureDefaultWorkflowColumnsExec(ctx, s.db, s.db, projectID)
}

func (s *Store) ensureDefaultWorkflowColumnsTx(ctx context.Context, tx *sql.Tx, projectID int64) error {
	return s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, projectID)
}

func (s *Store) ensureDefaultWorkflowColumnsExec(ctx context.Context, execer sqlExecer, queryer sqlRowQueryer, projectID int64) error {
	for _, col := range defaultWorkflowColumns() {
		if _, err := execer.ExecContext(ctx, `
INSERT OR IGNORE INTO project_workflow_columns(project_id, key, name, color, position, is_done, system)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			projectID, col.Key, col.Name, col.Color, col.Position, boolToInt(col.IsDone), boolToInt(col.System)); err != nil {
			return fmt.Errorf("ensure workflow column %q: %w", col.Key, err)
		}
	}
	return validateExactlyOneDoneColumn(ctx, queryer, projectID)
}

func (s *Store) InsertWorkflowColumns(ctx context.Context, projectID int64, cols []WorkflowColumn) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin workflow insert tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.insertWorkflowColumnsTx(ctx, tx, projectID, cols); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow insert tx: %w", err)
	}
	return nil
}

func (s *Store) insertWorkflowColumnsTx(ctx context.Context, tx *sql.Tx, projectID int64, cols []WorkflowColumn) error {
	return s.insertWorkflowColumnsExec(ctx, tx, projectID, cols)
}

// deleteProjectWorkflowColumnsExec removes all workflow columns for a project.
// Used before importing custom workflow columns.
func (s *Store) deleteProjectWorkflowColumnsExec(ctx context.Context, execer sqlExecer, projectID int64) error {
	if _, err := execer.ExecContext(ctx, `DELETE FROM project_workflow_columns WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete workflow columns: %w", err)
	}
	return nil
}

func (s *Store) insertWorkflowColumnsExec(ctx context.Context, execer sqlExecer, projectID int64, cols []WorkflowColumn) error {
	if len(cols) < 2 {
		return fmt.Errorf("%w: project workflow must have at least 2 columns", ErrValidation)
	}
	seen := make(map[string]struct{}, len(cols))
	doneCount := 0
	for i := range cols {
		cols[i].Name = strings.TrimSpace(cols[i].Name)
		cols[i].Key = strings.TrimSpace(strings.ToLower(cols[i].Key))
		cols[i].Color = strings.TrimSpace(cols[i].Color)
		cols[i].Position = i
		cols[i].System = false // custom workflow at project creation is always user-defined
		if cols[i].Color == "" {
			cols[i].Color = "#64748b"
		}

		if cols[i].Name == "" {
			return fmt.Errorf("%w: workflow column name cannot be empty", ErrValidation)
		}
		if !isValidColumnKey(cols[i].Key) {
			return fmt.Errorf("%w: invalid workflow column key %q", ErrValidation, cols[i].Key)
		}
		if !colorHexRe.MatchString(cols[i].Color) {
			return fmt.Errorf("%w: invalid workflow column color %q", ErrValidation, cols[i].Color)
		}
		if _, ok := seen[cols[i].Key]; ok {
			return fmt.Errorf("%w: duplicate workflow column key %q", ErrValidation, cols[i].Key)
		}
		seen[cols[i].Key] = struct{}{}
		if cols[i].IsDone {
			doneCount++
		}
	}
	if doneCount != 1 {
		return fmt.Errorf("%w: project workflow must have exactly one done column", ErrValidation)
	}

	for _, col := range cols {
		if _, err := execer.ExecContext(ctx, `
INSERT INTO project_workflow_columns(project_id, key, name, color, position, is_done, system)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			projectID, col.Key, col.Name, col.Color, col.Position, boolToInt(col.IsDone), boolToInt(col.System)); err != nil {
			return fmt.Errorf("insert workflow column %q: %w", col.Key, err)
		}
	}
	return nil
}

func (s *Store) GetProjectWorkflow(ctx context.Context, projectID int64) ([]WorkflowColumn, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position, is_done, system
FROM project_workflow_columns
WHERE project_id = ?
ORDER BY position ASC, id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list workflow columns: %w", err)
	}
	defer rows.Close()

	out := make([]WorkflowColumn, 0, 8)
	for rows.Next() {
		var col WorkflowColumn
		var isDone, isSystem int
		if err := rows.Scan(&col.ID, &col.ProjectID, &col.Key, &col.Name, &col.Color, &col.Position, &isDone, &isSystem); err != nil {
			return nil, fmt.Errorf("scan workflow column: %w", err)
		}
		col.IsDone = isDone == 1
		col.System = isSystem == 1
		out = append(out, col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows workflow columns: %w", err)
	}
	if len(out) == 0 {
		if err := s.EnsureDefaultWorkflowColumns(ctx, projectID); err != nil {
			return nil, err
		}
		return s.GetProjectWorkflow(ctx, projectID)
	}
	return out, nil
}

func (s *Store) GetProjectWorkflows(ctx context.Context, projectIDs []int64) (map[int64][]WorkflowColumn, error) {
	out := make(map[int64][]WorkflowColumn, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(projectIDs))
	seen := make(map[int64]struct{}, len(projectIDs))
	for _, projectID := range projectIDs {
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		args = append(args, projectID)
		out[projectID] = []WorkflowColumn{}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position, is_done, system
FROM project_workflow_columns
WHERE project_id IN `+makePlaceholders(len(args))+`
ORDER BY project_id ASC, position ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list workflow columns for projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var col WorkflowColumn
		var isDone, isSystem int
		if err := rows.Scan(&col.ID, &col.ProjectID, &col.Key, &col.Name, &col.Color, &col.Position, &isDone, &isSystem); err != nil {
			return nil, fmt.Errorf("scan workflow column for project: %w", err)
		}
		col.IsDone = isDone == 1
		col.System = isSystem == 1
		out[col.ProjectID] = append(out[col.ProjectID], col)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows workflow columns for projects: %w", err)
	}
	return out, nil
}

func (s *Store) GetWorkflowDoneColumnKey(ctx context.Context, projectID int64) (string, error) {
	var key string
	if err := s.db.QueryRowContext(ctx, `
SELECT key FROM project_workflow_columns
WHERE project_id = ? AND is_done = 1
LIMIT 1`, projectID).Scan(&key); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("%w: project has no done column", ErrValidation)
		}
		return "", fmt.Errorf("get done column key: %w", err)
	}
	return key, nil
}

// UpdateWorkflowColumn sets the display name and color for a workflow lane. Key, position, is_done, and system are unchanged.
func (s *Store) UpdateWorkflowColumn(ctx context.Context, projectID int64, key, name, color string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: invalid workflow column key", ErrValidation)
	}
	name = strings.TrimSpace(name)
	color = strings.TrimSpace(color)
	if name == "" || len(name) > maxWorkflowNameLength {
		return fmt.Errorf("%w: invalid workflow column name", ErrValidation)
	}
	if color == "" || !colorHexRe.MatchString(color) {
		return fmt.Errorf("%w: invalid workflow column color", ErrValidation)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE project_workflow_columns
SET name = ?, color = ?
WHERE project_id = ? AND key = ?`, name, color, projectID, key)
	if err != nil {
		return fmt.Errorf("update workflow column: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected update workflow column: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddWorkflowColumn(ctx context.Context, projectID int64, name string) (WorkflowColumn, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxWorkflowNameLength {
		return WorkflowColumn{}, fmt.Errorf("%w: invalid workflow column name", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return WorkflowColumn{}, fmt.Errorf("begin add workflow column tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	loadWorkflow := func() ([]WorkflowColumn, error) {
		rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position, is_done, system
FROM project_workflow_columns
WHERE project_id = ?
ORDER BY position ASC, id ASC`, projectID)
		if err != nil {
			return nil, fmt.Errorf("list workflow columns for add: %w", err)
		}
		defer rows.Close()
		out := make([]WorkflowColumn, 0, 8)
		for rows.Next() {
			var col WorkflowColumn
			var isDone, isSystem int
			if err := rows.Scan(&col.ID, &col.ProjectID, &col.Key, &col.Name, &col.Color, &col.Position, &isDone, &isSystem); err != nil {
				return nil, fmt.Errorf("scan workflow column for add: %w", err)
			}
			col.IsDone = isDone == 1
			col.System = isSystem == 1
			out = append(out, col)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows workflow columns for add: %w", err)
		}
		return out, nil
	}

	workflow, err := loadWorkflow()
	if err != nil {
		return WorkflowColumn{}, err
	}
	if len(workflow) == 0 {
		if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, projectID); err != nil {
			return WorkflowColumn{}, err
		}
		workflow, err = loadWorkflow()
		if err != nil {
			return WorkflowColumn{}, err
		}
	}
	if len(workflow) >= maxWorkflowColumns {
		return WorkflowColumn{}, fmt.Errorf("%w: workflow may have at most %d columns", ErrValidation, maxWorkflowColumns)
	}

	doneIdx := -1
	doneCount := 0
	usedKeys := make(map[string]struct{}, len(workflow))
	for i, col := range workflow {
		usedKeys[col.Key] = struct{}{}
		if col.IsDone {
			doneIdx = i
			doneCount++
		}
	}
	if doneCount != 1 || doneIdx < 0 {
		return WorkflowColumn{}, fmt.Errorf("%w: project workflow must have exactly one done column", ErrValidation)
	}

	baseKey := workflowKeyFromName(name)
	key, err := uniqueWorkflowKey(baseKey, usedKeys)
	if err != nil {
		return WorkflowColumn{}, err
	}

	insertPos := doneIdx
	for i := range workflow {
		nextPos := i
		if i >= doneIdx {
			nextPos = i + 1
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE project_workflow_columns
SET position = ?
WHERE id = ?`, nextPos, workflow[i].ID); err != nil {
			return WorkflowColumn{}, fmt.Errorf("shift workflow column positions: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx, `
INSERT INTO project_workflow_columns(project_id, key, name, color, position, is_done, system)
VALUES (?, ?, ?, ?, ?, 0, 0)`, projectID, key, name, defaultWorkflowColor, insertPos)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return WorkflowColumn{}, fmt.Errorf("%w: workflow column key already exists", ErrConflict)
		}
		return WorkflowColumn{}, fmt.Errorf("insert workflow column: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return WorkflowColumn{}, fmt.Errorf("last insert id workflow column: %w", err)
	}

	if err := validateExactlyOneDoneColumn(ctx, tx, projectID); err != nil {
		return WorkflowColumn{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkflowColumn{}, fmt.Errorf("commit add workflow column tx: %w", err)
	}

	return WorkflowColumn{
		ID:        id,
		ProjectID: projectID,
		Key:       key,
		Name:      name,
		Color:     defaultWorkflowColor,
		Position:  insertPos,
		IsDone:    false,
		System:    false,
	}, nil
}

func (s *Store) DeleteWorkflowColumn(ctx context.Context, projectID int64, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return fmt.Errorf("%w: invalid workflow column key", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete workflow column tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	loadWorkflow := func() ([]WorkflowColumn, error) {
		rows, err := tx.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position, is_done, system
FROM project_workflow_columns
WHERE project_id = ?
ORDER BY position ASC, id ASC`, projectID)
		if err != nil {
			return nil, fmt.Errorf("list workflow columns for delete: %w", err)
		}
		defer rows.Close()
		out := make([]WorkflowColumn, 0, 8)
		for rows.Next() {
			var col WorkflowColumn
			var isDone, isSystem int
			if err := rows.Scan(&col.ID, &col.ProjectID, &col.Key, &col.Name, &col.Color, &col.Position, &isDone, &isSystem); err != nil {
				return nil, fmt.Errorf("scan workflow column for delete: %w", err)
			}
			col.IsDone = isDone == 1
			col.System = isSystem == 1
			out = append(out, col)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows workflow columns for delete: %w", err)
		}
		return out, nil
	}

	workflow, err := loadWorkflow()
	if err != nil {
		return err
	}
	if len(workflow) == 0 {
		if err := s.ensureDefaultWorkflowColumnsExec(ctx, tx, tx, projectID); err != nil {
			return err
		}
		workflow, err = loadWorkflow()
		if err != nil {
			return err
		}
	}

	targetIdx := -1
	for i, col := range workflow {
		if col.Key == key {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return ErrNotFound
	}
	target := workflow[targetIdx]
	if target.IsDone {
		return fmt.Errorf("%w: cannot delete done workflow column", ErrValidation)
	}
	if len(workflow) <= 2 {
		return fmt.Errorf("%w: project workflow must have at least 2 columns", ErrValidation)
	}

	var todoCount int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos
WHERE project_id = ? AND column_key = ?`, projectID, key).Scan(&todoCount); err != nil {
		return fmt.Errorf("count todos for workflow column delete: %w", err)
	}
	if todoCount > 0 {
		return fmt.Errorf("%w: workflow column is not empty", ErrConflict)
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM project_workflow_columns
WHERE id = ?`, target.ID); err != nil {
		return fmt.Errorf("delete workflow column: %w", err)
	}

	nextPos := 0
	for i, col := range workflow {
		if i == targetIdx {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE project_workflow_columns
SET position = ?
WHERE id = ?`, nextPos, col.ID); err != nil {
			return fmt.Errorf("resequence workflow columns after delete: %w", err)
		}
		nextPos++
	}

	if err := validateExactlyOneDoneColumn(ctx, tx, projectID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete workflow column tx: %w", err)
	}
	return nil
}

func (s *Store) ValidateProjectColumnKey(ctx context.Context, projectID int64, columnKey string) (WorkflowColumn, error) {
	return validateProjectColumnKeyQueryer(ctx, s.db, projectID, columnKey)
}

func validateProjectColumnKeyTx(ctx context.Context, tx *sql.Tx, projectID int64, columnKey string) (WorkflowColumn, error) {
	return validateProjectColumnKeyQueryer(ctx, tx, projectID, columnKey)
}

func validateProjectColumnKeyQueryer(ctx context.Context, q sqlRowQueryer, projectID int64, columnKey string) (WorkflowColumn, error) {
	var col WorkflowColumn
	var isDone, isSystem int
	if err := q.QueryRowContext(ctx, `
SELECT id, project_id, key, name, color, position, is_done, system
FROM project_workflow_columns
WHERE project_id = ? AND key = ?
LIMIT 1`, projectID, columnKey).Scan(&col.ID, &col.ProjectID, &col.Key, &col.Name, &col.Color, &col.Position, &isDone, &isSystem); err != nil {
		if err == sql.ErrNoRows {
			return WorkflowColumn{}, fmt.Errorf("%w: invalid columnKey", ErrValidation)
		}
		return WorkflowColumn{}, fmt.Errorf("validate project column key: %w", err)
	}
	col.IsDone = isDone == 1
	col.System = isSystem == 1
	return col, nil
}

func (s *Store) validateExactlyOneDoneColumn(ctx context.Context, projectID int64) error {
	return validateExactlyOneDoneColumn(ctx, s.db, projectID)
}

func validateExactlyOneDoneColumn(ctx context.Context, queryer sqlRowQueryer, projectID int64) error {
	var cnt int
	if err := queryer.QueryRowContext(ctx, `
SELECT COUNT(*) FROM project_workflow_columns
WHERE project_id = ? AND is_done = 1`, projectID).Scan(&cnt); err != nil {
		return fmt.Errorf("count done columns: %w", err)
	}
	if cnt != 1 {
		return fmt.Errorf("%w: project workflow must have exactly one done column", ErrValidation)
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
