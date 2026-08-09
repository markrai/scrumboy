package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SprintState represents the lifecycle state of a sprint.
const (
	SprintStatePlanned = "PLANNED"
	SprintStateActive  = "ACTIVE"
	SprintStateClosed  = "CLOSED"
)

// DefaultSprintStart returns Monday 9:00 AM for the week containing now.
func DefaultSprintStart(now time.Time) time.Time {
	loc := now.Location()
	weekday := int(now.Weekday()) // 0=Sun, 1=Mon, ..., 6=Sat
	daysToMonday := (weekday + 6) % 7
	monday := now.AddDate(0, 0, -daysToMonday)
	monday = time.Date(
		monday.Year(),
		monday.Month(),
		monday.Day(),
		0, 0, 0, 0,
		loc,
	)
	return monday.Add(9 * time.Hour)
}

// DefaultSprintEnd returns 23:59 local time for the sprint end date.
func DefaultSprintEnd(start time.Time, weeks int) time.Time {
	if weeks != 1 && weeks != 2 {
		weeks = 2
	}
	days := weeks*7 - 1
	end := start.AddDate(0, 0, days)
	return time.Date(
		end.Year(),
		end.Month(),
		end.Day(),
		23, 59, 0, 0,
		end.Location(),
	)
}

// Sprint represents a project-scoped sprint.
// planned_* fields are user-entered schedule intent.
// started_at/closed_at are actual lifecycle timestamps captured on state transitions.
type Sprint struct {
	ID             int64
	ProjectID      int64
	Number         int64
	Name           string
	PlannedStartAt time.Time
	PlannedEndAt   time.Time
	StartedAt      *time.Time
	ClosedAt       *time.Time
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ActiveSprintInfo is the minimal active-sprint data for dashboard/API.
// StartAt/EndAt map to planned_start_at/planned_end_at for display.
type ActiveSprintInfo struct {
	ID      int64
	Name    string
	StartAt int64 // Unix ms
	EndAt   int64 // Unix ms
}

func lockProjectSprintsEnabledTx(ctx context.Context, tx *sql.Tx, projectID int64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET updated_at = updated_at
		WHERE id = ? AND sprints_enabled = 1
	`, projectID)
	if err != nil {
		return fmt.Errorf("lock project sprint capability: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("read project sprint capability lock result: %w", err)
	}
	if affected > 0 {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)`, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("check project sprint capability: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrSprintsDisabled
}

// serializeProjectWriteTx acquires SQLite's writer reservation without
// changing project data. Call it before reads in mutations that must later
// check sprint capability, so a concurrent disable cannot invalidate a read
// snapshot and turn the capability check into SQLITE_BUSY.
func serializeProjectWriteTx(ctx context.Context, tx *sql.Tx, projectID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID); err != nil {
		return fmt.Errorf("serialize project write: %w", err)
	}
	return nil
}

func serializeSprintProjectWriteTx(ctx context.Context, tx *sql.Tx, sprintID int64) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE projects
		SET updated_at = updated_at
		WHERE id = (SELECT project_id FROM sprints WHERE id = ?)
	`, sprintID); err != nil {
		return fmt.Errorf("serialize sprint project write: %w", err)
	}
	return nil
}

func sprintNameExistsTx(ctx context.Context, tx *sql.Tx, projectID int64, name string, excludeID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx,
		`SELECT 1 FROM sprints WHERE project_id = ? AND name = ? AND id != ? LIMIT 1`,
		projectID, name, excludeID,
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check sprint name uniqueness: %w", err)
	}
	return true, nil
}

func (s *Store) CreateSprint(ctx context.Context, projectID int64, name string, plannedStartAt, plannedEndAt time.Time) (Sprint, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return Sprint{}, fmt.Errorf("%w: invalid sprint name", ErrValidation)
	}
	if !plannedEndAt.After(plannedStartAt) && !plannedEndAt.Equal(plannedStartAt) {
		return Sprint{}, fmt.Errorf("%w: end_at must be >= start_at", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sprint{}, fmt.Errorf("begin create sprint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProjectSprintsEnabledTx(ctx, tx, projectID); err != nil {
		return Sprint{}, err
	}
	if dup, err := sprintNameExistsTx(ctx, tx, projectID, name, 0); err != nil {
		return Sprint{}, err
	} else if dup {
		return Sprint{}, fmt.Errorf("%w: a sprint with this name already exists in the project", ErrValidation)
	}

	nowMs := time.Now().UTC().UnixMilli()
	startAtMs := plannedStartAt.UnixMilli()
	endAtMs := plannedEndAt.UnixMilli()

	res, err := tx.ExecContext(ctx, `
			INSERT INTO sprints (
				project_id,
				name,
				planned_start_at,
				planned_end_at,
				state,
				number,
				created_at,
				updated_at
			)
			SELECT
				?, ?, ?, ?,
				?,
				COALESCE(MAX(number), 0) + 1,
				?, ?
			FROM sprints
			WHERE project_id = ?
		`, projectID, name, startAtMs, endAtMs, SprintStatePlanned, nowMs, nowMs, projectID)
	if err != nil {
		return Sprint{}, fmt.Errorf("insert sprint: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Sprint{}, fmt.Errorf("last insert id sprint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Sprint{}, fmt.Errorf("commit create sprint: %w", err)
	}
	return s.GetSprintByID(ctx, id)
}

func (s *Store) ListSprints(ctx context.Context, projectID int64) ([]Sprint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, number, name, planned_start_at, planned_end_at, started_at, closed_at, state, created_at, updated_at
		FROM sprints
		WHERE project_id = ?
		ORDER BY number ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list sprints: %w", err)
	}
	defer rows.Close()
	var out []Sprint
	for rows.Next() {
		var sp Sprint
		var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
		var startedAtMs, closedAtMs sql.NullInt64
		if err := rows.Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs); err != nil {
			return nil, fmt.Errorf("scan sprint: %w", err)
		}
		sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
		sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
		if startedAtMs.Valid {
			t := time.UnixMilli(startedAtMs.Int64).UTC()
			sp.StartedAt = &t
		}
		if closedAtMs.Valid {
			t := time.UnixMilli(closedAtMs.Int64).UTC()
			sp.ClosedAt = &t
		}
		sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows sprints: %w", err)
	}
	return out, nil
}

func (s *Store) HasSprints(ctx context.Context, projectID int64) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1
		FROM sprints
		WHERE project_id = ?
		LIMIT 1
	`, projectID).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("has sprints: %w", err)
	}
	return true, nil
}

