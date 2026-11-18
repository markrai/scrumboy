package mcp

import (
	"context"
	"net/http"

	"scrumboy/internal/store"
)

type externalResourceInput struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
	ResourceID  int64  `json:"resourceId"`
}

func externalResourcesPayload(resources []store.IntegrationResource) []map[string]any {
	items := make([]map[string]any, 0, len(resources))
	for _, r := range resources {
		items = append(items, map[string]any{
			"id": r.ID, "connectionId": r.ConnectionID,
			"kind": r.Kind, "externalId": r.ExternalID,
			"name": r.Name, "url": r.URL,
		})
	}
	return items
}

func (a *Adapter) externalResourceActor(ctx context.Context) (int64, *adapterError) {
	auth, bootstrap, err := a.authState(ctx)
	if err != nil {
		return 0, err
	}
	if a.mode != "full" || bootstrap {
		return 0, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "External references require an initialized full-mode instance", nil)
	}
	if !auth.Authenticated {
		return 0, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	id, ok := store.UserIDFromContext(ctx)
	if !ok || id <= 0 {
		return 0, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	return id, nil
}

func (a *Adapter) externalResourceProject(ctx context.Context, slug string) (int64, *adapterError) {
	if slug == "" {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	pc, err := a.store.GetProjectContextBySlug(ctx, slug, a.storeMode())
	if err != nil {
		return 0, mapStoreError(err)
	}
	return pc.Project.ID, nil
}

func (a *Adapter) externalResourceTarget(ctx context.Context, input any, requireTodo, requireResource bool) (int64, int64, externalResourceInput, *adapterError) {
	actor, authErr := a.externalResourceActor(ctx)
	if authErr != nil {
		return 0, 0, externalResourceInput{}, authErr
	}
	var in externalResourceInput
	if err := decodeInput(input, &in); err != nil {
		return 0, 0, in, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", nil)
	}
	if (requireTodo && in.LocalID <= 0) || (requireResource && in.ResourceID <= 0) {
		return 0, 0, in, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId or resourceId", nil)
	}
	projectID, resolveErr := a.externalResourceProject(ctx, in.ProjectSlug)
	if resolveErr != nil {
		return 0, 0, in, resolveErr
	}
	return actor, projectID, in, nil
}

func (a *Adapter) handleIntegrationResourcesList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	actor, projectID, _, err := a.externalResourceTarget(ctx, input, false, false)
	if err != nil { return nil, nil, err }
	items, storeErr := a.store.ListProjectIntegrationResources(ctx, actor, projectID)
	if storeErr != nil { return nil, nil, mapStoreError(storeErr) }
	return map[string]any{"items": externalResourcesPayload(items)}, map[string]any{}, nil
}

func (a *Adapter) handleTodosExternalLinksList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	actor, projectID, in, err := a.externalResourceTarget(ctx, input, true, false)
	if err != nil { return nil, nil, err }
	items, storeErr := a.store.ListTodoIntegrationResources(ctx, actor, projectID, in.LocalID)
	if storeErr != nil { return nil, nil, mapStoreError(storeErr) }
	return map[string]any{"items": externalResourcesPayload(items)}, map[string]any{}, nil
}

func (a *Adapter) handleTodosExternalLinkAdd(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	actor, projectID, in, err := a.externalResourceTarget(ctx, input, true, true)
	if err != nil { return nil, nil, err }
	if storeErr := a.store.LinkTodoIntegrationResource(ctx, actor, projectID, in.LocalID, in.ResourceID); storeErr != nil {
		return nil, nil, mapStoreError(storeErr)
	}
	items, storeErr := a.store.ListTodoIntegrationResources(ctx, actor, projectID, in.LocalID)
	if storeErr != nil { return nil, nil, mapStoreError(storeErr) }
	return map[string]any{"items": externalResourcesPayload(items)}, map[string]any{}, nil
}

func (a *Adapter) handleTodosExternalLinkRemove(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	actor, projectID, in, err := a.externalResourceTarget(ctx, input, true, true)
	if err != nil { return nil, nil, err }
	if storeErr := a.store.UnlinkTodoIntegrationResource(ctx, actor, projectID, in.LocalID, in.ResourceID); storeErr != nil {
		return nil, nil, mapStoreError(storeErr)
	}
	items, storeErr := a.store.ListTodoIntegrationResources(ctx, actor, projectID, in.LocalID)
	if storeErr != nil { return nil, nil, mapStoreError(storeErr) }
	return map[string]any{"items": externalResourcesPayload(items)}, map[string]any{}, nil
}
