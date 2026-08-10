package membership

import (
	"context"

	"scrumboy/internal/store"
)

// AddCommand contains the adapter-prepared target and canonical role for adding
// a project member. Requester identity is bound separately from request context
// by the future prepared service.
type AddCommand struct {
	TargetUserID int64
	Role         store.ProjectRole
}

// UpdateRoleCommand contains the adapter-prepared target and canonical role for
// changing an existing project membership.
type UpdateRoleCommand struct {
	TargetUserID int64
	Role         store.ProjectRole
}

// RemoveCommand identifies the project member to remove.
type RemoveCommand struct {
	TargetUserID int64
}

// MutationStore is the persistence capability shared by future REST and MCP
// membership-mutation services. Transport parsing and public validation remain
// adapter-owned; the store remains authoritative for membership authorization,
// invariants, audits, assignment cleanup, persistence, and transactions.
type MutationStore interface {
	AddProjectMember(
		ctx context.Context,
		requesterID int64,
		projectID int64,
		targetUserID int64,
		role store.ProjectRole,
	) error
	UpdateProjectMemberRole(
		ctx context.Context,
		requesterID int64,
		projectID int64,
		targetUserID int64,
		role store.ProjectRole,
	) error
	RemoveProjectMember(
		ctx context.Context,
		requesterID int64,
		projectID int64,
		targetUserID int64,
	) error
}

// MemberListStore is separate from MutationStore because the transports use
// the authorization-sensitive post-write read differently: REST returns the
// complete list, MCP add and update select the target, and MCP remove does not
// read the list.
type MemberListStore interface {
	ListProjectMembers(
		ctx context.Context,
		projectID int64,
		requesterID int64,
	) ([]store.ProjectMember, error)
}