func (s *Store) GetSprintByID(ctx context.Context, sprintID int64) (Sprint, error) {
	var sp Sprint
	var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
	var startedAtMs, closedAtMs sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, number, name, planned_start_at, planned_end_at, started_at, closed_at, state, created_at, updated_at
		FROM sprints WHERE id = ?
	`, sprintID).Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, fmt.Errorf("get sprint: %w", err)
	}
	sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
	sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
	if startedAtMs.Valid {
		t := time.UnixMilli(startedAtMs.Int64).UTC()
		sp.StartedAt = &t
	}
	if closedAtMs.Valid {
		t := time.UnixMilli(closedAtMs.Int64).UTC()
		sp.ClosedAt = &t
	}
	sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return sp, nil
}

func (s *Store) GetSprintByProjectNumber(ctx context.Context, projectID, number int64) (Sprint, error) {
	var sp Sprint
	var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
	var startedAtMs, closedAtMs sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, number, name, planned_start_at, planned_end_at, started_at, closed_at, state, created_at, updated_at
		FROM sprints
		WHERE project_id = ? AND number = ?
		LIMIT 1
	`, projectID, number).Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, fmt.Errorf("get sprint by project number: %w", err)
	}
	sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
	sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
	if startedAtMs.Valid {
		t := time.UnixMilli(startedAtMs.Int64).UTC()
		sp.StartedAt = &t
	}
	if closedAtMs.Valid {
		t := time.UnixMilli(closedAtMs.Int64).UTC()
		sp.ClosedAt = &t
	}
	sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return sp, nil
}

