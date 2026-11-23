package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// requireOwner ensures the user has the 'owner' system role.
// Returns ErrUnauthorized if the user is not an owner.
func (s *Store) requireOwner(ctx context.Context, userID int64) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.SystemRole != SystemRoleOwner {
		return ErrUnauthorized
	}
	return nil
}

// requireOwnerTx performs the owner check on the same database snapshot as a
// transaction's mutation. This is required for destructive operations whose
// authorization must not be invalidated between a preflight read and the write.
func requireOwnerTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	role, err := systemRoleTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if role != SystemRoleOwner {
		return ErrUnauthorized
	}
	return nil
}

// requireAdminTx is the Owner-or-Admin check on the same database snapshot as a transaction's mutation.
func requireAdminTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	role, err := systemRoleTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if role != SystemRoleOwner && role != SystemRoleAdmin {
		return ErrUnauthorized
	}
	return nil
}

// systemRoleTx reads a user's effective system role on the transaction's snapshot.
func systemRoleTx(ctx context.Context, tx *sql.Tx, userID int64) (SystemRole, error) {
	var (
		raw       string
		isService bool
	)
	if err := tx.QueryRowContext(ctx, `SELECT system_role, is_service <> 0 FROM users WHERE id = ?`, userID).Scan(&raw, &isService); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get user role: %w", err)
	}
	return effectiveSystemRole(raw, isService), nil
}

// requireAdmin ensures the user has 'admin' or 'owner' system role.
// Returns ErrUnauthorized if the user is not an admin or owner.
func (s *Store) requireAdmin(ctx context.Context, userID int64) error {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u.SystemRole != SystemRoleOwner && u.SystemRole != SystemRoleAdmin {
		return ErrUnauthorized
	}
	return nil
}

// requireOwnerOrAdmin is an alias for requireAdmin for clarity.
func (s *Store) requireOwnerOrAdmin(ctx context.Context, userID int64) error {
	return s.requireAdmin(ctx, userID)
}

// countOwners returns the number of users with the 'owner' role.
func (s *Store) countOwners(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE system_role = 'owner' AND is_service = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

// countOwnersTx returns the number of users with the 'owner' role within a transaction.
func countOwnersTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE system_role = 'owner' AND is_service = 0`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

func (u User) CanCreateBoards() bool {
	return !u.IsService && u.SystemRole != SystemRoleGuest
}

func isGuest(ctx context.Context, q sqlRowQueryer, userID int64) (bool, error) {
	var guest bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND system_role = 'guest')`, userID).Scan(&guest); err != nil {
		return false, fmt.Errorf("check guest role: %w", err)
	}
	return guest, nil
}

func requireBoardCreation(ctx context.Context, q sqlRowQueryer) error {
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return nil
	}
	var role string
	var service bool
	err := q.QueryRowContext(ctx, `SELECT system_role, is_service <> 0 FROM users WHERE id = ?`, userID).Scan(&role, &service)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check board creation permission: %w", err)
	}
	if isService, ok := ServiceIdentityFromContext(ctx, userID); ok {
		service = service || isService
	}
	actor := User{SystemRole: effectiveSystemRole(role, service), IsService: service}
	if actor.CanCreateBoards() {
		return nil
	}
	if service {
		return ErrServiceAccountRestricted
	}
	return ErrForbidden
}

func validateGuestMembership(ctx context.Context, tx *sql.Tx, projectID, userID int64, role ProjectRole) error {
	guest, err := isGuest(ctx, tx, userID)
	if err != nil || !guest {
		return err
	}
	var temporary bool
	if err := tx.QueryRowContext(ctx, `SELECT expires_at IS NOT NULL FROM projects WHERE id = ?`, projectID).Scan(&temporary); err != nil {
		return fmt.Errorf("check guest membership project: %w", err)
	}
	if temporary || role != RoleViewer {
		return fmt.Errorf("%w: guests can only be viewers on regular boards", ErrValidation)
	}
	return nil
}

func validateGuestRoleChange(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `
SELECT p.name, p.slug
FROM project_members pm
JOIN projects p ON p.id = pm.project_id
WHERE pm.user_id = ? AND (pm.role <> 'viewer' OR p.expires_at IS NOT NULL)
ORDER BY p.name, p.slug`, userID)
	if err != nil {
		return fmt.Errorf("check guest role memberships: %w", err)
	}
	defer rows.Close()
	var boards []string
	for rows.Next() {
		var name, slug string
		if err := rows.Scan(&name, &slug); err != nil {
			return fmt.Errorf("scan guest role membership: %w", err)
		}
		boards = append(boards, fmt.Sprintf("%s (%s)", name, slug))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list guest role memberships: %w", err)
	}
	if len(boards) > 0 {
		return fmt.Errorf("%w: resolve these board memberships before assigning Guest: %s", ErrConflict, strings.Join(boards, ", "))
	}
	return nil
}
