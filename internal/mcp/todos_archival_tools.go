package mcp

import (
	"context"
	"errors"
	"net/http"

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
	if len(in.LocalIDs) == 0 || len(in.LocalIDs) > 500 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "localIds must contain between 1 and 500 items", map[string]any{"field": "localIds"})
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
		if errors.Is(storeErr, todoapp.ErrArchiveMaintainerRequired) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(storeErr)
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
