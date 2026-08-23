package project

import (
	"context"

	"scrumboy/internal/store"
)

// MCPUpdateServiceDependencies contains only the managed-project preparation,
// atomic patch, and post-read capabilities used by MCP project update.
type MCPUpdateServiceDependencies struct {
	Access   ProjectAccessStore
	Manage   ProjectManageAuthorizationStore
	Patches  ProjectPatchMutationStore
	Projects ProjectByIDReadStore
}

// MCPUpdateService preserves MCP's one atomic patch, post-read projection, and
// structural realtime silence.
type MCPUpdateService struct {
	managed  mcpManagedProjectPreparer
	patches  ProjectPatchMutationStore
	projects ProjectByIDReadStore
}

// NewMCPUpdateService constructs the additive MCP update service.
func NewMCPUpdateService(deps MCPUpdateServiceDependencies) *MCPUpdateService {
	return &MCPUpdateService{
		managed: mcpManagedProjectPreparer{
			access: deps.Access,
			manage: deps.Manage,
		},
		patches:  deps.Patches,
		projects: deps.Projects,
	}
}

// PreparedMCPUpdate binds the managed project, actor, response role, and copied
// presence-aware patch values for one exact MCP update sequence.
type PreparedMCPUpdate struct {
	managed            preparedMCPManagedProject
	service            *MCPUpdateService
	name               *string
	defaultSprintWeeks *int
}

// Prepare performs access, actor, and management preparation without writing.
func (s *MCPUpdateService) Prepare(
	ctx context.Context,
	target ProjectSlugTarget,
	command MCPUpdateCommand,
) (*PreparedMCPUpdate, error) {
	managed, err := s.managed.prepare(ctx, target)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPUpdate{
		managed:            managed,
		service:            s,
		name:               cloneString(command.Name),
		defaultSprintWeeks: cloneInt(command.DefaultSprintWeeks),
	}, nil
}

// Update executes one atomic store patch and one post-read. It deliberately
// has no publication, retry, rollback, or compensation path.
func (p *PreparedMCPUpdate) Update() (MCPProjectResult, error) {
	patch := store.UpdateProjectPatch{
		Name:               cloneString(p.name),
		DefaultSprintWeeks: cloneInt(p.defaultSprintWeeks),
	}
	projectID := p.managed.projectContext.Project.ID
	if err := p.service.patches.UpdateProjectPatch(
		p.managed.ctx,
		projectID,
		p.managed.actorUserID,
		patch,
	); err != nil {
		return MCPProjectResult{}, err
	}

	updated, err := p.service.projects.GetProject(p.managed.ctx, projectID)
	if err != nil {
		return MCPProjectResult{}, err
	}
	return newMCPProjectResult(updated, p.managed.projectContext.Role), nil
}
