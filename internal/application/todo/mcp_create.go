package todo

import (
	"context"
	"errors"
	"fmt"

	"scrumboy/internal/store"
)

// MCPCreateAccessStore resolves the slug access boundary before MCP create
// normalization and data-dependent position policy run.
type MCPCreateAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

// MCPCreateLookupStore resolves project-local position references within the
// project bound during preparation.
type MCPCreateLookupStore interface {
	GetTodoByLocalID(ctx context.Context, projectID int64, localID int64, mode store.Mode) (store.Todo, error)
}

type MCPCreateServiceDependencies struct {
	Access MCPCreateAccessStore
	Lookup MCPCreateLookupStore
	Create CreateStore
}

// MCPCreateService owns slug access, project-scoped local-anchor resolution,
// and create persistence. It deliberately has no refresh dependency; MCP
// realtime behavior remains limited to effects published by the store.
type MCPCreateService struct {
	access MCPCreateAccessStore
	lookup MCPCreateLookupStore
	create CreateStore
}

func NewMCPCreateService(deps MCPCreateServiceDependencies) *MCPCreateService {
	return &MCPCreateService{
		access: deps.Access,
		lookup: deps.Lookup,
		create: deps.Create,
	}
}

// SlugCreateTarget identifies the slug whose access must be resolved before
// adapter-owned lane preparation and service-owned local-anchor policy run.
type SlugCreateTarget struct {
	Slug string
	Mode store.Mode
}

// PreparedMCPCreate binds the access context and value copies of the resolved
// project context and mode to position resolution and persistence.
type PreparedMCPCreate struct {
	ctx            context.Context
	service        *MCPCreateService
	projectContext store.ProjectContext
	mode           store.Mode
}

func (s *MCPCreateService) Prepare(ctx context.Context, target SlugCreateTarget) (*PreparedMCPCreate, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPCreate{
		ctx:            ctx,
		service:        s,
		projectContext: projectContext,
		mode:           target.Mode,
	}, nil
}

type MCPCreateValidationKind string

const (
	MCPCreateInvalidLocalReference  MCPCreateValidationKind = "invalid_local_reference"
	MCPCreateReferenceInWrongColumn MCPCreateValidationKind = "reference_in_wrong_column"
)

// MCPCreateValidationError identifies MCP-only local-position failures without
// coupling application policy to MCP status codes or error envelopes.
type MCPCreateValidationError struct {
	Kind       MCPCreateValidationKind
	Field      string
	LocalID    int64
	HasLocalID bool
}

func (e *MCPCreateValidationError) Error() string {
	return fmt.Sprintf("MCP todo create validation failed: %s", e.Kind)
}

// Create resolves MCP's project-local anchors in after-before order and then
// performs exactly one create with persistence-ready internal identities.
func (c *PreparedMCPCreate) Create(command MCPCreateCommand) (CreateResult, error) {
	project := c.projectContext.Project
	afterTodo, err := c.resolveLocalTodoForColumn(command.AfterLocalID, "afterLocalId", command.Values.ColumnKey)
	if err != nil {
		return CreateResult{}, err
	}
	beforeTodo, err := c.resolveLocalTodoForColumn(command.BeforeLocalID, "beforeLocalId", command.Values.ColumnKey)
	if err != nil {
		return CreateResult{}, err
	}

	position := ResolvedCreatePosition{}
	if afterTodo != nil {
		position.AfterTodoID = cloneCreateInt64Ptr(&afterTodo.ID)
	}
	if beforeTodo != nil {
		position.BeforeTodoID = cloneCreateInt64Ptr(&beforeTodo.ID)
	}
	input := MaterializeCreateInput(CreateCommand{
		Values:   command.Values,
		Position: position,
	})
	created, err := c.service.create.CreateTodo(c.ctx, project.ID, input, c.mode)
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Project: project, Todo: created}, nil
}

func (c *PreparedMCPCreate) resolveLocalTodoForColumn(localID *int64, field, targetColumnKey string) (*store.Todo, error) {
	if localID == nil {
		return nil, nil
	}
	if *localID <= 0 {
		return nil, &MCPCreateValidationError{
			Kind:    MCPCreateInvalidLocalReference,
			Field:   field,
			LocalID: *localID,
		}
	}

	projectID := c.projectContext.Project.ID
	todo, err := c.service.lookup.GetTodoByLocalID(c.ctx, projectID, *localID, c.mode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, &MCPCreateValidationError{
				Kind:       MCPCreateInvalidLocalReference,
				Field:      field,
				LocalID:    *localID,
				HasLocalID: true,
			}
		}
		return nil, err
	}
	if todo.ColumnKey != targetColumnKey {
		return nil, &MCPCreateValidationError{
			Kind:       MCPCreateReferenceInWrongColumn,
			Field:      field,
			LocalID:    *localID,
			HasLocalID: true,
		}
	}
	return &todo, nil
}
