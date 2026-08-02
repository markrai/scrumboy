package todo

import (
	"context"

	"scrumboy/internal/store"
)

const RefreshReasonTodoMoved = "todo_moved"

// MoveCommand is the transport-independent command for a project-local todo
// move. Neighbor identifiers are project-local todo IDs.
type MoveCommand struct {
	LocalID       int64
	ToColumnKey   string
	AfterLocalID  *int64
	BeforeLocalID *int64
}

// MoveResult contains domain values for transport-owned projection.
type MoveResult struct {
	Project store.Project
	Todo    store.Todo
}

// MoveStore is the persistence capability required to move a todo addressed
// by its project-local identifier.
type MoveStore interface {
	MoveTodoByLocalID(
		ctx context.Context,
		projectID int64,
		localID int64,
		toColumnKey string,
		afterLocalID *int64,
		beforeLocalID *int64,
		mode store.Mode,
	) (store.Todo, error)
}

// BoardRefreshPublisher is the ancillary invalidation capability used by REST
// moves. Publishing is best-effort and must not change command success.
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

// MoveServiceDependencies names the persistence and ancillary capabilities
// used by the canonical REST move use case.
type MoveServiceDependencies struct {
	Move    MoveStore
	Refresh BoardRefreshPublisher
}

// MoveService owns REST move persistence and post-commit refresh sequencing.
// Slug access remains in the shared REST board router so the route can reuse
// its already-authorized ProjectContext without a second lookup.
type MoveService struct {
	move    MoveStore
	refresh BoardRefreshPublisher
}

func NewMoveService(deps MoveServiceDependencies) *MoveService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &MoveService{move: deps.Move, refresh: refresh}
}

// ResolvedMoveTarget carries the project context already authorized by the
// shared REST board router.
type ResolvedMoveTarget struct {
	ProjectContext store.ProjectContext
	Mode           store.Mode
}

// PreparedMove binds the request context and a value copy of the authorized
// project context to the subsequent mutation.
type PreparedMove struct {
	ctx            context.Context
	service        *MoveService
	projectContext store.ProjectContext
	mode           store.Mode
}

// Prepare binds an already-resolved REST board target. It intentionally does
// not repeat slug access performed by handleBoard.
func (s *MoveService) Prepare(ctx context.Context, target ResolvedMoveTarget) *PreparedMove {
	return &PreparedMove{
		ctx:            ctx,
		service:        s,
		projectContext: target.ProjectContext,
		mode:           target.Mode,
	}
}

// Move executes the command with the context used during preparation and then
// publishes exactly one best-effort REST refresh after a successful store
// mutation.
func (m *PreparedMove) Move(command MoveCommand) (MoveResult, error) {
	project := m.projectContext.Project
	todo, err := m.service.move.MoveTodoByLocalID(
		m.ctx,
		project.ID,
		command.LocalID,
		command.ToColumnKey,
		command.AfterLocalID,
		command.BeforeLocalID,
		m.mode,
	)
	if err != nil {
		return MoveResult{}, err
	}

	m.service.refresh.PublishBoardRefresh(m.ctx, project.ID, RefreshReasonTodoMoved)
	return MoveResult{Project: project, Todo: todo}, nil
}
