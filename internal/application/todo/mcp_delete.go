package todo

import (
	"context"

	"scrumboy/internal/store"
)

// MCPDeleteAccessStore resolves the project slug access boundary before an
// MCP deletion may use its project-local todo identity.
type MCPDeleteAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

type MCPDeleteServiceDependencies struct {
	Access MCPDeleteAccessStore
	Delete DeleteStore
}

// MCPDeleteService owns slug access followed by local-ID deletion. It has no
// refresh dependency, preserving MCP deletion's realtime silence.
type MCPDeleteService struct {
	access MCPDeleteAccessStore
	delete DeleteStore
}

func NewMCPDeleteService(deps MCPDeleteServiceDependencies) *MCPDeleteService {
	return &MCPDeleteService{
		access: deps.Access,
		delete: deps.Delete,
	}
}

// SlugDeleteTarget identifies the project slug whose access must be resolved
// before deletion. The todo local ID remains part of DeleteCommand.
type SlugDeleteTarget struct {
	Slug string
	Mode store.Mode
}

// PreparedMCPDelete binds the access context and value copies of the resolved
// project context and mode to deletion execution.
type PreparedMCPDelete struct {
	ctx            context.Context
	service        *MCPDeleteService
	projectContext store.ProjectContext
	mode           store.Mode
}

func (s *MCPDeleteService) Prepare(ctx context.Context, target SlugDeleteTarget) (*PreparedMCPDelete, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPDelete{
		ctx:            ctx,
		service:        s,
		projectContext: projectContext,
		mode:           target.Mode,
	}, nil
}

// Delete performs exactly one local-ID deletion using the prepared project.
// Store errors pass through unchanged and no projection or realtime event is
// produced by this service.
func (d *PreparedMCPDelete) Delete(command DeleteCommand) error {
	return d.service.delete.DeleteTodoByLocalID(
		d.ctx,
		d.projectContext.Project.ID,
		command.LocalID,
		d.mode,
	)
}
