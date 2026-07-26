package mcp

import (
	"context"
	"net/http"

	"scrumboy/internal/store"
)

type metricsGetBurndownInput struct {
	ProjectSlug string `json:"projectSlug"`
	SprintId    *int64 `json:"sprintId"`
}

type metricsGetBacklogSizeInput struct {
	ProjectSlug string `json:"projectSlug"`
}

func burndownPointsToItems(points []store.BurndownPoint) []burndownPointItem {
	out := make([]burndownPointItem, 0, len(points))
	for _, p := range points {
		out = append(out, burndownPointItem{
			Date:             p.Date,
			IncompleteCount:  p.IncompleteCount,
			TotalScope:       p.TotalScope,
			IncompletePoints: p.IncompletePoints,
			TotalScopePoints: p.TotalScopePoints,
			NewTodosCount:    p.NewTodosCount,
		})
	}
	return out
}

func realBurndownPointsToItems(points []store.RealBurndownPoint) []realBurndownPointItem {
	out := make([]realBurndownPointItem, 0, len(points))
	for _, p := range points {
		out = append(out, realBurndownPointItem{
			Date:               p.Date,
			RemainingWork:      p.RemainingWork,
			InitialScope:       p.InitialScope,
			RemainingPoints:    p.RemainingPoints,
			InitialScopePoints: p.InitialScopePoints,
		})
	}
	return out
}

func (a *Adapter) handleMetricsGetBurndown(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "metrics_getBurndown is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "metrics_getBurndown is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in metricsGetBurndownInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.SprintId != nil && *in.SprintId <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	if in.SprintId != nil {
		points, sprintErr := a.store.GetRealBurndownForSprint(ctx, pc.Project.ID, *in.SprintId, a.storeMode())
		if sprintErr != nil {
			return nil, nil, mapStoreError(sprintErr)
		}
		return map[string]any{
			"points": realBurndownPointsToItems(points),
		}, map[string]any{}, nil
	}

	points, burndownErr := a.store.GetRealBurndown(ctx, pc.Project.ID, a.storeMode())
	if burndownErr != nil {
		return nil, nil, mapStoreError(burndownErr)
	}

	return map[string]any{
		"points": realBurndownPointsToItems(points),
	}, map[string]any{}, nil
}

func (a *Adapter) handleMetricsGetBacklogSize(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "metrics_getBacklogSize is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "metrics_getBacklogSize is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in metricsGetBacklogSizeInput
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

	points, sizeErr := a.store.GetBacklogSize(ctx, pc.Project.ID, a.storeMode())
	if sizeErr != nil {
		return nil, nil, mapStoreError(sizeErr)
	}

	return map[string]any{
		"points": burndownPointsToItems(points),
	}, map[string]any{}, nil
}
