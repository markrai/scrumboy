package sprint

import (
	"context"
	"errors"
	"time"

	"scrumboy/internal/store"
)

var (
	// ErrSprintMustBePlanned classifies the MCP activation precondition without
	// embedding transport fields, codes, or target identity.
	ErrSprintMustBePlanned = errors.New("sprint must be planned to activate")
	// ErrSprintEndNotAfterNow classifies the MCP preparation-time schedule
	// precondition. The store retains its own authoritative mutation-time check.
	ErrSprintEndNotAfterNow = errors.New("sprint end must be after now")
	// ErrSprintMustBeActive classifies the MCP close precondition without
	// embedding transport fields, codes, or target identity.
	ErrSprintMustBeActive = errors.New("sprint must be active to close")
)

// MCPLifecycleTarget carries only adapter-validated transport identity. Access,
// fresh-role authorization, and sprint ownership are established by preparation.
type MCPLifecycleTarget struct {
	ProjectSlug string
	SprintID    int64
	Mode        store.Mode
}

// MCPLifecycleServiceDependencies names the least capabilities required for
// MCP activation and close orchestration. It deliberately has no publisher.
type MCPLifecycleServiceDependencies struct {
	Access      MCPAccessStore
	Roles       RoleStore
	Sprints     SprintReadStore
	Transitions TransitionStore
	Now         func() time.Time
}

// MCPLifecycleService owns MCP access, fresh-role authorization, target and
// state preparation, transition persistence, and post-write sprint projection.
type MCPLifecycleService struct {
	access      MCPAccessStore
	roles       RoleStore
	sprints     SprintReadStore
	transitions TransitionStore
	now         func() time.Time
}

func NewMCPLifecycleService(deps MCPLifecycleServiceDependencies) *MCPLifecycleService {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &MCPLifecycleService{
		access:      deps.Access,
		roles:       deps.Roles,
		sprints:     deps.Sprints,
		transitions: deps.Transitions,
		now:         now,
	}
}

// PreparedMCPActivate binds the exact context plus the resolved project and
// requested stored sprint identities to one activation.
type PreparedMCPActivate struct {
	ctx       context.Context
	service   *MCPLifecycleService
	projectID int64
	sprintID  int64
}

// PreparedMCPClose binds the exact context plus the resolved project and
// requested stored sprint identities to one close.
type PreparedMCPClose struct {
	ctx       context.Context
	service   *MCPLifecycleService
	projectID int64
	sprintID  int64
}

// PrepareActivate preserves MCP's access, fresh-role, target, state, and time
// ordering. Its time check is a preparation precondition, not a replacement for
// the store's authoritative mutation-time validation.
func (s *MCPLifecycleService) PrepareActivate(
	ctx context.Context,
	target MCPLifecycleTarget,
) (*PreparedMCPActivate, error) {
	project, existing, err := prepareMCPSprintMutationTarget(
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
	if existing.State != store.SprintStatePlanned {
		return nil, ErrSprintMustBePlanned
	}
	if !existing.PlannedEndAt.After(s.now().UTC()) {
		return nil, ErrSprintEndNotAfterNow
	}

	return &PreparedMCPActivate{
		ctx:       ctx,
		service:   s,
		projectID: project.ID,
		sprintID:  target.SprintID,
	}, nil
}

// PrepareClose preserves MCP's access, fresh-role, target, and ACTIVE-state
// ordering. Close has no application clock dependency.
func (s *MCPLifecycleService) PrepareClose(
	ctx context.Context,
	target MCPLifecycleTarget,
) (*PreparedMCPClose, error) {
	project, existing, err := prepareMCPSprintMutationTarget(
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
	if existing.State != store.SprintStateActive {
		return nil, ErrSprintMustBeActive
	}

	return &PreparedMCPClose{
		ctx:       ctx,
		service:   s,
		projectID: project.ID,
		sprintID:  target.SprintID,
	}, nil
}

// Activate performs one store transition and, only after success, one result
// read. It deliberately performs no second application time check.
func (p *PreparedMCPActivate) Activate() (store.Sprint, error) {
	if err := p.service.transitions.ActivateSprint(p.ctx, p.projectID, p.sprintID); err != nil {
		return store.Sprint{}, err
	}
	return p.service.sprints.GetSprintByID(p.ctx, p.sprintID)
}

// Close performs one store transition and, only after success, one result read.
func (p *PreparedMCPClose) Close() (store.Sprint, error) {
	if err := p.service.transitions.CloseSprint(p.ctx, p.projectID, p.sprintID); err != nil {
		return store.Sprint{}, err
	}
	return p.service.sprints.GetSprintByID(p.ctx, p.sprintID)
}

// prepareMCPSprintMutationTarget is the shared existence and authorization gate
// for MCP lifecycle and deletion operations. The returned sprint proves target
// state/project membership; its identity never replaces the requested ID.
func prepareMCPSprintMutationTarget(
	ctx context.Context,
	access MCPAccessStore,
	roles RoleStore,
	sprints SprintReadStore,
	projectSlug string,
	mode store.Mode,
	sprintID int64,
) (store.Project, store.Sprint, error) {
	projectContext, err := access.GetProjectContextBySlug(ctx, projectSlug, mode)
	if err != nil {
		return store.Project{}, store.Sprint{}, err
	}

	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return store.Project{}, store.Sprint{}, ErrActorRequired
	}

	projectID := projectContext.Project.ID
	role, err := roles.GetProjectRole(ctx, projectID, actorID)
	if err != nil {
		return store.Project{}, store.Sprint{}, err
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return store.Project{}, store.Sprint{}, ErrMaintainerRequired
	}

	existing, err := sprints.GetSprintByID(ctx, sprintID)
	if err != nil {
		return store.Project{}, store.Sprint{}, err
	}
	if existing.ProjectID != projectID {
		return store.Project{}, store.Sprint{}, ErrSprintNotInProject
	}
	return projectContext.Project, existing, nil
}
