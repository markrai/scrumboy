package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// validMemberRoles maps normalized role strings to ProjectRole for PATCH membership.
// Maintainer, contributor, and viewer can be set via UpdateProjectMemberRole.
var validMemberRoles = map[string]ProjectRole{
	"maintainer":  RoleMaintainer,
	"contributor": RoleContributor,
	"viewer":      RoleViewer,
}

// ParseMemberRole normalizes s and returns the corresponding ProjectRole if valid.
// Returns (zero, false) for invalid roles (e.g. empty, unknown).
func ParseMemberRole(s string) (ProjectRole, bool) {
	role := strings.ToLower(strings.TrimSpace(s))
	pr, ok := validMemberRoles[role]
	return pr, ok
}

// Project membership and roles are separate from system roles.
//
// IMPORTANT: System roles (Owner, Admin, User) never grant project permissions.
// Project access is solely via project_members. A system Admin or Owner cannot
// access, modify, or delete a project without an explicit project_members entry.

// GetProjectRole returns the user's role for a project, or empty string if not a member
func (s *Store) GetProjectRole(ctx context.Context, projectID int64, userID int64) (ProjectRole, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `
		SELECT role FROM project_members
		WHERE project_id = ? AND user_id = ?
	`, projectID, userID).Scan(&role)

	if err == sql.ErrNoRows {
		return "", nil // Not a member
	}
	if err != nil {
		return "", fmt.Errorf("get project role: %w", err)
	}
	return ProjectRole(role), nil
}

func (s *Store) getProjectRoleTx(ctx context.Context, tx *sql.Tx, projectID, userID int64) (ProjectRole, error) {
	var role string
	err := tx.QueryRowContext(ctx, `
		SELECT role FROM project_members
		WHERE project_id = ? AND user_id = ?
	`, projectID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get project role tx: %w", err)
	}
	return ProjectRole(role), nil
}

// EnsureMaintainerMembership ensures a project has a maintainer membership for the given user
func (s *Store) EnsureMaintainerMembership(ctx context.Context, projectID int64, userID int64) error {
	nowMs := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
	`, projectID, userID, RoleMaintainer, nowMs)
	if err != nil {
		return fmt.Errorf("ensure maintainer membership: %w", err)
	}
	return nil
}

// countMaintainers returns the number of members with Maintainer+ (maintainer or owner).
// Used for the last-maintainer invariant: cannot remove if it would leave zero.
func (s *Store) countMaintainers(ctx context.Context, projectID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM project_members 
		WHERE project_id = ? AND role IN (?, ?)
	`, projectID, RoleMaintainer, RoleOwner).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count maintainers: %w", err)
	}
	return count, nil
}

// ListProjectMembers returns all members of a project.
// Requires userID to have Viewer+; returns ErrNotFound when access denied.
func (s *Store) ListProjectMembers(ctx context.Context, projectID int64, userID int64) ([]ProjectMember, error) {
	if err := s.CheckProjectRole(ctx, projectID, userID, RoleViewer); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT pm.user_id, u.email, u.name, u.image, pm.role, pm.created_at
		FROM project_members pm
		JOIN users u ON pm.user_id = u.id
		WHERE pm.project_id = ?
		ORDER BY pm.created_at
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project members: %w", err)
	}
	defer rows.Close()

	var members []ProjectMember
	for rows.Next() {
		var m ProjectMember
		var roleStr string
		var image sql.NullString
		var createdAtMs int64
		if err := rows.Scan(&m.UserID, &m.Email, &m.Name, &image, &roleStr, &createdAtMs); err != nil {
			return nil, fmt.Errorf("scan project member: %w", err)
		}
		if image.Valid {
			m.Image = &image.String
		}
		m.Role = ProjectRole(roleStr)
		m.CreatedAt = time.UnixMilli(createdAtMs).UTC()
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return members, nil
}

