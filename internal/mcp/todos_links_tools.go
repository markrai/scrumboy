package mcp

import (
	"context"
	"errors"
	"net/http"

	"scrumboy/internal/store"
)

type todosLinksListInput struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
}

type todosLinkAddInput struct {
	ProjectSlug   string `json:"projectSlug"`
	LocalID       int64  `json:"localId"`
	TargetLocalId int64  `json:"targetLocalId"`
	LinkType      string `json:"linkType"`
}

type todosLinkRemoveInput struct {
	ProjectSlug   string `json:"projectSlug"`
	LocalID       int64  `json:"localId"`
	TargetLocalId int64  `json:"targetLocalId"`
}

func todoLinkTargetsToItems(targets []store.TodoLinkTarget) []todoLinkItem {
	out := make([]todoLinkItem, 0, len(targets))
	for _, t := range targets {
		out = append(out, todoLinkItem{
			LocalID:  t.LocalID,
			Title:    t.Title,
			LinkType: t.LinkType,
		})
	}
	return out
}

func (a *Adapter) handleTodosLinksList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linksList is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linksList is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in todosLinksListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if _, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode()); getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	outbound, listErr := a.store.ListLinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	inbound, listErr := a.store.ListBacklinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	return map[string]any{
		"outbound": todoLinkTargetsToItems(outbound),
		"inbound":  todoLinkTargetsToItems(inbound),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosLinkAdd(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linkAdd is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linkAdd is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in todosLinkAddInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}
	if in.TargetLocalId <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid targetLocalId", map[string]any{"field": "targetLocalId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if _, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode()); getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	linkType := in.LinkType
	if linkType == "" {
		linkType = "relates_to"
	}

	if addErr := a.store.AddLink(ctx, pc.Project.ID, in.LocalID, in.TargetLocalId, linkType, a.storeMode()); addErr != nil {
		if errors.Is(addErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(addErr)
	}

	outbound, listErr := a.store.ListLinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	inbound, listErr := a.store.ListBacklinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	return map[string]any{
		"outbound": todoLinkTargetsToItems(outbound),
		"inbound":  todoLinkTargetsToItems(inbound),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosLinkRemove(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linkRemove is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_linkRemove is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in todosLinkRemoveInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}
	if in.TargetLocalId <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid targetLocalId", map[string]any{"field": "targetLocalId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if _, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode()); getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	if removeErr := a.store.RemoveLink(ctx, pc.Project.ID, in.LocalID, in.TargetLocalId, a.storeMode()); removeErr != nil {
		if errors.Is(removeErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(removeErr)
	}

	outbound, listErr := a.store.ListLinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	inbound, listErr := a.store.ListBacklinksForTodo(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	return map[string]any{
		"outbound": todoLinkTargetsToItems(outbound),
		"inbound":  todoLinkTargetsToItems(inbound),
	}, map[string]any{}, nil
}
