package todo

import (
	"context"

	"scrumboy/internal/store"
)

// LegacyMoveStore is the persistence capability required by the numeric
// compatibility MOVE route. TodoID, afterID, and beforeID are global todos.id
// identities.
type LegacyMoveStore interface {
	MoveTodo(
		ctx context.Context,
		todoID int64,
		toColumnKey string,
		afterID *int64,
		beforeID *int64,
		mode store.Mode,
	) (store.Todo, error)
}

// LegacyMoveCommand preserves the numeric MOVE vocabulary. TodoID is the
// global ID of the moving Todo; AfterTodoID and BeforeTodoID are global IDs
// of the optional anchors.
type LegacyMoveCommand struct {
	TodoID       int64
	ToColumnKey  string
	AfterTodoID  *int64
	BeforeTodoID *int64
}

// LegacyMoveResult returns only the persisted domain value needed by the
// numeric HTTP adapter's existing projection.
type LegacyMoveResult struct {
	Todo store.Todo
}

// LegacyMoveServiceDependencies names the global move and REST refresh
// capabilities used by the numeric MOVE compatibility use case.
type LegacyMoveServiceDependencies struct {
	Move            LegacyMoveStore
	Refresh         BoardRefreshPublisher
	Projects        CreatorNotificationProjectStore
	CreatorRequests CreatorNotificationRequestPublisher
}

// LegacyMoveService owns global-ID move persistence and post-commit refresh
// sequencing. Validation, authorization, anchor resolution, and projection
// remain in the HTTP adapter or store.
type LegacyMoveService struct {
	move                   LegacyMoveStore
	refresh                BoardRefreshPublisher
	projects               CreatorNotificationProjectStore
	creatorRequests        CreatorNotificationRequestPublisher
	creatorRequestsEnabled bool
}

func NewLegacyMoveService(deps LegacyMoveServiceDependencies) *LegacyMoveService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	creatorRequests := deps.CreatorRequests
	creatorRequestsEnabled := creatorRequests != nil
	if creatorRequests == nil {
		creatorRequests = nopCreatorNotificationRequestPublisher{}
	}
	return &LegacyMoveService{
		move:                   deps.Move,
		refresh:                refresh,
		projects:               deps.Projects,
		creatorRequests:        creatorRequests,
		creatorRequestsEnabled: creatorRequestsEnabled,
	}
}

// LegacyMoveTarget carries only the store mode already selected by the
// numeric REST adapter. The compatibility route has no resolved project
// context before its authoritative global-ID mutation.
type LegacyMoveTarget struct {
	Mode store.Mode
}

// PreparedLegacyMove binds the request context and mode without performing
// lookup, validation, authorization, or identity translation.
type PreparedLegacyMove struct {
	ctx     context.Context
	service *LegacyMoveService
	mode    store.Mode
}

// Prepare performs no persistence or access resolution.
func (s *LegacyMoveService) Prepare(ctx context.Context, target LegacyMoveTarget) *PreparedLegacyMove {
	return &PreparedLegacyMove{
		ctx:     ctx,
		service: s,
		mode:    target.Mode,
	}
}

// Move forwards the global Todo and anchor identities to the authoritative
// store mutation exactly once, then publishes one board refresh on success.
func (m *PreparedLegacyMove) Move(command LegacyMoveCommand) (LegacyMoveResult, error) {
	moved, err := m.service.move.MoveTodo(
		m.ctx,
		command.TodoID,
		command.ToColumnKey,
		command.AfterTodoID,
		command.BeforeTodoID,
		m.mode,
	)
	if err != nil {
		return LegacyMoveResult{}, err
	}

	effectCtx := m.ctx
	if m.service.creatorRequestsEnabled && m.service.projects != nil && shouldRequestCreatorNotification(m.ctx, moved) {
		if project, projectErr := m.service.projects.GetProject(m.ctx, moved.ProjectID); projectErr == nil {
			effectCtx = publishCreatorNotificationRequest(m.ctx, m.service.creatorRequests, project, moved, RefreshReasonTodoMoved, true)
		}
	}
	m.service.refresh.PublishBoardRefresh(effectCtx, moved.ProjectID, RefreshReasonTodoMoved, todoRefreshEntity(moved))
	return LegacyMoveResult{Todo: moved}, nil
}
