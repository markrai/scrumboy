package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	workflowapp "scrumboy/internal/application/workflow"
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

func mapWorkflowMutationPrepareError(err error) *adapterError {
	if errors.Is(err, workflowapp.ErrMaintainerRequired) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}
	return mapStoreError(err)
}

func mapWorkflowMutationUpdateError(err error) *adapterError {
	switch {
	case errors.Is(err, workflowapp.ErrWorkflowProjectionColumnMissing):
		projectionErr := newAdapterError(
			http.StatusInternalServerError,
			CodeInternal,
			"internal error",
			map[string]any{"detail": "updated workflow column not found in post-read"},
		)
		projectionErr.Cause = err
		return projectionErr
	case errors.Is(err, workflowapp.ErrWorkflowProjectionFailed):
		if cause := errors.Unwrap(err); cause != nil {
			mapped := mapStoreError(cause)
			mapped.Cause = err
			return mapped
		}

		projectionErr := newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", nil)
		projectionErr.Cause = err
		return projectionErr
	default:
		return mapStoreError(err)
	}
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

	prepared, prepareErr := a.workflowMutations.Prepare(ctx, workflowapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapWorkflowMutationPrepareError(prepareErr)
	}

	col, colErr := prepared.Create(workflowapp.CreateCommand{Name: in.Name})
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

	prepared, prepareErr := a.workflowMutations.Prepare(ctx, workflowapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapWorkflowMutationPrepareError(prepareErr)
	}

	col, updateErr := prepared.Update(workflowapp.UpdateCommand{
		Key:   in.ColumnKey,
		Name:  in.Name,
		Color: in.Color,
	})
	if updateErr != nil {
		return nil, nil, mapWorkflowMutationUpdateError(updateErr)
	}

	return map[string]any{
		"column": workflowColumnToItem(col),
	}, map[string]any{}, nil
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

	prepared, prepareErr := a.workflowMutations.Prepare(ctx, workflowapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapWorkflowMutationPrepareError(prepareErr)
	}

	if deleteErr := prepared.Delete(workflowapp.DeleteCommand{Key: in.ColumnKey}); deleteErr != nil {
		return nil, nil, mapStoreError(deleteErr)
	}

	return map[string]any{
		"deleted": map[string]any{
			"projectSlug": in.ProjectSlug,
			"columnKey":   in.ColumnKey,
		},
	}, map[string]any{}, nil
}
