package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	projectapp "scrumboy/internal/application/project"
	"scrumboy/internal/store"
)

type createProjectInput struct {
	Name string `json:"name"`
}

type projectsListInput struct {
	Limit  *int    `json:"limit"`
	Cursor *string `json:"cursor"`
}

type updateProjectEnvelope struct {
	ProjectSlug string          `json:"projectSlug"`
	Patch       json.RawMessage `json:"patch"`
}

type deleteProjectInput struct {
	ProjectSlug string `json:"projectSlug"`
}

// projectUpdatePatch is the decoded, validated shape of projects_update's
// patch object. Only fields present in the source patch are non-nil.
type projectUpdatePatch struct {
	Name               *string
	DefaultSprintWeeks *int
}

// allowedProjectUpdatePatchFields lists the only fields projects_update may
// change. Matches the todos_update pattern in todos_tools.go: unknown fields
// are rejected, and null is rejected for fields that have no "clear" meaning.
var allowedProjectUpdatePatchFields = map[string]struct{}{
	"name":               {},
	"defaultSprintWeeks": {},
}

// parseProjectUpdatePatch validates and decodes a projects_update patch
// object. It is a pure function (no store access) so it can be unit tested
// directly, matching the buildUpdatePatch pattern in todos_tools.go.
func parseProjectUpdatePatch(patchRaw json.RawMessage) (projectUpdatePatch, *adapterError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(patchRaw, &raw); err != nil {
		return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"detail": err.Error()})
	}
	if raw == nil {
		return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"field": "patch"})
	}
	for key := range raw {
		if _, ok := allowedProjectUpdatePatchFields[key]; !ok {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported patch field", map[string]any{"field": key})
		}
	}
	if len(raw) == 0 {
		return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "patch must include at least one field", map[string]any{"field": "patch"})
	}

	var out projectUpdatePatch

	if v, ok := raw["name"]; ok {
		if isNullJSON(v) {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "name cannot be null", map[string]any{"field": "name"})
		}
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid name", map[string]any{"field": "name"})
		}
		out.Name = &name
	}

	if v, ok := raw["defaultSprintWeeks"]; ok {
		if isNullJSON(v) {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "defaultSprintWeeks cannot be null", map[string]any{"field": "defaultSprintWeeks"})
		}
		var weeks int
		if err := json.Unmarshal(v, &weeks); err != nil {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid defaultSprintWeeks", map[string]any{"field": "defaultSprintWeeks"})
		}
		if weeks != 1 && weeks != 2 {
			return projectUpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "defaultSprintWeeks must be 1 or 2", map[string]any{"field": "defaultSprintWeeks"})
		}
		out.DefaultSprintWeeks = &weeks
	}

	return out, nil
}

func projectToItem(slug string, p store.Project, role store.ProjectRole) projectItem {
	return projectItem{
		ProjectSlug:        slug,
		ProjectID:          p.ID,
		Name:               p.Name,
		DominantColor:      p.DominantColor,
		DefaultSprintWeeks: p.DefaultSprintWeeks,
		ExpiresAt:          p.ExpiresAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
		Role:               role.String(),
	}
}

func normalizeProjectsListLimit(limit *int) (int, *adapterError) {
	if limit == nil {
		return 20, nil
	}
	if *limit <= 0 || *limit > 100 {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}
	return *limit, nil
}

// mapProjectApplicationError owns only the MCP projection of the neutral
// application actor sentinel. Store errors retain the established shared MCP
// mapping authority.
func mapProjectApplicationError(err error) *adapterError {
	if errors.Is(err, projectapp.ErrActorRequired) {
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	return mapStoreError(err)
}

func (a *Adapter) handleProjectsCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_create is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in createProjectInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing name", map[string]any{"field": "name"})
	}

	result, createErr := a.projectCreations.Create(ctx, projectapp.MCPDurableCreationCommand{
		Name: in.Name,
	})
	if createErr != nil {
		return nil, nil, mapStoreError(createErr)
	}

	return map[string]any{
		"project": projectToItem(result.Project.Slug, result.Project, result.Role),
	}, map[string]any{}, nil
}

func (a *Adapter) handleProjectsUpdate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_update is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var env updateProjectEnvelope
	if err := decodeInput(input, &env); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if env.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if len(env.Patch) == 0 || string(env.Patch) == "null" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing patch", map[string]any{"field": "patch"})
	}

	patch, patchErr := parseProjectUpdatePatch(env.Patch)
	if patchErr != nil {
		return nil, nil, patchErr
	}

	prepared, prepareErr := a.projectUpdates.Prepare(
		ctx,
		projectapp.ProjectSlugTarget{
			ProjectSlug: env.ProjectSlug,
			Mode:        a.storeMode(),
		},
		projectapp.MCPUpdateCommand{
			Name:               patch.Name,
			DefaultSprintWeeks: patch.DefaultSprintWeeks,
		},
	)
	if prepareErr != nil {
		return nil, nil, mapProjectApplicationError(prepareErr)
	}
	result, updateErr := prepared.Update()
	if updateErr != nil {
		return nil, nil, mapProjectApplicationError(updateErr)
	}

	return map[string]any{
		"project": projectToItem(result.Project.Slug, result.Project, result.Role),
	}, map[string]any{}, nil
}

func (a *Adapter) handleProjectsDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "projects_delete is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in deleteProjectInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	prepared, prepareErr := a.projectDeletions.Prepare(ctx, projectapp.MCPDeletionCommand{
		Project: projectapp.ProjectSlugTarget{
			ProjectSlug: in.ProjectSlug,
			Mode:        a.storeMode(),
		},
	})
	if prepareErr != nil {
		return nil, nil, mapProjectApplicationError(prepareErr)
	}
	result, deleteErr := prepared.Delete()
	if deleteErr != nil {
		return nil, nil, mapProjectApplicationError(deleteErr)
	}

	return map[string]any{
		"status":      "deleted",
		"projectSlug": result.ProjectSlug,
		"projectId":   result.ProjectID,
	}, map[string]any{}, nil
}

func (a *Adapter) handleProjectsList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(403, CodeCapabilityUnavailable, "projects_list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(403, CodeCapabilityUnavailable, "projects_list is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(401, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in projectsListInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	limit, limitErr := normalizeProjectsListLimit(in.Limit)
	if limitErr != nil {
		return nil, nil, limitErr
	}

	summaries, nextCursor, listErr := a.store.ListProjectSummaries(ctx, limit, in.Cursor)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	items := make([]projectItem, 0, len(summaries))
	for _, summary := range summaries {
		items = append(items, projectSummaryToItem(summary))
	}

	return map[string]any{"items": items}, map[string]any{
		"nextCursor": nextCursor,
		"hasMore":    nextCursor != nil,
	}, nil
}

func projectSummaryToItem(summary store.ProjectSummary) projectItem {
	return projectItem{
		ProjectSlug:        summary.Slug,
		ProjectID:          summary.ID,
		Name:               summary.Name,
		DominantColor:      summary.DominantColor,
		DefaultSprintWeeks: summary.DefaultSprintWeeks,
		ExpiresAt:          summary.ExpiresAt,
		CreatedAt:          summary.CreatedAt,
		UpdatedAt:          summary.UpdatedAt,
		Role:               summary.Role.String(),
	}
}
