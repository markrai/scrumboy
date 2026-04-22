package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// WallNote is one sticky note inside the wall document. Each note has an
// independent monotonic `Version` for optimistic concurrency on note-scoped
// updates; the note, not the wall, is the conflict unit.
type WallNote struct {
	ID      string  `json:"id"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Color   string  `json:"color"`
	Text    string  `json:"text"`
	Version int64   `json:"version"`
}

// WallEdge is a simple connection between two notes (Postbaby-parity
// Shift+drag edges). Edges are intentionally undirected and have no
// per-edge version; they are write-once / delete-once. The document-level
// `version` on Wall is the only realtime fingerprint.
type WallEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Wall is the shape returned by GetWall. Version is a coarse document-level
// counter used as a change fingerprint for realtime clients; per-note versions
// are the authoritative conflict unit.
type Wall struct {
	Notes     []WallNote `json:"notes"`
	Edges     []WallEdge `json:"edges"`
	Version   int64      `json:"version"`
	UpdatedAt int64      `json:"updatedAt"`
}

const (
	maxWallNotes      = 500
	maxWallEdges      = 2000
	maxWallTextBytes  = 4000
	defaultNoteWidth  = 180
	defaultNoteHeight = 140
	minNoteDimension  = 60
	maxNoteDimension  = 800
	maxNoteCoordinate = 100000
)

// Per-project mutex guards read/modify/write on the single JSON row. In-process
// only; single-instance Scrumboy does not need cross-process locking. A
// horizontal-scale deployment would need DB-level locking instead.
var (
	wallMuMapLock sync.Mutex
	wallMus       = map[int64]*sync.Mutex{}
)

func lockWall(projectID int64) *sync.Mutex {
	wallMuMapLock.Lock()
	m, ok := wallMus[projectID]
	if !ok {
		m = &sync.Mutex{}
		wallMus[projectID] = m
	}
	wallMuMapLock.Unlock()
	m.Lock()
	return m
}

func validateWallColor(c string) error {
	if !colorHexRe.MatchString(strings.TrimSpace(c)) {
		return fmt.Errorf("%w: invalid color", ErrValidation)
	}
	return nil
}

func clampNoteDim(v, fallback float64) float64 {
	if v == 0 {
		return fallback
	}
	if v < minNoteDimension {
		return minNoteDimension
	}
	if v > maxNoteDimension {
		return maxNoteDimension
	}
	return v
}

func clampNoteCoord(v float64) float64 {
	if v < -maxNoteCoordinate {
		return -maxNoteCoordinate
	}
	if v > maxNoteCoordinate {
		return maxNoteCoordinate
	}
	return v
}

func newNoteID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "n_" + hex.EncodeToString(b[:])
}

func newEdgeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "e_" + hex.EncodeToString(b[:])
}

// GetWall reads the wall document for a project. Side-effect free: no row is
// created when none exists; a synthetic empty wall is returned instead. The
// row is materialized only on the first durable write.
func (s *Store) GetWall(ctx context.Context, projectID int64) (Wall, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT notes, edges, version, updated_at FROM project_walls WHERE project_id = ?`,
		projectID)
	var notesJSON, edgesJSON string
	var version, updatedAt int64
	if err := row.Scan(&notesJSON, &edgesJSON, &version, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Wall{Notes: []WallNote{}, Edges: []WallEdge{}, Version: 0, UpdatedAt: 0}, nil
		}
		return Wall{}, fmt.Errorf("get wall: %w", err)
	}
	var notes []WallNote
	if notesJSON != "" {
		if err := json.Unmarshal([]byte(notesJSON), &notes); err != nil {
			return Wall{}, fmt.Errorf("decode wall notes: %w", err)
		}
	}
	if notes == nil {
		notes = []WallNote{}
	}
	var edges []WallEdge
	if edgesJSON != "" {
		if err := json.Unmarshal([]byte(edgesJSON), &edges); err != nil {
			return Wall{}, fmt.Errorf("decode wall edges: %w", err)
		}
	}
	if edges == nil {
		edges = []WallEdge{}
	}
	return Wall{Notes: notes, Edges: edges, Version: version, UpdatedAt: updatedAt}, nil
}

