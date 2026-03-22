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
