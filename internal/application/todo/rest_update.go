package todo

import (
	"context"

	"scrumboy/internal/store"
)

const RefreshReasonTodoUpdated = "todo_updated"

// UpdateServiceDependencies names the persistence and ancillary capabilities
// used by the canonical REST update use case.
type UpdateServiceDependencies struct {
	Update          UpdateStore
	Refresh         BoardRefreshPublisher
	CreatorRequests CreatorNotificationRequestPublisher
}

// UpdateService owns REST update persistence and post-commit direct-refresh
// gating. Assignment-event publication remains owned by the store.
type UpdateService struct {
	update          UpdateStore
	refresh         BoardRefreshPublisher
	creatorRequests CreatorNotificationRequestPublisher
}

func NewUpdateService(deps UpdateServiceDependencies) *UpdateService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	creatorRequests := deps.CreatorRequests
	if creatorRequests == nil {
		creatorRequests = nopCreatorNotificationRequestPublisher{}
	}
	return &UpdateService{update: deps.Update, refresh: refresh, creatorRequests: creatorRequests}
}

// ResolvedUpdateTarget carries the project context already authorized by the
// shared REST board router. Preparation intentionally performs no slug lookup.
type ResolvedUpdateTarget struct {
	ProjectContext store.ProjectContext
	Mode           store.Mode
}

// PreparedUpdate binds the request context and value copies of the authorized
// project context and mode to the subsequent mutation.
type PreparedUpdate struct {
	ctx            context.Context
	service        *UpdateService
	projectContext store.ProjectContext
	mode           store.Mode
}

// Prepare binds an already-resolved REST board target without performing
// persistence or repeating route-owned access resolution.
func (s *UpdateService) Prepare(ctx context.Context, target ResolvedUpdateTarget) *PreparedUpdate {
	return &PreparedUpdate{
		ctx:            ctx,
		service:        s,
		projectContext: target.ProjectContext,
		mode:           target.Mode,
	}
}

// Update expects a normalized REST patch: title, body, tags, estimation, and
// assignee are present replacement fields, while sprint is present only when
// the route received sprintId. The route remains responsible for establishing
// that invariant and for transport projection and error mapping.
func (u *PreparedUpdate) Update(command UpdateCommand) (UpdateResult, error) {
	project := u.projectContext.Project
	input := MaterializeUpdateInput(store.Todo{}, command.Patch)
	updated, err := u.service.update.UpdateTodoByLocalID(
		u.ctx,
		project.ID,
		command.LocalID,
		input,
		u.mode,
	)
	if err != nil {
		return UpdateResult{}, err
	}

	effectCtx := publishCreatorNotificationRequest(u.ctx, u.service.creatorRequests, project, updated, RefreshReasonTodoUpdated, true)
	if !updated.AssignmentChanged {
		u.service.refresh.PublishBoardRefresh(effectCtx, project.ID, RefreshReasonTodoUpdated, todoRefreshEntity(updated))
	}

	return UpdateResult{Project: project, Todo: updated}, nil
}
