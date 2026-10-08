package httpapi

import (
	"errors"
	"net/http"
	"strings"

	wallapp "scrumboy/internal/application/wall"
	"scrumboy/internal/store"
)

// handleBoardWallRoutes serves the Scrumbaby sticky-note wall.
//
// Scope: durable projects only. Any request for a wall route on an anonymous
// or temporary board (expires_at IS NOT NULL) returns 404, as does any wall
// request when the feature flag is off. The feature does not exist on
// non-durable boards, not merely read-only.
//
// Durable writes take a note-scoped shape (POST /notes, PATCH /notes/{id},
// DELETE /notes/{id}) backed by a server-side read/modify/write on a single
// JSON row per project; the note is the conflict unit. PUT /wall is retained
// for maintenance/recovery only. POST /wall/transient publishes ephemeral
// drag/move events to the realtime path and is never persisted.
func (s *Server) handleBoardWallRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if len(rest) < 2 || rest[1] != "wall" {
		return false
	}
	if !s.wallEnabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return true
	}
	project := pc.Project
	if project.ExpiresAt != nil {
		// Non-durable boards (anonymous + temp) do not expose the wall.
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return true
	}

	switch {
	case len(rest) == 2 && r.Method == http.MethodGet:
		s.handleWallGet(w, r, project)
		return true

	case len(rest) == 2 && r.Method == http.MethodPut:
		s.handleWallPut(w, r, project)
		return true

	case len(rest) == 3 && rest[2] == "transient" && r.Method == http.MethodPost:
		s.handleWallTransient(w, r, project.ID)
		return true

	case len(rest) == 3 && rest[2] == "notes" && r.Method == http.MethodPost:
		s.handleWallCreateNote(w, r, project.ID)
		return true

	case len(rest) == 3 && rest[2] == "stories" && r.Method == http.MethodPost:
		s.handleWallPinStory(w, r, project.ID)
		return true

	case len(rest) == 4 && rest[2] == "stories" && r.Method == http.MethodPatch:
		s.handleWallPatchStory(w, r, project.ID, rest[3])
		return true

	case len(rest) == 4 && rest[2] == "stories" && r.Method == http.MethodDelete:
		s.handleWallUnpinStory(w, r, project.ID, rest[3])
		return true

	case len(rest) == 4 && rest[2] == "notes" && r.Method == http.MethodPatch:
		s.handleWallPatchNote(w, r, project.ID, rest[3])
		return true

	case len(rest) == 4 && rest[2] == "notes" && r.Method == http.MethodDelete:
		s.handleWallDeleteNote(w, r, project.ID, rest[3])
		return true

	case len(rest) == 3 && rest[2] == "edges" && r.Method == http.MethodPost:
		s.handleWallCreateEdge(w, r, project.ID)
		return true

	case len(rest) == 4 && rest[2] == "edges" && r.Method == http.MethodDelete:
		s.handleWallDeleteEdge(w, r, project.ID, rest[3])
		return true
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	return true
}

func writeWallMutationPreparationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wallapp.ErrActorRequired):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, wallapp.ErrContributorRequired):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "contributor or higher required", nil)
	default:
		writeInternal(w, err)
	}
}

func (s *Server) handleWallGet(w http.ResponseWriter, r *http.Request, project store.Project) {
	wall, err := s.store.GetWall(s.requestContext(r), project.ID)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, wallToJSON(wall, project))
}

type wallNoteInputJSON struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Color  string  `json:"color"`
	Text   string  `json:"text"`
}

func (s *Server) handleWallCreateNote(w http.ResponseWriter, r *http.Request, projectID int64) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallNoteMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallNoteInputJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	note, err := prepared.Create(wallapp.CreateNoteCommand{
		X: in.X, Y: in.Y, Width: in.Width, Height: in.Height,
		Color: in.Color, Text: in.Text,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusCreated, wallNoteToJSON(note))
}

type wallNotePatchJSON struct {
	IfVersion int64    `json:"ifVersion"`
	X         *float64 `json:"x"`
	Y         *float64 `json:"y"`
	Width     *float64 `json:"width"`
	Height    *float64 `json:"height"`
	Color     *string  `json:"color"`
	Text      *string  `json:"text"`
}

