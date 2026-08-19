package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/store"
)

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	todoID, ok := parseInt64(rest[0])
	if !ok {
		writeValidationError(w, "invalid todo id", "invalid_todo_id", map[string]any{"field": "todoId"})
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
			writeValidationError(w, "missing assigneeUserId", "missing_assignee_user_id", map[string]any{"field": "assigneeUserId"})
			return true
		}

		var in struct {
			Title            string   `json:"title"`
			Body             string   `json:"body"`
			Tags             []string `json:"tags"`
			EstimationPoints *int64   `json:"estimationPoints"`
			AssigneeUserID   *int64   `json:"assigneeUserId"`
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
		prepared := s.todoLegacyUpdates.Prepare(s.requestContext(r), todoapp.LegacyUpdateTarget{
			Mode: s.storeMode(),
		})
		result, err := prepared.Update(todoapp.LegacyUpdateCommand{
			TodoID:           todoID,
			Title:            in.Title,
			Body:             in.Body,
			Tags:             in.Tags,
			EstimationPoints: in.EstimationPoints,
			AssigneeUserID:   in.AssigneeUserID,
			PriorityKey: todoapp.Field[*string]{
				Present: raw["priorityKey"] != nil,
				Value:   in.PriorityKey,
			},
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		writeJSON(w, http.StatusOK, s.legacyTodoMutationJSON(s.requestContext(r), result.Todo))
		return true

	case http.MethodDelete:
		prepared, err := s.todoLegacyDeletes.Prepare(s.requestContext(r), todoapp.LegacyDeleteTarget{
			TodoID: todoID,
			Mode:   s.storeMode(),
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		if err := prepared.Delete(); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
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
		writeValidationError(w, "missing toColumnKey", "missing_to_column_key", map[string]any{"field": "toColumnKey"})
		return true
	}
	prepared := s.todoLegacyMoves.Prepare(s.requestContext(r), todoapp.LegacyMoveTarget{
		Mode: s.storeMode(),
	})
	result, err := prepared.Move(todoapp.LegacyMoveCommand{
		TodoID:       todoID,
		ToColumnKey:  toColumnKey,
		AfterTodoID:  in.AfterID,
		BeforeTodoID: in.BeforeID,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	writeJSON(w, http.StatusOK, s.legacyTodoMutationJSON(s.requestContext(r), result.Todo))
	return true
}

// legacyTodoMutationJSON keeps response-only capability projection from
// changing the outcome of an already-committed numeric compatibility
// mutation. If capability cannot be read, suppress the sprint assignment so
// the response fails closed without reporting a transport failure.
func (s *Server) legacyTodoMutationJSON(ctx context.Context, todo store.Todo) todoJSON {
	project, err := s.store.GetProject(ctx, todo.ProjectID)
	if err != nil {
		todo.SprintID = nil
		return todoToJSON(todo)
	}
	return todoToJSONForProject(todo, project)
}
