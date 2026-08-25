package useradmin

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// RESTDeletionServiceDependencies contains only the destructive persistence
// capability required by REST user deletion.
type RESTDeletionServiceDependencies struct {
	Deletions UserDeletionStore
}

// RESTDeletionService binds trusted actor identity and performs one user
// deletion without adding a requester read or role classification.
type RESTDeletionService struct {
	deletions UserDeletionStore
}

// NewRESTDeletionService constructs the REST deletion service.
func NewRESTDeletionService(deps RESTDeletionServiceDependencies) *RESTDeletionService {
	return &RESTDeletionService{deletions: deps.Deletions}
}

// PreparedRESTDeletion binds one exact actor, target, and request context for
// a single destructive execution.
type PreparedRESTDeletion struct {
	ctx          context.Context
	service      *RESTDeletionService
	requesterID  int64
	targetUserID int64
	executeOnce  sync.Once
}

// Prepare binds trusted actor identity and losslessly copies the target. It
// deliberately performs no requester read, target read, role check, or write.
func (s *RESTDeletionService) Prepare(
	ctx context.Context,
	command DeleteCommand,
) (*PreparedRESTDeletion, error) {
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	return &PreparedRESTDeletion{
		ctx:          ctx,
		service:      s,
		requesterID:  requesterID,
		targetUserID: command.TargetUserID,
	}, nil
}

// Delete executes the existing store-owned deletion exactly once. Store
// authorization, target lookup, transaction, cascades, and rollback remain
// authoritative.
func (p *PreparedRESTDeletion) Delete() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}

	return p.service.deletions.DeleteUser(
		p.ctx,
		p.requesterID,
		p.targetUserID,
	)
}
