package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type createTodoInput struct {
	ProjectSlug      string   `json:"projectSlug"`
	Title            string   `json:"title"`
	Body             string   `json:"body"`
	Tags             []string `json:"tags"`
	ColumnKey        string   `json:"columnKey"`
	EstimationPoints *int64   `json:"estimationPoints"`
	SprintId         *int64   `json:"sprintId"`
	AssigneeUserId   *int64   `json:"assigneeUserId"`
	Position         *struct {
		AfterLocalId  *int64 `json:"afterLocalId"`
		BeforeLocalId *int64 `json:"beforeLocalId"`
	} `json:"position"`
}

type getTodoInput struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
}

type searchTodosInput struct {
	ProjectSlug     string  `json:"projectSlug"`
	Query           string  `json:"query"`
	Limit           *int    `json:"limit"`
	ExcludeLocalIds []int64 `json:"excludeLocalIds"`
}

type updateTodoEnvelope struct {
	ProjectSlug string          `json:"projectSlug"`
	LocalID     int64           `json:"localId"`
	Patch       json.RawMessage `json:"patch"`
}

type deleteTodoInput struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
}

type moveTodoInput struct {
	ProjectSlug   string `json:"projectSlug"`
	LocalID       int64  `json:"localId"`
	ToColumnKey   string `json:"toColumnKey"`
	AfterLocalId  *int64 `json:"afterLocalId"`
	BeforeLocalId *int64 `json:"beforeLocalId"`
}

