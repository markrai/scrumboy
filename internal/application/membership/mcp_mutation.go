package membership

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrMaintainerRequired reports that the slug-resolved MCP project role does
	// not satisfy the current membership-mutation precheck.
	ErrMaintainerRequired = errors.New("membership mutation maintainer required")

	// ErrAddedMemberMissing identifies a successful add and member-list read
	// whose result does not contain the requested target.
	ErrAddedMemberMissing = errors.New("added member missing from post-write projection")

	// ErrUpdatedMemberMissing identifies a successful role update and member-list
	// read whose result does not contain the requested target.
	ErrUpdatedMemberMissing = errors.New("updated member missing from post-write projection")
)

// MCPMutationAccessStore resolves the project-slug access boundary used by MCP
// membership mutations.
type MCPMutationAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// MCPMutationServiceDependencies names the access, persistence, and post-write
// read capabilities used by canonical MCP membership mutations.
type MCPMutationServiceDependencies struct {
	Access    MCPMutationAccessStore
	Mutations MutationStore
	Members   MemberListStore
}

// MCPMutationService owns slug access, the current Maintainer precheck,
// requester binding, persistence, and add/update target selection. It
// deliberately has no refresh or event dependency.
type MCPMutationService struct {
	access    MCPMutationAccessStore
	mutations MutationStore
	members   MemberListStore
}

func NewMCPMutationService(deps MCPMutationServiceDependencies) *MCPMutationService {
	return &MCPMutationService{
		access:    deps.Access,
		mutations: deps.Mutations,
		members:   deps.Members,
	}
}

// MCPMutationTarget contains only the slug and mode needed for access. Neither
// value is retained after preparation.
type MCPMutationTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// PreparedMCPMutation binds the exact access context, requester identity, and
// resolved project identity to subsequent membership mutations.
type PreparedMCPMutation struct {
	ctx         context.Context
	service     *MCPMutationService
	requesterID int64
	projectID   int64
}

// Prepare preserves the current MCP ordering: slug access, resolved-role
// precheck, then requester extraction from the same context. Semantic input
// validation remains adapter-owned and must precede this call.
func (s *MCPMutationService) Prepare(
	ctx context.Context,
	target MCPMutationTarget,
) (*PreparedMCPMutation, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	if !projectContext.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}
	return &PreparedMCPMutation{
		ctx:         ctx,
		service:     s,
		requesterID: requesterID,
		projectID:   projectContext.Project.ID,
	}, nil
}

// Add persists one adapter-prepared command, then returns the requested member
// from the authorization-sensitive post-write list.
func (p *PreparedMCPMutation) Add(command AddCommand) (store.ProjectMember, error) {
	if err := p.service.mutations.AddProjectMember(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
		command.Role,
	); err != nil {
		return store.ProjectMember{}, err
	}

	members, err := p.service.members.ListProjectMembers(p.ctx, p.projectID, p.requesterID)
	if err != nil {
		return store.ProjectMember{}, err
	}
	for _, member := range members {
		if member.UserID == command.TargetUserID {
			return member, nil
		}
	}
	return store.ProjectMember{}, ErrAddedMemberMissing
}

// UpdateRole deliberately performs no semantic comparison. Every successful
// mutation proceeds to the current target-selecting post-write read.
func (p *PreparedMCPMutation) UpdateRole(command UpdateRoleCommand) (store.ProjectMember, error) {
	if err := p.service.mutations.UpdateProjectMemberRole(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
		command.Role,
	); err != nil {
		return store.ProjectMember{}, err
	}

	members, err := p.service.members.ListProjectMembers(p.ctx, p.projectID, p.requesterID)
	if err != nil {
		return store.ProjectMember{}, err
	}
	for _, member := range members {
		if member.UserID == command.TargetUserID {
			return member, nil
		}
	}
	return store.ProjectMember{}, ErrUpdatedMemberMissing
}

// Remove preserves the current MCP mutation-only path. Requested-slug and
// target-user response projection remain adapter-owned.
func (p *PreparedMCPMutation) Remove(command RemoveCommand) error {
	return p.service.mutations.RemoveProjectMember(
		p.ctx,
		p.requesterID,
		p.projectID,
		command.TargetUserID,
	)
}
