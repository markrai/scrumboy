package todo

import (
	"context"
	"scrumboy/internal/store"
)

type MCPArchiveService struct {
	access  MCPMoveAccessStore
	archive ArchiveStore
}
type MCPArchiveServiceDependencies struct {
	Access  MCPMoveAccessStore
	Archive ArchiveStore
}

func NewMCPArchiveService(deps MCPArchiveServiceDependencies) *MCPArchiveService {
	return &MCPArchiveService{access: deps.Access, archive: deps.Archive}
}

type PreparedMCPArchive struct {
	ctx     context.Context
	service *MCPArchiveService
	project store.ProjectContext
	mode    store.Mode
}

// MCPArchiveTarget contains the slug and mode resolved from an MCP request.
type MCPArchiveTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

func (s *MCPArchiveService) Prepare(ctx context.Context, target MCPArchiveTarget) (*PreparedMCPArchive, error) {
	pc, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPArchive{ctx: ctx, service: s, project: pc, mode: target.Mode}, nil
}
func (p *PreparedMCPArchive) Archive(ids []int64) (store.TodoArchiveBatchResult, error) {
	return p.service.archive.ArchiveTodosByLocalID(p.ctx, p.project.Project.ID, ids, p.mode)
}
func (p *PreparedMCPArchive) Restore(ids []int64) (store.TodoArchiveBatchResult, error) {
	return p.service.archive.RestoreTodosByLocalID(p.ctx, p.project.Project.ID, ids, p.mode)
}
