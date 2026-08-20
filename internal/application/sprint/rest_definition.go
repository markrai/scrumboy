package sprint

import (
	"context"
	"errors"
	"strings"

	"scrumboy/internal/store"
)

var (
	// ErrActorRequired reports that the exact context supplied to sprint
	// mutation preparation does not contain an authenticated requester.
	ErrActorRequired = errors.New("sprint definition actor required")
	// ErrMaintainerRequired reports that the authenticated requester lacks the
	// required role. REST mutation preparation deliberately also uses this
	// result to collapse role lookup failures; MCP preparation returns those
	// failures raw.
	ErrMaintainerRequired = errors.New("sprint definition maintainer required")
)

// RESTDefinitionPublisher exposes the two semantic invalidations produced by
// successful canonical REST sprint-definition mutations. The HTTP adapter
// remains responsible for translating them to runtime refresh reasons.
type RESTDefinitionPublisher interface {
	PublishSprintCreated(ctx context.Context, projectID int64, name string)
	PublishSprintUpdated(ctx context.Context, projectID int64, name string)
}

type nopRESTDefinitionPublisher struct{}

func (nopRESTDefinitionPublisher) PublishSprintCreated(context.Context, int64, string) {}
func (nopRESTDefinitionPublisher) PublishSprintUpdated(context.Context, int64, string) {}

// RESTDefinitionServiceDependencies names the fresh-role, definition-write,
// and ancillary publication capabilities used by REST sprint definitions.
type RESTDefinitionServiceDependencies struct {
	Roles       RoleStore
	Definitions DefinitionStore
	Publisher   RESTDefinitionPublisher
}

// RESTDefinitionService owns the canonical REST actor/fresh-role gate,
// definition-write sequencing, and post-write semantic publication.
type RESTDefinitionService struct {
	roles       RoleStore
	definitions DefinitionStore
	publisher   RESTDefinitionPublisher
}

func NewRESTDefinitionService(deps RESTDefinitionServiceDependencies) *RESTDefinitionService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTDefinitionPublisher{}
	}
	return &RESTDefinitionService{
		roles:       deps.Roles,
		definitions: deps.Definitions,
		publisher:   publisher,
	}
}

// ResolvedRESTProjectTarget carries only the numeric project identity already
// resolved by the shared REST board router.
type ResolvedRESTProjectTarget struct {
	ProjectID int64
}

// ResolvedRESTSprintTarget carries the route-resolved project identity and the
// stored global sprint ID already read and project-verified by the REST adapter.
type ResolvedRESTSprintTarget struct {
	ProjectID int64
	SprintID  int64
	Name      string // already-read display name; not a new acquisition
}

// PreparedRESTCreate binds the exact authorized context and project identity
// to one subsequent REST sprint-definition create.
type PreparedRESTCreate struct {
	ctx       context.Context
	service   *RESTDefinitionService
	projectID int64
}

// PreparedRESTUpdate binds the exact authorized context, project identity, and
// verified stored sprint ID to one subsequent REST sprint-definition update.
type PreparedRESTUpdate struct {
	ctx       context.Context
	service   *RESTDefinitionService
	projectID int64
	sprintID  int64
	name      string
}

// PrepareCreate preserves the current REST authorization order after shared
// board-router access and before transport-owned body decoding.
func (s *RESTDefinitionService) PrepareCreate(
	ctx context.Context,
	target ResolvedRESTProjectTarget,
) (*PreparedRESTCreate, error) {
	if err := s.authorize(ctx, target.ProjectID); err != nil {
		return nil, err
	}
	return &PreparedRESTCreate{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
	}, nil
}

// PrepareUpdate preserves the current REST authorization order after the
// adapter has read the stored sprint and verified its project identity.
func (s *RESTDefinitionService) PrepareUpdate(
	ctx context.Context,
	target ResolvedRESTSprintTarget,
) (*PreparedRESTUpdate, error) {
	if err := s.authorize(ctx, target.ProjectID); err != nil {
		return nil, err
	}
	return &PreparedRESTUpdate{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
		sprintID:  target.SprintID,
		name:      target.Name,
	}, nil
}

func (s *RESTDefinitionService) authorize(ctx context.Context, projectID int64) error {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return ErrActorRequired
	}

	role, err := s.roles.GetProjectRole(ctx, projectID, userID)
	if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
		// The canonical REST route deliberately discards role lookup causes,
		// including cancellation, and maps both branches to the same fixed 403.
		return ErrMaintainerRequired
	}
	return nil
}

// Create persists one adapter-prepared definition and publishes the semantic
// REST invalidation only after CreateSprint returns a sprint without error.
func (p *PreparedRESTCreate) Create(command CreateCommand) (store.Sprint, error) {
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

	p.service.publisher.PublishSprintCreated(p.ctx, p.projectID, sprint.Name)
	return sprint, nil
}

// Update persists one adapter-prepared command, including an empty command,
// and publishes the semantic REST invalidation only after persistence succeeds.
func (p *PreparedRESTUpdate) Update(command UpdateCommand) error {
	if err := p.service.definitions.UpdateSprint(
		p.ctx,
		p.sprintID,
		MaterializeUpdateInput(command),
	); err != nil {
		return err
	}

	p.service.publisher.PublishSprintUpdated(p.ctx, p.projectID, publishedSprintUpdateName(p.name, command))
	return nil
}

func publishedSprintUpdateName(retained string, command UpdateCommand) string {
	if command.Name != nil {
		return strings.TrimSpace(*command.Name)
	}
	return retained
}
