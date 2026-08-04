// Package todolink defines application boundaries for directed todo-link use cases.
package todolink

import (
	"context"

	"scrumboy/internal/store"
)

// AddCommand contains the target and link type prepared by a transport adapter.
// A later prepared capability binds the source todo and project scope. Transport
// normalization and public validation remain adapter-owned; the store remains
// authoritative for link invariants and persistence policy.
type AddCommand struct {
	TargetLocalID int64
	LinkType      string
}

// RemoveCommand identifies the target side of a directed link. A later prepared
// capability binds the source side and project scope.
type RemoveCommand struct {
	TargetLocalID int64
}

// SourceLookupStore is the source-todo existence and access gate shared by the
// future REST and MCP prepared services. Persistence performs its own
// authoritative endpoint checks at mutation time.
type SourceLookupStore interface {
	GetTodoByLocalID(
		ctx context.Context,
		projectID int64,
		localID int64,
		mode store.Mode,
	) (store.Todo, error)
}

// MutationStore is the complete persistence capability for canonical directed
// todo-link writes. The store retains authorization, validation, duplicate,
// transaction, and audit ownership.
type MutationStore interface {
	AddLink(
		ctx context.Context,
		projectID int64,
		fromLocalID int64,
		toLocalID int64,
		linkType string,
		mode store.Mode,
	) error

	RemoveLink(
		ctx context.Context,
		projectID int64,
		fromLocalID int64,
		toLocalID int64,
		mode store.Mode,
	) error
}
