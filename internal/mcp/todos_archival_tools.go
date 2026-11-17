package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/store"
)

type archiveTodosInput struct {
	ProjectSlug string  `json:"projectSlug"`
	LocalIDs    []int64 `json:"localIds"`
}

func (a *Adapter) handleTodosArchive(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	return a.handleTodosArchiveState(ctx, input, true)
}

func (a *Adapter) handleTodosRestore(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	return a.handleTodosArchiveState(ctx, input, false)
}

func (a *Adapter) handleTodosArchiveState(ctx context.Context, input any, archive bool) (any, map[string]any, *adapterError) {
	auth, bootstrap, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if a.mode == "anonymous" || bootstrap {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todo archival is unavailable in this mode", nil)
	}
	if !auth.Authenticated {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	var in archiveTodosInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if len(in.LocalIDs) == 0 || len(in.LocalIDs) > store.MaxTodoArchiveBatch {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, fmt.Sprintf("localIds must contain between 1 and %d items", store.MaxTodoArchiveBatch), map[string]any{"field": "localIds"})
	}
	seen := make(map[int64]struct{}, len(in.LocalIDs))
	for _, id := range in.LocalIDs {
		if id <= 0 {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localIds", map[string]any{"field": "localIds"})
		}
		if _, ok := seen[id]; ok {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "localIds must be unique", map[string]any{"field": "localIds"})
		}
		seen[id] = struct{}{}
	}
	prepared, storeErr := a.todoArchival.Prepare(ctx, todoapp.MCPArchiveTarget{ProjectSlug: in.ProjectSlug, Mode: a.storeMode()})
	if storeErr != nil {
		return nil, nil, mapStoreError(storeErr)
	}
	if admitErr := a.admitExpiringBoard(ctx, in.ProjectSlug); admitErr != nil {
		return nil, nil, admitErr
	}
	var result store.TodoArchiveBatchResult
	if archive {
		result, storeErr = prepared.Archive(in.LocalIDs)
	} else {
		result, storeErr = prepared.Restore(in.LocalIDs)
	}
	if storeErr != nil {
		if errors.Is(storeErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(storeErr)
	}
	return result, map[string]any{}, nil
}

type listArchivedTodosInput struct {
	ProjectSlug string `json:"projectSlug"`
	Limit       *int   `json:"limit"`
	Cursor      string `json:"cursor"`
}

// handleTodosListArchived exposes the cursor-paginated archive page that
// GET /api/board/{slug}/archive serves, so MCP clients (and scrumboy-cli) can
// discover archived stories without already knowing their IDs.
func (a *Adapter) handleTodosListArchived(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrap, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_listArchived is unavailable in anonymous mode", nil)
	case bootstrap:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_listArchived is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	var in listArchivedTodosInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	limit := 50
	if in.Limit != nil {
		if *in.Limit <= 0 || *in.Limit > 100 {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
		}
		limit = *in.Limit
	}
	page, listErr := a.todoArchival.List(ctx, todoapp.MCPArchiveTarget{ProjectSlug: in.ProjectSlug, Mode: a.storeMode()}, limit, strings.TrimSpace(in.Cursor))
	if listErr != nil {
		if errors.Is(listErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(listErr)
	}
	items := make([]todoItem, 0, len(page.Todos))
	for _, t := range page.Todos {
		items = append(items, todoToItemForProject(in.ProjectSlug, page.Project, t))
	}
	return map[string]any{
		"items":      items,
		"nextCursor": page.NextCursor,
		"hasMore":    page.HasMore,
	}, map[string]any{}, nil
}
