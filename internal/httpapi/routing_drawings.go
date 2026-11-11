package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	publicboardapp "scrumboy/internal/application/publicboard"
	wallapp "scrumboy/internal/application/wall"
	"scrumboy/internal/store"
)

func (s *Server) decodeTodoDrawingsField(w http.ResponseWriter, raw json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, true
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &out); err != nil {
		writeValidationError(w, "invalid drawings (expected an object of drawing id to drawing document)", store.ReasonInvalidDrawing, map[string]any{"field": "drawings"})
		return nil, false
	}
	if len(out) > 0 && !s.drawingEnabled {
		writeValidationError(w, "drawings are disabled", store.ReasonDrawingsDisabled, map[string]any{"field": "drawings"})
		return nil, false
	}
	return out, true
}

func (s *Server) decodeNotesInkField(w http.ResponseWriter, raw json.RawMessage, present bool) (store.NotesInkChange, bool) {
	if !present {
		return store.NotesInkChange{}, true
	}
	if !s.drawingEnabled {
		writeValidationError(w, "drawings are disabled", store.ReasonDrawingsDisabled, map[string]any{"field": "notesInk"})
		return store.NotesInkChange{}, false
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && trimmed[0] != '{' {
		writeValidationError(w, "invalid notesInk (expected a drawing document or null)", store.ReasonInvalidDrawing, map[string]any{"field": "notesInk"})
		return store.NotesInkChange{}, false
	}
	return store.NotesInkChange{Present: true, Data: append(json.RawMessage(nil), trimmed...)}, true
}

func wantsDrawingSVG(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "application/json") {
		return false
	}
	return strings.Contains(accept, "image/svg+xml") || strings.Contains(accept, "image/*")
}

func writeDrawingResponse(w http.ResponseWriter, r *http.Request, raw json.RawMessage, cacheControl string) {
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Accept")
	if wantsDrawingSVG(r) {
		svg, err := renderDrawingSVG(raw)
		if err != nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(svg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func drawingsWithInkJSON(drawings map[string]json.RawMessage, ink json.RawMessage) map[string]any {
	if drawings == nil {
		drawings = map[string]json.RawMessage{}
	}
	var notesInk any
	if len(ink) > 0 {
		notesInk = ink
	}
	return map[string]any{"drawings": drawings, "notesInk": notesInk}
}

func (s *Server) handleBoardDrawingRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	isList := len(rest) == 4 && rest[1] == "todos" && rest[3] == "drawings"
	isOne := len(rest) == 3 && rest[1] == "drawings"
	if !isList && !isOne {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return true
	}
	if !s.drawingEnabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return true
	}
	ctx := s.requestContext(r)
	project := pc.Project
	if isList {
		localID, ok := parseInt64(rest[2])
		if !ok || localID < 1 {
			writeValidationError(w, "invalid todo localId", "invalid_todo_local_id", map[string]any{"field": "localId"})
			return true
		}
		if _, err := s.store.GetTodoByLocalID(ctx, project.ID, localID, s.storeMode()); err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		drawings, err := s.store.ListTodoDrawingsByLocalID(ctx, project.ID, localID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		ink, err := s.store.GetTodoNotesInkByLocalID(ctx, project.ID, localID)
		if err != nil {
			writeStoreErr(w, err, true)
			return true
		}
		w.Header().Set("Cache-Control", "private, no-cache")
		writeJSON(w, http.StatusOK, drawingsWithInkJSON(drawings, ink))
		return true
	}
	raw, err := s.store.GetProjectDrawing(ctx, project.ID, rest[2], true)
	if err != nil {
		writeStoreErr(w, err, true)
		return true
	}
	writeDrawingResponse(w, r, raw, "private, max-age=60")
	return true
}

func (s *Server) handlePublicDrawingRoutes(w http.ResponseWriter, r *http.Request, parts []string, prepared publicboardapp.PreparedRead) bool {
	isList := len(parts) == 5 && parts[2] == "todos" && parts[4] == "drawings"
	isOne := len(parts) == 4 && parts[2] == "drawings"
	if !isList && !isOne {
		return false
	}
	if !s.drawingEnabled || len(r.URL.Query()) != 0 {
		s.writePublicBoardError(w, publicboardapp.ErrPublicNotFound)
		return true
	}
	ctx := r.Context()
	if isList {
		localID, ok := parseInt64(parts[3])
		if !ok || localID < 1 {
			s.writePublicBoardError(w, publicboardapp.ErrInvalidPublicQuery)
			return true
		}
		drawings, err := s.store.ListPublicTodoDrawings(ctx, prepared.ProjectID(), prepared.Slug(), localID)
		if err != nil {
			s.writePublicDrawingError(w, err)
			return true
		}
		ink, err := s.store.GetPublicTodoNotesInk(ctx, prepared.ProjectID(), prepared.Slug(), localID)
		if err != nil {
			s.writePublicDrawingError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, drawingsWithInkJSON(drawings, ink))
		return true
	}
	raw, err := s.store.GetPublicDrawing(ctx, prepared.ProjectID(), prepared.Slug(), parts[3])
	if err != nil {
		s.writePublicDrawingError(w, err)
		return true
	}
	writeDrawingResponse(w, r, raw, "no-store")
	return true
}

func (s *Server) writePublicDrawingError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrUnauthorized) || errors.Is(err, store.ErrForbidden) {
		s.writePublicBoardError(w, publicboardapp.ErrPublicNotFound)
		return
	}
	s.writePublicBoardError(w, err)
}

type wallDrawingsInputJSON struct {
	BaseVersion int64             `json:"baseVersion"`
	Add         []json.RawMessage `json:"add"`
	Erase       []string          `json:"erase"`
}

func (s *Server) handleWallDrawings(w http.ResponseWriter, r *http.Request, project store.Project) {
	if !s.wallDrawingEnabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	mutationCtx := s.requestContext(r)
	prepared, err := s.wallDrawingMutations.Prepare(mutationCtx, r.Context(), wallapp.ResolvedRESTTarget{ProjectID: project.ID})
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallDrawingsInputJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	result, err := prepared.Apply(wallapp.ApplyDrawingsCommand{BaseVersion: in.BaseVersion, Add: in.Add, Erase: in.Erase})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	body := wallWithDrawingsJSON(result.Wall, project)
	body["applied"] = result.Applied
	writeJSON(w, http.StatusOK, body)
}
