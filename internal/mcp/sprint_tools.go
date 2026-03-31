package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"scrumboy/internal/store"
)

type sprintProjectInput struct {
	ProjectSlug string `json:"projectSlug"`
}

type sprintGetInput struct {
	ProjectSlug string `json:"projectSlug"`
	SprintID    int64  `json:"sprintId"`
}

type sprintCreateInput struct {
	ProjectSlug    string `json:"projectSlug"`
	Name           string `json:"name"`
	PlannedStartAt string `json:"plannedStartAt"`
	PlannedEndAt   string `json:"plannedEndAt"`
}

type sprintUpdateEnvelope struct {
	ProjectSlug string          `json:"projectSlug"`
	SprintID    int64           `json:"sprintId"`
	Patch       json.RawMessage `json:"patch"`
}

func (a *Adapter) handleSprintsList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.list is unavailable before bootstrap", nil)
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

	sprints, listErr := a.store.ListSprintsWithTodoCount(ctx, pc.Project.ID)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}
	unscheduledCount, countErr := a.store.CountUnscheduledTodos(ctx, pc.Project.ID)
	if countErr != nil {
		return nil, nil, mapStoreError(countErr)
	}

	items := make([]sprintItem, 0, len(sprints))
	for _, sp := range sprints {
		todoCount := sp.TodoCount
		items = append(items, sprintToItem(in.ProjectSlug, sp.Sprint, &todoCount))
	}

	return map[string]any{
			"items": items,
		}, map[string]any{
			"unscheduledCount": unscheduledCount,
		}, nil
}

func (a *Adapter) handleSprintsGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.get is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintGetInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.SprintID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	sp, getErr := a.store.GetSprintByID(ctx, in.SprintID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}
	if sp.ProjectID != pc.Project.ID {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	return map[string]any{
		"sprint": sprintToItem(in.ProjectSlug, sp, nil),
	}, map[string]any{}, nil
}

func (a *Adapter) handleSprintsGetActive(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.getActive is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.getActive is unavailable before bootstrap", nil)
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

	sp, activeErr := a.store.GetActiveSprintByProjectID(ctx, pc.Project.ID)
	if activeErr != nil {
		return nil, nil, mapStoreError(activeErr)
	}
	if sp == nil {
		return map[string]any{
			"sprint": nil,
		}, map[string]any{}, nil
	}
	if sp.ProjectID != pc.Project.ID {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	return map[string]any{
		"sprint": sprintToItem(in.ProjectSlug, *sp, nil),
	}, map[string]any{}, nil
}

func (a *Adapter) handleSprintsCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.create is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintCreateInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.Name == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing name", map[string]any{"field": "name"})
	}
	if in.PlannedStartAt == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing plannedStartAt", map[string]any{"field": "plannedStartAt"})
	}
	if in.PlannedEndAt == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing plannedEndAt", map[string]any{"field": "plannedEndAt"})
	}

	plannedStartAt, parseErr := time.Parse(time.RFC3339, in.PlannedStartAt)
	if parseErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedStartAt", map[string]any{"field": "plannedStartAt", "detail": parseErr.Error()})
	}
	plannedEndAt, parseErr := time.Parse(time.RFC3339, in.PlannedEndAt)
	if parseErr != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedEndAt", map[string]any{"field": "plannedEndAt", "detail": parseErr.Error()})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	role, roleErr := a.store.GetProjectRole(ctx, pc.Project.ID, userID)
	if roleErr != nil {
		return nil, nil, mapStoreError(roleErr)
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	sp, createErr := a.store.CreateSprint(ctx, pc.Project.ID, in.Name, plannedStartAt, plannedEndAt)
	if createErr != nil {
		return nil, nil, mapStoreError(createErr)
	}

	return map[string]any{
		"sprint": sprintToItem(in.ProjectSlug, sp, nil),
	}, map[string]any{}, nil
}

func (a *Adapter) handleSprintsUpdate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.update is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var env sprintUpdateEnvelope
	if err := decodeInput(input, &env); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if env.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if env.SprintID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}
	if len(env.Patch) == 0 || string(env.Patch) == "null" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing patch", map[string]any{"field": "patch"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, env.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	role, roleErr := a.store.GetProjectRole(ctx, pc.Project.ID, userID)
	if roleErr != nil {
		return nil, nil, mapStoreError(roleErr)
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	sp, getErr := a.store.GetSprintByID(ctx, env.SprintID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}
	if sp.ProjectID != pc.Project.ID {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	updateIn, changed, patchErr := buildSprintUpdateInput(env.Patch)
	if patchErr != nil {
		return nil, nil, patchErr
	}
	if !changed {
		return map[string]any{
			"sprint": sprintToItem(env.ProjectSlug, sp, nil),
		}, map[string]any{}, nil
	}

	if err := a.store.UpdateSprint(ctx, env.SprintID, updateIn); err != nil {
		return nil, nil, mapStoreError(err)
	}
	updated, updatedErr := a.store.GetSprintByID(ctx, env.SprintID)
	if updatedErr != nil {
		return nil, nil, mapStoreError(updatedErr)
	}

	return map[string]any{
		"sprint": sprintToItem(env.ProjectSlug, updated, nil),
	}, map[string]any{}, nil
}

func (a *Adapter) handleSprintsActivate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	return a.handleSprintAction(ctx, input, "activate")
}

