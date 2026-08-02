package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	todoapp "scrumboy/internal/application/todo"
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_create is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_create is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.todoCreates.Prepare(ctx, todoapp.SlugCreateTarget{
		Slug: in.ProjectSlug,
		Mode: a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapStoreError(prepareErr)
	}

	columnKey := normalizeColumnKey(in.ColumnKey)
	if columnKey == "" {
		columnKey = store.DefaultColumnBacklog
	}

	command := todoapp.MCPCreateCommand{
		Values: todoapp.CreateValues{
			Title:            in.Title,
			Body:             in.Body,
			Tags:             in.Tags,
			ColumnKey:        columnKey,
			EstimationPoints: in.EstimationPoints,
			AssigneeUserID:   in.AssigneeUserId,
			SprintID:         in.SprintId,
		},
	}
	if in.Position != nil {
		command.AfterLocalID = in.Position.AfterLocalId
		command.BeforeLocalID = in.Position.BeforeLocalId
	}

	result, createErr := prepared.Create(command)
	if createErr != nil {
		return nil, nil, mapMCPCreateError(createErr)
	}

	return map[string]any{
		"todo": todoToItem(in.ProjectSlug, result.Todo),
	}, map[string]any{}, nil
}

func mapMCPCreateError(err error) *adapterError {
	var validationErr *todoapp.MCPCreateValidationError
	if errors.As(err, &validationErr) {
		details := map[string]any{}
		if validationErr.Field != "" {
			details["field"] = validationErr.Field
		}
		if validationErr.HasLocalID {
			details["localId"] = validationErr.LocalID
		}

		switch validationErr.Kind {
		case todoapp.MCPCreateInvalidLocalReference:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid local todo reference", details)
		case todoapp.MCPCreateReferenceInWrongColumn:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "position reference must be in target column", details)
		}
	}
	if errors.Is(err, store.ErrUnauthorized) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return mapStoreError(err)
}

func (a *Adapter) handleTodosGet(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_get is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_get is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_search is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_search is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_update is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_update is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.todoUpdates.Prepare(ctx, todoapp.SlugUpdateTarget{
		Slug: env.ProjectSlug,
		Mode: a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapStoreError(prepareErr)
	}

	preparedTodo, getErr := prepared.PrepareTodo(env.LocalID)
	if getErr != nil {
		if errors.Is(getErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(getErr)
	}

	patch, patchErr := buildUpdatePatch(env.Patch)
	if patchErr != nil {
		return nil, nil, patchErr
	}

	result, updateErr := preparedTodo.Update(patch)
	if updateErr != nil {
		if errors.Is(updateErr, store.ErrUnauthorized) {
			return nil, nil, newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
		}
		return nil, nil, mapStoreError(updateErr)
	}

	return map[string]any{
		"todo": todoToItem(env.ProjectSlug, result.Todo),
	}, map[string]any{}, nil
}

func (a *Adapter) handleTodosDelete(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_delete is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_delete is unavailable before bootstrap", nil)
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
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_move is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "todos_move is unavailable before bootstrap", nil)
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

	prepared, prepareErr := a.todoMoves.Prepare(ctx, todoapp.SlugMoveTarget{
		Slug: in.ProjectSlug,
		Mode: a.storeMode(),
	})
	if prepareErr != nil {
		return nil, nil, mapStoreError(prepareErr)
	}
	toColumnKey := normalizeColumnKey(in.ToColumnKey)
	result, moveErr := prepared.Move(todoapp.MoveCommand{
		LocalID:       in.LocalID,
		ToColumnKey:   toColumnKey,
		AfterLocalID:  in.AfterLocalId,
		BeforeLocalID: in.BeforeLocalId,
	})
	if moveErr != nil {
		return nil, nil, mapMCPMoveError(moveErr)
	}

	return map[string]any{
		"todo": todoToItem(in.ProjectSlug, result.Todo),
	}, map[string]any{}, nil
}

func mapMCPMoveError(err error) *adapterError {
	var anchorReadErr *todoapp.MCPMoveAnchorReadError
	if errors.As(err, &anchorReadErr) {
		return mapStoreError(anchorReadErr.Err)
	}

	var validationErr *todoapp.MCPMoveValidationError
	if errors.As(err, &validationErr) {
		details := map[string]any{}
		if validationErr.Field != "" {
			details["field"] = validationErr.Field
		}
		if validationErr.HasLocalID {
			details["localId"] = validationErr.LocalID
		}

		switch validationErr.Kind {
		case todoapp.MCPMoveMissingColumn:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "missing toColumnKey", details)
		case todoapp.MCPMoveInvalidLocalReference:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid local todo reference", details)
		case todoapp.MCPMoveReferenceInWrongColumn:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "position reference must be in target column", details)
		case todoapp.MCPMoveAmbiguousAfterReference:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "afterLocalId is ambiguous unless it is already the last item in the target column", details)
		case todoapp.MCPMoveAmbiguousBeforeReference:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "beforeLocalId is ambiguous unless it is already the first item in the target column", details)
		case todoapp.MCPMoveInvalidNeighbor:
			return newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid neighbor reference", details)
		}
	}
	if errors.Is(err, store.ErrUnauthorized) {
		return newAdapterError(http.StatusForbidden, CodeForbidden, "forbidden", nil)
	}
	return mapStoreError(err)
}