func (s *Store) writeWallLocked(ctx context.Context, projectID int64, wall Wall) error {
	notesRaw, err := json.Marshal(wall.Notes)
	if err != nil {
		return fmt.Errorf("encode wall notes: %w", err)
	}
	if wall.Edges == nil {
		wall.Edges = []WallEdge{}
	}
	edgesRaw, err := json.Marshal(wall.Edges)
	if err != nil {
		return fmt.Errorf("encode wall edges: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO project_walls (project_id, notes, edges, version, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET notes = excluded.notes, edges = excluded.edges, version = excluded.version, updated_at = excluded.updated_at
`, projectID, string(notesRaw), string(edgesRaw), wall.Version, nowMs)
	if err != nil {
		return fmt.Errorf("write wall: %w", err)
	}
	return nil
}

// CreateNoteInput describes a new sticky note.
type CreateNoteInput struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
	Color  string
	Text   string
}

// CreateNote appends a new note to the wall, materializing the row on first
// write. Returns the new note and the updated wall document.
func (s *Store) CreateNote(ctx context.Context, projectID int64, in CreateNoteInput) (WallNote, Wall, error) {
	if err := validateWallColor(in.Color); err != nil {
		return WallNote{}, Wall{}, err
	}
	if len(in.Text) > maxWallTextBytes {
		return WallNote{}, Wall{}, fmt.Errorf("%w: note text too long", ErrValidation)
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return WallNote{}, Wall{}, err
	}
	if len(wall.Notes) >= maxWallNotes {
		return WallNote{}, Wall{}, fmt.Errorf("%w: wall note limit reached", ErrValidation)
	}

	note := WallNote{
		ID:      newNoteID(),
		X:       clampNoteCoord(in.X),
		Y:       clampNoteCoord(in.Y),
		Width:   clampNoteDim(in.Width, defaultNoteWidth),
		Height:  clampNoteDim(in.Height, defaultNoteHeight),
		Color:   strings.TrimSpace(in.Color),
		Text:    in.Text,
		Version: 1,
	}
	wall.Notes = append(wall.Notes, note)
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return WallNote{}, Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return note, wall, nil
}

// PatchNoteInput describes an optimistic-concurrency note update. All
// per-field pointers are optional; nil means "leave unchanged". IfVersion is
// the per-note version the client observed; any nonzero mismatch returns
// ErrConflict.
type PatchNoteInput struct {
	IfVersion int64
	X         *float64
	Y         *float64
	Width     *float64
	Height    *float64
	Color     *string
	Text      *string
}

// PatchNote applies a per-field update to a single note under the per-project
// lock. Returns ErrNotFound if the note (or wall row) is missing and
// ErrConflict on version mismatch.
func (s *Store) PatchNote(ctx context.Context, projectID int64, noteID string, in PatchNoteInput) (WallNote, Wall, error) {
	if in.Color != nil {
		if err := validateWallColor(*in.Color); err != nil {
			return WallNote{}, Wall{}, err
		}
	}
	if in.Text != nil && len(*in.Text) > maxWallTextBytes {
		return WallNote{}, Wall{}, fmt.Errorf("%w: note text too long", ErrValidation)
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return WallNote{}, Wall{}, err
	}
	idx := -1
	for i, n := range wall.Notes {
		if n.ID == noteID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return WallNote{}, Wall{}, ErrNotFound
	}
	note := wall.Notes[idx]
	if in.IfVersion != 0 && note.Version != in.IfVersion {
		return WallNote{}, Wall{}, fmt.Errorf("%w: note version mismatch", ErrConflict)
	}
	if in.X != nil {
		note.X = clampNoteCoord(*in.X)
	}
	if in.Y != nil {
		note.Y = clampNoteCoord(*in.Y)
	}
	if in.Width != nil {
		note.Width = clampNoteDim(*in.Width, defaultNoteWidth)
	}
	if in.Height != nil {
		note.Height = clampNoteDim(*in.Height, defaultNoteHeight)
	}
	if in.Color != nil {
		note.Color = strings.TrimSpace(*in.Color)
	}
	if in.Text != nil {
		note.Text = *in.Text
	}
	note.Version++
	wall.Notes[idx] = note
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return WallNote{}, Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return note, wall, nil
}

// DeleteNote removes a single note from the wall and any edges that
// referenced it. Returns ErrNotFound if the note (or wall row) is missing.
func (s *Store) DeleteNote(ctx context.Context, projectID int64, noteID string) (Wall, error) {
	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return Wall{}, err
	}
	idx := -1
	for i, n := range wall.Notes {
		if n.ID == noteID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Wall{}, ErrNotFound
	}
	wall.Notes = append(wall.Notes[:idx], wall.Notes[idx+1:]...)
	// Drop any edges that referenced the removed note - dangling edges would
	// be invisible to the client and waste storage.
	if len(wall.Edges) > 0 {
		kept := wall.Edges[:0]
		for _, e := range wall.Edges {
			if e.From == noteID || e.To == noteID {
				continue
			}
			kept = append(kept, e)
		}
		wall.Edges = kept
	}
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return wall, nil
}

// CreateEdge appends an undirected edge between two notes. Rejects self-loops
// and duplicates in either direction. Returns ErrNotFound if either endpoint
// note does not exist on the wall, ErrValidation on bad input, and a no-op
// (returning the existing edge) if a duplicate already exists.
func (s *Store) CreateEdge(ctx context.Context, projectID int64, fromNoteID, toNoteID string) (WallEdge, Wall, error) {
	fromNoteID = strings.TrimSpace(fromNoteID)
	toNoteID = strings.TrimSpace(toNoteID)
	if fromNoteID == "" || toNoteID == "" {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: from and to required", ErrValidation)
	}
	if fromNoteID == toNoteID {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: self-edges not allowed", ErrValidation)
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return WallEdge{}, Wall{}, err
	}
	haveFrom, haveTo := false, false
	for _, n := range wall.Notes {
		if n.ID == fromNoteID {
			haveFrom = true
		}
		if n.ID == toNoteID {
			haveTo = true
		}
	}
	if !haveFrom || !haveTo {
		return WallEdge{}, Wall{}, ErrNotFound
	}
	for _, e := range wall.Edges {
		if (e.From == fromNoteID && e.To == toNoteID) || (e.From == toNoteID && e.To == fromNoteID) {
			// Idempotent: return the existing edge unchanged, no version bump.
			return e, wall, nil
		}
	}
	if len(wall.Edges) >= maxWallEdges {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: wall edge limit reached", ErrValidation)
	}
	edge := WallEdge{ID: newEdgeID(), From: fromNoteID, To: toNoteID}
	wall.Edges = append(wall.Edges, edge)
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return WallEdge{}, Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return edge, wall, nil
}

// DeleteEdge removes a single edge from the wall by id. Returns ErrNotFound
// if the edge does not exist.
func (s *Store) DeleteEdge(ctx context.Context, projectID int64, edgeID string) (Wall, error) {
	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return Wall{}, err
	}
	idx := -1
	for i, e := range wall.Edges {
		if e.ID == edgeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Wall{}, ErrNotFound
	}
	wall.Edges = append(wall.Edges[:idx], wall.Edges[idx+1:]...)
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return wall, nil
}

// upsertWallForImportTx writes a wall payload into project_walls inside an
// import transaction. Validation is best-effort: invalid colors are rewritten
// to a safe default and over-long text is truncated, so one bad row in a
// backup does not fail the whole import. Edges referencing unknown notes are
// dropped. Passing a nil payload is a no-op; callers decide whether a missing
// wall field in the backup should wipe or preserve an existing wall row.
func upsertWallForImportTx(ctx context.Context, tx *sql.Tx, projectID int64, payload *WallExport) error {
	if payload == nil {
		return nil
	}
	notes := payload.Notes
	if len(notes) > maxWallNotes {
		notes = notes[:maxWallNotes]
	}
	normalized := make([]WallNote, 0, len(notes))
	seenIDs := make(map[string]struct{}, len(notes))
	for _, n := range notes {
		id := strings.TrimSpace(n.ID)
		if id == "" {
			id = newNoteID()
		}
		if _, dup := seenIDs[id]; dup {
			continue
		}
		seenIDs[id] = struct{}{}
		color := strings.TrimSpace(n.Color)
		if !colorHexRe.MatchString(color) {
			color = "#FFFFFF"
		}
		text := n.Text
		if len(text) > maxWallTextBytes {
			text = text[:maxWallTextBytes]
		}
		version := n.Version
		if version <= 0 {
			version = 1
		}
		normalized = append(normalized, WallNote{
			ID:      id,
			X:       clampNoteCoord(n.X),
			Y:       clampNoteCoord(n.Y),
			Width:   clampNoteDim(n.Width, defaultNoteWidth),
			Height:  clampNoteDim(n.Height, defaultNoteHeight),
			Color:   color,
			Text:    text,
			Version: version,
		})
	}

	edges := payload.Edges
	if len(edges) > maxWallEdges {
		edges = edges[:maxWallEdges]
	}
	keptEdges := make([]WallEdge, 0, len(edges))
	seenEdges := make(map[string]struct{}, len(edges))
	for _, e := range edges {
		from := strings.TrimSpace(e.From)
		to := strings.TrimSpace(e.To)
		if from == "" || to == "" || from == to {
			continue
		}
		if _, ok := seenIDs[from]; !ok {
			continue
		}
		if _, ok := seenIDs[to]; !ok {
			continue
		}
		// Normalize to an undirected key so the same pair imported in either
		// direction is deduplicated; edge direction is not significant.
		a, b := from, to
		if a > b {
			a, b = b, a
		}
		key := a + "|" + b
		if _, dup := seenEdges[key]; dup {
			continue
		}
		seenEdges[key] = struct{}{}
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = newEdgeID()
		}
		keptEdges = append(keptEdges, WallEdge{ID: id, From: from, To: to})
	}

	notesJSON, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode wall notes for import: %w", err)
	}
	edgesJSON, err := json.Marshal(keptEdges)
	if err != nil {
		return fmt.Errorf("encode wall edges for import: %w", err)
	}
	version := payload.Version
	if version <= 0 {
		version = 1
	}
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO project_walls (project_id, notes, edges, version, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id) DO UPDATE SET notes = excluded.notes, edges = excluded.edges, version = excluded.version, updated_at = excluded.updated_at
`, projectID, string(notesJSON), string(edgesJSON), version, nowMs); err != nil {
		return fmt.Errorf("upsert wall for import: %w", err)
	}
	return nil
}

// ReplaceWall overwrites the full notes list. Intended for maintenance/recovery
// only; note-scoped endpoints are the main write path.
func (s *Store) ReplaceWall(ctx context.Context, projectID int64, notes []WallNote) (Wall, error) {
	if len(notes) > maxWallNotes {
		return Wall{}, fmt.Errorf("%w: wall note limit reached", ErrValidation)
	}
	for i := range notes {
		if err := validateWallColor(notes[i].Color); err != nil {
			return Wall{}, err
		}
		if len(notes[i].Text) > maxWallTextBytes {
			return Wall{}, fmt.Errorf("%w: note text too long", ErrValidation)
		}
		notes[i].X = clampNoteCoord(notes[i].X)
		notes[i].Y = clampNoteCoord(notes[i].Y)
		notes[i].Width = clampNoteDim(notes[i].Width, defaultNoteWidth)
		notes[i].Height = clampNoteDim(notes[i].Height, defaultNoteHeight)
		if notes[i].ID == "" {
			notes[i].ID = newNoteID()
		}
		if notes[i].Version == 0 {
			notes[i].Version = 1
		}
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return Wall{}, err
	}
	wall.Notes = notes
	wall.Version++
	if err := s.writeWallLocked(ctx, projectID, wall); err != nil {
		return Wall{}, err
	}
	wall.UpdatedAt = time.Now().UTC().UnixMilli()
	return wall, nil
}