func (a *Adapter) handleSprintsClose(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	return a.handleSprintAction(ctx, input, "close")
}

func (a *Adapter) handleSprintsDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints.delete is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintGetInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.SprintID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	role, roleErr := a.store.GetProjectRole(ctx, pc.Project.ID, userID)
	if roleErr != nil {
		return nil, nil, mapStoreError(roleErr)
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	sp, getErr := a.store.GetSprintByID(ctx, in.SprintID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}
	if sp.ProjectID != pc.Project.ID {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	if err := a.store.DeleteSprint(ctx, pc.Project.ID, in.SprintID); err != nil {
		return nil, nil, mapStoreError(err)
	}

	return map[string]any{
		"status":      "deleted",
		"projectSlug": in.ProjectSlug,
		"sprintId":    in.SprintID,
	}, map[string]any{}, nil
}

func (a *Adapter) handleSprintAction(ctx context.Context, input any, action string) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	toolName := "sprints." + action
	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, toolName+" is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, toolName+" is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in sprintGetInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.SprintID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}
	role, roleErr := a.store.GetProjectRole(ctx, pc.Project.ID, userID)
	if roleErr != nil {
		return nil, nil, mapStoreError(roleErr)
	}
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	}

	sp, getErr := a.store.GetSprintByID(ctx, in.SprintID)
	if getErr != nil {
		return nil, nil, mapStoreError(getErr)
	}
	if sp.ProjectID != pc.Project.ID {
		return nil, nil, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	}

	switch action {
	case "activate":
		if sp.State != store.SprintStatePlanned {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint must be PLANNED to activate", map[string]any{"field": "sprintId"})
		}
		if !sp.PlannedEndAt.After(time.Now().UTC()) {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint end date is on or before now; cannot activate", map[string]any{"field": "plannedEndAt"})
		}
		if err := a.store.ActivateSprint(ctx, pc.Project.ID, in.SprintID); err != nil {
			return nil, nil, mapStoreError(err)
		}
	case "close":
		if sp.State != store.SprintStateActive {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint must be ACTIVE to close", map[string]any{"field": "sprintId"})
		}
		if err := a.store.CloseSprint(ctx, in.SprintID); err != nil {
			return nil, nil, mapStoreError(err)
		}
	default:
		return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": "unknown sprint action"})
	}

	updated, updatedErr := a.store.GetSprintByID(ctx, in.SprintID)
	if updatedErr != nil {
		return nil, nil, mapStoreError(updatedErr)
	}

	return map[string]any{
		"sprint": sprintToItem(in.ProjectSlug, updated, nil),
	}, map[string]any{}, nil
}

func sprintToItem(projectSlug string, sp store.Sprint, todoCount *int64) sprintItem {
	var startedAt *int64
	if sp.StartedAt != nil {
		v := sp.StartedAt.UnixMilli()
		startedAt = &v
	}
	var closedAt *int64
	if sp.ClosedAt != nil {
		v := sp.ClosedAt.UnixMilli()
		closedAt = &v
	}
	return sprintItem{
		ProjectSlug:    projectSlug,
		SprintID:       sp.ID,
		Number:         sp.Number,
		Name:           sp.Name,
		PlannedStartAt: sp.PlannedStartAt.UnixMilli(),
		PlannedEndAt:   sp.PlannedEndAt.UnixMilli(),
		StartedAt:      startedAt,
		ClosedAt:       closedAt,
		State:          sp.State,
		TodoCount:      todoCount,
	}
}

func buildSprintUpdateInput(patchRaw json.RawMessage) (store.UpdateSprintInput, bool, *adapterError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(patchRaw, &raw); err != nil {
		return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"detail": err.Error()})
	}
	if raw == nil {
		return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"field": "patch"})
	}

	allowed := map[string]struct{}{
		"name":           {},
		"plannedStartAt": {},
		"plannedEndAt":   {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported patch field", map[string]any{"field": key})
		}
	}

	var in store.UpdateSprintInput
	changed := false

	if v, ok := raw["name"]; ok {
		if isNullJSON(v) {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "name cannot be null", map[string]any{"field": "name"})
		}
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid name", map[string]any{"field": "name"})
		}
		in.Name = &name
		changed = true
	}

	if v, ok := raw["plannedStartAt"]; ok {
		if isNullJSON(v) {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "plannedStartAt cannot be null", map[string]any{"field": "plannedStartAt"})
		}
		var ms int64
		if err := json.Unmarshal(v, &ms); err != nil {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedStartAt", map[string]any{"field": "plannedStartAt"})
		}
		t := time.UnixMilli(ms).UTC()
		in.PlannedStartAt = &t
		changed = true
	}

	if v, ok := raw["plannedEndAt"]; ok {
		if isNullJSON(v) {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "plannedEndAt cannot be null", map[string]any{"field": "plannedEndAt"})
		}
		var ms int64
		if err := json.Unmarshal(v, &ms); err != nil {
			return store.UpdateSprintInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedEndAt", map[string]any{"field": "plannedEndAt"})
		}
		t := time.UnixMilli(ms).UTC()
		in.PlannedEndAt = &t
		changed = true
	}

	return in, changed, nil
}
