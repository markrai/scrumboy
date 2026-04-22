package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/store"
)

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	slug, ok := parseSlug(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid slug", map[string]any{"field": "slug"})
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
	if s.handleBoardLaneRoutes(w, r, rest, &pc) {
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

	// GET /api/board/{slug}
	// Always use paged response (default limitPerLane=20) so mobile and cached clients get columnsMeta and limited items.
	if len(rest) == 1 && r.Method == http.MethodGet {
		tag := r.URL.Query().Get("tag")
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if search == "" {
			search = ""
		}
		hasSprints, err := s.store.HasSprints(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		var sprintFilter store.SprintFilter
		if !hasSprints {
			sprintFilter = store.SprintFilter{Mode: "none"}
		} else {
			sprintFilter, err = s.parseSprintFilterFromQuery(r, project.ID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
				return true
			}
		}
		limitPerLane := 20
		if v := r.URL.Query().Get("limitPerLane"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limitPerLane = n
			}
		}
		project2, tags, workflow, cols, meta, err := s.store.GetBoardPaged(s.requestContext(r), pc, tag, search, sprintFilter, limitPerLane)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, boardToJSONWithMeta(project2, workflow, tags, cols, meta))
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
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return true
		}

		var in struct {
			DefaultSprintWeeks *int `json:"defaultSprintWeeks"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.DefaultSprintWeeks == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "defaultSprintWeeks required", map[string]any{"field": "defaultSprintWeeks"})
			return true
		}
		if *in.DefaultSprintWeeks != 1 && *in.DefaultSprintWeeks != 2 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "defaultSprintWeeks must be 1 or 2", map[string]any{"field": "defaultSprintWeeks"})
			return true
		}
		if project.DefaultSprintWeeks == *in.DefaultSprintWeeks {
			writeJSON(w, http.StatusOK, map[string]any{"defaultSprintWeeks": *in.DefaultSprintWeeks})
			return true
		}

		if err := s.store.UpdateProjectDefaultSprintWeeks(ctx, project.ID, userID, *in.DefaultSprintWeeks); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "project_settings_updated")
		writeJSON(w, http.StatusOK, map[string]any{"defaultSprintWeeks": *in.DefaultSprintWeeks})
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

		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return true
		}
		if len(in.Name) > 200 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column name", map[string]any{"field": "name"})
			return true
		}

		col, err := s.store.AddWorkflowColumn(ctx, project.ID, in.Name)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_added")
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
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow key", map[string]any{"field": "key"})
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return true
		}
		if len(in.Name) > 200 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column name", map[string]any{"field": "name"})
			return true
		}
		if in.Color == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "color required", map[string]any{"field": "color"})
			return true
		}
		if !store.ValidWorkflowColumnColor(in.Color) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column color", map[string]any{"field": "color"})
			return true
		}
		if err := s.store.UpdateWorkflowColumn(ctx, project.ID, columnKey, in.Name, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/workflow/{key} - delete an empty non-done lane.
	if len(rest) == 3 && rest[1] == "workflow" && r.Method == http.MethodDelete {
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
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow key", map[string]any{"field": "key"})
			return true
		}
		if err := s.store.DeleteWorkflowColumn(ctx, project.ID, columnKey); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	return false
}

func (s *Server) handleBoardLaneRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// GET /api/board/{slug}/lanes/{status}
	if len(rest) != 3 || rest[1] != "lanes" || r.Method != http.MethodGet {
		return false
	}

	columnKey := normalizeLaneKey(rest[2])
	tag := r.URL.Query().Get("tag")
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	sprintFilter, err := s.parseSprintFilterFromQuery(r, project.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
		return true
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	afterCursor := r.URL.Query().Get("afterCursor")
	afterRank, afterID := store.ParseLaneCursor(afterCursor)

	items, nextCursor, hasMore, err := s.store.ListTodosForBoardLane(s.requestContext(r), project.ID, columnKey, limit, afterRank, afterID, tag, search, sprintFilter)
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	writeJSON(w, http.StatusOK, lanePageToJSON(items, nextCursor, hasMore))
	return true
}

func (s *Server) handleBoardClaimRoute(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	project := pc.Project

	// POST /api/board/{slug}/claim
	// Escape hatch: convert an unowned temporary board into an owned durable project.
	// No UI assumptions; server-side only.
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
	s.emitRefreshNeeded(r.Context(), project.ID, "board_claimed")
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

		var afterID, beforeID *int64
		if in.Position != nil {
			afterID = in.Position.AfterID
			beforeID = in.Position.BeforeID
		}

		todo, err := s.store.CreateTodo(s.requestContext(r), project.ID, store.CreateTodoInput{
			Title:            in.Title,
			Body:             in.Body,
			Tags:             in.Tags,
			ColumnKey:        columnKey,
			EstimationPoints: in.EstimationPoints,
			SprintID:         in.SprintID,
			AssigneeUserID:   in.AssigneeUserID,
			AfterID:          afterID,
			BeforeID:         beforeID,
		}, s.storeMode())
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return true
			}
			writeStoreErr(w, err, true)
			return true
		}
		if !todo.AssignmentChanged {
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_created")
		}
		writeJSON(w, http.StatusCreated, todoToJSON(todo))
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
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
		return true
	}

	if _, err := s.store.GetTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode()); err != nil {
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

	case len(rest) == 4 && r.Method == http.MethodPost:
		var in struct {
			TargetLocalID int64  `json:"targetLocalId"`
			LinkType      string `json:"linkType"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.TargetLocalID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "targetLocalId required", map[string]any{"field": "targetLocalId"})
			return true
		}
		if in.TargetLocalID == localID {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cannot link todo to itself", map[string]any{"field": "targetLocalId"})
			return true
		}
		if in.LinkType == "" {
			in.LinkType = "relates_to"
		}

		if err := s.store.AddLink(s.requestContext(r), project.ID, localID, in.TargetLocalID, in.LinkType, s.storeMode()); err != nil {
			switch {
			case errors.Is(err, store.ErrValidation):
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid link", nil)
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "todo_links_updated")
		w.WriteHeader(http.StatusNoContent)
		return true

	case len(rest) == 5 && r.Method == http.MethodDelete:
		targetLocalID, ok := parseInt64(rest[4])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid targetLocalId", map[string]any{"field": "targetLocalId"})
			return true
		}
		if err := s.store.RemoveLink(s.requestContext(r), project.ID, localID, targetLocalID, s.storeMode()); err != nil {
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
		s.emitRefreshNeeded(r.Context(), project.ID, "todo_links_updated")
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
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
		writeJSON(w, http.StatusOK, todoToJSON(todo))
		return true
	}

	// Maintained mutation contract: frontend and characterization tests use the
	// slug/localId routes below. Legacy numeric /api/todos/{id} routes remain
	// compatibility-only in handleTodos.
	// PATCH/DELETE /api/board/{slug}/todos/{localId}
	if len(rest) == 3 && rest[1] == "todos" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
			return true
		}
		switch r.Method {
		case http.MethodPatch:
			var raw map[string]json.RawMessage
			if err := readJSON(w, r, s.maxBody, &raw); err != nil {
				return true
			}
			if _, ok := raw["assigneeUserId"]; !ok {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing assigneeUserId", map[string]any{"field": "assigneeUserId"})
				return true
			}

			var in struct {
				Title            string   `json:"title"`
				Body             string   `json:"body"`
				Tags             []string `json:"tags"`
				EstimationPoints *int64   `json:"estimationPoints"`
				AssigneeUserID   *int64   `json:"assigneeUserId"`
				SprintID         *int64   `json:"sprintId"`
			}
			payload, err := json.Marshal(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return true
			}
			if err := json.Unmarshal(payload, &in); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return true
			}
			updateIn := store.UpdateTodoInput{
				Title:            in.Title,
				Body:             in.Body,
				Tags:             in.Tags,
				EstimationPoints: in.EstimationPoints,
				AssigneeUserID:   in.AssigneeUserID,
			}
			if _, hasSprintID := raw["sprintId"]; hasSprintID {
				if in.SprintID == nil {
					updateIn.ClearSprint = true
				} else {
					updateIn.SprintID = in.SprintID
				}
			}
			todo, err := s.store.UpdateTodoByLocalID(s.requestContext(r), project.ID, localID, updateIn, s.storeMode())
			if err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
					return true
				}
				writeStoreErr(w, err, true)
				return true
			}
			if !todo.AssignmentChanged {
				s.emitRefreshNeeded(r.Context(), project.ID, "todo_updated")
			}
			writeJSON(w, http.StatusOK, todoToJSON(todo))
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
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_deleted")
			w.WriteHeader(http.StatusNoContent)
			return true
		}
	}

	// POST /api/board/{slug}/todos/{localId}/move
	if len(rest) == 4 && rest[1] == "todos" && rest[3] == "move" && r.Method == http.MethodPost {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing toColumnKey", map[string]any{"field": "toColumnKey"})
			return true
		}
		// Interpret afterId/beforeId as localIds for this project.
		todo, err := s.store.MoveTodoByLocalID(s.requestContext(r), project.ID, localID, toColumnKey, in.AfterID, in.BeforeID, s.storeMode())
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return true
			}
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "todo_moved")
		writeJSON(w, http.StatusOK, todoToJSON(todo))
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
		var in struct {
			Name           string `json:"name"`
			PlannedStartAt int64  `json:"plannedStartAt"`
			PlannedEndAt   int64  `json:"plannedEndAt"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.Name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return true
		}
		sprint, err := s.store.CreateSprint(ctx, project.ID, in.Name, time.UnixMilli(in.PlannedStartAt), time.UnixMilli(in.PlannedEndAt))
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_created")
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
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
			var in struct {
				Name           *string `json:"name"`
				PlannedStartAt *int64  `json:"plannedStartAt"`
				PlannedEndAt   *int64  `json:"plannedEndAt"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return true
			}
			opts := store.UpdateSprintInput{}
			if in.Name != nil {
				opts.Name = in.Name
			}
			if in.PlannedStartAt != nil {
				t := time.UnixMilli(*in.PlannedStartAt)
				opts.PlannedStartAt = &t
			}
			if in.PlannedEndAt != nil {
				t := time.UnixMilli(*in.PlannedEndAt)
				opts.PlannedEndAt = &t
			}
			if err := s.store.UpdateSprint(ctx, sprintID, opts); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "sprint_updated")
			w.WriteHeader(http.StatusNoContent)
			return true

		case http.MethodDelete:
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
			if err := s.store.DeleteSprint(ctx, project.ID, sprintID); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "sprint_deleted")
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return true
		}
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
		if err := s.store.ActivateSprint(ctx, project.ID, sprintID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_activated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// POST /api/board/{slug}/sprints/{sprintId}/close - close sprint (Maintainer+)
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "close" && r.Method == http.MethodPost {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return true
		}
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
		if err := s.store.CloseSprint(ctx, sprintID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_closed")
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
		// Convert TagCount to TagWithColor (tag_id and canDelete from store)
		tagList := make([]store.TagWithColor, len(tags))
		for i, tc := range tags {
			tagList[i] = store.TagWithColor{
				TagID:     tc.TagID,
				Name:      tc.Name,
				Color:     tc.Color,
				CanDelete: tc.CanDelete,
			}
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tagList))
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
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return true
		}
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}
		var patchColorErr error
		if project.ExpiresAt != nil {
			patchColorErr = s.store.UpdateTagColorForTemporaryBoard(ctx, project.ID, viewerUserID, tagID, in.Color)
		} else {
			patchColorErr = s.store.UpdateTagColor(ctx, viewerUserID, tagID, in.Color)
		}
		if patchColorErr != nil {
			writeStoreErr(w, patchColorErr, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// PATCH /api/board/{slug}/tags/{tagName}/color - update tag color by name (temporary boards only).
	// Durable projects must use /tags/id/{tagId}/color.
	if len(rest) == 4 && rest[1] == "tags" && rest[3] == "color" && r.Method == http.MethodPatch {
		if project.ExpiresAt == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based color update not allowed for durable projects; use /tags/id/{tagId}/color", nil)
			return true
		}
		linkTemporaryBoard := true
		ctx := s.requestContext(r)
		tagName := rest[2]
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}

		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}

		if err := s.store.UpdateTagColorForProject(ctx, project.ID, viewerUserID, tagName, in.Color, linkTemporaryBoard); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/tags/id/{tagId} - delete by tag_id (preferred; authority by tag_id).
	isAnonymousBoard := project.ExpiresAt != nil && project.CreatorUserID == nil
	if len(rest) == 4 && rest[1] == "tags" && rest[2] == "id" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return true
		}
		userID, _ := store.UserIDFromContext(ctx)
		if err := s.store.DeleteTag(ctx, userID, tagID, isAnonymousBoard); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// DELETE /api/board/{slug}/tags/{tagName} - delete by name (anonymous-only).
	// Name-based mutation routes are anonymous-only. Durable projects must use tag_id.
	if len(rest) == 3 && rest[1] == "tags" && r.Method == http.MethodDelete {
		if !isAnonymousBoard {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based delete not allowed for durable projects; use /tags/id/{tagId}", nil)
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
			s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
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
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
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
