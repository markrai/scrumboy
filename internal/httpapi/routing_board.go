package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	priorityapp "scrumboy/internal/application/priority"
	sprintapp "scrumboy/internal/application/sprint"
	todoapp "scrumboy/internal/application/todo"
	todolinkapp "scrumboy/internal/application/todolink"
	workflowapp "scrumboy/internal/application/workflow"
	"scrumboy/internal/store"
)

func writeWorkflowMutationPrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflowapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, workflowapp.ErrMaintainerRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
	default:
		writeInternal(w, err)
	}
}

func writePriorityMutationPrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, priorityapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, priorityapp.ErrMaintainerRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
	default:
		writeInternal(w, err)
	}
}

func writeSprintDefinitionPrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sprintapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, sprintapp.ErrMaintainerRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
	default:
		writeInternal(w, err)
	}
}

func writeSprintLifecyclePrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sprintapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, sprintapp.ErrMaintainerRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
	case errors.Is(err, sprintapp.ErrSprintNotInProject):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	default:
		writeStoreErr(w, err, true)
	}
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	slug, ok := parseSlug(rest[0])
	if !ok {
		writeValidationError(w, "invalid slug", "invalid_slug", map[string]any{"field": "slug"})
		return
	}

	if s.handleSlugBoardRead(w, r, rest, slug) {
		return
	}

	pc, err := s.store.GetProjectContextBySlug(s.requestContext(r), slug, s.storeMode())
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}

	if s.handleBoardReadEventsAndSettings(w, r, rest, &pc) {
		return
	}
	if s.handleBoardWorkflowRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardPriorityRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardClaimRoute(w, r, rest, &pc) {
		return
	}
	if s.handleBoardTodoRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardLinkRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardTodoItemRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardSprintRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardTagRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardMetricsRoutes(w, r, rest, &pc) {
		return
	}
	if s.handleBoardWallRoutes(w, r, rest, &pc) {
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleBoardReadEventsAndSettings(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/events
	if len(rest) == 2 && rest[1] == "events" && r.Method == http.MethodGet {
		s.handleBoardEvents(w, r, project.ID)
		return true
	}

	// PATCH /api/board/{slug}/settings - update board/project-level settings.
	if len(rest) == 2 && rest[1] == "settings" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		if err := s.store.CheckCanManageProject(ctx, project.ID, userID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}

		var in struct {
			DefaultSprintWeeks *int  `json:"defaultSprintWeeks"`
			SprintsEnabled     *bool `json:"sprintsEnabled"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.DefaultSprintWeeks == nil && in.SprintsEnabled == nil {
			writeValidationError(w, "defaultSprintWeeks required", "default_sprint_weeks_required", map[string]any{"field": "defaultSprintWeeks"})
			return true
		}
		if in.DefaultSprintWeeks != nil && *in.DefaultSprintWeeks != 1 && *in.DefaultSprintWeeks != 2 {
			writeValidationError(w, "defaultSprintWeeks must be 1 or 2", "invalid_default_sprint_weeks", map[string]any{"field": "defaultSprintWeeks"})
			return true
		}

		resp := map[string]any{}
		if in.DefaultSprintWeeks != nil {
			if project.DefaultSprintWeeks != *in.DefaultSprintWeeks {
				if err := s.store.UpdateProjectDefaultSprintWeeks(ctx, project.ID, userID, *in.DefaultSprintWeeks); err != nil {
					writeStoreErr(w, err, true)
					return true
				}
			}
			resp["defaultSprintWeeks"] = *in.DefaultSprintWeeks
		}
		if in.SprintsEnabled != nil {
			if err := s.store.UpdateProjectSprintsEnabled(ctx, project.ID, userID, *in.SprintsEnabled); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			resp["sprintsEnabled"] = *in.SprintsEnabled
		}
		s.emitRefreshNeeded(s.requestContext(r), project.ID, "project_settings_updated")
		writeJSON(w, http.StatusOK, resp)
		return true
	}

	return false
}

func (s *Server) handleBoardWorkflowRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/workflow/counts - unfiltered todo counts per lane (maintainer+).
	if len(rest) == 3 && rest[1] == "workflow" && rest[2] == "counts" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return true
		}
		counts, err := s.store.CountTodosByColumnKey(ctx, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if counts == nil {
			counts = map[string]int{}
		}
		writeJSON(w, http.StatusOK, workflowLaneCountsJSON{
			Slug:              project.Slug,
			CountsByColumnKey: counts,
		})
		return true
	}

	// POST /api/board/{slug}/workflow - add a new non-done lane before done.
	if len(rest) == 2 && rest[1] == "workflow" && r.Method == http.MethodPost {
		ctx := s.requestContext(r)
		prepared, err := s.workflowMutations.Prepare(ctx, workflowapp.ResolvedRESTMutationTarget{
			ProjectID: project.ID,
		})
		if err != nil {
			writeWorkflowMutationPrepareError(w, err)
			return true
		}

		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			writeValidationError(w, "name required", "name_required", map[string]any{"field": "name"})
			return true
		}
		if len(in.Name) > 200 {
			writeValidationError(w, "invalid workflow column name", "invalid_workflow_column_name", map[string]any{"field": "name"})
			return true
		}

		col, err := prepared.Create(workflowapp.CreateCommand{Name: in.Name})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, workflowColumnJSON{
			Key:      col.Key,
			Name:     col.Name,
			Color:    col.Color,
			IsDone:   col.IsDone,
			Position: col.Position,
		})
		return true
	}

	// PATCH /api/board/{slug}/workflow/{key} - update workflow lane label and color.
	if len(rest) == 3 && rest[1] == "workflow" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		prepared, err := s.workflowMutations.Prepare(ctx, workflowapp.ResolvedRESTMutationTarget{
			ProjectID: project.ID,
		})
		if err != nil {
			writeWorkflowMutationPrepareError(w, err)
			return true
		}
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeValidationError(w, "invalid workflow key", "invalid_workflow_key", map[string]any{"field": "key"})
			return true
		}

		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Color = strings.TrimSpace(in.Color)
		if in.Name == "" {
			writeValidationError(w, "name required", "name_required", map[string]any{"field": "name"})
			return true
		}
		if len(in.Name) > 200 {
			writeValidationError(w, "invalid workflow column name", "invalid_workflow_column_name", map[string]any{"field": "name"})
			return true
		}
		if in.Color == "" {
			writeValidationError(w, "color required", "color_required", map[string]any{"field": "color"})
			return true
		}
		if !store.ValidWorkflowColumnColor(in.Color) {
			writeValidationError(w, "invalid workflow column color", "invalid_workflow_column_color", map[string]any{"field": "color"})
			return true
		}
		if err := prepared.Update(workflowapp.UpdateCommand{
			Key:   columnKey,
			Name:  in.Name,
			Color: in.Color,
		}); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/workflow/{key} - delete an empty non-done lane.
	if len(rest) == 3 && rest[1] == "workflow" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		prepared, err := s.workflowMutations.Prepare(ctx, workflowapp.ResolvedRESTMutationTarget{
			ProjectID: project.ID,
		})
		if err != nil {
			writeWorkflowMutationPrepareError(w, err)
			return true
		}
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeValidationError(w, "invalid workflow key", "invalid_workflow_key", map[string]any{"field": "key"})
			return true
		}
		if err := prepared.Delete(workflowapp.DeleteCommand{Key: columnKey}); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}

func (s *Server) handleBoardPriorityRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project
	ctx := s.requestContext(r)

	// Readers may list the same definitions already included in the initial board projection.
	if len(rest) == 2 && rest[1] == "priorities" && r.Method == http.MethodGet {
		priorities, err := s.store.GetProjectPriorities(ctx, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		items := make([]priorityTierJSON, 0, len(priorities))
		for _, tier := range priorities {
			items = append(items, priorityTierJSON{
				Key: tier.Key, Name: tier.Name, Color: tier.Color, Position: tier.Position,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return true
	}

	if len(rest) == 3 && rest[1] == "priorities" && rest[2] == "counts" && r.Method == http.MethodGet {
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return true
		}
		counts, err := s.store.CountTodosByPriorityKey(ctx, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if counts == nil {
			counts = map[string]int{}
		}
		writeJSON(w, http.StatusOK, priorityTierCountsJSON{Slug: project.Slug, CountsByPriorityKey: counts})
		return true
	}

	if len(rest) == 2 && rest[1] == "priorities" && r.Method == http.MethodPost {
		prepared, err := s.priorityMutations.Prepare(ctx, priorityapp.ResolvedRESTMutationTarget{ProjectID: project.ID})
		if err != nil {
			writePriorityMutationPrepareError(w, err)
			return true
		}
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			writeValidationError(w, "name required", "invalid_priority_tier_name", map[string]any{"field": "name"})
			return true
		}
		if len(in.Name) > 200 {
			writeValidationError(w, "invalid priority tier name", "invalid_priority_tier_name", map[string]any{"field": "name"})
			return true
		}
		tier, err := prepared.Create(priorityapp.CreateCommand{Name: in.Name})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, priorityTierJSON{Key: tier.Key, Name: tier.Name, Color: tier.Color, Position: tier.Position})
		return true
	}

	if len(rest) == 3 && rest[1] == "priorities" && r.Method == http.MethodPatch {
		prepared, err := s.priorityMutations.Prepare(ctx, priorityapp.ResolvedRESTMutationTarget{ProjectID: project.ID})
		if err != nil {
			writePriorityMutationPrepareError(w, err)
			return true
		}
		key := strings.TrimSpace(rest[2])
		if key == "" {
			writeValidationError(w, "invalid priority key", "invalid_priority_key", map[string]any{"field": "key"})
			return true
		}
		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Color = strings.TrimSpace(in.Color)
		if in.Name == "" || len(in.Name) > 200 {
			writeValidationError(w, "invalid priority tier name", "invalid_priority_tier_name", map[string]any{"field": "name"})
			return true
		}
		if !store.ValidWorkflowColumnColor(in.Color) {
			writeValidationError(w, "invalid priority tier color", "invalid_priority_tier_color", map[string]any{"field": "color"})
			return true
		}
		if err := prepared.Update(priorityapp.UpdateCommand{Key: key, Name: in.Name, Color: in.Color}); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	if len(rest) == 3 && rest[1] == "priorities" && r.Method == http.MethodDelete {
		prepared, err := s.priorityMutations.Prepare(ctx, priorityapp.ResolvedRESTMutationTarget{ProjectID: project.ID})
		if err != nil {
			writePriorityMutationPrepareError(w, err)
			return true
		}
		key := strings.TrimSpace(rest[2])
		if key == "" {
			writeValidationError(w, "invalid priority key", "invalid_priority_key", map[string]any{"field": "key"})
			return true
		}
		if err := prepared.Delete(priorityapp.DeleteCommand{Key: key}); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}

func (s *Server) handleBoardClaimRoute(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// POST /api/board/{slug}/claim
	// Escape hatch: the recorded creator of a Full Mode Temporary Board converts it into a
	// Durable Project (owned, non-expiring). Anonymous Boards (no creator) are never claimable.
	// Disabled entirely in Anonymous Mode. No UI assumptions; server-side only. Authorization is
	// enforced in the store (ClaimTemporaryBoard); any unauthorized/ineligible state returns 404.
	if len(rest) != 2 || rest[1] != "claim" || r.Method != http.MethodPost {
		return false
	}

	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return true
	}
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return true
	}
	if err := s.store.ClaimTemporaryBoard(ctx, project.ID, userID); err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	s.emitRefreshNeeded(s.requestContext(r), project.ID, "board_claimed")
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (s *Server) handleBoardTodoRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// POST /api/board/{slug}/todos
	if len(rest) == 2 && rest[1] == "todos" && r.Method == http.MethodPost {
		var in struct {
			Title            string   `json:"title"`
			Body             string   `json:"body"`
			Tags             []string `json:"tags"`
			ColumnKey        string   `json:"columnKey"`
			Status           string   `json:"status"`
			EstimationPoints *int64   `json:"estimationPoints"`
			SprintID         *int64   `json:"sprintId"`
			AssigneeUserID   *int64   `json:"assigneeUserId"`
			PriorityKey      *string  `json:"priorityKey"`
			Position         *struct {
				AfterID  *int64 `json:"afterId"`
				BeforeID *int64 `json:"beforeId"`
			} `json:"position"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}

		columnKey := normalizeLaneKey(in.ColumnKey)
		if columnKey == "" && in.Status != "" {
			columnKey = normalizeLaneKey(in.Status)
		}
		if columnKey == "" {
			columnKey = store.DefaultColumnBacklog
		}

		position := todoapp.ResolvedCreatePosition{}
		if in.Position != nil {
			position.AfterTodoID = in.Position.AfterID
			position.BeforeTodoID = in.Position.BeforeID
		}

		prepared := s.todoCreates.Prepare(s.requestContext(r), todoapp.ResolvedCreateTarget{
			ProjectContext: *pc,
			Mode:           s.storeMode(),
		})
		result, err := prepared.Create(todoapp.CreateCommand{
			Values: todoapp.CreateValues{
				Title:            in.Title,
				Body:             in.Body,
				Tags:             in.Tags,
				ColumnKey:        columnKey,
				EstimationPoints: in.EstimationPoints,
				AssigneeUserID:   in.AssigneeUserID,
				SprintID:         in.SprintID,
				PriorityKey:      in.PriorityKey,
			},
			Position: position,
		})
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return true
			}
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, todoToJSONForProject(result.Todo, project))
		return true
	}

	// GET /api/board/{slug}/todos/search
	// NOTE: must be before /todos/{localId} parsing.
	if len(rest) == 3 && rest[1] == "todos" && rest[2] == "search" && r.Method == http.MethodGet {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
				limit = n
			}
		}

		var exclude []int64
		for _, raw := range strings.Split(r.URL.Query().Get("exclude"), ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
				exclude = append(exclude, n)
			}
		}

		items, err := s.store.SearchTodosForLinkPicker(s.requestContext(r), project.ID, q, limit, exclude, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"localId": it.LocalID,
				"title":   it.Title,
			})
		}
		writeJSON(w, http.StatusOK, out)
		return true
	}

	return false
}

