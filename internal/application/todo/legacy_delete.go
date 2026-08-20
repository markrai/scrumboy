package todo

import (
	"context"

	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

// LegacyDeleteProjectStore is the project lookup capability required by the
// numeric compatibility DELETE route. TodoID is the global todos.id identity.
type LegacyDeleteProjectStore interface {
	GetProjectIDForTodo(ctx context.Context, todoID int64) (int64, error)
}

// LegacyGlobalDeleteStore is the persistence capability required by the
// numeric compatibility DELETE route. TodoID is the global todos.id identity.
type LegacyGlobalDeleteStore interface {
	DeleteTodo(ctx context.Context, todoID int64, mode store.Mode) error
}

// LegacyDeleteServiceDependencies names the pre-delete project lookup,
// global-ID deletion, and REST refresh capabilities used by the numeric
// DELETE compatibility use case.
type LegacyDeleteServiceDependencies struct {
	Projects LegacyDeleteProjectStore
	Delete   LegacyGlobalDeleteStore
	Refresh  BoardRefreshPublisher
}

// LegacyDeleteService preserves the numeric DELETE route's existing
// project-lookup, global-ID deletion, and post-commit refresh sequence.
// Authorization and durable side effects remain store-owned.
type LegacyDeleteService struct {
	projects LegacyDeleteProjectStore
	delete   LegacyGlobalDeleteStore
	refresh  BoardRefreshPublisher
}

func NewLegacyDeleteService(deps LegacyDeleteServiceDependencies) *LegacyDeleteService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &LegacyDeleteService{
		projects: deps.Projects,
		delete:   deps.Delete,
		refresh:  refresh,
	}
}

// LegacyDeleteTarget carries the global Todo identity and store mode already
// selected by the numeric REST adapter. TodoID is the global todos.id identity.
type LegacyDeleteTarget struct {
	TodoID int64
	Mode   store.Mode
}

// PreparedLegacyDelete binds the request context, global Todo ID, pre-read
// project ID, and mode for the subsequent deletion.
type PreparedLegacyDelete struct {
	ctx       context.Context
	service   *LegacyDeleteService
	todoID    int64
	projectID int64
	mode      store.Mode
}

// Prepare resolves the project ID exactly once before deletion, preserving
// the compatibility route's existing two-step sequence. It performs no
// authorization, validation, deletion, or identity translation.
func (s *LegacyDeleteService) Prepare(ctx context.Context, target LegacyDeleteTarget) (*PreparedLegacyDelete, error) {
	projectID, err := s.projects.GetProjectIDForTodo(ctx, target.TodoID)
	if err != nil {
		return nil, err
	}

	return &PreparedLegacyDelete{
		ctx:       ctx,
		service:   s,
		todoID:    target.TodoID,
		projectID: projectID,
		mode:      target.Mode,
	}, nil
}

// Delete performs exactly one global-ID deletion and publishes one board
// refresh using the project ID captured before the Todo is removed.
func (d *PreparedLegacyDelete) Delete() error {
	if err := d.service.delete.DeleteTodo(d.ctx, d.todoID, d.mode); err != nil {
		return err
	}

	d.service.refresh.PublishBoardRefresh(d.ctx, d.projectID, RefreshReasonTodoDeleted, refresh.Entity{})
	return nil
}
