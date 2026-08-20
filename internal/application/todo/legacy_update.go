package todo

import (
	"context"

	"scrumboy/internal/store"
)

// LegacyUpdateStore is the persistence capability required by the numeric
// compatibility PATCH route. TodoID is the global todos.id identity.
type LegacyUpdateStore interface {
	UpdateTodo(
		ctx context.Context,
		todoID int64,
		input store.UpdateTodoInput,
		mode store.Mode,
	) (store.Todo, error)
}

// CreatorNotificationProjectStore supplies the project facts that the legacy
// global-ID compatibility path learns only after its authoritative mutation.
type CreatorNotificationProjectStore interface {
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
}

// LegacyUpdateCommand preserves the numeric PATCH vocabulary. TodoID is a
// global Todo ID; the ordinary fields are replacement values, while
// PriorityKey retains omitted/clear/set presence. Sprint mutation is
// deliberately absent from this compatibility command.
type LegacyUpdateCommand struct {
	TodoID int64

	Title            string
	Body             string
	Tags             []string
	EstimationPoints *int64
	AssigneeUserID   *int64
	PriorityKey      Field[*string]
}

// LegacyUpdateResult returns only the persisted domain value needed by the
// numeric HTTP adapter's existing projection.
type LegacyUpdateResult struct {
	Todo store.Todo
}

// LegacyUpdateServiceDependencies names the global update and REST refresh
// capabilities used by the numeric PATCH compatibility use case.
type LegacyUpdateServiceDependencies struct {
	Update          LegacyUpdateStore
	Refresh         BoardRefreshPublisher
	Projects        CreatorNotificationProjectStore
	CreatorRequests CreatorNotificationRequestPublisher
}

// LegacyUpdateService owns global-ID update persistence and post-commit
// direct-refresh gating. Authorization and assignment publication remain in
// the store; HTTP validation, error mapping, and projection remain adapters.
type LegacyUpdateService struct {
	update                 LegacyUpdateStore
	refresh                BoardRefreshPublisher
	projects               CreatorNotificationProjectStore
	creatorRequests        CreatorNotificationRequestPublisher
	creatorRequestsEnabled bool
}

func NewLegacyUpdateService(deps LegacyUpdateServiceDependencies) *LegacyUpdateService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	creatorRequests := deps.CreatorRequests
	creatorRequestsEnabled := creatorRequests != nil
	if creatorRequests == nil {
		creatorRequests = nopCreatorNotificationRequestPublisher{}
	}
	return &LegacyUpdateService{
		update:                 deps.Update,
		refresh:                refresh,
		projects:               deps.Projects,
		creatorRequests:        creatorRequests,
		creatorRequestsEnabled: creatorRequestsEnabled,
	}
}

// LegacyUpdateTarget carries only the store mode already selected by the
// numeric REST adapter. The compatibility route has no resolved project
// context before its authoritative global-ID mutation.
type LegacyUpdateTarget struct {
	Mode store.Mode
}

// PreparedLegacyUpdate binds the request context and mode without performing
// lookup, validation, or authorization.
type PreparedLegacyUpdate struct {
	ctx     context.Context
	service *LegacyUpdateService
	mode    store.Mode
}

// Prepare performs no persistence or access resolution.
func (s *LegacyUpdateService) Prepare(ctx context.Context, target LegacyUpdateTarget) *PreparedLegacyUpdate {
	return &PreparedLegacyUpdate{
		ctx:     ctx,
		service: s,
		mode:    target.Mode,
	}
}

// Update forwards the global Todo identity and replacement input to the
// authoritative store mutation exactly once. A successful assignment change
// relies on the store-owned assignment event; all other successes publish one
// direct todo_updated board refresh.
func (u *PreparedLegacyUpdate) Update(command LegacyUpdateCommand) (LegacyUpdateResult, error) {
	var priorityKey *string
	if command.PriorityKey.Present {
		priorityKey = cloneUpdateStringPtr(command.PriorityKey.Value)
	}
	input := store.UpdateTodoInput{
		Title:              command.Title,
		Body:               command.Body,
		Tags:               cloneUpdateStrings(command.Tags),
		EstimationPoints:   cloneUpdateInt64Ptr(command.EstimationPoints),
		AssigneeUserID:     cloneUpdateInt64Ptr(command.AssigneeUserID),
		PriorityKey:        priorityKey,
		PriorityKeyPresent: command.PriorityKey.Present,
	}
	updated, err := u.service.update.UpdateTodo(u.ctx, command.TodoID, input, u.mode)
	if err != nil {
		return LegacyUpdateResult{}, err
	}

	effectCtx := u.ctx
	if u.service.creatorRequestsEnabled && u.service.projects != nil && shouldRequestCreatorNotification(u.ctx, updated) {
		if project, projectErr := u.service.projects.GetProject(u.ctx, updated.ProjectID); projectErr == nil {
			effectCtx = publishCreatorNotificationRequest(u.ctx, u.service.creatorRequests, project, updated, RefreshReasonTodoUpdated, true)
		}
	}
	if !updated.AssignmentChanged {
		u.service.refresh.PublishBoardRefresh(effectCtx, updated.ProjectID, RefreshReasonTodoUpdated, todoRefreshEntity(updated))
	}

	return LegacyUpdateResult{Todo: updated}, nil
}
