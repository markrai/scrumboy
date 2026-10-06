package project

import "context"

// MCPDeletionServiceDependencies contains only the managed-project preparation
// and deletion capabilities required by MCP project deletion.
type MCPDeletionServiceDependencies struct {
	Access   ProjectAccessStore
	Manage   ProjectManageAuthorizationStore
	Projects ProjectDeletionStore
}

// MCPDeletionService preserves MCP's access/manage/delete sequence, canonical
// pre-read slug projection, and structural realtime silence.
type MCPDeletionService struct {
	managed  mcpManagedProjectPreparer
	projects ProjectDeletionStore
}

// NewMCPDeletionService constructs the additive MCP deletion service.
func NewMCPDeletionService(deps MCPDeletionServiceDependencies) *MCPDeletionService {
	return &MCPDeletionService{
		managed: mcpManagedProjectPreparer{
			access: deps.Access,
			manage: deps.Manage,
		},
		projects: deps.Projects,
	}
}

// PreparedMCPDeletion binds one managed project, actor, and canonical pre-read
// slug without performing a write.
type PreparedMCPDeletion struct {
	managed     preparedMCPManagedProject
	service     *MCPDeletionService
	projectSlug string
}

// Prepare performs the shared access, actor, and management sequence.
func (s *MCPDeletionService) Prepare(
	ctx context.Context,
	command MCPDeletionCommand,
) (*PreparedMCPDeletion, error) {
	managed, err := s.managed.prepare(ctx, command.Project)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPDeletion{
		managed:     managed,
		service:     s,
		projectSlug: managed.projectContext.Project.Slug,
	}, nil
}

// Delete performs one deletion and returns only the canonical pre-read slug
// and committed project identity. It performs no post-read or publication.
func (p *PreparedMCPDeletion) Delete() (MCPDeletionResult, error) {
	deleted, err := p.service.projects.DeleteProject(
		p.managed.ctx,
		p.managed.projectContext.Project.ID,
		p.managed.actorUserID,
	)
	if err != nil {
		return MCPDeletionResult{}, err
	}
	return MCPDeletionResult{
		ProjectSlug: p.projectSlug,
		ProjectID:   deleted.ProjectID,
	}, nil
}
