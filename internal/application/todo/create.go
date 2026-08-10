package todo

import (
	"context"

	"scrumboy/internal/store"
)

// CreateValues contains the transport-neutral values shared by canonical REST
// and MCP todo creation. Adapters retain transport-specific decoding and
// compatibility normalization, while the store retains validation and
// persistence normalization.
type CreateValues struct {
	Title            string
	Body             string
	Tags             []string
	ColumnKey        string
	EstimationPoints *int64
	AssigneeUserID   *int64
	SprintID         *int64
}

// ResolvedCreatePosition carries internal todo-row identities (store.Todo.ID)
// that are ready for persistence. They are distinct from MCP's project-local
// todo references.
type ResolvedCreatePosition struct {
	AfterTodoID  *int64
	BeforeTodoID *int64
}

// CreateCommand contains transport-neutral create values and
// persistence-ready position identities.
type CreateCommand struct {
	Values   CreateValues
	Position ResolvedCreatePosition
}

// MCPCreateCommand retains project-local position identities until a prepared
// MCP service resolves them within its bound project. It is not accepted by the
// store-input materializer.
type MCPCreateCommand struct {
	Values        CreateValues
	AfterLocalID  *int64
	BeforeLocalID *int64
}

// CreateResult contains domain values for transport-owned projection.
type CreateResult struct {
	Project store.Project
	Todo    store.Todo
}

// CreateStore is the single persistence capability shared by future REST and
// MCP create services.
type CreateStore interface {
	CreateTodo(
		ctx context.Context,
		projectID int64,
		in store.CreateTodoInput,
		mode store.Mode,
	) (store.Todo, error)
}

// MaterializeCreateInput converts a resolved command to the store's existing
// create vocabulary. It defensively copies mutable values but intentionally
// performs no access, validation, normalization, persistence, or publication.
func MaterializeCreateInput(command CreateCommand) store.CreateTodoInput {
	return store.CreateTodoInput{
		Title:            command.Values.Title,
		Body:             command.Values.Body,
		Tags:             cloneCreateStrings(command.Values.Tags),
		ColumnKey:        command.Values.ColumnKey,
		EstimationPoints: cloneCreateInt64Ptr(command.Values.EstimationPoints),
		SprintID:         cloneCreateInt64Ptr(command.Values.SprintID),
		AssigneeUserID:   cloneCreateInt64Ptr(command.Values.AssigneeUserID),
		AfterID:          cloneCreateInt64Ptr(command.Position.AfterTodoID),
		BeforeID:         cloneCreateInt64Ptr(command.Position.BeforeTodoID),
	}
}

func cloneCreateStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneCreateInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
