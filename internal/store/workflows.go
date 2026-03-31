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

func (s *Store) UpdateWorkflowColumnName(ctx context.Context, projectID int64, key, newName string) error {
	key = strings.TrimSpace(key)
	newName = strings.TrimSpace(newName)
	if newName == "" || len(newName) > 200 {
		return fmt.Errorf("%w: invalid workflow column name", ErrValidation)
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE project_workflow_columns
SET name = ?
WHERE project_id = ? AND key = ?`, newName, projectID, key)
	if err != nil {
		return fmt.Errorf("update workflow column name: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected update workflow column name: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
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
