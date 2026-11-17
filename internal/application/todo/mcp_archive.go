package todo

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

// ErrArchiveReadsNotConfigured is returned by List when Reads was not wired.
// Archive/Restore do not need Reads; List does.
var ErrArchiveReadsNotConfigured = errors.New("archive list reads are not configured")

type MCPArchiveService struct {
	access  MCPMoveAccessStore
	archive ArchiveStore
	reads   ArchiveReadStore
}
type MCPArchiveServiceDependencies struct {
	Access  MCPMoveAccessStore
	Archive ArchiveStore
	// Reads backs List. It may be nil when only Archive/Restore are used;
	// List then returns ErrArchiveReadsNotConfigured instead of panicking.
	Reads ArchiveReadStore
}

func NewMCPArchiveService(deps MCPArchiveServiceDependencies) *MCPArchiveService {
	return &MCPArchiveService{access: deps.Access, archive: deps.Archive, reads: deps.Reads}
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

// MCPArchivedListResult is one page of archived stories, newest archived first.
type MCPArchivedListResult struct {
	Project    store.Project
	Todos      []store.Todo
	NextCursor string
	HasMore    bool
}

// List returns one cursor-paginated page of the project's archived stories,
// using the same store read as GET /api/board/{slug}/archive. cursor is the
// opaque NextCursor of a previous page ("" for the first page).
func (s *MCPArchiveService) List(ctx context.Context, target MCPArchiveTarget, limit int, cursor string) (MCPArchivedListResult, error) {
	if s.reads == nil {
		return MCPArchivedListResult{}, ErrArchiveReadsNotConfigured
	}
	pc, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return MCPArchivedListResult{}, err
	}
	var afterAt, afterID *int64
	if cursor != "" {
		a, b, err := store.ParseArchiveCursor(cursor)
		if err != nil {
			return MCPArchivedListResult{}, err
		}
		afterAt, afterID = &a, &b
	}
	todos, next, more, err := s.reads.ListArchivedTodos(ctx, pc.Project.ID, limit, afterAt, afterID, target.Mode)
	if err != nil {
		return MCPArchivedListResult{}, err
	}
	return MCPArchivedListResult{Project: pc.Project, Todos: todos, NextCursor: next, HasMore: more}, nil
}