func (a *Adapter) handleTodosCreate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.create is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in createTodoInput
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

	columnKey := normalizeColumnKey(in.ColumnKey)
	if columnKey == "" {
		columnKey = store.DefaultColumnBacklog
	}

	afterID, beforeID, posErr := a.resolvePositionIDs(ctx, pc.Project.ID, in.Position, a.storeMode(), columnKey)
	if posErr != nil {
		return nil, nil, posErr
	}

	todo, createErr := a.store.CreateTodo(ctx, pc.Project.ID, store.CreateTodoInput{
		Title:            in.Title,
		Body:             in.Body,
		Tags:             in.Tags,
		ColumnKey:        columnKey,
		EstimationPoints: in.EstimationPoints,
		SprintID:         in.SprintId,
		AssigneeUserID:   in.AssigneeUserId,
		AfterID:          afterID,
		BeforeID:         beforeID,
	}, a.storeMode())
	if createErr != nil {
		if errors.Is(createErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(createErr)
	}

	return map[string]any{
		"todo": todoToItem(in.ProjectSlug, todo),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.get is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in getTodoInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	todo, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	return map[string]any{
		"todo": todoToItem(in.ProjectSlug, todo),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosSearch(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.search is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.search is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in searchTodosInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}

	limit := 20
	if in.Limit != nil {
		if *in.Limit <= 0 || *in.Limit > 50 {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
		}
		limit = *in.Limit
	}
	for _, id := range in.ExcludeLocalIds {
		if id <= 0 {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid excludeLocalIds", map[string]any{"field": "excludeLocalIds"})
		}
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	items, searchErr := a.store.SearchTodosForLinkPicker(ctx, pc.Project.ID, strings.TrimSpace(in.Query), limit, in.ExcludeLocalIds, a.storeMode())
	if searchErr != nil {
		if errors.Is(searchErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(searchErr)
	}

	out := make([]todoSearchItem, 0, len(items))
	for _, item := range items {
		out = append(out, todoSearchItem{
			ProjectSlug: in.ProjectSlug,
			LocalID:     item.LocalID,
			Title:       item.Title,
		})
	}

	return map[string]any{
		"items": out,
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosUpdate(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.update is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var env updateTodoEnvelope
	if err := decodeInput(input, &env); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if env.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if env.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}
	if len(env.Patch) == 0 || string(env.Patch) == "null" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing patch", map[string]any{"field": "patch"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, env.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	existing, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, env.LocalID, a.storeMode())
	if getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	updateIn, changed, patchErr := buildUpdateTodoInput(existing, env.Patch)
	if patchErr != nil {
		return nil, nil, patchErr
	}
	if !changed {
		return map[string]any{
			"todo": todoToItem(env.ProjectSlug, existing),
		}, map[string]any{}, nil
	}

	todo, updateErr := a.store.UpdateTodoByLocalID(ctx, pc.Project.ID, env.LocalID, updateIn, a.storeMode())
	if updateErr != nil {
		if errors.Is(updateErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(updateErr)
	}

	return map[string]any{
		"todo": todoToItem(env.ProjectSlug, todo),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.delete is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in deleteTodoInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	deleteErr := a.store.DeleteTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if deleteErr != nil {
		if errors.Is(deleteErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(deleteErr)
	}

	return map[string]any{
		"status":      "deleted",
		"projectSlug": in.ProjectSlug,
		"localId":     in.LocalID,
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosMove(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.move is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos.move is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in moveTodoInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}
	if in.ProjectSlug == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing projectSlug", map[string]any{"field": "projectSlug"})
	}
	if in.LocalID <= 0 {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid localId", map[string]any{"field": "localId"})
	}
	if in.AfterLocalId != nil && in.BeforeLocalId != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "at most one neighbor reference may be set", map[string]any{"fields": []string{"afterLocalId", "beforeLocalId"}})
	}
	if in.AfterLocalId != nil && *in.AfterLocalId == in.LocalID {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "afterLocalId cannot equal localId", map[string]any{"field": "afterLocalId"})
	}
	if in.BeforeLocalId != nil && *in.BeforeLocalId == in.LocalID {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "beforeLocalId cannot equal localId", map[string]any{"field": "beforeLocalId"})
	}

	pc, pcErr := a.store.GetProjectContextBySlug(ctx, in.ProjectSlug, a.storeMode())
	if pcErr != nil {
		return nil, nil, mapStoreError(pcErr)
	}

	movingTodo, getErr := a.store.GetTodoByLocalID(ctx, pc.Project.ID, in.LocalID, a.storeMode())
	if getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	toColumnKey := normalizeColumnKey(in.ToColumnKey)
	if toColumnKey == "" {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing toColumnKey", map[string]any{"field": "toColumnKey"})
	}

	afterTodo, err := a.resolveLocalTodoForColumn(ctx, pc.Project.ID, in.AfterLocalId, "afterLocalId", a.storeMode(), toColumnKey)
	if err != nil {
		return nil, nil, err
	}
	beforeTodo, err := a.resolveLocalTodoForColumn(ctx, pc.Project.ID, in.BeforeLocalId, "beforeLocalId", a.storeMode(), toColumnKey)
	if err != nil {
		return nil, nil, err
	}
	if err := a.validateMoveAnchors(ctx, pc.Project.ID, toColumnKey, afterTodo, beforeTodo, a.storeMode()); err != nil {
		return nil, nil, err
	}

	todo, moveErr := a.store.MoveTodoByLocalID(ctx, pc.Project.ID, movingTodo.LocalID, toColumnKey, in.AfterLocalId, in.BeforeLocalId, a.storeMode())
	if moveErr != nil {
		if errors.Is(moveErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		if errors.Is(moveErr, store.ErrNotFound) && (in.AfterLocalId != nil || in.BeforeLocalId != nil) {
			return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid neighbor reference", map[string]any{})
		}
		return nil, nil, mapStoreError(moveErr)
	}

	return map[string]any{
		"todo": todoToItem(in.ProjectSlug, todo),
	}, map[string]any{}, nil
}

func buildUpdateTodoInput(existing store.Todo, patchRaw json.RawMessage) (store.UpdateTodoInput, bool, *adapterError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(patchRaw, &raw); err != nil {
		return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"detail": err.Error()})
	}
	if raw == nil {
		return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"field": "patch"})
	}

	allowed := map[string]struct{}{
		"title":            {},
		"body":             {},
		"tags":             {},
		"estimationPoints": {},
		"assigneeUserId":   {},
		"sprintId":         {},
	}
	for key := range raw {
		if _, ok := allowed[key]; !ok {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported patch field", map[string]any{"field": key})
		}
	}

	in := store.UpdateTodoInput{
		Title:            existing.Title,
		Body:             existing.Body,
		Tags:             cloneStrings(existing.Tags),
		EstimationPoints: cloneInt64(existing.EstimationPoints),
		AssigneeUserID:   cloneInt64(existing.AssigneeUserID),
		SprintID:         cloneInt64(existing.SprintID),
	}
	changed := false

	if v, ok := raw["title"]; ok {
		if isNullJSON(v) {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "title cannot be null", map[string]any{"field": "title"})
		}
		var title string
		if err := json.Unmarshal(v, &title); err != nil {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid title", map[string]any{"field": "title"})
		}
		in.Title = title
		changed = true
	}

	if v, ok := raw["body"]; ok {
		if isNullJSON(v) {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "body cannot be null", map[string]any{"field": "body"})
		}
		var body string
		if err := json.Unmarshal(v, &body); err != nil {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid body", map[string]any{"field": "body"})
		}
		in.Body = body
		changed = true
	}

	if v, ok := raw["tags"]; ok {
		if isNullJSON(v) {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "tags cannot be null", map[string]any{"field": "tags"})
		}
		var tags []string
		if err := json.Unmarshal(v, &tags); err != nil {
			return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tags", map[string]any{"field": "tags"})
		}
		in.Tags = tags
		changed = true
	}

	if v, ok := raw["estimationPoints"]; ok {
		if isNullJSON(v) {
			in.EstimationPoints = nil
		} else {
			var points int64
			if err := json.Unmarshal(v, &points); err != nil {
				return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid estimationPoints", map[string]any{"field": "estimationPoints"})
			}
			in.EstimationPoints = &points
		}
		changed = true
	}

	if v, ok := raw["assigneeUserId"]; ok {
		if isNullJSON(v) {
			in.AssigneeUserID = nil
		} else {
			var assignee int64
			if err := json.Unmarshal(v, &assignee); err != nil {
				return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid assigneeUserId", map[string]any{"field": "assigneeUserId"})
			}
			in.AssigneeUserID = &assignee
		}
		changed = true
	}

	if v, ok := raw["sprintId"]; ok {
		if isNullJSON(v) {
			in.SprintID = nil
			in.ClearSprint = true
		} else {
			var sprintID int64
			if err := json.Unmarshal(v, &sprintID); err != nil {
				return store.UpdateTodoInput{}, false, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
			}
			in.SprintID = &sprintID
			in.ClearSprint = false
		}
		changed = true
	}

	return in, changed, nil
}

