package workflow

import (
	"context"
	"errors"

	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

const (
	refreshReasonWorkflowColumnAdded   = "workflow_column_added"
	refreshReasonWorkflowColumnUpdated = "workflow_column_updated"
	refreshReasonWorkflowColumnDeleted = "workflow_column_deleted"
)

var (
	ErrActorRequired      = errors.New("workflow mutation actor required")
	ErrMaintainerRequired = errors.New("workflow mutation maintainer required")
)

// RESTMutationRoleStore is the explicit project-role lookup required by the
// canonical REST workflow mutation boundary.
type RESTMutationRoleStore interface {
	GetProjectRole(
		ctx context.Context,
		projectID int64,
		userID int64,
	) (store.ProjectRole, error)
}

// BoardRefreshPublisher is the ancillary invalidation capability used by REST
// workflow mutations. Publishing is best-effort and cannot change mutation
// success.
type BoardRefreshPublisher interface {
	PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity refresh.Entity)
}

// BoardRefreshPublisherFunc adapts a function to BoardRefreshPublisher.
type BoardRefreshPublisherFunc func(ctx context.Context, projectID int64, reason string, entity refresh.Entity)

func (f BoardRefreshPublisherFunc) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity refresh.Entity) {
	if f != nil {
		f(ctx, projectID, reason, entity)
	}
}

type nopBoardRefreshPublisher struct{}

func (nopBoardRefreshPublisher) PublishBoardRefresh(context.Context, int64, string, refresh.Entity) {}

// RESTMutationServiceDependencies names the authorization, persistence, and
// ancillary capabilities used by canonical REST workflow mutations.
type RESTMutationServiceDependencies struct {
	Roles     RESTMutationRoleStore
	Mutations MutationStore
	Refresh   BoardRefreshPublisher
}

// RESTMutationService owns the explicit REST Maintainer gate, workflow
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

// ResolvedRESTMutationTarget carries only the project identity already resolved
// by the shared REST board router. Preparation performs no slug lookup.
type ResolvedRESTMutationTarget struct {
	ProjectID int64
}

// PreparedRESTMutation binds the authorized project identity and exact request
// context to subsequent workflow mutations.
type PreparedRESTMutation struct {
	ctx       context.Context
	service   *RESTMutationService
	projectID int64
}

// Prepare preserves the current REST authorization order: actor extraction and
// an explicit fresh Maintainer-role lookup happen after router access but before
// operation-specific parsing and validation.
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
func (p *PreparedRESTMutation) Create(command CreateCommand) (store.WorkflowColumn, error) {
	column, err := p.service.mutations.AddWorkflowColumn(p.ctx, p.projectID, command.Name)
	if err != nil {
		return store.WorkflowColumn{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonWorkflowColumnAdded, refresh.Entity{Name: column.Name})
	return column, nil
}

// Update persists one adapter-prepared update command and publishes the REST
// invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Update(command UpdateCommand) error {
	err := p.service.mutations.UpdateWorkflowColumn(
		p.ctx,
		p.projectID,
		command.Key,
		command.Name,
		command.Color,
	)
	if err != nil {
		return err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonWorkflowColumnUpdated, refresh.Entity{Name: command.Name})
	return nil
}

// Delete persists one adapter-prepared delete command and publishes the REST
// invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Delete(command DeleteCommand) error {
	if err := p.service.mutations.DeleteWorkflowColumn(p.ctx, p.projectID, command.Key); err != nil {
		return err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonWorkflowColumnDeleted, refresh.Entity{})
	return nil
}
