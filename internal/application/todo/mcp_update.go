package todo

import (
	"context"

	"scrumboy/internal/store"
)

// MCPUpdateAccessStore resolves the slug access boundary required before an
// MCP update may perform data-dependent work.
type MCPUpdateAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

// MCPUpdateLookupStore resolves the existing project-local todo before sparse
// patch validation and execution.
type MCPUpdateLookupStore interface {
	GetTodoByLocalID(ctx context.Context, projectID int64, localID int64, mode store.Mode) (store.Todo, error)
}

type MCPUpdateServiceDependencies struct {
	Access MCPUpdateAccessStore
	Lookup MCPUpdateLookupStore
	Update UpdateStore
}

// MCPUpdateService owns access, existing-todo binding, and sparse update
// persistence. It deliberately has no refresh dependency; MCP realtime
// behavior remains limited to effects published by the store.
type MCPUpdateService struct {
	access MCPUpdateAccessStore
	lookup MCPUpdateLookupStore
	update UpdateStore
}

func NewMCPUpdateService(deps MCPUpdateServiceDependencies) *MCPUpdateService {
	return &MCPUpdateService{
		access: deps.Access,
		lookup: deps.Lookup,
		update: deps.Update,
	}
}

// SlugUpdateTarget identifies the slug whose access must be resolved before
// the existing todo and its sparse patch may be considered.
type SlugUpdateTarget struct {
	Slug string
	Mode store.Mode
}

// PreparedMCPUpdate binds the access context and value copies of the resolved
// project context and mode. Todo lookup is intentionally a separate stage so
// transport patch validation can retain its established ordering.
type PreparedMCPUpdate struct {
	ctx            context.Context
	service        *MCPUpdateService
	projectContext store.ProjectContext
	mode           store.Mode
}

func (s *MCPUpdateService) Prepare(ctx context.Context, target SlugUpdateTarget) (*PreparedMCPUpdate, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPUpdate{
		ctx:            ctx,
		service:        s,
		projectContext: projectContext,
		mode:           target.Mode,
	}, nil
}

// PreparedMCPTodoUpdate binds a defensively copied existing todo after access
// and lookup have succeeded. Later update execution cannot replace the bound
// context, project, mode, or todo identity.
type PreparedMCPTodoUpdate struct {
	ctx            context.Context
	service        *MCPUpdateService
	projectContext store.ProjectContext
	mode           store.Mode
	existing       store.Todo
}

func (u *PreparedMCPUpdate) PrepareTodo(localID int64) (*PreparedMCPTodoUpdate, error) {
	existing, err := u.service.lookup.GetTodoByLocalID(
		u.ctx,
		u.projectContext.Project.ID,
		localID,
		u.mode,
	)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPTodoUpdate{
		ctx:            u.ctx,
		service:        u.service,
		projectContext: u.projectContext,
		mode:           u.mode,
		existing:       cloneMCPUpdateTodo(existing),
	}, nil
}

// Update applies a transport-validated sparse patch. Syntactically empty
// patches return the bound values without persistence; any present field,
// including a semantic no-op or explicit clear, performs exactly one update.
func (u *PreparedMCPTodoUpdate) Update(patch UpdatePatch) (UpdateResult, error) {
	project := u.projectContext.Project
	if !patch.HasFields() {
		return UpdateResult{Project: project, Todo: cloneMCPUpdateTodo(u.existing)}, nil
	}

	input := MaterializeUpdateInput(u.existing, patch)
	updated, err := u.service.update.UpdateTodoByLocalID(
		u.ctx,
		project.ID,
		u.existing.LocalID,
		input,
		u.mode,
	)
	if err != nil {
		return UpdateResult{}, err
	}
	return UpdateResult{Project: project, Todo: updated}, nil
}

func cloneMCPUpdateTodo(todo store.Todo) store.Todo {
	todo.Tags = cloneUpdateStrings(todo.Tags)
	todo.EstimationPoints = cloneUpdateInt64Ptr(todo.EstimationPoints)
	todo.AssigneeUserID = cloneUpdateInt64Ptr(todo.AssigneeUserID)
	todo.SprintID = cloneUpdateInt64Ptr(todo.SprintID)
	todo.PriorityKey = cloneUpdateStringPtr(todo.PriorityKey)
	if todo.DoneAt != nil {
		doneAt := *todo.DoneAt
		todo.DoneAt = &doneAt
	}
	return todo
}
