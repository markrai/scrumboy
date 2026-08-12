package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	priorityapp "scrumboy/internal/application/priority"
	"scrumboy/internal/store"
)

type priorityListInput struct {
	ProjectSlug string `json:"projectSlug"`
}

type priorityCreateInput struct {
	ProjectSlug string `json:"projectSlug"`
	Name        string `json:"name"`
}

type priorityUpdateInput struct {
	ProjectSlug string `json:"projectSlug"`
	PriorityKey string `json:"priorityKey"`
	Name        string `json:"name"`
	Color       string `json:"color"`
}

type priorityDeleteInput struct {
	ProjectSlug string `json:"projectSlug"`
	PriorityKey string `json:"priorityKey"`
}

func priorityTierToItem(tier store.PriorityTier) priorityTierItem {
	return priorityTierItem{
		Key:      tier.Key,
		Name:     tier.Name,
		Color:    tier.Color,
		Position: tier.Position,
	}
}

func mapPriorityMutationPrepareError(err error) *adapterError {
	if errors.Is(err, priorityapp.ErrMaintainerRequired) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}
	return mapStoreError(err)
}

func mapPriorityMutationUpdateError(err error) *adapterError {
	switch {
	case errors.Is(err, priorityapp.ErrPriorityProjectionTierMissing):
		projectionErr := newAdapterError(
			http.StatusInternalServerError,
			CodeInternal,
			"internal error",
			map[string]any{"detail": "updated priority tier not found in post-read"},
		)
		projectionErr.Cause = err
		return projectionErr
	case errors.Is(err, priorityapp.ErrPriorityProjectionFailed):
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

func (a *Adapter) handlePriorityList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_list is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in priorityListInput
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

	tiers, tiersErr := a.store.GetProjectPriorities(ctx, pc.Project.ID)
	if tiersErr != nil {
		return nil, nil, mapStoreError(tiersErr)
	}

	items := make([]priorityTierItem, 0, len(tiers))
	for _, tier := range tiers {
		items = append(items, priorityTierToItem(tier))
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handlePriorityCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_create is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in priorityCreateInput
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
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid priority tier name", map[string]any{"field": "name"})
	}

	prepared, prepareErr := a.priorityMutations.Prepare(ctx, priorityapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapPriorityMutationPrepareError(prepareErr)
	}

	tier, tierErr := prepared.Create(priorityapp.CreateCommand{Name: in.Name})
	if tierErr != nil {
		return nil, nil, mapStoreError(tierErr)
	}

	return map[string]any{
		"priority": priorityTierToItem(tier),
	}, map[string]any{}, nil
}

func (a *Adapter) handlePriorityUpdate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_update is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in priorityUpdateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	in.PriorityKey = strings.TrimSpace(in.PriorityKey)
	if in.PriorityKey == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing priorityKey", map[string]any{"field": "priorityKey"})
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "name required", map[string]any{"field": "name"})
	}
	if len(in.Name) > 200 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid priority tier name", map[string]any{"field": "name"})
	}
	in.Color = strings.TrimSpace(in.Color)
	if in.Color == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "color required", map[string]any{"field": "color"})
	}
	if !store.ValidWorkflowColumnColor(in.Color) {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid priority tier color", map[string]any{"field": "color"})
	}

	prepared, prepareErr := a.priorityMutations.Prepare(ctx, priorityapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapPriorityMutationPrepareError(prepareErr)
	}

	tier, updateErr := prepared.Update(priorityapp.UpdateCommand{
		Key:   in.PriorityKey,
		Name:  in.Name,
		Color: in.Color,
	})
	if updateErr != nil {
		return nil, nil, mapPriorityMutationUpdateError(updateErr)
	}

	return map[string]any{
		"priority": priorityTierToItem(tier),
	}, map[string]any{}, nil
}

func (a *Adapter) handlePriorityDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "priorities_delete is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in priorityDeleteInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	in.PriorityKey = strings.TrimSpace(in.PriorityKey)
	if in.PriorityKey == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing priorityKey", map[string]any{"field": "priorityKey"})
	}

	prepared, prepareErr := a.priorityMutations.Prepare(ctx, priorityapp.MCPMutationTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapPriorityMutationPrepareError(prepareErr)
	}

	if deleteErr := prepared.Delete(priorityapp.DeleteCommand{Key: in.PriorityKey}); deleteErr != nil {
		return nil, nil, mapStoreError(deleteErr)
	}

	return map[string]any{
		"deleted": map[string]any{
			"projectSlug": in.ProjectSlug,
			"priorityKey": in.PriorityKey,
		},
	}, map[string]any{}, nil
}