func (s *Store) GetActiveSprintByProjectID(ctx context.Context, projectID int64) (*Sprint, error) {
	var sp Sprint
	var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
	var startedAtMs, closedAtMs sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.project_id, s.number, s.name, s.planned_start_at, s.planned_end_at, s.started_at, s.closed_at, s.state, s.created_at, s.updated_at
		FROM sprints s
		JOIN projects p ON p.id = s.project_id AND p.sprints_enabled = 1
		WHERE s.project_id = ? AND s.state = ?
		LIMIT 1
	`, projectID, SprintStateActive).Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get active sprint: %w", err)
	}
	sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
	sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
	if startedAtMs.Valid {
		t := time.UnixMilli(startedAtMs.Int64).UTC()
		sp.StartedAt = &t
	}
	if closedAtMs.Valid {
		t := time.UnixMilli(closedAtMs.Int64).UTC()
		sp.ClosedAt = &t
	}
	sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return &sp, nil
}

// GetActiveSprintsByProjectIDs returns active sprint info per project in one batched query.
// Keys are project IDs; value is nil if no active sprint.
func (s *Store) GetActiveSprintsByProjectIDs(ctx context.Context, projectIDs []int64) (map[int64]*ActiveSprintInfo, error) {
	if len(projectIDs) == 0 {
		return map[int64]*ActiveSprintInfo{}, nil
	}
	// Build placeholders for IN clause
	args := make([]any, 0, len(projectIDs)+1)
	args = append(args, SprintStateActive)
	for _, id := range projectIDs {
		args = append(args, id)
	}
	placeholders := makePlaceholders(len(projectIDs))
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.project_id, s.name, s.planned_start_at, s.planned_end_at
		FROM sprints s
		JOIN projects p ON p.id = s.project_id AND p.sprints_enabled = 1
		WHERE s.state = ? AND s.project_id IN `+placeholders,
		args...)
	if err != nil {
		return nil, fmt.Errorf("get active sprints by project ids: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]*ActiveSprintInfo)
	for rows.Next() {
		var id, projectID int64
		var name string
		var startAtMs, endAtMs int64
		if err := rows.Scan(&id, &projectID, &name, &startAtMs, &endAtMs); err != nil {
			return nil, fmt.Errorf("scan active sprint: %w", err)
		}
		out[projectID] = &ActiveSprintInfo{ID: id, Name: name, StartAt: startAtMs, EndAt: endAtMs}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows active sprints: %w", err)
	}
	return out, nil
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return "()"
	}
	b := make([]byte, 0, n*4+2)
	b = append(b, '(')
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	b = append(b, ')')
	return string(b)
}

func (s *Store) ActivateSprint(ctx context.Context, projectID, sprintID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin activate sprint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return err
	}

	sp, err := s.getSprintByIDTx(ctx, tx, sprintID)
	if err != nil {
		return err
	}
	if sp.ProjectID != projectID {
		return fmt.Errorf("%w: sprint does not belong to project", ErrValidation)
	}
	if err := lockProjectSprintsEnabledTx(ctx, tx, projectID); err != nil {
		return err
	}
	if sp.State == SprintStateActive {
		return nil // already active
	}
	if sp.State != SprintStatePlanned {
		return fmt.Errorf("%w: sprint must be PLANNED to activate", ErrValidation)
	}
	nowMs := time.Now().UTC().UnixMilli()
	if nowMs >= sp.PlannedEndAt.UnixMilli() {
		return fmt.Errorf("%w: sprint end date is on or before now; cannot activate", ErrValidation)
	}

	// Deactivate current ACTIVE for this project
	if _, err := tx.ExecContext(ctx, `
		UPDATE sprints
		SET state = ?, closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE project_id = ? AND state = ?
	`, SprintStateClosed, nowMs, nowMs, projectID, SprintStateActive); err != nil {
		return fmt.Errorf("deactivate current sprint: %w", err)
	}
	// Activate the given sprint
	res, err := tx.ExecContext(ctx, `
		UPDATE sprints
		SET state = ?, started_at = COALESCE(started_at, ?), updated_at = ?
		WHERE id = ? AND state = ?
	`, SprintStateActive, nowMs, nowMs, sprintID, SprintStatePlanned)
	if err != nil {
		return fmt.Errorf("activate sprint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) CloseSprint(ctx context.Context, projectID, sprintID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin close sprint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProjectSprintsEnabledTx(ctx, tx, projectID); err != nil {
		return err
	}
	nowMs := time.Now().UTC().UnixMilli()
	res, err := tx.ExecContext(ctx, `
		UPDATE sprints
		SET state = ?, closed_at = COALESCE(closed_at, ?), updated_at = ?
		WHERE id = ? AND project_id = ? AND state = ?
	`, SprintStateClosed, nowMs, nowMs, sprintID, projectID, SprintStateActive)
	if err != nil {
		return fmt.Errorf("close sprint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit close sprint: %w", err)
	}
	return nil
}

