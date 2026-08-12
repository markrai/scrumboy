package todo

import (
	"context"

	"scrumboy/internal/store"
)

// DeleteServiceDependencies names the persistence and ancillary capabilities
// used by the canonical REST delete use case.
type DeleteServiceDependencies struct {
	Delete  DeleteStore
	Refresh BoardRefreshPublisher
}

// DeleteService owns REST delete persistence and post-commit refresh
// sequencing. Slug access and local-ID validation remain in the REST adapter,
// while deletion authorization remains in the store.
type DeleteService struct {
	delete  DeleteStore
	refresh BoardRefreshPublisher
}

func NewDeleteService(deps DeleteServiceDependencies) *DeleteService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &DeleteService{delete: deps.Delete, refresh: refresh}
}

// ResolvedDeleteTarget carries the project context already authorized by the
// shared REST board router. Preparation intentionally performs no lookup or
// authorization.
type ResolvedDeleteTarget struct {
	ProjectContext store.ProjectContext
	Mode           store.Mode
}

// PreparedDelete binds the request context and value copies of the authorized
// project context and mode to the subsequent deletion.
type PreparedDelete struct {
	ctx            context.Context
	service        *DeleteService
	projectContext store.ProjectContext
	mode           store.Mode
}

// Prepare binds an already-resolved REST board target without performing
// persistence or repeating route-owned access resolution.
func (s *DeleteService) Prepare(ctx context.Context, target ResolvedDeleteTarget) *PreparedDelete {
	return &PreparedDelete{
		ctx:            ctx,
		service:        s,
		projectContext: target.ProjectContext,
		mode:           target.Mode,
	}
}

// Delete performs exactly one local-ID deletion and publishes one best-effort
// REST refresh only after persistence succeeds. No post-delete projection is
// needed by either the application or transport contract.
func (d *PreparedDelete) Delete(command DeleteCommand) error {
	project := d.projectContext.Project
	if err := d.service.delete.DeleteTodoByLocalID(
		d.ctx,
		project.ID,
		command.LocalID,
		d.mode,
	); err != nil {
		return err
	}

	d.service.refresh.PublishBoardRefresh(d.ctx, project.ID, RefreshReasonTodoDeleted)
	return nil
}
