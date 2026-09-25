package useradmin

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// MCPDeletionServiceDependencies separates the requester authority read from
// the destructive persistence capability.
type MCPDeletionServiceDependencies struct {
	RequesterRead UserReadStore
	Deletions     UserDeletionStore
}

// MCPDeletionService preserves MCP's requester read and exact Owner
// classification before one user deletion. It has no publication capability.
type MCPDeletionService struct {
	requesterRead UserReadStore
	deletions     UserDeletionStore
}

// NewMCPDeletionService constructs the MCP deletion service.
func NewMCPDeletionService(deps MCPDeletionServiceDependencies) *MCPDeletionService {
	return &MCPDeletionService{
		requesterRead: deps.RequesterRead,
		deletions:     deps.Deletions,
	}
}

// PreparedMCPDeletion binds one exact Owner requester, target, and request
// context for a single destructive execution.
type PreparedMCPDeletion struct {
	ctx          context.Context
	service      *MCPDeletionService
	requesterID  int64
	targetUserID int64
	executeOnce  sync.Once
}

// Prepare preserves MCP's trusted actor extraction, requester read, and exact
// Owner classification. It performs no target read or write.
func (s *MCPDeletionService) Prepare(
	ctx context.Context,
	command DeleteCommand,
) (*PreparedMCPDeletion, error) {
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

	return &PreparedMCPDeletion{
		ctx:          ctx,
		service:      s,
		requesterID:  requesterID,
		targetUserID: command.TargetUserID,
	}, nil
}

// Delete executes the existing store-owned deletion exactly once. It performs
// no target read, post-delete read, result projection, or publication.
func (p *PreparedMCPDeletion) Delete() error {
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
