package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type boardGetInput struct {
	ProjectSlug    string            `json:"projectSlug"`
	Tag            string            `json:"tag"`
	Search         string            `json:"search"`
	SprintId       *int64            `json:"sprintId"`
	Limit          int               `json:"limit"`
	CursorByColumn map[string]string `json:"cursorByColumn"`
}

func (a *Adapter) handleBoardGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "board.get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "board.get is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in boardGetInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}

	tag := strings.TrimSpace(in.Tag)
	if tag != "" {
		tag = store.CanonicalizeTag(tag)
		if tag == "" {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tag", map[string]any{"field": "tag"})
		}
	}
	search := strings.TrimSpace(in.Search)

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	sprintFilter, filterErr := a.resolveBoardSprintFilter(ctx, pc.Project.ID, in.SprintId)
	if filterErr != nil {
		return nil, nil, filterErr
	}

	workflow, workflowErr := a.store.GetProjectWorkflow(ctx, pc.Project.ID)
	if workflowErr != nil {
		return nil, nil, mapStoreError(workflowErr)
	}

	knownColumns := make(map[string]struct{}, len(workflow))
	for _, col := range workflow {
		knownColumns[col.Key] = struct{}{}
	}
	for key := range in.CursorByColumn {
		if _, ok := knownColumns[key]; !ok {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid column cursor", map[string]any{"field": "cursorByColumn", "columnKey": key})
		}
	}

	columns := make([]boardColumnItem, 0, len(workflow))
	nextCursorByColumn := make(map[string]any, len(workflow))
	hasMoreByColumn := make(map[string]bool, len(workflow))
	totalCountByColumn := make(map[string]int, len(workflow))

	for _, col := range workflow {
		afterRank := int64(math.MinInt64)
		afterID := int64(0)
		if token, ok := in.CursorByColumn[col.Key]; ok && strings.TrimSpace(token) != "" {
			rawCursor, decodeErr := decodeBoardCursor(token)
			if decodeErr != nil {
				return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid board cursor", map[string]any{"field": "cursorByColumn", "columnKey": col.Key})
			}
			rank, id := store.ParseLaneCursor(rawCursor)
			if rank == 0 && id == 0 {
				return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid board cursor", map[string]any{"field": "cursorByColumn", "columnKey": col.Key})
			}
			afterRank = rank
			afterID = id
		}

		todos, _, hasMore, listErr := a.store.ListTodosForBoardLane(ctx, pc.Project.ID, col.Key, limit, afterRank, afterID, tag, search, sprintFilter)
		if listErr != nil {
			return nil, nil, mapStoreError(listErr)
		}
		totalCount, countErr := a.store.CountTodosForBoardLane(ctx, pc.Project.ID, col.Key, tag, search, sprintFilter)
		if countErr != nil {
			return nil, nil, mapStoreError(countErr)
		}

		items := make([]todoItem, 0, len(todos))
		for _, todo := range todos {
			items = append(items, todoToItem(in.ProjectSlug, todo))
		}
		columns = append(columns, boardColumnItem{
			Key:    col.Key,
			Name:   col.Name,
			IsDone: col.IsDone,
			Items:  items,
		})

		if hasMore && len(todos) > 0 {
			nextCursorByColumn[col.Key] = encodeBoardCursor(fmt.Sprintf("%d:%d", todos[len(todos)-1].Rank, todos[len(todos)-1].ID))
		} else {
			nextCursorByColumn[col.Key] = nil
		}
		hasMoreByColumn[col.Key] = hasMore
		totalCountByColumn[col.Key] = totalCount
	}

	if pc.Project.ExpiresAt != nil {
		if err := a.store.UpdateBoardActivity(ctx, pc.Project.ID); err != nil {
			return nil, nil, mapStoreError(err)
		}
	}

	return map[string]any{
			"project": boardProjectItem{
				ProjectSlug: in.ProjectSlug,
				Name:        pc.Project.Name,
				Role:        pc.Role.String(),
			},
			"columns": columns,
		}, map[string]any{
			"nextCursorByColumn": nextCursorByColumn,
			"hasMoreByColumn":    hasMoreByColumn,
			"totalCountByColumn": totalCountByColumn,
		}, nil
}

func (a *Adapter) resolveBoardSprintFilter(ctx context.Context, projectID int64, sprintID *int64) (store.SprintFilter, *adapterError) {
	if sprintID == nil {
		return store.SprintFilter{Mode: "none"}, nil
	}
	if *sprintID <= 0 {
		return store.SprintFilter{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}

	sp, err := a.store.GetSprintByID(ctx, *sprintID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.SprintFilter{}, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
		}
		return store.SprintFilter{}, mapStoreError(err)
	}
	if sp.ProjectID != projectID {
		return store.SprintFilter{}, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}
	return store.SprintFilter{Mode: "sprint", SprintID: *sprintID}, nil
}

func encodeBoardCursor(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeBoardCursor(token string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