func buildUpdatePatch(patchRaw json.RawMessage) (todoapp.UpdatePatch, *adapterError) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(patchRaw, &raw); err != nil {
		return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"detail": err.Error()})
	}
	if raw == nil {
		return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid patch", map[string]any{"field": "patch"})
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
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "unsupported patch field", map[string]any{"field": key})
		}
	}

	var patch todoapp.UpdatePatch

	if v, ok := raw["title"]; ok {
		if isNullJSON(v) {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "title cannot be null", map[string]any{"field": "title"})
		}
		var title string
		if err := json.Unmarshal(v, &title); err != nil {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid title", map[string]any{"field": "title"})
		}
		patch.Title = todoapp.Field[string]{Present: true, Value: title}
	}

	if v, ok := raw["body"]; ok {
		if isNullJSON(v) {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "body cannot be null", map[string]any{"field": "body"})
		}
		var body string
		if err := json.Unmarshal(v, &body); err != nil {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid body", map[string]any{"field": "body"})
		}
		patch.Body = todoapp.Field[string]{Present: true, Value: body}
	}

	if v, ok := raw["tags"]; ok {
		if isNullJSON(v) {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "tags cannot be null", map[string]any{"field": "tags"})
		}
		var tags []string
		if err := json.Unmarshal(v, &tags); err != nil {
			return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid tags", map[string]any{"field": "tags"})
		}
		patch.Tags = todoapp.Field[[]string]{Present: true, Value: tags}
	}

	if v, ok := raw["estimationPoints"]; ok {
		if isNullJSON(v) {
			patch.EstimationPoints = todoapp.Field[*int64]{Present: true}
		} else {
			var points int64
			if err := json.Unmarshal(v, &points); err != nil {
				return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid estimationPoints", map[string]any{"field": "estimationPoints"})
			}
			patch.EstimationPoints = todoapp.Field[*int64]{Present: true, Value: &points}
		}
	}

	if v, ok := raw["assigneeUserId"]; ok {
		if isNullJSON(v) {
			patch.AssigneeUserID = todoapp.Field[*int64]{Present: true}
		} else {
			var assignee int64
			if err := json.Unmarshal(v, &assignee); err != nil {
				return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid assigneeUserId", map[string]any{"field": "assigneeUserId"})
			}
			patch.AssigneeUserID = todoapp.Field[*int64]{Present: true, Value: &assignee}
		}
	}

	if v, ok := raw["sprintId"]; ok {
		if isNullJSON(v) {
			patch.SprintID = todoapp.Field[*int64]{Present: true}
		} else {
			var sprintID int64
			if err := json.Unmarshal(v, &sprintID); err != nil {
				return todoapp.UpdatePatch{}, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
			}
			patch.SprintID = todoapp.Field[*int64]{Present: true, Value: &sprintID}
		}
	}

	return patch, nil
}

func isNullJSON(v json.RawMessage) bool {
	return strings.TrimSpace(string(v)) == "null"
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