func (s *Server) handleBoardLinkRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET/POST/DELETE /api/board/{slug}/todos/{localId}/links[/targetLocalId]
	// NOTE: must be before /todos/{localId} parsing.
	if len(rest) < 4 || rest[1] != "todos" || rest[3] != "links" {
		return false
	}

	localID, ok := parseInt64(rest[2])
	if !ok {
		writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
		return true
	}

	ctx := s.requestContext(r)
	isAdd := len(rest) == 4 && r.Method == http.MethodPost
	isRemove := len(rest) == 5 && r.Method == http.MethodDelete

	var prepared *todolinkapp.PreparedRESTMutation
	if isAdd || isRemove {
		var err error
		prepared, err = s.todoLinkMutations.Prepare(ctx, todolinkapp.ResolvedRESTMutationTarget{
			ProjectID:     project.ID,
			SourceLocalID: localID,
			Mode:          s.storeMode(),
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
	} else {
		if _, err := s.store.GetTodoByLocalID(ctx, project.ID, localID, s.storeMode()); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
	}

	switch {
	case len(rest) == 4 && r.Method == http.MethodGet:
		outbound, err := s.store.ListLinksForTodo(s.requestContext(r), project.ID, localID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		inbound, err := s.store.ListBacklinksForTodo(s.requestContext(r), project.ID, localID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}

		outboundJSON := make([]map[string]any, 0, len(outbound))
		for _, t := range outbound {
			outboundJSON = append(outboundJSON, map[string]any{
				"localId":  t.LocalID,
				"title":    t.Title,
				"linkType": t.LinkType,
			})
		}
		inboundJSON := make([]map[string]any, 0, len(inbound))
		for _, t := range inbound {
			inboundJSON = append(inboundJSON, map[string]any{
				"localId":  t.LocalID,
				"title":    t.Title,
				"linkType": t.LinkType,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"outbound": outboundJSON,
			"inbound":  inboundJSON,
		})
		return true

	case isAdd:
		var in struct {
			TargetLocalID int64  `json:"targetLocalId"`
			LinkType      string `json:"linkType"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.TargetLocalID <= 0 {
			writeValidationError(w, "targetLocalId required", "target_local_id_required", map[string]any{"field": "targetLocalId"})
			return true
		}
		if in.TargetLocalID == localID {
			writeValidationError(w, "cannot link todo to itself", "cannot_link_todo_to_itself", map[string]any{"field": "targetLocalId"})
			return true
		}
		if in.LinkType == "" {
			in.LinkType = "relates_to"
		}

		if err := prepared.Add(todolinkapp.AddCommand{
			TargetLocalID: in.TargetLocalID,
			LinkType:      in.LinkType,
		}); err != nil {
			switch {
			case errors.Is(err, store.ErrValidation):
				writeValidationError(w, "invalid link", "invalid_link", nil)
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true

	case isRemove:
		targetLocalID, ok := parseInt64(rest[4])
		if !ok {
			writeValidationError(w, "invalid targetLocalId", "invalid_target_local_id", map[string]any{"field": "targetLocalId"})
			return true
		}
		if err := prepared.Remove(todolinkapp.RemoveCommand{TargetLocalID: targetLocalID}); err != nil {
			switch {
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true

	default:
		return false
	}
}

func (s *Server) handleBoardTodoItemRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/todos/{localId}
	if len(rest) == 3 && rest[1] == "todos" && r.Method == http.MethodGet {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
			return true
		}
		todo, err := s.store.GetTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode())
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
		writeJSON(w, http.StatusOK, todoToJSONForProject(todo, project))
		return true
	}

	// Maintained mutation contract: frontend and characterization tests use the
	// slug/localId routes below. Legacy numeric /api/todos/{id} routes remain
	// compatibility-only in handleTodos.
	// PATCH/DELETE /api/board/{slug}/todos/{localId}
	if len(rest) == 3 && rest[1] == "todos" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
			return true
		}
		switch r.Method {
		case http.MethodPatch:
			var raw map[string]json.RawMessage
			if err := readJSON(w, r, s.maxBody, &raw); err != nil {
				return true
			}
			if _, ok := raw["assigneeUserId"]; !ok {
				writeValidationError(w, "missing assigneeUserId", "missing_assignee_user_id", map[string]any{"field": "assigneeUserId"})
				return true
			}

			var in struct {
				Title            string   `json:"title"`
				Body             string   `json:"body"`
				Tags             []string `json:"tags"`
				EstimationPoints *int64   `json:"estimationPoints"`
				AssigneeUserID   *int64   `json:"assigneeUserId"`
				SprintID         *int64   `json:"sprintId"`
				PriorityKey      *string  `json:"priorityKey"`
			}
			payload, err := json.Marshal(raw)
			if err != nil {
				writeValidationError(w, "invalid json payload", "invalid_json", nil)
				return true
			}
			if err := json.Unmarshal(payload, &in); err != nil {
				writeValidationError(w, "invalid json payload", "invalid_json", nil)
				return true
			}
			patch := todoapp.UpdatePatch{
				Title:            todoapp.Field[string]{Present: true, Value: in.Title},
				Body:             todoapp.Field[string]{Present: true, Value: in.Body},
				Tags:             todoapp.Field[[]string]{Present: true, Value: in.Tags},
				EstimationPoints: todoapp.Field[*int64]{Present: true, Value: in.EstimationPoints},
				AssigneeUserID:   todoapp.Field[*int64]{Present: true, Value: in.AssigneeUserID},
			}
			if _, hasSprintID := raw["sprintId"]; hasSprintID {
				patch.SprintID = todoapp.Field[*int64]{Present: true, Value: in.SprintID}
			}
			if _, hasPriorityKey := raw["priorityKey"]; hasPriorityKey {
				patch.PriorityKey = todoapp.Field[*string]{Present: true, Value: in.PriorityKey}
			}
			prepared := s.todoUpdates.Prepare(s.requestContext(r), todoapp.ResolvedUpdateTarget{
				ProjectContext: *pc,
				Mode:           s.storeMode(),
			})
			result, err := prepared.Update(todoapp.UpdateCommand{LocalID: localID, Patch: patch})
			if err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
					return true
				}
				writeStoreErr(w, err, true)
				return true
			}
			writeJSON(w, http.StatusOK, todoToJSONForProject(result.Todo, project))
			return true

		case http.MethodDelete:
			if err := s.store.DeleteTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode()); err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
					return true
				}
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(s.requestContext(r), project.ID, "todo_deleted")
			w.WriteHeader(http.StatusNoContent)
			return true
		}
	}

	// POST /api/board/{slug}/todos/{localId}/move
	if len(rest) == 4 && rest[1] == "todos" && rest[3] == "move" && r.Method == http.MethodPost {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
			return true
		}
		var in struct {
			ToColumnKey string `json:"toColumnKey"`
			ToStatus    string `json:"toStatus"`
			AfterID     *int64 `json:"afterId"`
			BeforeID    *int64 `json:"beforeId"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		toColumnKey := in.ToColumnKey
		if toColumnKey == "" && in.ToStatus != "" {
			toColumnKey = normalizeLaneKey(in.ToStatus)
		}
		if toColumnKey == "" {
			writeValidationError(w, "missing toColumnKey", "missing_to_column_key", map[string]any{"field": "toColumnKey"})
			return true
		}
		// Interpret afterId/beforeId as localIds for this project. The shared
		// board router already resolved access, so prepare from that value instead
		// of repeating the slug lookup.
		prepared := s.todoMoves.Prepare(s.requestContext(r), todoapp.ResolvedMoveTarget{
			ProjectContext: *pc,
			Mode:           s.storeMode(),
		})
		result, err := prepared.Move(todoapp.MoveCommand{
			LocalID:       localID,
			ToColumnKey:   toColumnKey,
			AfterLocalID:  in.AfterID,
			BeforeLocalID: in.BeforeID,
		})
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return true
			}
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, todoToJSONForProject(result.Todo, project))
		return true
	}

	return false
}

func (s *Server) handleBoardSprintRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/sprints - list sprints with todoCount and unscheduledCount
	if len(rest) == 2 && rest[1] == "sprints" && r.Method == http.MethodGet {
		sprints, err := s.store.ListSprintsWithTodoCount(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if len(sprints) == 0 {
			writeJSON(w, http.StatusNoContent, nil)
			return true
		}
		unscheduledCount, err := s.store.CountUnscheduledTodos(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"sprints": sprintsWithTodoCountToJSON(sprints), "unscheduledCount": unscheduledCount})
		return true
	}

	// POST /api/board/{slug}/sprints - create sprint (Maintainer+)
	if len(rest) == 2 && rest[1] == "sprints" && r.Method == http.MethodPost {
		ctx := s.requestContext(r)
		prepared, err := s.sprintDefinitions.PrepareCreate(ctx, sprintapp.ResolvedRESTProjectTarget{
			ProjectID: project.ID,
		})
		if err != nil {
			writeSprintDefinitionPrepareError(w, err)
			return true
		}
		var in struct {
			Name           string `json:"name"`
			PlannedStartAt int64  `json:"plannedStartAt"`
			PlannedEndAt   int64  `json:"plannedEndAt"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.Name == "" {
			writeValidationError(w, "name required", "name_required", map[string]any{"field": "name"})
			return true
		}
		sprint, err := prepared.Create(sprintapp.CreateCommand{
			Name:           in.Name,
			PlannedStartAt: time.UnixMilli(in.PlannedStartAt),
			PlannedEndAt:   time.UnixMilli(in.PlannedEndAt),
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusCreated, sprintToJSON(sprint))
		return true
	}

	// GET /api/board/{slug}/sprints/active - get active sprint
	if len(rest) == 3 && rest[1] == "sprints" && rest[2] == "active" && r.Method == http.MethodGet {
		sp, err := s.store.GetActiveSprintByProjectID(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if sp == nil {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		writeJSON(w, http.StatusOK, sprintToJSON(*sp))
		return true
	}

	// GET/PATCH/DELETE /api/board/{slug}/sprints/{sprintId}
	if len(rest) == 3 && rest[1] == "sprints" {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid sprintId", "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return true
		}
		sp, err := s.store.GetSprintByID(s.requestContext(r), sprintID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if sp.ProjectID != project.ID {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "sprint not found", nil)
			return true
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, sprintToJSON(sp))
			return true

		case http.MethodPatch:
			ctx := s.requestContext(r)
			prepared, err := s.sprintDefinitions.PrepareUpdate(ctx, sprintapp.ResolvedRESTSprintTarget{
				ProjectID: project.ID,
				SprintID:  sp.ID,
			})
			if err != nil {
				writeSprintDefinitionPrepareError(w, err)
				return true
			}
			var in struct {
				Name           *string `json:"name"`
				PlannedStartAt *int64  `json:"plannedStartAt"`
				PlannedEndAt   *int64  `json:"plannedEndAt"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return true
			}
			command := sprintapp.UpdateCommand{Name: in.Name}
			if in.PlannedStartAt != nil {
				t := time.UnixMilli(*in.PlannedStartAt)
				command.PlannedStartAt = &t
			}
			if in.PlannedEndAt != nil {
				t := time.UnixMilli(*in.PlannedEndAt)
				command.PlannedEndAt = &t
			}
			if err := prepared.Update(command); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			w.WriteHeader(http.StatusNoContent)
			return true

		case http.MethodDelete:
			ctx := s.requestContext(r)
			prepared, err := s.sprintDeletions.PrepareDelete(ctx, sprintapp.DeletionTarget{
				ProjectID: project.ID,
				SprintID:  sprintID,
			})
			if err != nil {
				writeSprintLifecyclePrepareError(w, err)
				return true
			}
			if err := prepared.Delete(); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			w.WriteHeader(http.StatusNoContent)
			return true

		default:
			return false
		}
	}

	// GET /api/board/{slug}/sprints/{sprintId}/burndown - sprint-scoped burndown
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "burndown" && r.Method == http.MethodGet {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid sprintId", "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return true
		}
		points, err := s.store.GetRealBurndownForSprint(s.requestContext(r), project.ID, sprintID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return true
	}

	// POST /api/board/{slug}/sprints/{sprintId}/activate - activate sprint (Maintainer+)
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "activate" && r.Method == http.MethodPost {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid sprintId", "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return true
		}
		ctx := s.requestContext(r)
		prepared, err := s.sprintLifecycle.PrepareActivate(ctx, sprintapp.TransitionTarget{
			ProjectID: project.ID,
			SprintID:  sprintID,
		})
		if err != nil {
			writeSprintLifecyclePrepareError(w, err)
			return true
		}
		if err := prepared.Activate(); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// POST /api/board/{slug}/sprints/{sprintId}/close - close sprint (Maintainer+)
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "close" && r.Method == http.MethodPost {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeValidationError(w, "invalid sprintId", "invalid_sprint_id", map[string]any{"field": "sprintId"})
			return true
		}
		ctx := s.requestContext(r)
		prepared, err := s.sprintLifecycle.PrepareClose(ctx, sprintapp.TransitionTarget{
			ProjectID: project.ID,
			SprintID:  sprintID,
		})
		if err != nil {
			writeSprintLifecyclePrepareError(w, err)
			return true
		}
		if err := prepared.Close(); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}

