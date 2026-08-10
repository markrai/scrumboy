package membership

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

// MembershipAction identifies the established structured membership event
// action emitted after a successful canonical REST mutation.
type MembershipAction string

const (
	MembershipActionAdded       MembershipAction = "added"
	MembershipActionRoleChanged MembershipAction = "role_changed"
	MembershipActionRemoved     MembershipAction = "removed"
)

// ErrActorRequired reports that the exact context supplied to Prepare does not
// contain an authenticated requester.
var ErrActorRequired = errors.New("membership mutation actor required")

// RESTMutationPublisher exposes the two distinct best-effort publications made
// after a REST membership mutation and its authorization-sensitive post-read
// both succeed.
type RESTMutationPublisher interface {
	PublishMembersUpdated(ctx context.Context, projectID int64)
	PublishMembershipChanged(
		ctx context.Context,
		projectID int64,
		actorUserID int64,
		targetUserID int64,
		action MembershipAction,
	)
}

type nopRESTMutationPublisher struct{}

func (nopRESTMutationPublisher) PublishMembersUpdated(context.Context, int64) {}

func (nopRESTMutationPublisher) PublishMembershipChanged(
	context.Context,
	int64,
	int64,
	int64,
	MembershipAction,
) {
}

// RESTMutationServiceDependencies names the persistence, post-write read, and
// publication capabilities used by canonical REST membership mutations.
type RESTMutationServiceDependencies struct {
	Mutations MutationStore
	Members   MemberListStore
	Publisher RESTMutationPublisher
}

// RESTMutationService owns requester binding and the mutation, member-list read,
// and ordered publication sequence. Store authorization remains authoritative.
type RESTMutationService struct {
	mutations MutationStore
	members   MemberListStore
	publisher RESTMutationPublisher
}

func NewRESTMutationService(deps RESTMutationServiceDependencies) *RESTMutationService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTMutationPublisher{}
	}
	return &RESTMutationService{
		mutations: deps.Mutations,
		members:   deps.Members,
		publisher: publisher,
	}
}

// ResolvedRESTMutationTarget carries only the numeric project identity already
// parsed by the REST adapter. The store mutation remains the access boundary.
type ResolvedRESTMutationTarget struct {
	ProjectID int64
}

// PreparedRESTMutation binds the exact request context, requester identity, and
// project identity to subsequent membership mutations.
type PreparedRESTMutation struct {
	ctx         context.Context
	service     *RESTMutationService
	requesterID int64
	projectID   int64
}

// Prepare derives requester identity only after the adapter has completed its
// current path, body, and role validation. It performs no persistence, access
// lookup, member-list read, or publication.
func (s *RESTMutationService) Prepare(
	ctx context.Context,
	target ResolvedRESTMutationTarget,
) (*PreparedRESTMutation, error) {
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}
	return &PreparedRESTMutation{
		ctx:         ctx,
		service:     s,
		requesterID: requesterID,
		projectID:   target.ProjectID,
	}, nil
}

// Add performs one adapter-prepared add, then the established post-write read
// and ordered REST publications.
func (p *PreparedRESTMutation) Add(command AddCommand) ([]store.ProjectMember, error) {
	if err := p.service.mutations.AddProjectMember(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
		command.Role,
	); err != nil {
		return nil, err
	}
	return p.complete(command.TargetUserID, MembershipActionAdded)
}

// UpdateRole deliberately performs no semantic comparison. A successful store
// call always proceeds through the current read and publication sequence.
func (p *PreparedRESTMutation) UpdateRole(command UpdateRoleCommand) ([]store.ProjectMember, error) {
	if err := p.service.mutations.UpdateProjectMemberRole(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
		command.Role,
	); err != nil {
		return nil, err
	}
	return p.complete(command.TargetUserID, MembershipActionRoleChanged)
}

// Remove preserves the current self-removal behavior: a successful mutation may
// still be followed by an authorization-sensitive member-list failure.
func (p *PreparedRESTMutation) Remove(command RemoveCommand) ([]store.ProjectMember, error) {
	if err := p.service.mutations.RemoveProjectMember(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
	); err != nil {
		return nil, err
	}
	return p.complete(command.TargetUserID, MembershipActionRemoved)
}

func (p *PreparedRESTMutation) complete(
	targetUserID int64,
	action MembershipAction,
) ([]store.ProjectMember, error) {
	members, err := p.service.members.ListProjectMembers(p.ctx, p.projectID, p.requesterID)
	if err != nil {
		return nil, err
	}
	p.service.publisher.PublishMembersUpdated(p.ctx, p.projectID)
	p.service.publisher.PublishMembershipChanged(
		p.ctx,
		p.projectID,
		p.requesterID,
		targetUserID,
		action,
	)
	return members, nil
}
