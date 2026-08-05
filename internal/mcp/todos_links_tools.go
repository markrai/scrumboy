package mcp

import (
	"context"
	"errors"
	"net/http"

	todolinkapp "scrumboy/internal/application/todolink"
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

func unwrapTodoLinkMutationStageError(err error) (error, *adapterError) {
	cause := errors.Unwrap(err)
	if cause != nil {
		return cause, nil
	}

	mapped := newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", nil)
	mapped.Cause = err
	return nil, mapped
}

func mapTodoLinkMutationStoreError(err error) *adapterError {
	if errors.Is(err, store.ErrUnauthorized) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return mapStoreError(err)
}

func mapTodoLinkMutationPrepareError(err error) *adapterError {
	if errors.Is(err, todolinkapp.ErrMCPSourceLookupFailed) {
		cause, invariantErr := unwrapTodoLinkMutationStageError(err)
		if invariantErr != nil {
			return invariantErr
		}
		return mapTodoLinkMutationStoreError(cause)
	}
	return mapStoreError(err)
}

func mapTodoLinkMutationOperationError(err error) *adapterError {
	if errors.Is(err, todolinkapp.ErrMCPProjectionFailed) {
		cause, invariantErr := unwrapTodoLinkMutationStageError(err)
		if invariantErr != nil {
			return invariantErr
		}
		return mapStoreError(cause)
	}
	return mapTodoLinkMutationStoreError(err)
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

	prepared, prepareErr := a.todoLinkMutations.Prepare(ctx, todolinkapp.MCPMutationTarget{
		ProjectSlug:   in.ProjectSlug,
		SourceLocalID: in.LocalID,
		Mode:          a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapTodoLinkMutationPrepareError(prepareErr)
	}

	linkType := in.LinkType
	if linkType == "" {
		linkType = "relates_to"
	}

	linkSet, addErr := prepared.Add(todolinkapp.AddCommand{
		TargetLocalID: in.TargetLocalId,
		LinkType:      linkType,
	})
	if addErr != nil {
		return nil, nil, mapTodoLinkMutationOperationError(addErr)
	}

	return map[string]any{
		"outbound": todoLinkTargetsToItems(linkSet.Outbound),
		"inbound":  todoLinkTargetsToItems(linkSet.Inbound),
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

	prepared, prepareErr := a.todoLinkMutations.Prepare(ctx, todolinkapp.MCPMutationTarget{
		ProjectSlug:   in.ProjectSlug,
		SourceLocalID: in.LocalID,
		Mode:          a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapTodoLinkMutationPrepareError(prepareErr)
	}

	linkSet, removeErr := prepared.Remove(todolinkapp.RemoveCommand{
		TargetLocalID: in.TargetLocalId,
	})
	if removeErr != nil {
		return nil, nil, mapTodoLinkMutationOperationError(removeErr)
	}

	return map[string]any{
		"outbound": todoLinkTargetsToItems(linkSet.Outbound),
		"inbound":  todoLinkTargetsToItems(linkSet.Inbound),
	}, map[string]any{}, nil
}
