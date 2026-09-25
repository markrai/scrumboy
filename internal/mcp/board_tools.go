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
	Tags           []string          `json:"tags"`
	Search         string            `json:"search"`
	Assignee       string            `json:"assignee"`
	Priority       string            `json:"priority"`
	Sort           string            `json:"sort"`
	SprintId       *int64            `json:"sprintId"`
	ColumnKey      string            `json:"columnKey"`
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

func boardGetRawTagFields(input any) (tag json.RawMessage, tags json.RawMessage, ok bool) {
	b, err := json.Marshal(input)
	if err != nil {
		return nil, nil, false
	}
	var raw struct {
		Tag  json.RawMessage `json:"tag"`
		Tags json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, nil, false
	}
	return raw.Tag, raw.Tags, true
}

func jsonRawIsStringArray(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func boardGetTagsHasInvalidType(input any) bool {
	_, tags, ok := boardGetRawTagFields(input)
	if !ok || len(tags) == 0 {
		return false
	}
	return !jsonRawIsStringArray(tags)
}

func resolveBoardGetTagFilters(input any, in boardGetInput) ([]string, *adapterError) {
	tagRaw, tagsRaw, ok := boardGetRawTagFields(input)
	if !ok {
		return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": "malformed board_get input"})
	}
	if len(tagRaw) > 0 && len(tagsRaw) > 0 {
		return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "tag and tags are mutually exclusive", map[string]any{})
	}
	if len(tagsRaw) > 0 {
		normalized, err := boardapp.NormalizeTagFilters(in.Tags)
		if err != nil {
			return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "too many tag filters", map[string]any{"field": "tags"})
		}
		if len(normalized) == 0 {
			return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tags", map[string]any{"field": "tags"})
		}
		return normalized, nil
	}
	tag := strings.TrimSpace(in.Tag)
	if tag == "" {
		return nil, nil
	}
	return []string{tag}, nil
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
	if boardGetTagsHasInvalidType(input) {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tags", map[string]any{"field": "tags"})
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

	// Tag filters stay transport-normalized only. The store applies scope-aware
	// matching: durable projects group via TagGroupKey; temporary boards exact-match
	// the raw displayed name (so a "make space" chip is not rewritten to "make-space").
	search := strings.TrimSpace(in.Search)
	actorUserID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	assigneeFilter, assigneeErr := store.ParseAssigneeFilter(in.Assignee, &actorUserID)
	if assigneeErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid assignee", map[string]any{"field": "assignee"})
	}
	priorityFilter, priorityErr := store.ParsePriorityFilter(in.Priority)
	if priorityErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid priority", map[string]any{"field": "priority"})
	}
	sortOrder, sortErr := store.ParseSortOrder(in.Sort)
	if sortErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sort", map[string]any{"field": "sort"})
	}

	tagFilters, tagErr := resolveBoardGetTagFilters(input, in)
	if tagErr != nil {
		return nil, nil, tagErr
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
		TagFilters:     tagFilters,
		SearchFilter:   search,
		AssigneeFilter: assigneeFilter,
		PriorityFilter: priorityFilter,
		SprintID:       in.SprintId,
		ColumnKey:      strings.TrimSpace(in.ColumnKey),
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

	if errors.Is(err, boardapp.ErrInvalidMCPBoardColumnKey) {
		return newAdapterError(
			http.StatusBadRequest,
			CodeValidationError,
			"invalid columnKey",
			map[string]any{"field": "columnKey"},
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