func isNullJSON(v json.RawMessage) bool {
	return strings.TrimSpace(string(v)) == "null"
}

func cloneStrings(v []string) []string {
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	copy(out, v)
	return out
}

func cloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func (a *Adapter) resolvePositionIDs(ctx context.Context, projectID int64, position *struct {
	AfterLocalId  *int64 `json:"afterLocalId"`
	BeforeLocalId *int64 `json:"beforeLocalId"`
}, mode store.Mode, columnKey string) (*int64, *int64, *adapterError) {
	if position == nil {
		return nil, nil, nil
	}

	afterTodo, err := a.resolveLocalTodoForColumn(ctx, projectID, position.AfterLocalId, "afterLocalId", mode, columnKey)
	if err != nil {
		return nil, nil, err
	}
	beforeTodo, err := a.resolveLocalTodoForColumn(ctx, projectID, position.BeforeLocalId, "beforeLocalId", mode, columnKey)
	if err != nil {
		return nil, nil, err
	}
	var afterID, beforeID *int64
	if afterTodo != nil {
		id := afterTodo.ID
		afterID = &id
	}
	if beforeTodo != nil {
		id := beforeTodo.ID
		beforeID = &id
	}
	return afterID, beforeID, nil
}

func (a *Adapter) resolveLocalTodoForColumn(ctx context.Context, projectID int64, localID *int64, field string, mode store.Mode, targetColumnKey string) (*store.Todo, *adapterError) {
	if localID == nil {
		return nil, nil
	}
	if *localID <= 0 {
		return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid local todo reference", map[string]any{"field": field})
	}

	todo, err := a.store.GetTodoByLocalID(ctx, projectID, *localID, mode)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid local todo reference", map[string]any{"field": field, "localId": *localID})
		}
		if errors.Is(err, store.ErrUnauthorized) {
			return nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, mapStoreError(err)
	}
	if todo.ColumnKey != targetColumnKey {
		return nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "position reference must be in target column", map[string]any{"field": field, "localId": *localID})
	}
	return &todo, nil
}

func (a *Adapter) validateMoveAnchors(ctx context.Context, projectID int64, columnKey string, afterTodo, beforeTodo *store.Todo, mode store.Mode) *adapterError {
	// One-sided anchors are intentionally stricter than the raw store API here.
	// The backend rank logic can place "after X" or "before Y" non-adjacently when
	// there are additional todos on the far side, so MCP only accepts anchors that
	// are already at the boundary of the target column.
	if afterTodo != nil {
		items, _, _, err := a.store.ListTodosForBoardLane(ctx, projectID, columnKey, 1, afterTodo.Rank, afterTodo.ID, "", "", store.SprintFilter{})
		if err != nil {
			return mapStoreError(err)
		}
		if len(items) > 0 {
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "afterLocalId is ambiguous unless it is already the last item in the target column", map[string]any{"field": "afterLocalId", "localId": afterTodo.LocalID})
		}
	}

	if beforeTodo != nil {
		const laneStartRank int64 = -1 << 63
		items, _, _, err := a.store.ListTodosForBoardLane(ctx, projectID, columnKey, 1, laneStartRank, 0, "", "", store.SprintFilter{})
		if err != nil {
			return mapStoreError(err)
		}
		if len(items) > 0 && items[0].LocalID != beforeTodo.LocalID {
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "beforeLocalId is ambiguous unless it is already the first item in the target column", map[string]any{"field": "beforeLocalId", "localId": beforeTodo.LocalID})
		}
	}

	return nil
}

func todoToItem(projectSlug string, todo store.Todo) todoItem {
	return todoItem{
		ProjectSlug:      projectSlug,
		LocalID:          todo.LocalID,
		Title:            todo.Title,
		Body:             todo.Body,
		ColumnKey:        todo.ColumnKey,
		Tags:             todo.Tags,
		EstimationPoints: todo.EstimationPoints,
		AssigneeUserId:   todo.AssigneeUserID,
		SprintId:         todo.SprintID,
		CreatedAt:        todo.CreatedAt,
		UpdatedAt:        todo.UpdatedAt,
		DoneAt:           todo.DoneAt,
	}
}