func (s *Server) handleWallPatchNote(w http.ResponseWriter, r *http.Request, projectID int64, noteID string) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallNoteMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		writeValidationError(w, "noteId required", "note_id_required", map[string]any{"field": "noteId"})
		return
	}
	var in wallNotePatchJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	note, err := prepared.Patch(wallapp.PatchNoteCommand{
		NoteID:    noteID,
		IfVersion: in.IfVersion,
		X:         in.X, Y: in.Y, Width: in.Width, Height: in.Height,
		Color: in.Color, Text: in.Text,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, wallNoteToJSON(note))
}

func (s *Server) handleWallDeleteNote(w http.ResponseWriter, r *http.Request, projectID int64, noteID string) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallNoteMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	noteID = strings.TrimSpace(noteID)
	if noteID == "" {
		writeValidationError(w, "noteId required", "note_id_required", map[string]any{"field": "noteId"})
		return
	}
	if err := prepared.Delete(wallapp.DeleteNoteCommand{NoteID: noteID}); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type wallReplaceJSON struct {
	Notes []wallNoteInputJSON `json:"notes"`
}

func (s *Server) handleWallPut(w http.ResponseWriter, r *http.Request, project store.Project) {
	projectID := project.ID
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallReplacements.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallReplaceJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	notes := make([]wallapp.NoteDraft, 0, len(in.Notes))
	for _, n := range in.Notes {
		notes = append(notes, wallapp.NoteDraft{
			X: n.X, Y: n.Y, Width: n.Width, Height: n.Height,
			Color: n.Color, Text: n.Text,
		})
	}
	wall, err := prepared.Replace(wallapp.ReplaceWallCommand{Notes: notes})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, wallToJSON(wall, project))
}

type wallTransientInputJSON struct {
	NoteID       string  `json:"noteId"`
	StoryLocalID *int64  `json:"storyLocalId"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
}

// handleWallTransient publishes an ephemeral drag/move event. The payload is
// never persisted; it flows through common event fanout. Throttling is the
// caller's responsibility (~100ms coalesce).
//
// Transient payload shape is exactly one target plus coordinates:
// {noteId, x, y, by} or {storyLocalId, x, y, by}. The `by` field is the
// authenticated user id of the caller and exists solely so the originating
// client can suppress its own echoes when applying transients.
func (s *Server) handleWallTransient(w http.ResponseWriter, r *http.Request, projectID int64) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallTransientMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallTransientInputJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	hasNote := strings.TrimSpace(in.NoteID) != ""
	if !hasNote && in.StoryLocalID == nil {
		writeValidationError(w, "noteId required", "note_id_required", map[string]any{"field": "noteId"})
		return
	}
	if hasNote == (in.StoryLocalID != nil) || (in.StoryLocalID != nil && *in.StoryLocalID <= 0) {
		writeValidationError(w, "exactly one wall transient target required", "wall_transient_target_required", nil)
		return
	}
	if err := prepared.Publish(wallapp.TransientCommand{
		NoteID:       in.NoteID,
		StoryLocalID: in.StoryLocalID,
		X:            in.X,
		Y:            in.Y,
	}); err != nil {
		writeInternal(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type wallStoryPinJSON struct {
	LocalID int64   `json:"localId"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
}

