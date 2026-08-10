package workflow

import (
	"context"

	"scrumboy/internal/store"
)

// CreateCommand contains values already prepared by the transport adapter for
// creating a workflow column.
type CreateCommand struct {
	Name string
}

// UpdateCommand contains the adapter-prepared values for updating a workflow
// column. It deliberately preserves values exactly as supplied by the adapter.
type UpdateCommand struct {
	Key   string
	Name  string
	Color string
}

// DeleteCommand identifies the workflow column to delete.
type DeleteCommand struct {
	Key string
}

// MutationStore is the persistence capability shared by future REST and MCP
// workflow-column mutation services. Transport normalization and public
// validation remain adapter-owned; the store remains authoritative for workflow
// invariants, persistence validation, and transaction ownership.
type MutationStore interface {
	AddWorkflowColumn(
		ctx context.Context,
		projectID int64,
		name string,
	) (store.WorkflowColumn, error)
	UpdateWorkflowColumn(
		ctx context.Context,
		projectID int64,
		key string,
		name string,
		color string,
	) error
	DeleteWorkflowColumn(
		ctx context.Context,
		projectID int64,
		key string,
	) error
}

// WorkflowReadStore is kept separate from MutationStore because only the MCP
// update projection requires a post-write workflow read.
type WorkflowReadStore interface {
	GetProjectWorkflow(
		ctx context.Context,
		projectID int64,
	) ([]store.WorkflowColumn, error)
}
