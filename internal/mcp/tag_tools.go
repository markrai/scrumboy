package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	tagapp "scrumboy/internal/application/tag"
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

// updateProjectTagColorInput is the input for tags_updateProjectColor. TagID and
// TagName are pointers so a supplied-but-invalid value (0, negative, empty string) is
// distinguishable from an absent one and cannot be silently ignored when the other
// field is also present.
type updateProjectTagColorInput struct {
	ProjectSlug string  `json:"projectSlug"`
	TagID       *int64  `json:"tagId"`
	TagName     *string `json:"tagName"`
	Color       *string `json:"color"`
}

// deleteProjectTagInput is the input for tags_deleteProject (projectSlug + tagId; project-scoped rows only).
type deleteProjectTagInput struct {
	ProjectSlug string `json:"projectSlug"`
	TagID       int64  `json:"tagId"`
}

func mapTagColorPrepareError(err error) *adapterError {
	switch {
	case errors.Is(err, tagapp.ErrActorRequired):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	case errors.Is(err, tagapp.ErrMaintainerRequired):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	default:
		return mapStoreError(err)
	}
}

func mapTagColorUpdateError(err error) *adapterError {
	if errors.Is(err, tagapp.ErrColorProjectionMissing) {
		return newAdapterError(
			http.StatusInternalServerError,
			CodeInternal,
			"internal error",
			map[string]any{"detail": "updated project tag not found in post-read"},
		)
	}
	return mapStoreError(err)
}

func mapTagDeletionPrepareError(err error) *adapterError {
	switch {
	case errors.Is(err, tagapp.ErrActorRequired):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	case errors.Is(err, tagapp.ErrMaintainerRequired):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	default:
		return mapStoreError(err)
	}
}

func mapTagDeletionExecuteError(err error) *adapterError {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	case errors.Is(err, store.ErrUnauthorized):
		return newAdapterError(http.StatusForbidden, CodeForbidden, err.Error(), nil)
	case errors.Is(err, store.ErrConflict):
		return newAdapterError(http.StatusConflict, CodeConflict, err.Error(), nil)
	default:
		return mapStoreError(err)
	}
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
		items = append(items, toProjectTagItem(tag))
	}

	return map[string]any{
		"items": items,
	}, map[string]any{}, nil
}

// toProjectTagItem projects a grouped store.TagCount into the MCP wire item.
func toProjectTagItem(tc store.TagCount) projectTagItem {
	return projectTagItem{
		TagID:            tc.TagID,
		Name:             tc.Name,
		Count:            tc.Count,
		Color:            tc.Color,
		DeleteScope:      tc.DeleteScope(),
		CanDeleteMine:    tc.CanDeleteMine,
		CanDeleteProject: tc.CanDeleteProject,
		CanUpdateColor:   tc.CanUpdateColor,
	}
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

	prepared, prepareErr := a.tagColors.PrepareMineID(ctx, tagapp.MCPMineIDColorTarget{
		TagID: in.TagID,
		Color: tagapp.NewColorIntent(in.Color),
	})
	if prepareErr != nil {
		return nil, nil, mapTagColorPrepareError(prepareErr)
	}
	tag, updateErr := prepared.Update()
	if updateErr != nil {
		return nil, nil, mapTagColorUpdateError(updateErr)
	}

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

	prepared, prepareErr := a.tagDeletions.PrepareMineID(ctx, tagapp.MCPMineIDDeletionTarget{
		TagID: in.TagID,
	})
	if prepareErr != nil {
		return nil, nil, mapTagDeletionPrepareError(prepareErr)
	}
	if deleteErr := prepared.Delete(); deleteErr != nil {
		return nil, nil, mapTagDeletionExecuteError(deleteErr)
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
	// Exactly-one is decided on what the caller *supplied*, not on what happens to be
	// valid: sending both a malformed tagId and a tagName must fail loudly rather than
	// quietly falling through to the personal-color path. Presence is therefore taken
	// from the pointer, and the value is validated only afterwards.
	suppliedID := in.TagID != nil
	suppliedName := in.TagName != nil
	if suppliedID == suppliedName {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "provide exactly one of tagId or tagName", map[string]any{"field": "tagId"})
	}
	if suppliedID && *in.TagID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tagId", map[string]any{"field": "tagId"})
	}
	if suppliedName && strings.TrimSpace(*in.TagName) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tagName", map[string]any{"field": "tagName"})
	}
	if in.Color != nil && strings.TrimSpace(*in.Color) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "color cannot be empty; use null to clear", map[string]any{"field": "color"})
	}

	if suppliedName {
		prepared, prepareErr := a.tagColors.PrepareProjectName(ctx, tagapp.MCPProjectNameColorTarget{
			ProjectSlug: in.ProjectSlug,
			Mode:        a.storeMode(),
			Name:        *in.TagName,
			Color:       tagapp.NewColorIntent(in.Color),
		})
		if prepareErr != nil {
			return nil, nil, mapTagColorPrepareError(prepareErr)
		}
		tag, updateErr := prepared.Update()
		if updateErr != nil {
			return nil, nil, mapTagColorUpdateError(updateErr)
		}
		return map[string]any{"tag": toProjectTagItem(tag)}, map[string]any{}, nil
	}

	prepared, prepareErr := a.tagColors.PrepareProjectID(ctx, tagapp.MCPProjectIDColorTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
		TagID:       *in.TagID,
		Color:       tagapp.NewColorIntent(in.Color),
	})
	if prepareErr != nil {
		return nil, nil, mapTagColorPrepareError(prepareErr)
	}
	tag, updateErr := prepared.Update()
	if updateErr != nil {
		return nil, nil, mapTagColorUpdateError(updateErr)
	}
	return map[string]any{"tag": toProjectTagItem(tag)}, map[string]any{}, nil
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

	// MCP project deletion deliberately remains board-scoped-only. The prepared
	// service verifies the project-scoped row before performing the destructive
	// operation, so personal rows continue to return not found.
	prepared, prepareErr := a.tagDeletions.PrepareProjectID(ctx, tagapp.MCPProjectIDDeletionTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
		TagID:       in.TagID,
	})
	if prepareErr != nil {
		return nil, nil, mapTagDeletionPrepareError(prepareErr)
	}
	if deleteErr := prepared.Delete(); deleteErr != nil {
		return nil, nil, mapTagDeletionExecuteError(deleteErr)
	}

	return map[string]any{
		"deleted": map[string]any{
			"projectSlug": in.ProjectSlug,
			"tagId":       in.TagID,
		},
	}, map[string]any{}, nil
}
