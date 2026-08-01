package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type createProjectInput struct {
	Name string `json:"name"`
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
		Image:              p.Image,
		DominantColor:      p.DominantColor,
		DefaultSprintWeeks: p.DefaultSprintWeeks,
		ExpiresAt:          p.ExpiresAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
		Role:               role.String(),
	}
}

// requireProjectManageContext resolves a project slug and verifies the caller may
// update or delete it. Durable projects require Maintainer+; Temporary Boards require
// Temporary Board owner; Anonymous Boards return not found.
func (a *Adapter) requireProjectManageContext(ctx context.Context, projectSlug string) (store.ProjectContext, *adapterError) {
	pc, pcErr := a.store.GetProjectContextBySlug(ctx, projectSlug, a.storeMode())
	if pcErr != nil {
		return store.ProjectContext{}, mapStoreError(pcErr)
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return store.ProjectContext{}, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	if err := a.store.CheckCanManageProject(ctx, pc.Project.ID, requesterID); err != nil {
		return store.ProjectContext{}, mapStoreError(err)
	}

	if pc.Project.ExpiresAt != nil && pc.Project.CreatorUserID != nil && *pc.Project.CreatorUserID == requesterID {
		pc.Role = store.RoleMaintainer
	}

	return pc, nil
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

	project, createErr := a.store.CreateProject(ctx, in.Name)
	if createErr != nil {
		return nil, nil, mapStoreError(createErr)
	}

	return map[string]any{
		"project": projectToItem(project.Slug, project, store.RoleMaintainer),
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

	pc, pcErr := a.requireProjectManageContext(ctx, env.ProjectSlug)
	if pcErr != nil {
		return nil, nil, pcErr
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	storePatch := store.UpdateProjectPatch{
		Name:               patch.Name,
		DefaultSprintWeeks: patch.DefaultSprintWeeks,
	}
	if updErr := a.store.UpdateProjectPatch(ctx, pc.Project.ID, requesterID, storePatch); updErr != nil {
		return nil, nil, mapStoreError(updErr)
	}

	updated, getErr := a.store.GetProject(ctx, pc.Project.ID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}

	return map[string]any{
		"project": projectToItem(updated.Slug, updated, pc.Role),
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

	pc, pcErr := a.requireProjectManageContext(ctx, in.ProjectSlug)
	if pcErr != nil {
		return nil, nil, pcErr
	}

	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	deleted, deleteErr := a.store.DeleteProject(ctx, pc.Project.ID, requesterID)
	if deleteErr != nil {
		return nil, nil, mapStoreError(deleteErr)
	}

	return map[string]any{
		"status":      "deleted",
		"projectSlug": pc.Project.Slug,
		"projectId":   deleted.ProjectID,
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

	entries, listErr := a.store.ListProjects(ctx)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	items := make([]projectItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, projectListEntryToItem(entry))
	}

	return map[string]any{"items": items}, map[string]any{}, nil
}

func projectListEntryToItem(entry store.ProjectListEntry) projectItem {
	return projectItem{
		ProjectSlug:        entry.Project.Slug,
		ProjectID:          entry.Project.ID,
		Name:               entry.Project.Name,
		Image:              entry.Project.Image,
		DominantColor:      entry.Project.DominantColor,
		DefaultSprintWeeks: entry.Project.DefaultSprintWeeks,
		ExpiresAt:          entry.Project.ExpiresAt,
		CreatedAt:          entry.Project.CreatedAt,
		UpdatedAt:          entry.Project.UpdatedAt,
		Role:               entry.Role.String(),
	}
}
