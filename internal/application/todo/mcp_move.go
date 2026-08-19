package todo

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

// SlugMoveTarget identifies the slug whose access must be resolved for an MCP
// move before data-dependent command validation runs.
type SlugMoveTarget struct {
	Slug string
	Mode store.Mode
}

type MCPMoveAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

type MCPMoveLookupStore interface {
	GetTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) (store.Todo, error)
}

type MCPMoveLaneStore interface {
	ListTodosForBoardLane(
		ctx context.Context,
		projectID int64,
		columnKey string,
		limit int,
		afterA int64,
		afterB int64,
		tagFilter string,
		searchFilter string,
		assigneeFilter store.AssigneeFilter,
		priorityFilter store.PriorityFilter,
		sprintFilter store.SprintFilter,
		sortOrder store.SortOrder,
	) ([]store.Todo, string, bool, error)
}

type MCPMoveServiceDependencies struct {
	Access          MCPMoveAccessStore
	Lookup          MCPMoveLookupStore
	Lanes           MCPMoveLaneStore
	Move            MoveStore
	CreatorRequests CreatorNotificationRequestPublisher
}

// MCPMoveService owns the stricter one-sided anchor policy specific to MCP.
// It deliberately has no board-refresh dependency. Optional creator
// consideration is an explicit application-owned request.
type MCPMoveService struct {
	access          MCPMoveAccessStore
	lookup          MCPMoveLookupStore
	lanes           MCPMoveLaneStore
	move            MoveStore
	creatorRequests CreatorNotificationRequestPublisher
}

func NewMCPMoveService(deps MCPMoveServiceDependencies) *MCPMoveService {
	creatorRequests := deps.CreatorRequests
	if creatorRequests == nil {
		creatorRequests = nopCreatorNotificationRequestPublisher{}
	}
	return &MCPMoveService{
		access:          deps.Access,
		lookup:          deps.Lookup,
		lanes:           deps.Lanes,
		move:            deps.Move,
		creatorRequests: creatorRequests,
	}
}

// PreparedMCPMove binds the access context and a value copy of the authorized
// project context to lookup, anchor policy, and mutation operations.
type PreparedMCPMove struct {
	ctx            context.Context
	service        *MCPMoveService
	projectContext store.ProjectContext
	mode           store.Mode
}

func (s *MCPMoveService) Prepare(ctx context.Context, target SlugMoveTarget) (*PreparedMCPMove, error) {
	pc, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}
	return &PreparedMCPMove{
		ctx:            ctx,
		service:        s,
		projectContext: pc,
		mode:           target.Mode,
	}, nil
}

func (m *PreparedMCPMove) Move(command MoveCommand) (MoveResult, error) {
	project := m.projectContext.Project
	movingTodo, err := m.service.lookup.GetTodoByLocalID(m.ctx, project.ID, command.LocalID, m.mode)
	if err != nil {
		return MoveResult{}, err
	}

	// Preserve existing MCP precedence: the moving todo is resolved after
	// project access and before the target column is required.
	if command.ToColumnKey == "" {
		return MoveResult{}, &MCPMoveValidationError{Kind: MCPMoveMissingColumn, Field: "toColumnKey"}
	}

	afterTodo, err := m.resolveLocalTodoForColumn(command.AfterLocalID, "afterLocalId", command.ToColumnKey)
	if err != nil {
		return MoveResult{}, err
	}
	beforeTodo, err := m.resolveLocalTodoForColumn(command.BeforeLocalID, "beforeLocalId", command.ToColumnKey)
	if err != nil {
		return MoveResult{}, err
	}
	if err := m.validateAnchors(command.ToColumnKey, afterTodo, beforeTodo); err != nil {
		return MoveResult{}, err
	}

	todo, err := m.service.move.MoveTodoByLocalID(
		m.ctx,
		project.ID,
		movingTodo.LocalID,
		command.ToColumnKey,
		command.AfterLocalID,
		command.BeforeLocalID,
		m.mode,
	)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) && (command.AfterLocalID != nil || command.BeforeLocalID != nil) {
			return MoveResult{}, &MCPMoveValidationError{Kind: MCPMoveInvalidNeighbor}
		}
		return MoveResult{}, err
	}

	publishCreatorNotificationRequest(m.ctx, m.service.creatorRequests, project, todo, RefreshReasonTodoMoved, false)
	return MoveResult{Project: project, Todo: todo}, nil
}

func (m *PreparedMCPMove) resolveLocalTodoForColumn(localID *int64, field, targetColumnKey string) (*store.Todo, error) {
	if localID == nil {
		return nil, nil
	}
	if *localID <= 0 {
		return nil, &MCPMoveValidationError{
			Kind:    MCPMoveInvalidLocalReference,
			Field:   field,
			LocalID: *localID,
		}
	}

	projectID := m.projectContext.Project.ID
	todo, err := m.service.lookup.GetTodoByLocalID(m.ctx, projectID, *localID, m.mode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, &MCPMoveValidationError{
				Kind:       MCPMoveInvalidLocalReference,
				Field:      field,
				LocalID:    *localID,
				HasLocalID: true,
			}
		}
		return nil, err
	}
	if todo.ColumnKey != targetColumnKey {
		return nil, &MCPMoveValidationError{
			Kind:       MCPMoveReferenceInWrongColumn,
			Field:      field,
			LocalID:    *localID,
			HasLocalID: true,
		}
	}
	return &todo, nil
}

func (m *PreparedMCPMove) validateAnchors(columnKey string, afterTodo, beforeTodo *store.Todo) error {
	projectID := m.projectContext.Project.ID
	if afterTodo != nil {
		items, _, _, err := m.service.lanes.ListTodosForBoardLane(
			m.ctx,
			projectID,
			columnKey,
			1,
			afterTodo.Rank,
			afterTodo.ID,
			"",
			"",
			store.AssigneeFilter{},
			store.PriorityFilter{},
			store.SprintFilter{},
			store.SortOrderDefault,
		)
		if err != nil {
			return &MCPMoveAnchorReadError{Err: err}
		}
		if len(items) > 0 {
			return &MCPMoveValidationError{
				Kind:       MCPMoveAmbiguousAfterReference,
				Field:      "afterLocalId",
				LocalID:    afterTodo.LocalID,
				HasLocalID: true,
			}
		}
	}

	if beforeTodo != nil {
		const laneStartRank int64 = -1 << 63
		items, _, _, err := m.service.lanes.ListTodosForBoardLane(
			m.ctx,
			projectID,
			columnKey,
			1,
			laneStartRank,
			0,
			"",
			"",
			store.AssigneeFilter{},
			store.PriorityFilter{},
			store.SprintFilter{},
			store.SortOrderDefault,
		)
		if err != nil {
			return &MCPMoveAnchorReadError{Err: err}
		}
		if len(items) > 0 && items[0].LocalID != beforeTodo.LocalID {
			return &MCPMoveValidationError{
				Kind:       MCPMoveAmbiguousBeforeReference,
				Field:      "beforeLocalId",
				LocalID:    beforeTodo.LocalID,
				HasLocalID: true,
			}
		}
	}

	return nil
}
