package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"scrumboy/internal/projectcolor"
	"scrumboy/internal/store"
)

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, rest []string) {
	// In anonymous deployment mode, block most numeric-ID project routes.
	// Only slug-based routes (/api/board/{slug}) are allowed for pastebin semantics.
	//
	// Exception: PATCH /api/projects/{id} stays available only for active anonymous temp boards
	// so paste-style boards can be renamed without exposing broader project mutation paths.
	//
	// See TestAnonymousMode_RenameProjectAuthorization for the contract.
	if s.mode == "anonymous" {
		// Keep PATCH reachable so eligible anonymous temp boards can be renamed.
		// The route still applies temp-board gating below before store mutation runs.
		if len(rest) == 1 && r.Method == http.MethodPatch {
			// Continue to PATCH handler below
		} else {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
	}

	if s.handleProjectsRoot(w, r, rest) {
		return
	}
	if len(rest) == 0 {
		return
	}

	projectID, ok := parseInt64(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid project id", map[string]any{"field": "projectId"})
		return
	}

	if s.mode == "anonymous" && len(rest) == 1 && r.Method == http.MethodPatch {
		p, err := s.store.GetProject(s.requestContext(r), projectID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				return
			}
			writeStoreErr(w, err, true)
			return
		}
		if p.ExpiresAt == nil || p.CreatorUserID != nil || !p.ExpiresAt.After(time.Now().UTC()) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
	}

	if s.handleProjectsProjectItem(w, r, rest, projectID) {
		return
	}
	if s.handleProjectsProjectReads(w, r, rest, projectID) {
		return
	}
	if s.handleProjectsProjectMembers(w, r, rest, projectID) {
		return
	}
	if s.handleProjectsProjectAvailableUsers(w, r, rest, projectID) {
		return
	}
	if s.handleProjectsProjectTags(w, r, rest, projectID) {
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleProjectsRoot(w http.ResponseWriter, r *http.Request, rest []string) bool {
	// /api/projects
	if len(rest) != 0 {
		return false
	}

	switch r.Method {
	case http.MethodGet:
		// If auth is enabled, require authentication.
		if _, ok := store.UserIDFromContext(s.requestContext(r)); !ok {
			if n, err := s.store.CountUsers(s.requestContext(r)); err == nil && n > 0 {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
		}
		projects, err := s.store.ListProjects(s.requestContext(r))
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
			writeInternal(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, projectsToJSON(projects))
		return true

	case http.MethodPost:
		var in struct {
			Name     string `json:"name"`
			Workflow *[]struct {
				Key      string `json:"key"`
				Name     string `json:"name"`
				Color    string `json:"color"`
				Position int    `json:"position"`
				IsDone   bool   `json:"isDone"`
			} `json:"workflow"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		var workflow []store.WorkflowColumn
		if in.Workflow != nil {
			workflow = make([]store.WorkflowColumn, 0, len(*in.Workflow))
			for _, col := range *in.Workflow {
				workflow = append(workflow, store.WorkflowColumn{
					Key:      col.Key,
					Name:     col.Name,
					Color:    col.Color,
					Position: col.Position,
					IsDone:   col.IsDone,
				})
			}
		}
		p, err := s.store.CreateProjectWithWorkflow(s.requestContext(r), in.Name, workflow)
		if err != nil {
			writeStoreErr(w, err, false)
			return true
		}
		writeJSON(w, http.StatusCreated, projectToJSON(p))
		return true

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return true
	}
}

func (s *Server) handleProjectsProjectItem(w http.ResponseWriter, r *http.Request, rest []string, projectID int64) bool {
	// /api/projects/{id}
	if len(rest) != 1 {
		return false
	}

	switch r.Method {
	case http.MethodDelete:
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		if err := s.store.DeleteProject(ctx, projectID, userID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), projectID, "project_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true

	case http.MethodPatch:
		ctx := s.requestContext(r)
		userID, hasUser := store.UserIDFromContext(ctx)
		var in struct {
			Name  *string `json:"name"`
			Image *string `json:"image"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.Image != nil {
			if !hasUser {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return true
			}
			color := projectcolor.ExtractFromDataURL(*in.Image)
			if err := s.store.UpdateProjectImage(ctx, projectID, userID, in.Image, color); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
		}
		if in.Name != nil {
			uid := int64(0)
			if hasUser {
				uid = userID
			}
			if err := s.store.UpdateProjectName(ctx, projectID, uid, *in.Name); err != nil {
				writeStoreErr(w, err, true)
				return true
			}
		}
		project, err := s.store.GetProject(s.requestContext(r), projectID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if in.Name != nil || in.Image != nil {
			s.emitRefreshNeeded(r.Context(), projectID, "project_updated")
		}
		writeJSON(w, http.StatusOK, projectToJSON(project))
		return true

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return true
	}
}

func (s *Server) handleProjectsProjectReads(w http.ResponseWriter, r *http.Request, rest []string, projectID int64) bool {
	if len(rest) == 2 && rest[1] == "board" && r.Method == http.MethodGet {
		pc, err := s.store.GetProjectContextForRead(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		tag := r.URL.Query().Get("tag")
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if search == "" {
			search = ""
		}
		sprintFilter, err := s.parseSprintFilterFromQuery(r, projectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
			return true
		}
		project, tags, workflow, cols, err := s.store.GetBoard(s.requestContext(r), &pc, tag, search, sprintFilter)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, boardToJSON(project, workflow, tags, cols))
		return true
	}

	if len(rest) == 2 && rest[1] == "burndown" && r.Method == http.MethodGet {
		points, err := s.store.GetRealBurndown(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return true
	}

	if len(rest) == 2 && rest[1] == "backlog-size" && r.Method == http.MethodGet {
		points, err := s.store.GetBacklogSize(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, burndownToJSON(points))
		return true
	}

	return false
}

func (s *Server) handleProjectsProjectMembers(w http.ResponseWriter, r *http.Request, rest []string, projectID int64) bool {
	if len(rest) == 2 && rest[1] == "members" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		members, err := s.store.ListProjectMembers(ctx, projectID, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return true
	}

	if len(rest) == 2 && rest[1] == "members" && r.Method == http.MethodPost {
		var in struct {
			UserID int64  `json:"user_id"`
			Role   string `json:"role"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}

		role, ok := store.ParseProjectRole(in.Role)
		if !ok || !store.IsValidProjectRole(role) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid role", map[string]any{"field": "role"})
			return true
		}

		userID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}

		if err := s.store.AddProjectMember(s.requestContext(r), userID, projectID, in.UserID, role); err != nil {
			writeStoreErr(w, err, true)
			return true
		}

		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return true
	}

	if len(rest) == 3 && rest[1] == "members" && r.Method == http.MethodDelete {
		targetUserID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid user id", map[string]any{"field": "userId"})
			return true
		}

		requesterID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}

		if err := s.store.RemoveProjectMember(s.requestContext(r), requesterID, projectID, targetUserID); err != nil {
			writeStoreErr(w, err, true)
			return true
		}

		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, requesterID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return true
	}

	if len(rest) == 3 && rest[1] == "members" && r.Method == http.MethodPatch {
		targetUserID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid user id", map[string]any{"field": "userId"})
			return true
		}
		var in struct {
			Role string `json:"role"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		role, ok := store.ParseMemberRole(in.Role)
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid role", map[string]any{"field": "role"})
			return true
		}
		requesterID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		if err := s.store.UpdateProjectMemberRole(s.requestContext(r), requesterID, projectID, targetUserID, role); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrConflict):
				writeError(w, http.StatusConflict, "CONFLICT", err.Error(), nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			case errors.Is(err, store.ErrValidation):
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "role"})
			default:
				writeStoreErr(w, err, true)
			}
			return true
		}
		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, requesterID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return true
	}

	return false
}

func (s *Server) handleProjectsProjectAvailableUsers(w http.ResponseWriter, r *http.Request, rest []string, projectID int64) bool {
	if len(rest) != 2 || rest[1] != "available-users" || r.Method != http.MethodGet {
		return false
	}

	userID, ok := store.UserIDFromContext(s.requestContext(r))
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return true
	}

	users, err := s.store.ListAvailableUsersForProject(s.requestContext(r), userID, projectID)
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}

	usersJSON := make([]userJSON, 0, len(users))
	for _, u := range users {
		usersJSON = append(usersJSON, userToJSON(u))
	}
	writeJSON(w, http.StatusOK, usersJSON)
	return true
}

func (s *Server) handleProjectsProjectTags(w http.ResponseWriter, r *http.Request, rest []string, projectID int64) bool {
	if len(rest) == 2 && rest[1] == "tags" && r.Method == http.MethodGet {
		pc, err := s.store.GetProjectContextForRead(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		tags, err := s.store.ListTagCounts(s.requestContext(r), &pc)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
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
		if err := s.store.UpdateTagColor(ctx, viewerUserID, tagID, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), projectID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	if len(rest) == 4 && rest[1] == "tags" && rest[3] == "color" && r.Method == http.MethodPatch {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based color update not allowed for durable projects; use /tags/id/{tagId}/color", nil)
		return true
	}

	if len(rest) == 4 && rest[1] == "tags" && rest[2] == "id" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return true
		}
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return true
		}
		if err := s.store.DeleteTag(ctx, userID, tagID, false); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), projectID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	if len(rest) == 3 && rest[1] == "tags" && r.Method == http.MethodDelete {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based delete not allowed for durable projects; use /tags/id/{tagId}", nil)
		return true
	}

	return false
}
