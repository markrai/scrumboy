package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	maxPriorityTiers     = 12
	defaultPriorityColor = "#64748b"
	maxPriorityNameLen   = 200
)

const (
	ReasonInvalidPriorityKey          = "invalid_priority_key"
	ReasonInvalidPriorityTierName     = "invalid_priority_tier_name"
	ReasonInvalidPriorityTierColor    = "invalid_priority_tier_color"
	ReasonPriorityTierLimitReached    = "priority_tier_limit_reached"
	ReasonPriorityTierMinimumRequired = "priority_tier_minimum_required"
	ReasonPriorityTierInUse           = "priority_tier_in_use"
)

type reasonedStoreError struct {
	base   error
	reason string
	msg    string
}

func (e *reasonedStoreError) Error() string  { return e.msg }
func (e *reasonedStoreError) Unwrap() error  { return e.base }
func (e *reasonedStoreError) Reason() string { return e.reason }

func priorityError(base error, reason, message string) error {
	return &reasonedStoreError{base: base, reason: reason, msg: message}
}

// ErrorReason returns a stable public classification only for explicitly
// reason-bearing domain errors. Dynamic import diagnostics intentionally do
// not use this mechanism.
func ErrorReason(err error) string {
	var reasoned interface{ Reason() string }
	if errors.As(err, &reasoned) {
		return reasoned.Reason()
	}
	return ""
}

