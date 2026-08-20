package todo

import (
	"context"

	"scrumboy/internal/store"
)

const RefreshReasonTodoCreated = "todo_created"

// CreateServiceDependencies names the persistence and ancillary capabilities
// used by the canonical REST create use case.
type CreateServiceDependencies struct {
	Create  CreateStore
	Refresh BoardRefreshPublisher
}

// CreateService owns REST create persistence and post-commit direct-refresh
// gating. Assignment-event publication remains owned by the store.
type CreateService struct {
	create  CreateStore
	refresh BoardRefreshPublisher
}

func NewCreateService(deps CreateServiceDependencies) *CreateService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &CreateService{create: deps.Create, refresh: refresh}
}

// ResolvedCreateTarget carries the project context already authorized by the
// shared REST board router. Preparation intentionally performs no slug lookup.
type ResolvedCreateTarget struct {
	ProjectContext store.ProjectContext
	Mode           store.Mode
}

// PreparedCreate binds the request context and value copies of the authorized
// project context and mode to the subsequent mutation.
type PreparedCreate struct {
	ctx            context.Context
	service        *CreateService
	projectContext store.ProjectContext
	mode           store.Mode
}

// Prepare binds an already-resolved REST board target without performing
// persistence or repeating route-owned access resolution.
func (s *CreateService) Prepare(ctx context.Context, target ResolvedCreateTarget) *PreparedCreate {
	return &PreparedCreate{
		ctx:            ctx,
		service:        s,
		projectContext: target.ProjectContext,
		mode:           target.Mode,
	}
}

// Create expects an adapter-prepared command whose lane and internal position
// identities are ready for persistence. Other values remain unnormalized so
// the store retains its existing validation and normalization policy.
func (c *PreparedCreate) Create(command CreateCommand) (CreateResult, error) {
	project := c.projectContext.Project
	input := MaterializeCreateInput(command)
	created, err := c.service.create.CreateTodo(c.ctx, project.ID, input, c.mode)
	if err != nil {
		return CreateResult{}, err
	}

	if !created.AssignmentChanged {
		c.service.refresh.PublishBoardRefresh(c.ctx, project.ID, RefreshReasonTodoCreated, todoRefreshEntity(created))
	}

	return CreateResult{Project: project, Todo: created}, nil
}