type wallStoryPatchJSON struct {
	IfVersion int64   `json:"ifVersion"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
}

func (s *Server) handleWallPinStory(w http.ResponseWriter, r *http.Request, projectID int64) {
	prepared, err := s.wallStoryMutations.Prepare(
		s.requestContext(r), r.Context(), wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallStoryPinJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if in.LocalID <= 0 {
		writeValidationError(w, "valid localId required", "invalid_todo_id", map[string]any{"field": "localId"})
		return
	}
	placement, created, err := prepared.Pin(wallapp.PinStoryCommand{LocalID: in.LocalID, X: in.X, Y: in.Y})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, wallStoryPlacementToJSON(placement))
}

func (s *Server) handleWallPatchStory(w http.ResponseWriter, r *http.Request, projectID int64, rawLocalID string) {
	prepared, err := s.wallStoryMutations.Prepare(
		s.requestContext(r), r.Context(), wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	localID, ok := parseInt64(rawLocalID)
	if !ok || localID <= 0 {
		writeValidationError(w, "invalid todo id", "invalid_todo_id", map[string]any{"field": "localId"})
		return
	}
	var in wallStoryPatchJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	placement, err := prepared.Patch(wallapp.PatchStoryCommand{
		LocalID: localID, IfVersion: in.IfVersion, X: in.X, Y: in.Y,
	})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, wallStoryPlacementToJSON(placement))
}

func (s *Server) handleWallUnpinStory(w http.ResponseWriter, r *http.Request, projectID int64, rawLocalID string) {
	prepared, err := s.wallStoryMutations.Prepare(
		s.requestContext(r), r.Context(), wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	localID, ok := parseInt64(rawLocalID)
	if !ok || localID <= 0 {
		writeValidationError(w, "invalid todo id", "invalid_todo_id", map[string]any{"field": "localId"})
		return
	}
	if err := prepared.Unpin(wallapp.UnpinStoryCommand{LocalID: localID}); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type wallEdgeInputJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// handleWallCreateEdge creates an undirected edge between two wall endpoints
// (raw note IDs or canonical story endpoints). CreateEdge is
// store-idempotent. HTTP
// compatibility still returns 201 and refreshes after every nil store result,
// including duplicate no-ops.
func (s *Server) handleWallCreateEdge(w http.ResponseWriter, r *http.Request, projectID int64) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallEdgeMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	var in wallEdgeInputJSON
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	from := strings.TrimSpace(in.From)
	to := strings.TrimSpace(in.To)
	if from == "" || to == "" {
		writeValidationError(w, "from and to required", "wall_edge_endpoints_required", nil)
		return
	}
	edge, err := prepared.Create(wallapp.CreateEdgeCommand{From: from, To: to})
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	writeJSON(w, http.StatusCreated, wallEdgeToJSON(edge))
}

func (s *Server) handleWallDeleteEdge(w http.ResponseWriter, r *http.Request, projectID int64, edgeID string) {
	mutationCtx := s.requestContext(r)
	effectCtx := r.Context()
	prepared, err := s.wallEdgeMutations.Prepare(
		mutationCtx,
		effectCtx,
		wallapp.ResolvedRESTTarget{ProjectID: projectID},
	)
	if err != nil {
		writeWallMutationPreparationError(w, err)
		return
	}
	edgeID = strings.TrimSpace(edgeID)
	if edgeID == "" {
		writeValidationError(w, "edgeId required", "edge_id_required", map[string]any{"field": "edgeId"})
		return
	}
	if err := prepared.Delete(wallapp.DeleteEdgeCommand{EdgeID: edgeID}); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func wallEdgeToJSON(e store.WallEdge) map[string]any {
	return map[string]any{
		"id":   e.ID,
		"from": e.From,
		"to":   e.To,
	}
}

func wallNoteToJSON(n store.WallNote) map[string]any {
	return map[string]any{
		"id":      n.ID,
		"x":       n.X,
		"y":       n.Y,
		"width":   n.Width,
		"height":  n.Height,
		"color":   n.Color,
		"text":    n.Text,
		"version": n.Version,
	}
}

func wallStoryPlacementToJSON(p store.WallStoryPlacement) map[string]any {
	return map[string]any{"localId": p.TodoLocalID, "x": p.X, "y": p.Y, "version": p.Version}
}

func wallToJSON(wall store.Wall, project store.Project) map[string]any {
	notes := make([]map[string]any, 0, len(wall.Notes))
	for _, n := range wall.Notes {
		notes = append(notes, wallNoteToJSON(n))
	}
	edges := make([]map[string]any, 0, len(wall.Edges))
	for _, e := range wall.Edges {
		edges = append(edges, wallEdgeToJSON(e))
	}
	stories := make([]map[string]any, 0, len(wall.Stories))
	for _, story := range wall.Stories {
		item := wallStoryPlacementToJSON(story)
		item["todo"] = todoToJSONForProject(story.Todo, project)
		stories = append(stories, item)
	}
	return map[string]any{
		"notes":     notes,
		"edges":     edges,
		"stories":   stories,
		"version":   wall.Version,
		"updatedAt": wall.UpdatedAt,
	}
}
