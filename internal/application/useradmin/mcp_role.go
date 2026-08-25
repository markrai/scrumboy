package useradmin

import (
	"context"
	"errors"
	"sync"

	"scrumboy/internal/store"
)

var (
	// ErrOwnerRequired reports that the requester pre-read did not return an
	// exact system Owner. Admin is deliberately insufficient for MCP.
	ErrOwnerRequired = errors.New("user administration owner required")

	// ErrMCPRoleProjectionFailed classifies a target read that failed after a
	// successful MCP role mutation while retaining its underlying cause.
	ErrMCPRoleProjectionFailed = errors.New("user administration MCP role projection failed")
)

// mcpRoleProjectionError preserves the original cause and its exact text while
// marking the post-write projection stage for the MCP adapter mapper.
type mcpRoleProjectionError struct {
	cause error
}

func (e *mcpRoleProjectionError) Error() string {
	return e.cause.Error()
}

func (e *mcpRoleProjectionError) Unwrap() error {
	return e.cause
}

func (e *mcpRoleProjectionError) Is(target error) bool {
	return target == ErrMCPRoleProjectionFailed
}

// MCPRoleServiceDependencies separates requester authority reads from target
// projection reads even when production supplies one concrete store.
type MCPRoleServiceDependencies struct {
	RequesterRead  UserReadStore
	Mutations      UserRoleMutationStore
	ProjectionRead UserReadStore
}

// MCPRoleService owns MCP's requester read, exact Owner classification, role
// mutation, and post-write target projection. It has no publication capability.
type MCPRoleService struct {
	requesterRead  UserReadStore
	mutations      UserRoleMutationStore
	projectionRead UserReadStore
}

// NewMCPRoleService constructs the MCP role service.
func NewMCPRoleService(deps MCPRoleServiceDependencies) *MCPRoleService {
	return &MCPRoleService{
		requesterRead:  deps.RequesterRead,
		mutations:      deps.Mutations,
		projectionRead: deps.ProjectionRead,
	}
}

// PreparedMCPRoleChange binds the trusted Owner, adapter-parsed command
// values, and exact request context for one MCP role-mutation sequence.
type PreparedMCPRoleChange struct {
	ctx          context.Context
	service      *MCPRoleService
	requesterID  int64
	targetUserID int64
	newRole      store.SystemRole
	executeOnce  sync.Once
}

// Prepare preserves the characterized MCP order: trusted actor extraction,
// requester read, and exact Owner classification. It performs no write.
func (s *MCPRoleService) Prepare(
	ctx context.Context,
	command RoleChangeCommand,
) (*PreparedMCPRoleChange, error) {
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	requester, err := s.requesterRead.GetUser(ctx, requesterID)
	if err != nil {
		return nil, err
	}
	if requester.SystemRole != store.SystemRoleOwner {
		return nil, ErrOwnerRequired
	}

	return &PreparedMCPRoleChange{
		ctx:          ctx,
		service:      s,
		requesterID:  requesterID,
		targetUserID: command.TargetUserID,
		newRole:      command.NewRole,
	}, nil
}

// Update executes exactly one role mutation and one target projection read on
// success. Only projection failures receive stage classification.
func (p *PreparedMCPRoleChange) Update() (store.User, error) {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return store.User{}, ErrPreparedMutationAlreadyExecuted
	}

	if err := p.service.mutations.UpdateUserRole(
		p.ctx,
		p.requesterID,
		p.targetUserID,
		p.newRole,
	); err != nil {
		return store.User{}, err
	}

	updated, err := p.service.projectionRead.GetUser(p.ctx, p.targetUserID)
	if err != nil {
		return store.User{}, &mcpRoleProjectionError{cause: err}
	}
	return updated, nil
}
