package sprint

import (
	"context"

	"scrumboy/internal/store"
)

// RESTTransitionPublisher exposes the semantic invalidations produced by
// successful canonical REST sprint transitions. The HTTP adapter remains
// responsible for translating them to runtime refresh reasons.
type RESTTransitionPublisher interface {
	PublishSprintActivated(ctx context.Context, projectID int64)
	PublishSprintClosed(ctx context.Context, projectID int64)
}

type nopRESTTransitionPublisher struct{}

func (nopRESTTransitionPublisher) PublishSprintActivated(context.Context, int64) {}
func (nopRESTTransitionPublisher) PublishSprintClosed(context.Context, int64)    {}

// RESTLifecycleServiceDependencies names the fresh-role, close-target-read,
// transition-write, and semantic publication capabilities used by canonical
// REST sprint transitions.
type RESTLifecycleServiceDependencies struct {
	Roles       RoleStore
	Sprints     SprintReadStore
	Transitions TransitionStore
	Publisher   RESTTransitionPublisher
}

// RESTLifecycleService owns canonical REST actor/fresh-role authorization,
// close target verification, transition sequencing, and post-write semantic
// publication. It deliberately owns no transport parsing, state policy, clock,
// or runtime event vocabulary.
type RESTLifecycleService struct {
	roles       RoleStore
	sprints     SprintReadStore
	transitions TransitionStore
	publisher   RESTTransitionPublisher
}

func NewRESTLifecycleService(deps RESTLifecycleServiceDependencies) *RESTLifecycleService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTTransitionPublisher{}
	}
	return &RESTLifecycleService{
		roles:       deps.Roles,
		sprints:     deps.Sprints,
		transitions: deps.Transitions,
		publisher:   publisher,
	}
}

// PreparedRESTActivate binds the exact authorized context and copied numeric
// identities to one subsequent REST sprint activation.
type PreparedRESTActivate struct {
	ctx       context.Context
	service   *RESTLifecycleService
	projectID int64
	sprintID  int64
}

// PreparedRESTClose binds the exact authorized context and copied numeric
// identities after the close target has passed its project-identity gate.
type PreparedRESTClose struct {
	ctx       context.Context
	service   *RESTLifecycleService
	projectID int64
	sprintID  int64
}

// PrepareActivate preserves REST's actor and fresh-role gate without adding a
// sprint read, lifecycle-state precheck, or time policy.
func (s *RESTLifecycleService) PrepareActivate(
	ctx context.Context,
	target TransitionTarget,
) (*PreparedRESTActivate, error) {
	if err := authorizeRESTLifecycleMutation(ctx, s.roles, target.ProjectID); err != nil {
		return nil, err
	}
	return &PreparedRESTActivate{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
		sprintID:  target.SprintID,
	}, nil
}

// PrepareClose preserves REST's actor/fresh-role/target-read ordering. The
// target read is an existence and project-identity gate; state policy and the
// final project-scoped mutation remain authoritative in the store.
func (s *RESTLifecycleService) PrepareClose(
	ctx context.Context,
	target TransitionTarget,
) (*PreparedRESTClose, error) {
	if err := authorizeRESTLifecycleMutation(ctx, s.roles, target.ProjectID); err != nil {
		return nil, err
	}

	sprint, err := s.sprints.GetSprintByID(ctx, target.SprintID)
	if err != nil {
		return nil, err
	}
	if sprint.ProjectID != target.ProjectID {
		return nil, ErrSprintNotInProject
	}

	return &PreparedRESTClose{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
		sprintID:  target.SprintID,
	}, nil
}

func authorizeRESTLifecycleMutation(ctx context.Context, roles RoleStore, projectID int64) error {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return ErrActorRequired
	}

	role, err := roles.GetProjectRole(ctx, projectID, userID)
	if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
		// Canonical REST deliberately collapses role dependency failures and
		// insufficient authority into the same fixed forbidden result.
		return ErrMaintainerRequired
	}
	return nil
}

// Activate performs exactly one project-scoped transition and publishes only
// after persistence succeeds. A successful idempotent store result remains a
// publishable REST success.
func (p *PreparedRESTActivate) Activate() error {
	if err := p.service.transitions.ActivateSprint(p.ctx, p.projectID, p.sprintID); err != nil {
		return err
	}
	p.service.publisher.PublishSprintActivated(p.ctx, p.projectID)
	return nil
}

// Close performs exactly one project-scoped transition and publishes only
// after persistence succeeds.
func (p *PreparedRESTClose) Close() error {
	if err := p.service.transitions.CloseSprint(p.ctx, p.projectID, p.sprintID); err != nil {
		return err
	}
	p.service.publisher.PublishSprintClosed(p.ctx, p.projectID)
	return nil
}
