package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type updateMineTagColorInput struct {
	TagID int64   `json:"tagId"`
	Color *string `json:"color"`
}

// deleteMineTagInput is the input for tags_deleteMine (tagId only; mine-scope / user library).
type deleteMineTagInput struct {
	TagID int64 `json:"tagId"`
}

type updateProjectTagColorInput struct {
	ProjectSlug string  `json:"projectSlug"`
	TagID       int64   `json:"tagId"`
	Color       *string `json:"color"`
}

// deleteProjectTagInput is the input for tags_deleteProject (projectSlug + tagId; project-scoped rows only).
type deleteProjectTagInput struct {
	ProjectSlug string `json:"projectSlug"`
	TagID       int64  `json:"tagId"`
}

func (a *Adapter) handleTagsListProject(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_listProject is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_listProject is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_listMine is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_listMine is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_updateMineColor is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_updateMineColor is unavailable before bootstrap", nil)
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
	if in.Color != nil && strings.TrimSpace(*in.Color) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "color cannot be empty; use null to clear", map[string]any{"field": "color"})
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

func (a *Adapter) handleTagsDeleteMine(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_deleteMine is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_deleteMine is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in deleteMineTagInput
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
	if _, found := findMineTag(tags, in.TagID); !found {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	if err := a.store.DeleteTag(ctx, userID, in.TagID, false); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
		case errors.Is(err, store.ErrUnauthorized):
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, err.Error(), nil)
		case errors.Is(err, store.ErrConflict):
			return nil, nil, newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
		default:
			return nil, nil, mapStoreError(err)
		}
	}

	return map[string]any{
		"deleted": map[string]any{
			"tagId": in.TagID,
		},
	}, map[string]any{}, nil
}

func (a *Adapter) handleTagsUpdateProjectColor(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_updateProjectColor is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_updateProjectColor is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in updateProjectTagColorInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.TagID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tagId", map[string]any{"field": "tagId"})
	}
	if in.Color != nil && strings.TrimSpace(*in.Color) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "color cannot be empty; use null to clear", map[string]any{"field": "color"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	// GetProjectScopedTagByID only matches board-scoped tags (project_id set, user_id NULL),
	// which per migration 019 are created exclusively for anonymous temporary boards. On a
	// durable/authenticated project every tag is user-owned and merely linked via project_tags,
	// so that lookup always 404s here even though tags.listProject reports the tag as part of
	// the project. Use the same project-tag set listProject exposes as the existence check
	// instead, covering both board-scoped and user-owned-but-project-linked tags.
	projectTags, listErr := a.store.ListTagCounts(ctx, &pc)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	if _, found := findProjectTagCount(projectTags, in.TagID); !found {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	// UpdateTagColor dispatches on the tag row's own scope: board-scoped tags get their shared
	// tags.color updated directly, while user-owned tags (the normal case on durable projects)
	// get this viewer's per-viewer color preference updated — the same store call the HTTP board
	// API uses for durable projects (see UpdateTagColorForProject in routing_board.go).
	updateErr := a.store.UpdateTagColor(ctx, &userID, in.TagID, in.Color)
	if updateErr != nil {
		// Clearing a color that was never set on a user-owned tag returns ErrNotFound from
		// UpdateTagColor; tags.updateMineColor already treats that as a harmless no-op, so
		// mirror that here rather than surfacing a confusing 404 on a successful clear.
		if !(isColorClear(in.Color) && errors.Is(updateErr, store.ErrNotFound)) {
			return nil, nil, mapStoreError(updateErr)
		}
	}

	projectTags, listErr = a.store.ListTagCounts(ctx, &pc)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	if tc, found := findProjectTagCount(projectTags, in.TagID); found {
		return map[string]any{
			"tag": projectTagItem{
				TagID:     tc.TagID,
				Name:      tc.Name,
				Count:     tc.Count,
				Color:     tc.Color,
				CanDelete: tc.CanDelete,
			},
		}, map[string]any{}, nil
	}

	// Tag existence in project scope was already verified above; if it disappears
	// here, treat it as an internal inconsistency rather than weakening the contract.
	return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": "updated project tag not found in post-read"})
}

func (a *Adapter) handleTagsDeleteProject(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_deleteProject is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "tags_deleteProject is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in deleteProjectTagInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.TagID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tagId", map[string]any{"field": "tagId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	// Deliberately still board-scoped-only (unlike handleTagsUpdateProjectColor): DeleteTag on a
	// user-owned tag deletes it across every project that user has used it in, not just this one,
	// so tags.deleteProject intentionally 404s for user-owned tags (see
	// TestMCPTagsDeleteProjectUserOwnedTagNotFound). tags.updateProjectColor is non-destructive
	// and safely dispatches to a per-viewer color update for user-owned tags, so only that path
	// was widened.
	if _, tagErr := a.store.GetProjectScopedTagByID(ctx, pc.Project.ID, in.TagID); tagErr != nil {
		return nil, nil, mapStoreError(tagErr)
	}

	p := pc.Project
	isAnonymousBoard := p.ExpiresAt != nil && p.CreatorUserID == nil

	if err := a.store.DeleteTag(ctx, userID, in.TagID, isAnonymousBoard); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
		case errors.Is(err, store.ErrUnauthorized):
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, err.Error(), nil)
		case errors.Is(err, store.ErrConflict):
			return nil, nil, newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
		default:
			return nil, nil, mapStoreError(err)
		}
	}

	return map[string]any{
		"deleted": map[string]any{
			"projectSlug": in.ProjectSlug,
			"tagId":       in.TagID,
		},
	}, map[string]any{}, nil
}

func findProjectTagCount(tags []store.TagCount, tagID int64) (store.TagCount, bool) {
	for _, tag := range tags {
		if tag.TagID == tagID {
			return tag, true
		}
	}
	return store.TagCount{}, false
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
	return color == nil
}

func normalizedMineColor(color *string) *string {
	if isColorClear(color) {
		return nil
	}
	return color
}
