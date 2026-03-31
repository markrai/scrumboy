package mcp

import (
	"context"
	"errors"
	"net/http"

	"scrumboy/internal/store"
)

type updateMineTagColorInput struct {
	TagID int64   `json:"tagId"`
	Color *string `json:"color"`
}

func (a *Adapter) handleTagsListProject(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.listProject is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.listProject is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintProjectInput
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

	tags, tagsErr := a.store.ListTagCounts(ctx, &pc)
	if tagsErr != nil {
		return nil, nil, mapStoreError(tagsErr)
	}

	items := make([]projectTagItem, 0, len(tags))
	for _, tag := range tags {
		items = append(items, projectTagItem{
			TagID:     tag.TagID,
			Name:      tag.Name,
			Count:     tag.Count,
			Color:     tag.Color,
			CanDelete: tag.CanDelete,
		})
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleTagsListMine(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.listMine is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.listMine is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	tags, tagsErr := a.store.ListUserTags(ctx, userID)
	if tagsErr != nil {
		return nil, nil, mapStoreError(tagsErr)
	}

	items := make([]mineTagItem, 0, len(tags))
	for _, tag := range tags {
		items = append(items, mineTagItem{
			TagID:     tag.TagID,
			Name:      tag.Name,
			Color:     tag.Color,
			CanDelete: tag.CanDelete,
		})
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

func (a *Adapter) handleTagsUpdateMineColor(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.updateMineColor is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags.updateMineColor is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in updateMineTagColorInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.TagID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tagId", map[string]any{"field": "tagId"})
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	tags, tagsErr := a.store.ListUserTags(ctx, userID)
	if tagsErr != nil {
		return nil, nil, mapStoreError(tagsErr)
	}
	tag, found := findMineTag(tags, in.TagID)
	if !found {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	updateErr := a.store.UpdateTagColor(ctx, &userID, in.TagID, in.Color)
	if updateErr != nil {
		// Clearing a color preference when none exists is a harmless no-op for this
		// mine-scope MCP tool; normalize the store quirk into a successful clear.
		if !(isColorClear(in.Color) && errors.Is(updateErr, store.ErrNotFound)) {
			return nil, nil, mapStoreError(updateErr)
		}
	}

	tag.Color = normalizedMineColor(in.Color)
	return map[string]any{
		"tag": mineTagItem{
			TagID:     tag.TagID,
			Name:      tag.Name,
			Color:     tag.Color,
			CanDelete: tag.CanDelete,
		},
	}, map[string]any{}, nil
}

func findMineTag(tags []store.TagWithColor, tagID int64) (store.TagWithColor, bool) {
	for _, tag := range tags {
		if tag.TagID == tagID {
			return tag, true
		}
	}
	return store.TagWithColor{}, false
}

func isColorClear(color *string) bool {
	return color == nil || *color == ""
}

func normalizedMineColor(color *string) *string {
	if isColorClear(color) {
		return nil
	}
	return color
}
