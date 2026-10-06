package store

import (
	"context"
	"database/sql"
	"fmt"
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
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT system_role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get user role: %w", err)
	}
	if parsed, ok := ParseSystemRole(role); !ok || parsed != SystemRoleOwner {
		return ErrUnauthorized
	}
	return nil
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
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE system_role = 'owner'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}

// countOwnersTx returns the number of users with the 'owner' role within a transaction.
func countOwnersTx(ctx context.Context, tx *sql.Tx) (int, error) {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE system_role = 'owner'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return n, nil
}
