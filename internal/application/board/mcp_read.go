package board

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strings"

	"scrumboy/internal/store"
)

// ErrInvalidMCPBoardSprintID identifies a non-positive internal sprint ID.
// Transport adapters remain responsible for mapping it to public error copy.
var ErrInvalidMCPBoardSprintID = errors.New("invalid MCP board sprint ID")

// MCPBoardCursorErrorKind identifies the application-level cursor condition
// without coupling the application service to a transport error envelope.
type MCPBoardCursorErrorKind uint8

const (
	MCPBoardCursorUnknownColumn MCPBoardCursorErrorKind = iota + 1
	MCPBoardCursorMalformed
)

// MCPBoardCursorError preserves the exact offending workflow column for the
// adapter's stable validation details.
type MCPBoardCursorError struct {
	Kind      MCPBoardCursorErrorKind
	ColumnKey string
}

func (e *MCPBoardCursorError) Error() string {
	switch e.Kind {
	case MCPBoardCursorUnknownColumn:
		return "unknown MCP board cursor column"
	default:
		return "malformed MCP board cursor"
	}
}

// MCPBoardReadTarget identifies the slug whose read access must be resolved.
type MCPBoardReadTarget struct {
	Slug string
	Mode store.Mode
}

// MCPBoardReadQuery contains input already normalized by the MCP adapter plus
// the later, access-dependent sprint and cursor inputs.
type MCPBoardReadQuery struct {
	TagFilter      string
	SearchFilter   string
	AssigneeFilter store.AssigneeFilter
	SprintID       *int64
	Limit          int
	CursorByColumn map[string]string
	SortOrder      store.SortOrder
}

// MCPBoardReadColumn is one workflow-ordered lane in the application result.
// Public MCP projection remains adapter-owned.
type MCPBoardReadColumn struct {
	Workflow   store.WorkflowColumn
	Todos      []store.Todo
	NextCursor *string
	HasMore    bool
	TotalCount int
}

// MCPBoardReadResult contains the store-backed values needed by the MCP
// adapter to preserve its existing public projection.
type MCPBoardReadResult struct {
	Project store.Project
	Role    store.ProjectRole
	Columns []MCPBoardReadColumn
}

// MCPBoardReadSprintStore is the persistence capability required for MCP's
// internal sprint-ID filtering semantics.
type MCPBoardReadSprintStore interface {
	GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error)
}

// MCPBoardReadWorkflowStore is the persistence capability required to project
// every board workflow column in order.
type MCPBoardReadWorkflowStore interface {
	GetProjectWorkflow(ctx context.Context, projectID int64) ([]store.WorkflowColumn, error)
}

// MCPBoardReadLaneStore is the persistence capability required for MCP's
// separate per-lane page and matching-count operations.
type MCPBoardReadLaneStore interface {
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
		sprintFilter store.SprintFilter,
		sortOrder store.SortOrder,
	) ([]store.Todo, string, bool, error)
	CountTodosForBoardLane(
		ctx context.Context,
		projectID int64,
		columnKey string,
		tagFilter string,
		searchFilter string,
		assigneeFilter store.AssigneeFilter,
		sprintFilter store.SprintFilter,
	) (int, error)
}

// MCPBoardReadActivityStore is the persistence capability required to refresh
// an expiring board after a successful MCP read.
type MCPBoardReadActivityStore interface {
	UpdateBoardActivity(ctx context.Context, projectID int64) error
}

// MCPBoardReadActivityFailureReporter observes ancillary refresh failures
// without coupling the application service to a logging package.
type MCPBoardReadActivityFailureReporter func(ctx context.Context, projectID int64, err error)

// MCPBoardReadServiceDependencies names the persistence role supplied to each
// part of the MCP board-read operation.
type MCPBoardReadServiceDependencies struct {
	Access                       SlugReadAccessStore
	Sprints                      MCPBoardReadSprintStore
	Workflow                     MCPBoardReadWorkflowStore
	Lanes                        MCPBoardReadLaneStore
	Activity                     MCPBoardReadActivityStore
	ReportActivityRefreshFailure MCPBoardReadActivityFailureReporter
}

// MCPBoardReadService owns the persistence orchestration specific to MCP
// board_get without changing the REST board-read services.
type MCPBoardReadService struct {
	access                       SlugReadAccessStore
	sprints                      MCPBoardReadSprintStore
	workflow                     MCPBoardReadWorkflowStore
	lanes                        MCPBoardReadLaneStore
	activity                     MCPBoardReadActivityStore
	reportActivityRefreshFailure MCPBoardReadActivityFailureReporter
}

func NewMCPBoardReadService(deps MCPBoardReadServiceDependencies) *MCPBoardReadService {
	reportActivityRefreshFailure := deps.ReportActivityRefreshFailure
	if reportActivityRefreshFailure == nil {
		reportActivityRefreshFailure = func(context.Context, int64, error) {}
	}
	return &MCPBoardReadService{
		access:                       deps.Access,
		sprints:                      deps.Sprints,
		workflow:                     deps.Workflow,
		lanes:                        deps.Lanes,
		activity:                     deps.Activity,
		reportActivityRefreshFailure: reportActivityRefreshFailure,
	}
}

// PreparedMCPBoardRead is a short-lived capability that binds every data
// operation to the context used to authorize the slug read.
type PreparedMCPBoardRead struct {
	ctx            context.Context
	service        *MCPBoardReadService
	projectContext store.ProjectContext
}

