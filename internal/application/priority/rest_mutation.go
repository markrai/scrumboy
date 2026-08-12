package priority

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

const (
	refreshReasonPriorityTierAdded   = "priority_tier_added"
	refreshReasonPriorityTierUpdated = "priority_tier_updated"
	refreshReasonPriorityTierDeleted = "priority_tier_deleted"
)

var (
	ErrActorRequired      = errors.New("priority mutation actor required")
	ErrMaintainerRequired = errors.New("priority mutation maintainer required")
)

// RESTMutationRoleStore is the explicit project-role lookup required by the
// canonical REST priority-tier mutation boundary.
type RESTMutationRoleStore interface {
	GetProjectRole(
		ctx context.Context,
		projectID int64,
		userID int64,
	) (store.ProjectRole, error)
}

// BoardRefreshPublisher is the ancillary invalidation capability used by REST
// priority-tier mutations. Publishing is best-effort and cannot change
// mutation success.
type BoardRefreshPublisher interface {
	PublishBoardRefresh(ctx context.Context, projectID int64, reason string)
}

// BoardRefreshPublisherFunc adapts a function to BoardRefreshPublisher.
type BoardRefreshPublisherFunc func(ctx context.Context, projectID int64, reason string)

func (f BoardRefreshPublisherFunc) PublishBoardRefresh(ctx context.Context, projectID int64, reason string) {
	if f != nil {
		f(ctx, projectID, reason)
	}
}

type nopBoardRefreshPublisher struct{}

func (nopBoardRefreshPublisher) PublishBoardRefresh(context.Context, int64, string) {}

// RESTMutationServiceDependencies names the authorization, persistence, and
// ancillary capabilities used by canonical REST priority-tier mutations.
type RESTMutationServiceDependencies struct {
	Roles     RESTMutationRoleStore
	Mutations MutationStore
	Refresh   BoardRefreshPublisher
}

// RESTMutationService owns the explicit REST Maintainer gate, priority-tier
// persistence sequencing, and post-commit board refresh publication.
type RESTMutationService struct {
	roles     RESTMutationRoleStore
	mutations MutationStore
	refresh   BoardRefreshPublisher
}

func NewRESTMutationService(deps RESTMutationServiceDependencies) *RESTMutationService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &RESTMutationService{
		roles:     deps.Roles,
		mutations: deps.Mutations,
		refresh:   refresh,
	}
}

// ResolvedRESTMutationTarget carries only the project identity already
// resolved by the shared REST board router. Preparation performs no slug
// lookup.
type ResolvedRESTMutationTarget struct {
	ProjectID int64
}

// PreparedRESTMutation binds the authorized project identity and exact
// request context to subsequent priority-tier mutations.
type PreparedRESTMutation struct {
	ctx       context.Context
	service   *RESTMutationService
	projectID int64
}

// Prepare mirrors the workflow-column REST authorization order: actor
// extraction and an explicit fresh Maintainer-role lookup happen after router
// access but before operation-specific parsing and validation.
func (s *RESTMutationService) Prepare(
	ctx context.Context,
	target ResolvedRESTMutationTarget,
) (*PreparedRESTMutation, error) {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	role, err := s.roles.GetProjectRole(ctx, target.ProjectID, userID)
	if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}

	return &PreparedRESTMutation{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
	}, nil
}

// Create persists one adapter-prepared create command and publishes the REST
// invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Create(command CreateCommand) (store.PriorityTier, error) {
	tier, err := p.service.mutations.AddPriorityTier(p.ctx, p.projectID, command.Name)
	if err != nil {
		return store.PriorityTier{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonPriorityTierAdded)
	return tier, nil
}

// Update persists one adapter-prepared update command and publishes the REST
// invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Update(command UpdateCommand) error {
	err := p.service.mutations.UpdatePriorityTier(
		p.ctx,
		p.projectID,
		command.Key,
		command.Name,
		command.Color,
	)
	if err != nil {
		return err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonPriorityTierUpdated)
	return nil
}

// Delete persists one adapter-prepared delete command and publishes the REST
// invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Delete(command DeleteCommand) error {
	if err := p.service.mutations.DeletePriorityTier(p.ctx, p.projectID, command.Key); err != nil {
		return err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonPriorityTierDeleted)
	return nil
}
