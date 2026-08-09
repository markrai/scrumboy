package sprint

import (
	"context"

	"scrumboy/internal/store"
)

// MCPDeletionTarget is distinct from MCPLifecycleTarget so destructive and
// transition preparation cannot be interchanged accidentally.
type MCPDeletionTarget struct {
	ProjectSlug string
	SprintID    int64
	Mode        store.Mode
}

// MCPDeletionServiceDependencies exposes only the access, role, target-read,
// and deletion capabilities needed by MCP sprint deletion.
type MCPDeletionServiceDependencies struct {
	Access    MCPAccessStore
	Roles     RoleStore
	Sprints   SprintReadStore
	Deletions DeletionStore
}

// MCPDeletionService owns the MCP deletion preparation gate and one deletion.
// It deliberately has no transition, projection-read, or publisher capability.
type MCPDeletionService struct {
	access    MCPAccessStore
	roles     RoleStore
	sprints   SprintReadStore
	deletions DeletionStore
}

func NewMCPDeletionService(deps MCPDeletionServiceDependencies) *MCPDeletionService {
	return &MCPDeletionService{
		access:    deps.Access,
		roles:     deps.Roles,
		sprints:   deps.Sprints,
		deletions: deps.Deletions,
	}
}

// PreparedMCPDelete binds the exact context plus the resolved project and
// requested stored sprint identities to one deletion.
type PreparedMCPDelete struct {
	ctx       context.Context
	service   *MCPDeletionService
	projectID int64
	sprintID  int64
}

// PrepareDelete applies the common MCP authorization and target gate without
// introducing a lifecycle-state restriction.
func (s *MCPDeletionService) PrepareDelete(
	ctx context.Context,
	target MCPDeletionTarget,
) (*PreparedMCPDelete, error) {
	project, _, err := prepareMCPSprintMutationTarget(
		ctx,
		s.access,
		s.roles,
		s.sprints,
		target.ProjectSlug,
		target.Mode,
		target.SprintID,
	)
	if err != nil {
		return nil, err
	}
	if !project.SprintsEnabled {
		return nil, store.ErrSprintsDisabled
	}

	return &PreparedMCPDelete{
		ctx:       ctx,
		service:   s,
		projectID: project.ID,
		sprintID:  target.SprintID,
	}, nil
}

// Delete performs exactly one project-scoped deletion and returns its raw
// error. Public success projection remains adapter-owned.
func (p *PreparedMCPDelete) Delete() error {
	return p.service.deletions.DeleteSprint(p.ctx, p.projectID, p.sprintID)
}
