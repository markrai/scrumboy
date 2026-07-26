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

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	// tagName targets a grouped personal label and changes only the caller's own
	// display preference, so any authenticated project member may do it. tagId targets
	// a board-scoped tag whose color is shared with everyone, so it stays maintainer+.
	if suppliedName {
		if err := a.store.SetViewerTagColorByName(ctx, pc.Project.ID, userID, *in.TagName, in.Color); err != nil {
			return nil, nil, mapStoreError(err)
		}
		projectTags, listErr := a.store.ListTagCounts(ctx, &pc)
		if listErr != nil {
			return nil, nil, mapStoreError(listErr)
		}
		// The listing labels a group by its grouping key, which is the canonical name
		// when one exists and the raw stored name otherwise; TagGroupKey mirrors that.
		if tc, found := findProjectTagCountByName(projectTags, store.TagGroupKey(*in.TagName)); found {
			return map[string]any{"tag": toProjectTagItem(tc)}, map[string]any{}, nil
		}
		return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": "updated project tag not found in post-read"})
	}

	tagID := *in.TagID

	// On durable projects, grouped listProject exposes a real tagId only for
	// board-scoped tags (personal groups report tagId 0), so a tagId lookup here
	// naturally restricts this path to board-scoped tags; personal color updates must
	// use tagName. Temporary boards keep the row-level projection, so every listed row
	// remains addressable by tagId exactly as before.
	projectTags, listErr := a.store.ListTagCounts(ctx, &pc)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	if _, found := findProjectTagCount(projectTags, tagID); !found {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	// tagId addresses a shared board-scoped color on durable projects (personal groups
	// omit tagId from listProject) and remains maintainer+ on temporary boards too.
	if !pc.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	var updateErr error
	if pc.Project.ExpiresAt != nil {
		updateErr = a.store.UpdateTagColor(ctx, &userID, tagID, in.Color)
	} else {
		// Durable: also verify project membership and that the tag belongs here.
		updateErr = a.store.UpdateTagColorForDurableProjectByID(ctx, pc.Project.ID, userID, tagID, in.Color)
	}
	if updateErr != nil {
		if !(isColorClear(in.Color) && errors.Is(updateErr, store.ErrNotFound)) {
			return nil, nil, mapStoreError(updateErr)
		}
	}

	projectTags, listErr = a.store.ListTagCounts(ctx, &pc)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	if tc, found := findProjectTagCount(projectTags, tagID); found {
		return map[string]any{"tag": toProjectTagItem(tc)}, map[string]any{}, nil
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

func findProjectTagCountByName(tags []store.TagCount, canonicalName string) (store.TagCount, bool) {
	for _, tag := range tags {
		if tag.Name == canonicalName {
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
