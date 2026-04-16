package httpapi

import (
	"encoding/json"
	"net/http"

	"scrumboy/internal/store"
)

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	todoID, ok := parseInt64(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo id", map[string]any{"field": "todoId"})
		return
	}

	if s.handleTodosPatchOrDelete(w, r, rest, todoID) {
		return
	}
	if s.handleTodosMove(w, r, rest, todoID) {
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleTodosPatchOrDelete(w http.ResponseWriter, r *http.Request, rest []string, todoID int64) bool {
	// /api/todos/{id}
	if len(rest) != 1 {
		return false
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
		todo, err := s.store.UpdateTodo(s.requestContext(r), todoID, store.UpdateTodoInput{
			Title:            in.Title,
			Body:             in.Body,
			Tags:             in.Tags,
			EstimationPoints: in.EstimationPoints,
			AssigneeUserID:   in.AssigneeUserID,
		}, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if !todo.AssignmentChanged {
			s.emitRefreshNeeded(r.Context(), todo.ProjectID, "todo_updated")
		}
		writeJSON(w, http.StatusOK, todoToJSON(todo))
		return true

	case http.MethodDelete:
		projectID, err := s.store.GetProjectIDForTodo(s.requestContext(r), todoID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if err := s.store.DeleteTodo(s.requestContext(r), todoID, s.storeMode()); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		s.emitRefreshNeeded(r.Context(), projectID, "todo_deleted")
		w.WriteHeader(http.StatusNoContent)
		return true

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return true
	}
}

func (s *Server) handleTodosMove(w http.ResponseWriter, r *http.Request, rest []string, todoID int64) bool {
	// /api/todos/{id}/move
	if len(rest) != 2 || rest[1] != "move" || r.Method != http.MethodPost {
		return false
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
	todo, err := s.store.MoveTodo(s.requestContext(r), todoID, toColumnKey, in.AfterID, in.BeforeID, s.storeMode())
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	s.emitRefreshNeeded(r.Context(), todo.ProjectID, "todo_moved")
	writeJSON(w, http.StatusOK, todoToJSON(todo))
	return true
}