// priorityTierDefinitionReason is shared by CRUD and import so both paths
// enforce the same persistent-state constraints. Import callers deliberately
// turn the classification into contextual raw diagnostics rather than
// exposing a reason containing imported/user-provided data.
func priorityTierDefinitionReason(key, name, color string, requireKey, allowEmptyColor bool) string {
	if requireKey && !isValidColumnKey(strings.ToLower(strings.TrimSpace(key))) {
		return ReasonInvalidPriorityKey
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxPriorityNameLen {
		return ReasonInvalidPriorityTierName
	}
	color = strings.TrimSpace(color)
	if (!allowEmptyColor || color != "") && !colorHexRe.MatchString(color) {
		return ReasonInvalidPriorityTierColor
	}
	return ""
}

func priorityTierCountReason(count int) string {
	if count > maxPriorityTiers {
		return ReasonPriorityTierLimitReached
	}
	return ""
}

func defaultPriorityTiers() []PriorityTier {
	return []PriorityTier{
		{Key: "low", Name: "Low", Color: "#9CA3AF", Position: 0},
		{Key: "medium", Name: "Medium", Color: "#F59E0B", Position: 1},
		{Key: "high", Name: "High", Color: "#F97316", Position: 2},
		{Key: "urgent", Name: "Urgent", Color: "#EF4444", Position: 3},
	}
}

func (s *Store) EnsureDefaultPriorityTiers(ctx context.Context, projectID int64) error {
	return s.ensureDefaultPriorityTiersExec(ctx, s.db, projectID)
}

func (s *Store) ensureDefaultPriorityTiersTx(ctx context.Context, tx *sql.Tx, projectID int64) error {
	return s.ensureDefaultPriorityTiersExec(ctx, tx, projectID)
}

func (s *Store) ensureDefaultPriorityTiersExec(ctx context.Context, execer sqlExecer, projectID int64) error {
	for _, tier := range defaultPriorityTiers() {
		if _, err := execer.ExecContext(ctx, `
INSERT OR IGNORE INTO project_priorities(project_id, key, name, color, position)
VALUES (?, ?, ?, ?, ?)`,
			projectID, tier.Key, tier.Name, tier.Color, tier.Position); err != nil {
			return fmt.Errorf("ensure priority tier %q: %w", tier.Key, err)
		}
	}
	return nil
}

// deleteProjectPrioritiesExec removes all priority tiers for a project.
// Used before importing custom priority tiers.
func (s *Store) deleteProjectPrioritiesExec(ctx context.Context, execer sqlExecer, projectID int64) error {
	if _, err := execer.ExecContext(ctx, `DELETE FROM project_priorities WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("delete priority tiers: %w", err)
	}
	return nil
}

// insertPriorityTiersExec inserts pre-validated, position-normalized priority tiers for a project.
// Used when importing custom priority tiers; callers must call deleteProjectPrioritiesExec first.
func (s *Store) insertPriorityTiersExec(ctx context.Context, execer sqlExecer, projectID int64, tiers []PriorityTier) error {
	for _, tier := range tiers {
		if _, err := execer.ExecContext(ctx, `
INSERT INTO project_priorities(project_id, key, name, color, position)
VALUES (?, ?, ?, ?, ?)`,
			projectID, tier.Key, tier.Name, tier.Color, tier.Position); err != nil {
			return fmt.Errorf("insert priority tier %q: %w", tier.Key, err)
		}
	}
	return nil
}

func (s *Store) GetProjectPriorities(ctx context.Context, projectID int64) ([]PriorityTier, error) {
	return s.getProjectPrioritiesQueryer(ctx, s.db, projectID)
}

type sqlRowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Store) getProjectPrioritiesQueryer(ctx context.Context, q sqlRowsQueryer, projectID int64) ([]PriorityTier, error) {
	rows, err := q.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position
FROM project_priorities
WHERE project_id = ?
ORDER BY position ASC, id ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list priority tiers: %w", err)
	}
	defer rows.Close()

	out := make([]PriorityTier, 0, 4)
	for rows.Next() {
		var tier PriorityTier
		if err := rows.Scan(&tier.ID, &tier.ProjectID, &tier.Key, &tier.Name, &tier.Color, &tier.Position); err != nil {
			return nil, fmt.Errorf("scan priority tier: %w", err)
		}
		out = append(out, tier)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows priority tiers: %w", err)
	}
	return out, nil
}

func (s *Store) GetProjectPrioritiesForProjects(ctx context.Context, projectIDs []int64) (map[int64][]PriorityTier, error) {
	out := make(map[int64][]PriorityTier, len(projectIDs))
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
		out[projectID] = []PriorityTier{}
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, key, name, color, position
FROM project_priorities
WHERE project_id IN `+makePlaceholders(len(args))+`
ORDER BY project_id ASC, position ASC, id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list priority tiers for projects: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tier PriorityTier
		if err := rows.Scan(&tier.ID, &tier.ProjectID, &tier.Key, &tier.Name, &tier.Color, &tier.Position); err != nil {
			return nil, fmt.Errorf("scan priority tier for project: %w", err)
		}
		out[tier.ProjectID] = append(out[tier.ProjectID], tier)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows priority tiers for projects: %w", err)
	}
	return out, nil
}

// UpdatePriorityTier sets the display name and color for a priority tier. Key and position are unchanged.
func (s *Store) UpdatePriorityTier(ctx context.Context, projectID int64, key, name, color string) error {
	key = strings.TrimSpace(key)
	name = strings.TrimSpace(name)
	color = strings.TrimSpace(color)
	switch reason := priorityTierDefinitionReason(key, name, color, true, false); reason {
	case ReasonInvalidPriorityKey:
		return priorityError(ErrValidation, reason, "validation error: invalid priority tier key")
	case ReasonInvalidPriorityTierName:
		return priorityError(ErrValidation, reason, "validation error: invalid priority tier name")
	case ReasonInvalidPriorityTierColor:
		return priorityError(ErrValidation, reason, "validation error: invalid priority tier color")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE project_priorities
SET name = ?, color = ?
WHERE project_id = ? AND key = ?`, name, color, projectID, key)
	if err != nil {
		return fmt.Errorf("update priority tier: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected update priority tier: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddPriorityTier(ctx context.Context, projectID int64, name string) (PriorityTier, error) {
	name = strings.TrimSpace(name)
	if priorityTierDefinitionReason("", name, defaultPriorityColor, false, false) == ReasonInvalidPriorityTierName {
		return PriorityTier{}, priorityError(ErrValidation, ReasonInvalidPriorityTierName, "validation error: invalid priority tier name")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return PriorityTier{}, fmt.Errorf("begin add priority tier tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return PriorityTier{}, err
	}

	tiers, err := s.getProjectPrioritiesQueryer(ctx, tx, projectID)
	if err != nil {
		return PriorityTier{}, err
	}
	if len(tiers) == 0 {
		return PriorityTier{}, fmt.Errorf("priority invariant: project %d has no priority tiers", projectID)
	}
	if priorityTierCountReason(len(tiers)+1) == ReasonPriorityTierLimitReached {
		return PriorityTier{}, priorityError(ErrValidation, ReasonPriorityTierLimitReached, fmt.Sprintf("validation error: project may have at most %d priority tiers", maxPriorityTiers))
	}

	usedKeys := make(map[string]struct{}, len(tiers))
	for _, tier := range tiers {
		usedKeys[tier.Key] = struct{}{}
	}

	baseKey := workflowKeyFromName(name)
	key, err := uniqueWorkflowKey(baseKey, usedKeys)
	if err != nil {
		return PriorityTier{}, err
	}

	position := len(tiers)
	res, err := tx.ExecContext(ctx, `
INSERT INTO project_priorities(project_id, key, name, color, position)
VALUES (?, ?, ?, ?, ?)`, projectID, key, name, defaultPriorityColor, position)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return PriorityTier{}, fmt.Errorf("%w: priority tier key already exists", ErrConflict)
		}
		return PriorityTier{}, fmt.Errorf("insert priority tier: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return PriorityTier{}, fmt.Errorf("last insert id priority tier: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return PriorityTier{}, fmt.Errorf("commit add priority tier tx: %w", err)
	}

	return PriorityTier{
		ID:        id,
		ProjectID: projectID,
		Key:       key,
		Name:      name,
		Color:     defaultPriorityColor,
		Position:  position,
	}, nil
}

func (s *Store) DeletePriorityTier(ctx context.Context, projectID int64, key string) error {
	key = strings.TrimSpace(key)
	if !isValidColumnKey(strings.ToLower(key)) {
		return priorityError(ErrValidation, ReasonInvalidPriorityKey, "validation error: invalid priority tier key")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete priority tier tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return err
	}

	tiers, err := s.getProjectPrioritiesQueryer(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if len(tiers) == 0 {
		return fmt.Errorf("priority invariant: project %d has no priority tiers", projectID)
	}

	targetIdx := -1
	for i, tier := range tiers {
		if tier.Key == key {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return ErrNotFound
	}
	if len(tiers) <= 1 {
		return priorityError(ErrValidation, ReasonPriorityTierMinimumRequired, "validation error: project must have at least 1 priority tier")
	}

	var todoCount int
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM todos
WHERE project_id = ? AND priority_key = ?`, projectID, key).Scan(&todoCount); err != nil {
		return fmt.Errorf("count todos for priority tier delete: %w", err)
	}
	if todoCount > 0 {
		return priorityError(ErrConflict, ReasonPriorityTierInUse, "conflict: priority tier is not empty")
	}

	if _, err := tx.ExecContext(ctx, `
DELETE FROM project_priorities
WHERE id = ?`, tiers[targetIdx].ID); err != nil {
		return fmt.Errorf("delete priority tier: %w", err)
	}

	nextPos := 0
	for i, tier := range tiers {
		if i == targetIdx {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE project_priorities
SET position = ?
WHERE id = ?`, nextPos, tier.ID); err != nil {
			return fmt.Errorf("resequence priority tiers after delete: %w", err)
		}
		nextPos++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete priority tier tx: %w", err)
	}
	return nil
}

func (s *Store) ValidateProjectPriorityKey(ctx context.Context, projectID int64, priorityKey string) (PriorityTier, error) {
	return validateProjectPriorityKeyQueryer(ctx, s.db, projectID, priorityKey)
}

func validateProjectPriorityKeyTx(ctx context.Context, tx *sql.Tx, projectID int64, priorityKey string) (PriorityTier, error) {
	return validateProjectPriorityKeyQueryer(ctx, tx, projectID, priorityKey)
}

func validateProjectPriorityKeyQueryer(ctx context.Context, q sqlRowQueryer, projectID int64, priorityKey string) (PriorityTier, error) {
	var tier PriorityTier
	if err := q.QueryRowContext(ctx, `
SELECT id, project_id, key, name, color, position
FROM project_priorities
WHERE project_id = ? AND key = ?
LIMIT 1`, projectID, priorityKey).Scan(&tier.ID, &tier.ProjectID, &tier.Key, &tier.Name, &tier.Color, &tier.Position); err != nil {
		if err == sql.ErrNoRows {
			return PriorityTier{}, priorityError(ErrValidation, ReasonInvalidPriorityKey, "validation error: invalid priorityKey")
		}
		return PriorityTier{}, fmt.Errorf("validate project priority key: %w", err)
	}
	return tier, nil
}

// CountTodosByPriorityKey returns per-priority-tier todo counts for a project.
// Todos with no priority set are excluded (unlike workflow columns, "unset" is a valid state, not a lane).
func (s *Store) CountTodosByPriorityKey(ctx context.Context, projectID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT priority_key, COUNT(*)
FROM todos
WHERE project_id = ? AND priority_key IS NOT NULL
GROUP BY priority_key`, projectID)
	if err != nil {
		return nil, fmt.Errorf("count todos by priority key: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, fmt.Errorf("scan todo priority count: %w", err)
		}
		out[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todo priority counts: %w", err)
	}
	return out, nil
}
