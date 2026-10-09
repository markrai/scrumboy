package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ProjectPublicationState is the narrow persistence result for publication
// management. It is not an API or portable-export DTO.
type ProjectPublicationState struct {
	ProjectID int64
	Slug      string
	Enabled   bool
	Changed   bool
}

var reservedPublicProjectSlugs = map[string]struct{}{
	"agora":     {},
	"anon":      {},
	"api":       {},
	"auth":      {},
	"dashboard": {},
	"healthz":   {},
	"mcp":       {},
	"oauth":     {},
	"p":         {},
	"temp":      {},
}

func isReservedPublicProjectSlug(slug string) bool {
	_, reserved := reservedPublicProjectSlugs[slug]
	return reserved
}

// UpdateProjectPublicViewing atomically rechecks durable-project Maintainer
// membership, changes publication state, and appends its audit event. The
// application layer is responsible for the operator capability gate.
func (s *Store) UpdateProjectPublicViewing(ctx context.Context, projectID, actorUserID int64, enabled bool) (ProjectPublicationState, error) {
	if projectID <= 0 {
		return ProjectPublicationState{}, fmt.Errorf("%w: invalid project id", ErrValidation)
	}
	if actorUserID <= 0 {
		return ProjectPublicationState{}, ErrUnauthorized
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return ProjectPublicationState{}, fmt.Errorf("begin update project public viewing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := serializeProjectWriteTx(ctx, tx, projectID); err != nil {
		return ProjectPublicationState{}, err
	}

	var (
		slug       string
		expiresAt  sql.NullInt64
		currentInt int
	)
	err = tx.QueryRowContext(ctx, `
SELECT slug, expires_at, public_view_enabled
FROM projects
WHERE id = ? AND import_batch_id IS NULL`, projectID).Scan(&slug, &expiresAt, &currentInt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectPublicationState{}, ErrNotFound
	}
	if err != nil {
		return ProjectPublicationState{}, fmt.Errorf("get project publication state: %w", err)
	}
	if expiresAt.Valid {
		return ProjectPublicationState{}, ErrNotFound
	}

	role, err := s.getProjectRoleTx(ctx, tx, projectID, actorUserID)
	if err != nil {
		return ProjectPublicationState{}, err
	}
	if role != RoleMaintainer {
		if role.HasMinimumRole(RoleViewer) {
			return ProjectPublicationState{}, ErrForbidden
		}
		return ProjectPublicationState{}, ErrNotFound
	}
	if enabled && isReservedPublicProjectSlug(slug) {
		return ProjectPublicationState{}, fmt.Errorf("%w: project slug is reserved", ErrValidation)
	}

	current := currentInt == 1
	result := ProjectPublicationState{
		ProjectID: projectID,
		Slug:      slug,
		Enabled:   current,
	}
	if current == enabled {
		if err := tx.Commit(); err != nil {
			return ProjectPublicationState{}, fmt.Errorf("commit unchanged project public viewing: %w", err)
		}
		return result, nil
	}

	if _, err := tx.ExecContext(ctx, `UPDATE projects SET public_view_enabled = ? WHERE id = ?`, boolToInt(enabled), projectID); err != nil {
		return ProjectPublicationState{}, fmt.Errorf("update project public viewing: %w", err)
	}
	action := "project_public_viewing_disabled"
	if enabled {
		action = "project_public_viewing_enabled"
	}
	metadata := map[string]any{
		"from_enabled": current,
		"to_enabled":   enabled,
		"slug":         slug,
	}
	if err := insertAuditEventTx(ctx, tx, projectID, &actorUserID, action, "project", &projectID, metadata); err != nil {
		return ProjectPublicationState{}, fmt.Errorf("audit %s: %w", action, err)
	}
	if err := tx.Commit(); err != nil {
		return ProjectPublicationState{}, fmt.Errorf("commit update project public viewing: %w", err)
	}
	result.Enabled = enabled
	result.Changed = true
	return result, nil
}
