package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	boardapp "scrumboy/internal/application/board"
	"scrumboy/internal/store"
)

type boardGetInput struct {
	ProjectSlug    string            `json:"projectSlug"`
	Tag            string            `json:"tag"`
	Search         string            `json:"search"`
	Assignee       string            `json:"assignee"`
	Sort           string            `json:"sort"`
	SprintId       *int64            `json:"sprintId"`
	Limit          int               `json:"limit"`
	CursorByColumn map[string]string `json:"cursorByColumn"`
}

func boardGetAssigneeHasInvalidType(input any) bool {
	b, err := json.Marshal(input)
	if err != nil {
		return false
	}
	var raw struct {
		Assignee json.RawMessage `json:"assignee"`
	}
	if err := json.Unmarshal(b, &raw); err != nil || len(raw.Assignee) == 0 {
		return false
	}
	var value any
	if err := json.Unmarshal(raw.Assignee, &value); err != nil {
		return false
	}
	_, ok := value.(string)
	return !ok
}

func (a *Adapter) handleBoardGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "board_get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "board_get is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if boardGetAssigneeHasInvalidType(input) {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid assignee", map[string]any{"field": "assignee"})
	}

	var in boardGetInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	// Keep target-independent input validation before project access. Sprint
	// membership and workflow/cursor validation remain in the prepared read
	// below because those checks depend on the authorized project.
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}

	// Pass the trimmed tag through unchanged. The store normalizes scope-aware:
	// durable projects group via TagGroupKey; temporary boards exact-match the raw
	// displayed name (so a "make space" chip is not rewritten to "make-space").
	tag := strings.TrimSpace(in.Tag)
	search := strings.TrimSpace(in.Search)
	actorUserID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	assigneeFilter, assigneeErr := store.ParseAssigneeFilter(in.Assignee, &actorUserID)
	if assigneeErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid assignee", map[string]any{"field": "assignee"})
	}
	sortOrder, sortErr := store.ParseSortOrder(in.Sort)
	if sortErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sort", map[string]any{"field": "sort"})
	}

	// This is the target-dependent validation boundary: denied, missing, and
	// expired projects mask later sprint and cursor errors as not found.
	prepared, prepareErr := a.boardReads.Prepare(ctx, boardapp.MCPBoardReadTarget{
		Slug: in.ProjectSlug,
		Mode: a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapStoreError(prepareErr)
	}

	result, readErr := prepared.Read(boardapp.MCPBoardReadQuery{
		TagFilter:      tag,
		SearchFilter:   search,
		AssigneeFilter: assigneeFilter,
		SprintID:       in.SprintId,
		Limit:          limit,
		CursorByColumn: in.CursorByColumn,
		SortOrder:      sortOrder,
	})
	if readErr != nil {
		return nil, nil, mapMCPBoardReadError(readErr)
	}

	// Lookup accepts normalization-equivalent input, but successful output uses
	// the persisted identity consistently for the project and every todo.
	projectSlug := result.Project.Slug
	columns := make([]boardColumnItem, 0, len(result.Columns))
	nextCursorByColumn := make(map[string]any, len(result.Columns))
	hasMoreByColumn := make(map[string]bool, len(result.Columns))
	totalCountByColumn := make(map[string]int, len(result.Columns))
	for _, lane := range result.Columns {
		items := make([]todoItem, 0, len(lane.Todos))
		for _, todo := range lane.Todos {
			items = append(items, todoToItem(projectSlug, todo))
		}
		columns = append(columns, boardColumnItem{
			Key:    lane.Workflow.Key,
			Name:   lane.Workflow.Name,
			IsDone: lane.Workflow.IsDone,
			Items:  items,
		})

		if lane.NextCursor != nil {
			nextCursorByColumn[lane.Workflow.Key] = *lane.NextCursor
		} else {
			nextCursorByColumn[lane.Workflow.Key] = nil
		}
		hasMoreByColumn[lane.Workflow.Key] = lane.HasMore
		totalCountByColumn[lane.Workflow.Key] = lane.TotalCount
	}

	return map[string]any{
			"project": boardProjectItem{
				ProjectSlug: projectSlug,
				Name:        result.Project.Name,
				Role:        result.Role.String(),
			},
			"columns": columns,
		}, map[string]any{
			"nextCursorByColumn": nextCursorByColumn,
			"hasMoreByColumn":    hasMoreByColumn,
			"totalCountByColumn": totalCountByColumn,
		}, nil
}

func mapMCPBoardReadError(err error) *adapterError {
	if errors.Is(err, boardapp.ErrInvalidMCPBoardSprintID) {
		return newAdapterError(
			http.StatusBadRequest,
			CodeValidationError,
			"invalid sprintId",
			map[string]any{"field": "sprintId"},
		)
	}

	var cursorErr *boardapp.MCPBoardCursorError
	if errors.As(err, &cursorErr) {
		message := "invalid board cursor"
		if cursorErr.Kind == boardapp.MCPBoardCursorUnknownColumn {
			message = "invalid column cursor"
		}
		return newAdapterError(
			http.StatusBadRequest,
			CodeValidationError,
			message,
			map[string]any{
				"field":     "cursorByColumn",
				"columnKey": cursorErr.ColumnKey,
			},
		)
	}

	return mapStoreError(err)
}