// Prepare resolves slug access exactly once before sprint and cursor
// validation that depends on the authorized project.
func (s *MCPBoardReadService) Prepare(
	ctx context.Context,
	target MCPBoardReadTarget,
) (*PreparedMCPBoardRead, error) {
	pc, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}

	return &PreparedMCPBoardRead{
		ctx:            ctx,
		service:        s,
		projectContext: pc,
	}, nil
}

// Read executes the MCP board projection using the context bound during
// preparation. It intentionally accepts no replacement context.
func (r *PreparedMCPBoardRead) Read(query MCPBoardReadQuery) (MCPBoardReadResult, error) {
	projectID := r.projectContext.Project.ID

	sprintFilter, err := r.resolveSprintFilter(query.SprintID)
	if err != nil {
		return MCPBoardReadResult{}, err
	}

	workflow, err := r.service.workflow.GetProjectWorkflow(r.ctx, projectID)
	if err != nil {
		return MCPBoardReadResult{}, err
	}

	knownColumns := make(map[string]struct{}, len(workflow))
	for _, column := range workflow {
		knownColumns[column.Key] = struct{}{}
	}
	for columnKey := range query.CursorByColumn {
		if _, ok := knownColumns[columnKey]; !ok {
			return MCPBoardReadResult{}, &MCPBoardCursorError{
				Kind:      MCPBoardCursorUnknownColumn,
				ColumnKey: columnKey,
			}
		}
	}

	columns := make([]MCPBoardReadColumn, 0, len(workflow))
	for _, column := range workflow {
		afterA, afterB := mcpBoardCursorSentinel(query.SortOrder)
		if token, ok := query.CursorByColumn[column.Key]; ok && strings.TrimSpace(token) != "" {
			rawCursor, decodeErr := decodeMCPBoardCursor(token)
			if decodeErr != nil {
				return MCPBoardReadResult{}, malformedMCPBoardCursor(column.Key)
			}
			afterA, afterB = store.ParseLaneCursor(rawCursor)
			if afterA == 0 && afterB == 0 {
				return MCPBoardReadResult{}, malformedMCPBoardCursor(column.Key)
			}
		}

		todos, _, hasMore, err := r.service.lanes.ListTodosForBoardLane(
			r.ctx,
			projectID,
			column.Key,
			query.Limit,
			afterA,
			afterB,
			query.TagFilter,
			query.SearchFilter,
			query.AssigneeFilter,
			sprintFilter,
			query.SortOrder,
		)
		if err != nil {
			return MCPBoardReadResult{}, err
		}

		totalCount, err := r.service.lanes.CountTodosForBoardLane(
			r.ctx,
			projectID,
			column.Key,
			query.TagFilter,
			query.SearchFilter,
			query.AssigneeFilter,
			sprintFilter,
		)
		if err != nil {
			return MCPBoardReadResult{}, err
		}

		var nextCursor *string
		if hasMore && len(todos) > 0 {
			token := encodeMCPBoardCursor(mcpBoardLaneCursor(todos[len(todos)-1], query.SortOrder))
			nextCursor = &token
		}

		columns = append(columns, MCPBoardReadColumn{
			Workflow:   column,
			Todos:      todos,
			NextCursor: nextCursor,
			HasMore:    hasMore,
			TotalCount: totalCount,
		})
	}

	if r.projectContext.Project.ExpiresAt != nil {
		if err := r.service.activity.UpdateBoardActivity(r.ctx, projectID); err != nil {
			r.service.reportActivityRefreshFailure(r.ctx, projectID, err)
		}
	}

	return MCPBoardReadResult{
		Project: r.projectContext.Project,
		Role:    r.projectContext.Role,
		Columns: columns,
	}, nil
}

func (r *PreparedMCPBoardRead) resolveSprintFilter(sprintID *int64) (store.SprintFilter, error) {
	if sprintID == nil {
		return store.SprintFilter{Mode: "none"}, nil
	}
	if *sprintID <= 0 {
		return store.SprintFilter{}, ErrInvalidMCPBoardSprintID
	}

	sprint, err := r.service.sprints.GetSprintByID(r.ctx, *sprintID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.SprintFilter{}, store.ErrNotFound
		}
		return store.SprintFilter{}, err
	}
	if sprint.ProjectID != r.projectContext.Project.ID {
		return store.SprintFilter{}, store.ErrNotFound
	}
	return store.SprintFilter{Mode: "sprint", SprintID: *sprintID}, nil
}

func malformedMCPBoardCursor(columnKey string) error {
	return &MCPBoardCursorError{
		Kind:      MCPBoardCursorMalformed,
		ColumnKey: columnKey,
	}
}

func mcpBoardCursorSentinel(sortOrder store.SortOrder) (a, b int64) {
	if sortOrder == store.SortOrderNewest {
		return math.MaxInt64, math.MaxInt64
	}
	return math.MinInt64, 0
}

func mcpBoardLaneCursor(todo store.Todo, sortOrder store.SortOrder) string {
	switch sortOrder {
	case store.SortOrderNewest, store.SortOrderOldest:
		return fmt.Sprintf("%d:%d", todo.CreatedAt.UnixMilli(), todo.ID)
	default:
		return fmt.Sprintf("%d:%d", todo.Rank, todo.ID)
	}
}

func encodeMCPBoardCursor(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeMCPBoardCursor(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
