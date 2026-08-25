package useradmin

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// RESTRoleServiceDependencies contains only the role persistence and target
// projection capabilities required by the REST role-mutation sequence.
type RESTRoleServiceDependencies struct {
	Mutations      UserRoleMutationStore
	ProjectionRead UserReadStore
}

// RESTRoleService preserves REST's actor-bound mutation and post-write target
// projection without adding MCP's requester Owner pre-read.
type RESTRoleService struct {
	mutations      UserRoleMutationStore
	projectionRead UserReadStore
}

// NewRESTRoleService constructs the REST role service.
func NewRESTRoleService(deps RESTRoleServiceDependencies) *RESTRoleService {
	return &RESTRoleService{
		mutations:      deps.Mutations,
		projectionRead: deps.ProjectionRead,
	}
}

// PreparedRESTRoleChange binds the trusted actor, adapter-parsed command
// values, and exact request context for one REST role-mutation sequence.
type PreparedRESTRoleChange struct {
	ctx          context.Context
	service      *RESTRoleService
	requesterID  int64
	targetUserID int64
	newRole      store.SystemRole
	executeOnce  sync.Once
}

// Prepare binds trusted actor identity and losslessly copies the command. It
// deliberately performs no requester read, target read, validation, or write.
func (s *RESTRoleService) Prepare(
	ctx context.Context,
	command RoleChangeCommand,
) (*PreparedRESTRoleChange, error) {
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	return &PreparedRESTRoleChange{
		ctx:          ctx,
		service:      s,
		requesterID:  requesterID,
		targetUserID: command.TargetUserID,
		newRole:      command.NewRole,
	}, nil
}

// Update executes exactly one role mutation and, only after it succeeds, one
// target projection read. Neither failure path retries or compensates.
func (p *PreparedRESTRoleChange) Update() (store.User, error) {
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

	return p.service.projectionRead.GetUser(p.ctx, p.targetUserID)
}
