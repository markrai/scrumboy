package todo

import (
	"context"
	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

type RESTArchiveService struct {
	service *ArchiveService
	refresh BoardRefreshPublisher
}
type RESTArchiveServiceDependencies struct {
	Archive ArchiveStore
	Refresh BoardRefreshPublisher
}

func NewRESTArchiveService(deps RESTArchiveServiceDependencies) *RESTArchiveService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &RESTArchiveService{service: NewArchiveService(deps.Archive), refresh: refresh}
}

type PreparedArchive struct {
	ctx            context.Context
	service        *RESTArchiveService
	projectContext store.ProjectContext
	mode           store.Mode
}

// ResolvedArchiveTarget carries the project context already authorized by the
// shared REST board router. Preparation performs no additional lookup.
type ResolvedArchiveTarget struct {
	ProjectContext store.ProjectContext
	Mode           store.Mode
}

func (s *RESTArchiveService) Prepare(ctx context.Context, target ResolvedArchiveTarget) *PreparedArchive {
	return &PreparedArchive{ctx: ctx, service: s, projectContext: target.ProjectContext, mode: target.Mode}
}

func (p *PreparedArchive) Archive(cmd ArchiveBatchCommand) (store.TodoArchiveBatchResult, error) {
	r, err := p.service.service.Archive(p.ctx, p.projectContext.Project.ID, cmd, p.mode)
	if err == nil && r.TransitionedCount > 0 {
		p.service.refresh.PublishBoardRefresh(p.ctx, p.projectContext.Project.ID, RefreshReasonTodoArchived, refresh.Entity{})
	}
	return r, err
}
func (p *PreparedArchive) Restore(cmd ArchiveBatchCommand) (store.TodoArchiveBatchResult, error) {
	r, err := p.service.service.Restore(p.ctx, p.projectContext.Project.ID, cmd, p.mode)
	if err == nil && r.TransitionedCount > 0 {
		p.service.refresh.PublishBoardRefresh(p.ctx, p.projectContext.Project.ID, RefreshReasonTodoRestored, refresh.Entity{})
	}
	return r, err
}