// SprintWithTodoCount extends Sprint with todo count for delete confirmation.
type SprintWithTodoCount struct {
	Sprint
	TodoCount int64
}

// UpdateSprintInput holds optional fields for sprint update (state-dependent).
type UpdateSprintInput struct {
	Name           *string
	PlannedStartAt *time.Time
	PlannedEndAt   *time.Time
}

func (s *Store) DeleteSprint(ctx context.Context, projectID, sprintID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete sprint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return err
	}
	sp, err := s.getSprintByIDTx(ctx, tx, sprintID)
	if err != nil {
		return err
	}
	if sp.ProjectID != projectID {
		return ErrNotFound
	}
	if err := lockProjectSprintsEnabledTx(ctx, tx, projectID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM sprints WHERE id = ?`, sprintID)
	if err != nil {
		return fmt.Errorf("delete sprint: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete sprint: %w", err)
	}
	return nil
}

func (s *Store) UpdateSprint(ctx context.Context, sprintID int64, in UpdateSprintInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update sprint: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeSprintProjectWriteTx(ctx, tx, sprintID); err != nil {
		return err
	}
	sp, err := s.getSprintByIDTx(ctx, tx, sprintID)
	if err != nil {
		return err
	}
	if err := lockProjectSprintsEnabledTx(ctx, tx, sp.ProjectID); err != nil {
		return err
	}
	// Enforce state rules: reject forbidden fields, do not partially apply.
	switch sp.State {
	case SprintStatePlanned:
		if in.Name != nil || in.PlannedStartAt != nil || in.PlannedEndAt != nil {
			// all allowed
		}
	case SprintStateActive:
		if in.Name != nil || in.PlannedStartAt != nil {
			return fmt.Errorf("%w: only endAt can be updated for ACTIVE sprint", ErrValidation)
		}
		if in.PlannedEndAt == nil {
			return nil // nothing to update
		}
	case SprintStateClosed:
		if in.PlannedStartAt != nil || in.PlannedEndAt != nil {
			return fmt.Errorf("%w: dates cannot be updated for CLOSED sprint", ErrValidation)
		}
		if in.Name == nil {
			return nil // nothing to update
		}
	}

	// Build update
	var set []string
	var args []any
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 200 {
			return fmt.Errorf("%w: invalid sprint name", ErrValidation)
		}
		if dup, err := sprintNameExistsTx(ctx, tx, sp.ProjectID, name, sprintID); err != nil {
			return err
		} else if dup {
			return fmt.Errorf("%w: a sprint with this name already exists in the project", ErrValidation)
		}
		set = append(set, "name = ?")
		args = append(args, name)
	}
	if in.PlannedStartAt != nil {
		if sp.State != SprintStatePlanned {
			return fmt.Errorf("%w: startAt cannot be updated for this sprint state", ErrValidation)
		}
		set = append(set, "planned_start_at = ?")
		args = append(args, in.PlannedStartAt.UnixMilli())
	}
	if in.PlannedEndAt != nil {
		if sp.State == SprintStateClosed {
			return fmt.Errorf("%w: endAt cannot be updated for CLOSED sprint", ErrValidation)
		}
		startAtMs := sp.PlannedStartAt.UnixMilli()
		if in.PlannedStartAt != nil {
			startAtMs = in.PlannedStartAt.UnixMilli()
		}
		if in.PlannedEndAt.UnixMilli() < startAtMs {
			return fmt.Errorf("%w: end_at must be >= start_at", ErrValidation)
		}
		set = append(set, "planned_end_at = ?")
		args = append(args, in.PlannedEndAt.UnixMilli())
	}
	if len(set) == 0 {
		return nil
	}
	set = append(set, "updated_at = ?")
	args = append(args, time.Now().UTC().UnixMilli())
	args = append(args, sprintID)
	query := `UPDATE sprints SET ` + strings.Join(set, ", ") + ` WHERE id = ?`
	_, err = tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update sprint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update sprint: %w", err)
	}
	return nil
}

// CountUnscheduledTodos returns the number of todos in the project with sprint_id IS NULL (backlog).
func (s *Store) CountUnscheduledTodos(ctx context.Context, projectID int64) (int64, error) {
	var c int64
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM todos WHERE project_id = ? AND sprint_id IS NULL
	`, projectID).Scan(&c)
	if err != nil {
		return 0, fmt.Errorf("count unscheduled todos: %w", err)
	}
	return c, nil
}

