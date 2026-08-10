package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	sprintapp "scrumboy/internal/application/sprint"
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

func mapSprintDefinitionPrepareError(err error) *adapterError {
	switch {
	case errors.Is(err, sprintapp.ErrActorRequired):
		return newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	case errors.Is(err, sprintapp.ErrMaintainerRequired):
		return newAdapterError(http.StatusForbidden, CodeForbidden, "maintainer or higher required", nil)
	case errors.Is(err, sprintapp.ErrSprintNotInProject):
		return newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil)
	default:
		return mapStoreError(err)
	}
}

func mapSprintLifecyclePrepareError(err error) *adapterError {
	switch {
	case errors.Is(err, sprintapp.ErrSprintMustBePlanned):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint must be PLANNED to activate", map[string]any{"field": "sprintId"})
	case errors.Is(err, sprintapp.ErrSprintEndNotAfterNow):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint end date is on or before now; cannot activate", map[string]any{"field": "plannedEndAt"})
	case errors.Is(err, sprintapp.ErrSprintMustBeActive):
		return newAdapterError(http.StatusBadRequest, CodeValidationError, "sprint must be ACTIVE to close", map[string]any{"field": "sprintId"})
	default:
		return mapSprintDefinitionPrepareError(err)
	}
}

func (a *Adapter) handleSprintsList(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_list is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_list is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_get is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_getActive is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_getActive is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_create is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.sprintDefinitions.PrepareCreate(ctx, sprintapp.MCPProjectTarget{
		ProjectSlug: in.ProjectSlug,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapSprintDefinitionPrepareError(prepareErr)
	}

	sp, createErr := prepared.Create(sprintapp.CreateCommand{
		Name:           in.Name,
		PlannedStartAt: plannedStartAt,
		PlannedEndAt:   plannedEndAt,
	})
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_update is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.sprintDefinitions.PrepareUpdate(ctx, sprintapp.MCPSprintTarget{
		ProjectSlug: env.ProjectSlug,
		SprintID:    env.SprintID,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapSprintDefinitionPrepareError(prepareErr)
	}

	command, patchErr := buildSprintUpdateCommand(env.Patch)
	if patchErr != nil {
		return nil, nil, patchErr
	}

	updated, updateErr := prepared.Update(command)
	if updateErr != nil {
		return nil, nil, mapStoreError(updateErr)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "sprints_delete is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.sprintDeletions.PrepareDelete(ctx, sprintapp.MCPDeletionTarget{
		ProjectSlug: in.ProjectSlug,
		SprintID:    in.SprintID,
		Mode:        a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapSprintDefinitionPrepareError(prepareErr)
	}
	if deleteErr := prepared.Delete(); deleteErr != nil {
		return nil, nil, mapStoreError(deleteErr)
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

	toolName := "sprints_" + action
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

	var updated store.Sprint
	switch action {
	case "activate":
		prepared, prepareErr := a.sprintLifecycle.PrepareActivate(ctx, sprintapp.MCPLifecycleTarget{
			ProjectSlug: in.ProjectSlug,
			SprintID:    in.SprintID,
			Mode:        a.storeMode(),
		})
		if prepareErr != nil {
			return nil, nil, mapSprintLifecyclePrepareError(prepareErr)
		}
		var activateErr error
		updated, activateErr = prepared.Activate()
		if activateErr != nil {
			return nil, nil, mapStoreError(activateErr)
		}
	case "close":
		prepared, prepareErr := a.sprintLifecycle.PrepareClose(ctx, sprintapp.MCPLifecycleTarget{
			ProjectSlug: in.ProjectSlug,
			SprintID:    in.SprintID,
			Mode:        a.storeMode(),
		})
		if prepareErr != nil {
			return nil, nil, mapSprintLifecyclePrepareError(prepareErr)
		}
		var closeErr error
		updated, closeErr = prepared.Close()
		if closeErr != nil {
			return nil, nil, mapStoreError(closeErr)
		}
	default:
		return nil, nil, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": "unknown sprint action"})
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

func buildSprintUpdateCommand(patchRaw json.RawMessage) (sprintapp.UpdateCommand, *adapterError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(patchRaw, &raw); err != nil {
		return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"detail": err.Error()})
	}
	if raw == nil {
		return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"field": "patch"})
	}

	allowed := map[string]struct{}{
		"name":           {},
		"plannedStartAt": {},
		"plannedEndAt":   {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported patch field", map[string]any{"field": key})
		}
	}

	var command sprintapp.UpdateCommand

	if v, ok := raw["name"]; ok {
		if isNullJSON(v) {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "name cannot be null", map[string]any{"field": "name"})
		}
		var name string
		if err := json.Unmarshal(v, &name); err != nil {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid name", map[string]any{"field": "name"})
		}
		command.Name = &name
	}

	if v, ok := raw["plannedStartAt"]; ok {
		if isNullJSON(v) {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "plannedStartAt cannot be null", map[string]any{"field": "plannedStartAt"})
		}
		var ms int64
		if err := json.Unmarshal(v, &ms); err != nil {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedStartAt", map[string]any{"field": "plannedStartAt"})
		}
		t := time.UnixMilli(ms).UTC()
		command.PlannedStartAt = &t
	}

	if v, ok := raw["plannedEndAt"]; ok {
		if isNullJSON(v) {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "plannedEndAt cannot be null", map[string]any{"field": "plannedEndAt"})
		}
		var ms int64
		if err := json.Unmarshal(v, &ms); err != nil {
			return sprintapp.UpdateCommand{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid plannedEndAt", map[string]any{"field": "plannedEndAt"})
		}
		t := time.UnixMilli(ms).UTC()
		command.PlannedEndAt = &t
	}

	return command, nil
}
