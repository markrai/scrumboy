package mcp

import (
	"context"
	"errors"
	"net/http"
	"time"

	todoapp "scrumboy/internal/application/todo"
)

type countCompletedTodosInput struct {
	ProjectSlug string `json:"projectSlug"`
	Period      string `json:"period"`
	Timezone    string `json:"timezone"`
}

func (a *Adapter) handleTodosCountCompleted(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_countCompleted is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_countCompleted is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in countCompletedTodosInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	result, countErr := a.todoCompletionCounts.Count(ctx, todoapp.SlugCompletionCountTarget{
		Slug: in.ProjectSlug,
		Mode: a.storeMode(),
	}, todoapp.CompletionCountCommand{
		Period:   in.Period,
		Timezone: in.Timezone,
	})
	if countErr != nil {
		switch {
		case errors.Is(countErr, todoapp.ErrUnsupportedCompletionPeriod):
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported period", map[string]any{"field": "period"})
		case errors.Is(countErr, todoapp.ErrInvalidCompletionTimezone):
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid timezone", map[string]any{"field": "timezone"})
		default:
			return nil, nil, mapStoreError(countErr)
		}
	}

	return map[string]any{
		"count":    result.Count,
		"period":   result.Period,
		"timezone": result.Timezone,
		"startAt":  result.Start.Format(time.RFC3339),
		"endAt":    result.End.Format(time.RFC3339),
	}, map[string]any{}, nil
}