func (s *Server) handleBoardTagRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/tags - return all tags used in project (grouped by name)
	if len(rest) == 2 && rest[1] == "tags" && r.Method == http.MethodGet {
		tags, err := s.store.ListTagCounts(s.requestContext(r), pc)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, tagCountsToJSON(tags))
		return true
	}

	// GET /api/board/{slug}/tags/user - return user's tags for autocomplete
	if len(rest) == 3 && rest[1] == "tags" && rest[2] == "user" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		tags, err := s.store.ListUserTagsForProject(ctx, userID, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tags))
		return true
	}

	// PATCH /api/board/{slug}/tags/id/{tagId}/color - update tag color by tag_id (works for both durable and anonymous).
	if len(rest) == 5 && rest[1] == "tags" && rest[2] == "id" && rest[4] == "color" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeValidationError(w, "invalid tagId", "invalid_tag_id", map[string]any{"field": "tagId"})
			return true
		}
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		var patchColorErr error
		if project.ExpiresAt != nil {
			var viewerUserID *int64
			if userID, ok := store.UserIDFromContext(ctx); ok {
				viewerUserID = &userID
			}
			patchColorErr = s.store.UpdateTagColorForTemporaryBoard(ctx, project.ID, viewerUserID, tagID, in.Color)
		} else {
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
			patchColorErr = s.store.UpdateTagColorForDurableProjectByID(ctx, project.ID, userID, tagID, in.Color)
		}
		if patchColorErr != nil {
			writeStoreErr(w, patchColorErr, true)
			return true
		}
		s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// PATCH /api/board/{slug}/tags/{tagName}/color - update tag color by name.
	// Temporary/anonymous boards resolve board-scoped or link-holder tags; durable
	// projects set the authenticated viewer's personal color for the grouped label.
	if len(rest) == 4 && rest[1] == "tags" && rest[3] == "color" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		tagName := rest[2]
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}

		if project.ExpiresAt == nil {
			// Durable project: name-based personal color for any authenticated member.
			// SetViewerTagColorByName enforces membership. Same known limitation as the
			// /api/projects route: only this project is refreshed, and the viewer's other
			// boards sharing the backing rows pick the color up on next load.
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
			if err := s.store.SetViewerTagColorByName(ctx, project.ID, userID, tagName, in.Color); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_color_updated")
			w.WriteHeader(http.StatusNoContent)
			return true
		}

		linkTemporaryBoard := true
		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}

		if err := s.store.UpdateTagColorForProject(ctx, project.ID, viewerUserID, tagName, in.Color, linkTemporaryBoard); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/tags/id/{tagId} - delete by tag_id (preferred; authority by tag_id).
	isAnonymousBoard := project.ExpiresAt != nil && project.CreatorUserID == nil
	if len(rest) == 4 && rest[1] == "tags" && rest[2] == "id" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeValidationError(w, "invalid tagId", "invalid_tag_id", map[string]any{"field": "tagId"})
			return true
		}
		if project.ExpiresAt != nil {
			// Temporary/anonymous boards keep the previous DeleteTag path.
			userID, _ := store.UserIDFromContext(ctx)
			if err := s.store.DeleteTag(ctx, userID, tagID, isAnonymousBoard); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_deleted")
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		affected, err := s.store.DeleteTagForDurableProjectByID(ctx, project.ID, userID, tagID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitTagDeletedRefresh(s.requestContext(r), project.ID, affected)
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/tags/{tagName} - delete by name.
	// Durable projects delete only the caller's own personal tag rows for the name
	// (grouped label persists if other members still use it). Anonymous/temporary
	// boards keep the board-scoped resolution below.
	if len(rest) == 3 && rest[1] == "tags" && r.Method == http.MethodDelete {
		if !isAnonymousBoard && project.ExpiresAt == nil {
			ctx := s.requestContext(r)
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
			affected, err := s.store.DeleteMyTagByName(ctx, project.ID, userID, rest[2])
			if err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitTagDeletedRefresh(s.requestContext(r), project.ID, affected)
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		if !isAnonymousBoard {
			writeValidationError(w, "name-based delete not allowed for durable projects; use /tags/id/{tagId}", "name_based_tag_route_not_allowed", nil)
			return true
		}
		ctx := s.requestContext(r)
		tagName := rest[2]
		userID, hasUserID := store.UserIDFromContext(ctx)

		boardTagID, err := s.store.GetBoardScopedTagIDByName(ctx, project.ID, tagName)
		if err == nil {
			if err := s.store.DeleteTag(ctx, 0, boardTagID, isAnonymousBoard); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_deleted")
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		if !errors.Is(err, store.ErrNotFound) {
			writeStoreErr(w, err, true)
			return true
		}

		if !hasUserID {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		tagID, err := s.store.GetTagIDByName(ctx, userID, tagName)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if err := s.store.DeleteTag(ctx, userID, tagID, isAnonymousBoard); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(s.requestContext(r), project.ID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}

func (s *Server) handleBoardMetricsRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/burndown
	if len(rest) == 2 && rest[1] == "burndown" && r.Method == http.MethodGet {
		points, err := s.store.GetRealBurndown(s.requestContext(r), project.ID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return true
	}

	// GET /api/board/{slug}/backlog-size
	if len(rest) == 2 && rest[1] == "backlog-size" && r.Method == http.MethodGet {
		points, err := s.store.GetBacklogSize(s.requestContext(r), project.ID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, burndownToJSON(points))
		return true
	}

	return false
}