// AddProjectMember adds a user to a project with the specified role.
// Authorization: Requester must have Maintainer+ in the project. No system-role override.
func (s *Store) AddProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64, role ProjectRole) error {
	// Validate project exists
	_, err := s.getProject(ctx, projectID)
	if err != nil {
		return err
	}

	// Validate target user exists
	_, err = s.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}

	requesterProjectRole, err := s.GetProjectRole(ctx, projectID, requesterID)
	if err != nil {
		return err
	}

	// Authorization: only project Maintainer+ may add members
	if !requesterProjectRole.HasMinimumRole(RoleMaintainer) {
		return ErrUnauthorized
	}

	// Check for duplicate membership
	existingRole, err := s.GetProjectRole(ctx, projectID, targetUserID)
	if err != nil {
		return err
	}
	if existingRole != "" {
		return fmt.Errorf("%w: user is already a member", ErrConflict)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin add project member: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nowMs := time.Now().UTC().UnixMilli()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
	`, projectID, targetUserID, role, nowMs)
	if err != nil {
		return fmt.Errorf("add project member: %w", err)
	}

	actorUserID := &requesterID
	meta := map[string]any{"user_id": targetUserID, "role": string(role)}
	targetID := targetUserID
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "member_added", "member", &targetID, meta); err != nil {
		return fmt.Errorf("audit member_added: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit add project member: %w", err)
	}
	return nil
}

// RemoveProjectMember removes a user from a project.
// Authorization: Requester must be project maintainer
// Validation: Cannot remove last maintainer
func (s *Store) RemoveProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64) error {
	// Check requester has Maintainer+
	requesterRole, err := s.GetProjectRole(ctx, projectID, requesterID)
	if err != nil {
		return err
	}
	if !requesterRole.HasMinimumRole(RoleMaintainer) {
		return ErrUnauthorized
	}

	// Get target user's role
	targetRole, err := s.GetProjectRole(ctx, projectID, targetUserID)
	if err != nil {
		return err
	}
	if targetRole == "" {
		return ErrNotFound
	}

	// If removing Maintainer or Owner, ensure it's not the last one
	if targetRole.HasMinimumRole(RoleMaintainer) {
		count, err := s.countMaintainers(ctx, projectID)
		if err != nil {
			return err
		}
		if count <= 1 {
			return fmt.Errorf("%w: cannot remove last maintainer", ErrValidation)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin remove project member: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM todos
		WHERE project_id = ? AND assignee_user_id = ?
	`, projectID, targetUserID)
	if err != nil {
		return fmt.Errorf("list assigned todos: %w", err)
	}
	defer rows.Close()

	var todoIDs []int64
	for rows.Next() {
		var todoID int64
		if err := rows.Scan(&todoID); err != nil {
			return fmt.Errorf("scan assigned todo: %w", err)
		}
		todoIDs = append(todoIDs, todoID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows assigned todos: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()
	for _, todoID := range todoIDs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO todo_assignee_events(
				project_id,
				todo_id,
				actor_user_id,
				from_assignee_user_id,
				to_assignee_user_id,
				reason,
				created_at
			)
			VALUES (?, ?, ?, ?, NULL, 'member_removed', ?)
		`, projectID, todoID, requesterID, targetUserID, nowMs)
		if err != nil {
			return fmt.Errorf("insert assignee audit event: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE todos
			SET assignee_user_id = NULL
			WHERE id = ?
		`, todoID); err != nil {
			return fmt.Errorf("unassign todo: %w", err)
		}
	}

	actorUserID := &requesterID
	meta := map[string]any{"user_id": targetUserID, "role": string(targetRole)}
	targetID := targetUserID
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "member_removed", "member", &targetID, meta); err != nil {
		return fmt.Errorf("audit member_removed: %w", err)
	}

	// Remove membership
	res, err := tx.ExecContext(ctx, `
		DELETE FROM project_members
		WHERE project_id = ? AND user_id = ?
	`, projectID, targetUserID)
	if err != nil {
		return fmt.Errorf("remove project member: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit remove project member: %w", err)
	}

	return nil
}

// UpdateProjectMemberRole updates a project member's role (maintainer, contributor, or viewer).
// Authorization: requester must be Maintainer+. Validations: target must be member;
// no self-demotion to contributor or viewer; cannot demote the last maintainer to contributor or viewer.
func (s *Store) UpdateProjectMemberRole(ctx context.Context, requesterID, projectID, targetUserID int64, newRole ProjectRole) error {
	// 1. Requester must be Maintainer+
	requesterRole, err := s.GetProjectRole(ctx, projectID, requesterID)
	if err != nil {
		return err
	}
	if !requesterRole.HasMinimumRole(RoleMaintainer) {
		return ErrUnauthorized
	}

	// 2. Normalize and validate role
	role := strings.ToLower(strings.TrimSpace(string(newRole)))
	if _, ok := validMemberRoles[role]; !ok {
		return fmt.Errorf("%w: invalid role", ErrValidation)
	}

	// 3. Target must be a member
	currentRole, err := s.GetProjectRole(ctx, projectID, targetUserID)
	if err != nil {
		return err
	}
	if currentRole == "" {
		return ErrNotFound
	}

	// 4. Skip UPDATE if role unchanged
	currentRoleNorm := strings.ToLower(string(currentRole))
	if currentRoleNorm == role {
		return nil
	}

	// 5. No self-demotion (to contributor or viewer)
	if requesterID == targetUserID && (role == "contributor" || role == "viewer") {
		return fmt.Errorf("%w: cannot demote yourself", ErrConflict)
	}

	// 6. Last-maintainer protection (cannot demote last maintainer to contributor or viewer)
	if currentRole.HasMinimumRole(RoleMaintainer) && (role == "contributor" || role == "viewer") {
		count, err := s.countMaintainers(ctx, projectID)
		if err != nil {
			return err
		}
		if count <= 1 {
			return fmt.Errorf("%w: cannot demote the last maintainer", ErrConflict)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update project member role: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?
	`, role, projectID, targetUserID)
	if err != nil {
		return fmt.Errorf("update project member role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n != 1 {
		return ErrNotFound
	}

	actorUserID := &requesterID
	meta := map[string]any{"user_id": targetUserID, "from_role": string(currentRole), "to_role": role}
	targetID := targetUserID
	if err := insertAuditEventTx(ctx, tx, projectID, actorUserID, "member_role_changed", "member", &targetID, meta); err != nil {
		return fmt.Errorf("audit member_role_changed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update project member role: %w", err)
	}
	return nil
}

// ListAvailableUsersForProject returns users that can be added to a project.
// Requires requester to have Maintainer+ in the project.
// Returns all system users excluding current project members.
func (s *Store) ListAvailableUsersForProject(ctx context.Context, requesterID, projectID int64) ([]User, error) {
	role, err := s.GetProjectRole(ctx, projectID, requesterID)
	if err != nil {
		return nil, err
	}
	if !role.HasMinimumRole(RoleMaintainer) {
		return nil, ErrUnauthorized
	}

	// Get all users excluding current project members
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email, name, is_bootstrap, system_role, created_at
		FROM users
		WHERE id NOT IN (
			SELECT user_id FROM project_members WHERE project_id = ?
		)
		ORDER BY created_at
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list available users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var isBootstrap bool
		var systemRoleStr string
		var createdAt int64
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.IsBootstrap = isBootstrap
		if r, ok := ParseSystemRole(systemRoleStr); ok {
			u.SystemRole = r
		} else {
			u.SystemRole = SystemRoleUser
		}
		u.CreatedAt = time.UnixMilli(createdAt).UTC()
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return users, nil
}
