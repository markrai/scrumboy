package priority

import (
	"context"

	"scrumboy/internal/store"
)

// CreateCommand contains values already prepared by the transport adapter for
// creating a priority tier.
type CreateCommand struct {
	Name string
}

// UpdateCommand contains the adapter-prepared values for updating a priority
// tier. It deliberately preserves values exactly as supplied by the adapter.
type UpdateCommand struct {
	Key   string
	Name  string
	Color string
}

// DeleteCommand identifies the priority tier to delete.
type DeleteCommand struct {
	Key string
}

// MutationStore is the persistence capability shared by REST and MCP
// priority-tier mutation services. Transport normalization and public
// validation remain adapter-owned; the store remains authoritative for
// priority invariants, persistence validation, and transaction ownership.
type MutationStore interface {
	AddPriorityTier(
		ctx context.Context,
		projectID int64,
		name string,
	) (store.PriorityTier, error)
	UpdatePriorityTier(
		ctx context.Context,
		projectID int64,
		key string,
		name string,
		color string,
	) error
	DeletePriorityTier(
		ctx context.Context,
		projectID int64,
		key string,
	) error
}

// PriorityReadStore is kept separate from MutationStore because only the MCP
// update projection requires a post-write priority-tier read.
type PriorityReadStore interface {
	GetProjectPriorities(
		ctx context.Context,
		projectID int64,
	) ([]store.PriorityTier, error)
}