func (s *Store) ListSprintsWithTodoCount(ctx context.Context, projectID int64) ([]SprintWithTodoCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.project_id, s.number, s.name, s.planned_start_at, s.planned_end_at, s.started_at, s.closed_at, s.state, s.created_at, s.updated_at,
		       COALESCE(t.cnt, 0) as todo_count
		FROM sprints s
		LEFT JOIN (SELECT sprint_id, COUNT(*) as cnt FROM todos WHERE sprint_id IS NOT NULL GROUP BY sprint_id) t ON t.sprint_id = s.id
		WHERE s.project_id = ?
		ORDER BY s.number ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list sprints with todo count: %w", err)
	}
	defer rows.Close()
	var out []SprintWithTodoCount
	for rows.Next() {
		var sp SprintWithTodoCount
		var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
		var startedAtMs, closedAtMs sql.NullInt64
		if err := rows.Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs, &sp.TodoCount); err != nil {
			return nil, fmt.Errorf("scan sprint: %w", err)
		}
		sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
		sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
		if startedAtMs.Valid {
			t := time.UnixMilli(startedAtMs.Int64).UTC()
			sp.StartedAt = &t
		}
		if closedAtMs.Valid {
			t := time.UnixMilli(closedAtMs.Int64).UTC()
			sp.ClosedAt = &t
		}
		sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows sprints: %w", err)
	}
	return out, nil
}

func (s *Store) getSprintByIDTx(ctx context.Context, tx *sql.Tx, sprintID int64) (Sprint, error) {
	var sp Sprint
	var startAtMs, endAtMs, createdAtMs, updatedAtMs int64
	var startedAtMs, closedAtMs sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT id, project_id, number, name, planned_start_at, planned_end_at, started_at, closed_at, state, created_at, updated_at
		FROM sprints WHERE id = ?
	`, sprintID).Scan(&sp.ID, &sp.ProjectID, &sp.Number, &sp.Name, &startAtMs, &endAtMs, &startedAtMs, &closedAtMs, &sp.State, &createdAtMs, &updatedAtMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, fmt.Errorf("get sprint: %w", err)
	}
	sp.PlannedStartAt = time.UnixMilli(startAtMs).UTC()
	sp.PlannedEndAt = time.UnixMilli(endAtMs).UTC()
	if startedAtMs.Valid {
		t := time.UnixMilli(startedAtMs.Int64).UTC()
		sp.StartedAt = &t
	}
	if closedAtMs.Valid {
		t := time.UnixMilli(closedAtMs.Int64).UTC()
		sp.ClosedAt = &t
	}
	sp.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	sp.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
	return sp, nil
}
