package mcp

import (
	"context"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type workflowListInput struct {
	ProjectSlug string `json:"projectSlug"`
}

type workflowCreateInput struct {
	ProjectSlug string `json:"projectSlug"`
	Name        string `json:"name"`
}

type workflowUpdateInput struct {
	ProjectSlug string `json:"projectSlug"`
	ColumnKey   string `json:"columnKey"`
	Name        string `json:"name"`
	Color       string `json:"color"`
}

type workflowDeleteInput struct {
	ProjectSlug string `json:"projectSlug"`
	ColumnKey   string `json:"columnKey"`
}

func workflowColumnToItem(col store.WorkflowColumn) workflowColumnItem {
	return workflowColumnItem{
		Key:      col.Key,
		Name:     col.Name,
		Color:    col.Color,
		Position: col.Position,
		IsDone:   col.IsDone,
		System:   col.System,
	}
}

func (a *Adapter) requireMaintainerProjectContext(ctx context.Context, projectSlug string) (store.ProjectContext, *adapterError) {
	pc, pcErr := a.store.GetProjectContextBySlug(ctx, projectSlug, a.storeMode())
	if pcErr != nil {
		return store.ProjectContext{}, mapStoreError(pcErr)
	}
	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return store.ProjectContext{}, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}
	return pc, nil
}

func (a *Adapter) handleWorkflowList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_list is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in workflowListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	workflow, workflowErr := a.store.GetProjectWorkflow(ctx, pc.Project.ID)
	if workflowErr != nil {
		return nil, nil, mapStoreError(workflowErr)
	}

	items := make([]workflowColumnItem, 0, len(workflow))
	for _, col := range workflow {
		items = append(items, workflowColumnToItem(col))
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleWorkflowCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_create is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in workflowCreateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "name required", map[string]any{"field": "name"})
	}
	if len(in.Name) > 200 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid workflow column name", map[string]any{"field": "name"})
	}

	pc, pcErr := a.requireMaintainerProjectContext(ctx, in.ProjectSlug)
	if pcErr != nil {
		return nil, nil, pcErr
	}

	col, colErr := a.store.AddWorkflowColumn(ctx, pc.Project.ID, in.Name)
	if colErr != nil {
		return nil, nil, mapStoreError(colErr)
	}

	return map[string]any{
		"column": workflowColumnToItem(col),
	}, map[string]any{}, nil
}

func (a *Adapter) handleWorkflowUpdate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_update is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in workflowUpdateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	in.ColumnKey = strings.TrimSpace(in.ColumnKey)
	if in.ColumnKey == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing columnKey", map[string]any{"field": "columnKey"})
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "name required", map[string]any{"field": "name"})
	}
	if len(in.Name) > 200 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid workflow column name", map[string]any{"field": "name"})
	}
	in.Color = strings.TrimSpace(in.Color)
	if in.Color == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "color required", map[string]any{"field": "color"})
	}
	if !store.ValidWorkflowColumnColor(in.Color) {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid workflow column color", map[string]any{"field": "color"})
	}

	pc, pcErr := a.requireMaintainerProjectContext(ctx, in.ProjectSlug)
	if pcErr != nil {
		return nil, nil, pcErr
	}

	if updateErr := a.store.UpdateWorkflowColumn(ctx, pc.Project.ID, in.ColumnKey, in.Name, in.Color); updateErr != nil {
		return nil, nil, mapStoreError(updateErr)
	}

	workflow, workflowErr := a.store.GetProjectWorkflow(ctx, pc.Project.ID)
	if workflowErr != nil {
		return nil, nil, mapStoreError(workflowErr)
	}
	for _, col := range workflow {
		if col.Key == in.ColumnKey {
			return map[string]any{
				"column": workflowColumnToItem(col),
			}, map[string]any{}, nil
		}
	}

	// Existence was already verified by the store update above succeeding; if the
	// column vanishes here, treat it as an internal inconsistency rather than
	// weakening the contract with a not-found response.
	return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": "updated workflow column not found in post-read"})
}

func (a *Adapter) handleWorkflowDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "workflow_delete is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in workflowDeleteInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	in.ColumnKey = strings.TrimSpace(in.ColumnKey)
	if in.ColumnKey == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing columnKey", map[string]any{"field": "columnKey"})
	}

	pc, pcErr := a.requireMaintainerProjectContext(ctx, in.ProjectSlug)
	if pcErr != nil {
		return nil, nil, pcErr
	}

	if deleteErr := a.store.DeleteWorkflowColumn(ctx, pc.Project.ID, in.ColumnKey); deleteErr != nil {
		return nil, nil, mapStoreError(deleteErr)
	}

	return map[string]any{
		"deleted": map[string]any{
			"projectSlug": in.ProjectSlug,
			"columnKey":   in.ColumnKey,
		},
	}, map[string]any{}, nil
}
