// Package useradmin defines the application boundary for selected system-user
// administration mutations.
package useradmin

import (
	"context"

	"scrumboy/internal/store"
)

// CreateCommand contains the REST-decoded values supplied for creation of a
// non-bootstrap local user. Store normalization and validation remain
// authoritative.
type CreateCommand struct {
	Email    string
	Name     string
	Password string
}

// RoleChangeCommand identifies the target and the role already parsed by the
// transport adapter.
type RoleChangeCommand struct {
	TargetUserID int64
	NewRole      store.SystemRole
}

// DeleteCommand identifies the target selected for user deletion.
type DeleteCommand struct {
	TargetUserID int64
}

// UserCreationStore creates one non-bootstrap local user using the existing
// store-owned normalization, hashing, seeding, and transaction semantics.
type UserCreationStore interface {
	CreateUser(
		ctx context.Context,
		email string,
		password string,
		name string,
	) (store.User, error)
}

// UserReadStore reads one user for later authority or result-projection stages.
type UserReadStore interface {
	GetUser(ctx context.Context, userID int64) (store.User, error)
}

// UserRoleMutationStore applies one system-role mutation while preserving the
// store-owned Owner and last-Owner invariants.
type UserRoleMutationStore interface {
	UpdateUserRole(
		ctx context.Context,
		requesterID int64,
		targetUserID int64,
		newRole store.SystemRole,
	) error
}

// UserDeletionStore deletes one user using the existing store-owned authority,
// cascade, foreign-key, and transaction behavior.
type UserDeletionStore interface {
	DeleteUser(
		ctx context.Context,
		requesterID int64,
		targetUserID int64,
	) error
}
