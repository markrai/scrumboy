package sprint

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrSprintNotInProject reports that a sprint-mutation target was read
	// successfully but belongs to a different resolved project.
	ErrSprintNotInProject = errors.New("sprint definition target not in project")
)

// MCPAccessStore resolves the project-slug access boundary shared by canonical
// MCP sprint-definition, lifecycle, and deletion mutations.
type MCPAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// SprintReadStore provides target and post-mutation sprint reads shared by
// prepared sprint definition, lifecycle, and deletion operations.
type SprintReadStore interface {
	GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error)
}

// MCPDefinitionServiceDependencies names the access, fresh-role,
// definition-write, and sprint-read capabilities used by MCP definitions.
type MCPDefinitionServiceDependencies struct {
	Access      MCPAccessStore
	Roles       RoleStore
	Definitions DefinitionStore
	Sprints     SprintReadStore
}

// MCPDefinitionService owns MCP project access, actor/fresh-role
// authorization, update target verification, definition persistence, and the
// update result read. It deliberately has no publication or transport
// dependency.
type MCPDefinitionService struct {
	access      MCPAccessStore
	roles       RoleStore
	definitions DefinitionStore
	sprints     SprintReadStore
}

func NewMCPDefinitionService(deps MCPDefinitionServiceDependencies) *MCPDefinitionService {
	return &MCPDefinitionService{
		access:      deps.Access,
		roles:       deps.Roles,
		definitions: deps.Definitions,
		sprints:     deps.Sprints,
	}
}

// MCPProjectTarget carries only the slug and mode needed to prepare an MCP
// sprint-definition create capability.
type MCPProjectTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// MCPSprintTarget carries only the slug, stored sprint ID, and mode needed to
// prepare an MCP sprint-definition update capability.
type MCPSprintTarget struct {
	ProjectSlug string
	SprintID    int64
	Mode        store.Mode
}

// PreparedMCPCreate binds the exact authorized context and resolved project ID
// to one subsequent MCP sprint-definition create.
type PreparedMCPCreate struct {
	ctx       context.Context
	service   *MCPDefinitionService
	projectID int64
}

// PreparedMCPUpdate binds the exact authorized context, target-verified stored
// sprint ID, and pre-read sprint to one subsequent MCP definition update.
type PreparedMCPUpdate struct {
	ctx      context.Context
	service  *MCPDefinitionService
	sprintID int64
	existing store.Sprint
}

// PrepareCreate preserves the characterized MCP order: project-slug access,
// actor extraction, then a fresh role lookup.
func (s *MCPDefinitionService) PrepareCreate(
	ctx context.Context,
	target MCPProjectTarget,
) (*PreparedMCPCreate, error) {
	project, err := s.authorize(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	if !project.SprintsEnabled {
		return nil, store.ErrSprintsDisabled
	}

	return &PreparedMCPCreate{
		ctx:       ctx,
		service:   s,
		projectID: project.ID,
	}, nil
}

// PrepareUpdate preserves the characterized MCP authorization order, then
// reads the requested stored sprint and verifies its resolved project.
func (s *MCPDefinitionService) PrepareUpdate(
	ctx context.Context,
	target MCPSprintTarget,
) (*PreparedMCPUpdate, error) {
	project, err := s.authorize(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}

	existing, err := s.sprints.GetSprintByID(ctx, target.SprintID)
	if err != nil {
		return nil, err
	}
	if existing.ProjectID != project.ID {
		return nil, ErrSprintNotInProject
	}
	if !project.SprintsEnabled {
		return nil, store.ErrSprintsDisabled
	}

	return &PreparedMCPUpdate{
		ctx:      ctx,
		service:  s,
		sprintID: target.SprintID,
		existing: existing,
	}, nil
}

func (s *MCPDefinitionService) authorize(
	ctx context.Context,
	projectSlug string,
	mode store.Mode,
) (store.Project, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, projectSlug, mode)
	if err != nil {
		return store.Project{}, err
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return store.Project{}, ErrActorRequired
	}

	role, err := s.roles.GetProjectRole(ctx, projectContext.Project.ID, userID)
	if err != nil {
		return store.Project{}, err
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return store.Project{}, ErrMaintainerRequired
	}

	return projectContext.Project, nil
}

// Create persists one adapter-prepared definition and returns the exact store
// result. MCP definition services are structurally unable to publish.
func (p *PreparedMCPCreate) Create(command CreateCommand) (store.Sprint, error) {
	sprint, err := p.service.definitions.CreateSprint(
		p.ctx,
		p.projectID,
		command.Name,
		command.PlannedStartAt,
		command.PlannedEndAt,
	)
	if err != nil {
		return store.Sprint{}, err
	}
	return sprint, nil
}

// Update returns the retained target for an empty command. A non-empty command
// performs one definition write followed by one result read using the bound,
// target-verified stored sprint ID.
func (p *PreparedMCPUpdate) Update(command UpdateCommand) (store.Sprint, error) {
	if !command.HasFields() {
		return p.existing, nil
	}

	if err := p.service.definitions.UpdateSprint(
		p.ctx,
		p.sprintID,
		MaterializeUpdateInput(command),
	); err != nil {
		return store.Sprint{}, err
	}

	updated, err := p.service.sprints.GetSprintByID(p.ctx, p.sprintID)
	if err != nil {
		return store.Sprint{}, err
	}
	return updated, nil
}
