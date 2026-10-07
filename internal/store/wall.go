package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

// WallEdge is a simple connection between two wall endpoints: raw note IDs or
// canonical story endpoints ("story:<todo local ID>"). Edges are intentionally
// undirected and have no per-edge version; they are write-once / delete-once.
// The document-level `version` on Wall is the only realtime fingerprint.
type WallEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// WallStoryPlacement is the Wall-owned spatial placement for one canonical
// Todo. Todo content is hydrated at read time and is never copied into this
// relation. TodoLocalID is the portable, project-scoped identity exposed at
// API and backup boundaries; TodoID remains an internal database key.
type WallStoryPlacement struct {
	TodoID      int64
	TodoLocalID int64
	X           float64
	Y           float64
	Version     int64
	Todo        Todo
}

// Wall is the shape returned by GetWall. Version is a coarse document-level
// counter used as a change fingerprint for realtime clients; per-note versions
// are the authoritative conflict unit.
type Wall struct {
	Notes     []WallNote           `json:"notes"`
	Edges     []WallEdge           `json:"edges"`
	Stories   []WallStoryPlacement `json:"stories"`
	Version   int64                `json:"version"`
	UpdatedAt int64                `json:"updatedAt"`
}

const (
	maxWallNotes      = 500
	maxWallStories    = 500
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
			return Wall{Notes: []WallNote{}, Edges: []WallEdge{}, Stories: []WallStoryPlacement{}, Version: 0, UpdatedAt: 0}, nil
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
	stories, err := s.listWallStoryPlacements(ctx, projectID)
	if err != nil {
		return Wall{}, err
	}
	return Wall{Notes: notes, Edges: edges, Stories: stories, Version: version, UpdatedAt: updatedAt}, nil
}

func (s *Store) listWallStoryPlacements(ctx context.Context, projectID int64) ([]WallStoryPlacement, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT wsp.todo_id, t.local_id, wsp.x, wsp.y, wsp.version,
       t.title, t.body, t.column_key, t.rank, t.estimation_points,
       t.assignee_user_id, t.created_by_user_id, t.sprint_id, t.priority_key,
       t.created_at, t.updated_at, t.done_at, t.archived_at,
       COALESCE(GROUP_CONCAT(g.name, ','), '')
FROM wall_story_placements wsp
JOIN todos t ON t.id = wsp.todo_id AND t.project_id = wsp.project_id
LEFT JOIN todo_tags tt ON tt.todo_id = t.id
LEFT JOIN tags g ON g.id = tt.tag_id
WHERE wsp.project_id = ?
GROUP BY wsp.todo_id, t.id
ORDER BY wsp.todo_id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list wall story placements: %w", err)
	}
	defer rows.Close()

	out := make([]WallStoryPlacement, 0)
	for rows.Next() {
		var p WallStoryPlacement
		var estimation, assignee, creator, sprint, doneAt, archivedAt sql.NullInt64
		var priority sql.NullString
		var createdAt, updatedAt int64
		var tagsCSV string
		p.Todo.ProjectID = projectID
		if err := rows.Scan(
			&p.TodoID, &p.TodoLocalID, &p.X, &p.Y, &p.Version,
			&p.Todo.Title, &p.Todo.Body, &p.Todo.ColumnKey, &p.Todo.Rank, &estimation,
			&assignee, &creator, &sprint, &priority, &createdAt, &updatedAt,
			&doneAt, &archivedAt, &tagsCSV,
		); err != nil {
			return nil, fmt.Errorf("scan wall story placement: %w", err)
		}
		p.Todo.ID = p.TodoID
		p.Todo.LocalID = p.TodoLocalID
		if estimation.Valid {
			v := estimation.Int64
			p.Todo.EstimationPoints = &v
		}
		if assignee.Valid {
			v := assignee.Int64
			p.Todo.AssigneeUserID = &v
		}
		if creator.Valid {
			v := creator.Int64
			p.Todo.CreatedByUserID = &v
		}
		if sprint.Valid {
			v := sprint.Int64
			p.Todo.SprintID = &v
		}
		if priority.Valid {
			v := priority.String
			p.Todo.PriorityKey = &v
		}
		p.Todo.CreatedAt = time.UnixMilli(createdAt).UTC()
		p.Todo.UpdatedAt = time.UnixMilli(updatedAt).UTC()
		if doneAt.Valid {
			v := time.UnixMilli(doneAt.Int64).UTC()
			p.Todo.DoneAt = &v
		}
		if archivedAt.Valid {
			v := time.UnixMilli(archivedAt.Int64).UTC()
			p.Todo.ArchivedAt = &v
		}
		if tagsCSV != "" {
			seen := make(map[string]struct{})
			for _, tag := range strings.Split(tagsCSV, ",") {
				seen[tag] = struct{}{}
			}
			p.Todo.Tags = make([]string, 0, len(seen))
			for tag := range seen {
				p.Todo.Tags = append(p.Todo.Tags, tag)
			}
			sort.Strings(p.Todo.Tags)
		} else {
			p.Todo.Tags = []string{}
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows wall story placements: %w", err)
	}
	return out, nil
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

// PinWallStory idempotently places the canonical project Todo identified by
// localID on the Wall. The bool reports whether a new placement was created.
func (s *Store) PinWallStory(ctx context.Context, projectID, localID int64, x, y float64) (WallStoryPlacement, bool, error) {
	if localID <= 0 {
		return WallStoryPlacement{}, false, fmt.Errorf("%w: invalid todo local id", ErrValidation)
	}
	mu := lockWall(projectID)
	defer mu.Unlock()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return WallStoryPlacement{}, false, fmt.Errorf("begin pin wall story: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var todoID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM todos WHERE project_id = ? AND local_id = ?`,
		projectID, localID,
	).Scan(&todoID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WallStoryPlacement{}, false, ErrNotFound
		}
		return WallStoryPlacement{}, false, fmt.Errorf("resolve wall story todo: %w", err)
	}

	var existing WallStoryPlacement
	err = tx.QueryRowContext(ctx,
		`SELECT x, y, version FROM wall_story_placements WHERE project_id = ? AND todo_id = ?`,
		projectID, todoID,
	).Scan(&existing.X, &existing.Y, &existing.Version)
	if err == nil {
		existing.TodoID = todoID
		existing.TodoLocalID = localID
		if err := tx.Commit(); err != nil {
			return WallStoryPlacement{}, false, fmt.Errorf("commit existing wall story: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WallStoryPlacement{}, false, fmt.Errorf("get existing wall story: %w", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wall_story_placements WHERE project_id = ?`, projectID,
	).Scan(&count); err != nil {
		return WallStoryPlacement{}, false, fmt.Errorf("count wall stories: %w", err)
	}
	if count >= maxWallStories {
		return WallStoryPlacement{}, false, fmt.Errorf("%w: wall story limit reached", ErrValidation)
	}

	p := WallStoryPlacement{
		TodoID: todoID, TodoLocalID: localID,
		X: clampNoteCoord(x), Y: clampNoteCoord(y), Version: 1,
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO wall_story_placements(project_id, todo_id, x, y, version)
VALUES (?, ?, ?, ?, 1)`, projectID, todoID, p.X, p.Y); err != nil {
		return WallStoryPlacement{}, false, fmt.Errorf("insert wall story placement: %w", err)
	}
	if err := bumpWallVersionTx(ctx, tx, projectID); err != nil {
		return WallStoryPlacement{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return WallStoryPlacement{}, false, fmt.Errorf("commit pin wall story: %w", err)
	}
	return p, true, nil
}

// PatchWallStory moves one Wall placement without changing Todo workflow data.
func (s *Store) PatchWallStory(ctx context.Context, projectID, localID int64, ifVersion int64, x, y float64) (WallStoryPlacement, error) {
	mu := lockWall(projectID)
	defer mu.Unlock()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return WallStoryPlacement{}, fmt.Errorf("begin patch wall story: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var p WallStoryPlacement
	err = tx.QueryRowContext(ctx, `
SELECT wsp.todo_id, t.local_id, wsp.x, wsp.y, wsp.version
FROM wall_story_placements wsp
JOIN todos t ON t.id = wsp.todo_id AND t.project_id = wsp.project_id
WHERE wsp.project_id = ? AND t.local_id = ?`, projectID, localID).
		Scan(&p.TodoID, &p.TodoLocalID, &p.X, &p.Y, &p.Version)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WallStoryPlacement{}, ErrNotFound
		}
		return WallStoryPlacement{}, fmt.Errorf("get wall story placement: %w", err)
	}
	if ifVersion != 0 && p.Version != ifVersion {
		return WallStoryPlacement{}, fmt.Errorf("%w: story placement version mismatch", ErrConflict)
	}
	p.X = clampNoteCoord(x)
	p.Y = clampNoteCoord(y)
	p.Version++
	if _, err := tx.ExecContext(ctx, `
UPDATE wall_story_placements SET x = ?, y = ?, version = ?
WHERE project_id = ? AND todo_id = ?`, p.X, p.Y, p.Version, projectID, p.TodoID); err != nil {
		return WallStoryPlacement{}, fmt.Errorf("update wall story placement: %w", err)
	}
	if err := bumpWallVersionTx(ctx, tx, projectID); err != nil {
		return WallStoryPlacement{}, err
	}
	if err := tx.Commit(); err != nil {
		return WallStoryPlacement{}, fmt.Errorf("commit patch wall story: %w", err)
	}
	return p, nil
}

// UnpinWallStory removes only the Wall placement; the canonical Todo remains.
// Incident story edges are removed in the same transaction, and the Wall
// version bumps exactly once.
func (s *Store) UnpinWallStory(ctx context.Context, projectID, localID int64) error {
	mu := lockWall(projectID)
	defer mu.Unlock()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin unpin wall story: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
DELETE FROM wall_story_placements
WHERE project_id = ? AND todo_id = (
  SELECT id FROM todos WHERE project_id = ? AND local_id = ?
)`, projectID, projectID, localID)
	if err != nil {
		return fmt.Errorf("delete wall story placement: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("wall story delete rows affected: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	if _, err := removeWallStoryEdgesTx(ctx, tx, projectID, FormatWallStoryEndpoint(localID)); err != nil {
		return err
	}
	if err := bumpWallVersionTx(ctx, tx, projectID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unpin wall story: %w", err)
	}
	return nil
}

func bumpWallVersionTx(ctx context.Context, tx *sql.Tx, projectID int64) error {
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO project_walls(project_id, notes, edges, version, updated_at)
VALUES (?, '[]', '[]', 1, ?)
ON CONFLICT(project_id) DO UPDATE SET
  version = project_walls.version + 1,
  updated_at = excluded.updated_at`, projectID, nowMs); err != nil {
		return fmt.Errorf("bump wall version: %w", err)
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

// CreateEdge appends an undirected edge between two wall endpoints: raw note
// IDs or canonical story endpoints ("story:<todo local ID>"). Rejects
// self-loops and duplicates in either direction. Returns ErrNotFound if
// either endpoint does not exist on the wall, ErrValidation on bad input, and
// a no-op (returning the existing edge) if a duplicate already exists.
func (s *Store) CreateEdge(ctx context.Context, projectID int64, fromEndpoint, toEndpoint string) (WallEdge, Wall, error) {
	fromEndpoint = strings.TrimSpace(fromEndpoint)
	toEndpoint = strings.TrimSpace(toEndpoint)
	if fromEndpoint == "" || toEndpoint == "" {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: from and to required", ErrValidation)
	}
	from, err := ParseWallEdgeEndpoint(fromEndpoint)
	if err != nil {
		return WallEdge{}, Wall{}, err
	}
	to, err := ParseWallEdgeEndpoint(toEndpoint)
	if err != nil {
		return WallEdge{}, Wall{}, err
	}
	if from.Canonical() == to.Canonical() {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: self-edges not allowed", ErrValidation)
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return WallEdge{}, Wall{}, err
	}
	if !resolveWallEdgeEndpoint(wall, from) || !resolveWallEdgeEndpoint(wall, to) {
		return WallEdge{}, Wall{}, ErrNotFound
	}
	for _, e := range wall.Edges {
		if (e.From == from.Canonical() && e.To == to.Canonical()) || (e.From == to.Canonical() && e.To == from.Canonical()) {
			// Idempotent: return the existing edge unchanged, no version bump.
			return e, wall, nil
		}
	}
	if len(wall.Edges) >= maxWallEdges {
		return WallEdge{}, Wall{}, fmt.Errorf("%w: wall edge limit reached", ErrValidation)
	}
	edge := WallEdge{ID: newEdgeID(), From: from.Canonical(), To: to.Canonical()}
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
func upsertWallForImportTx(ctx context.Context, tx *sql.Tx, projectID int64, payload *WallExport, warnings *[]string) error {
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

	// Current-format Wall payloads replace placements, including an explicit
	// empty array. Legacy payloads omitted stories entirely; preserving the
	// target placements matters for merge, while replace/copy targets are new
	// projects and therefore already have an empty placement set. Accepted
	// story local IDs are resolved before edge filtering so story endpoints
	// validate against the resulting placement set either way.
	storiesPresent := payload.StoriesPresent || payload.Stories != nil
	storyLocalIDs := make(map[int64]struct{})
	if storiesPresent {
		if _, err := tx.ExecContext(ctx, `DELETE FROM wall_story_placements WHERE project_id = ?`, projectID); err != nil {
			return fmt.Errorf("clear wall story placements for import: %w", err)
		}
		seenStories := make(map[int64]struct{}, len(payload.Stories))
		for _, story := range payload.Stories {
			if story.LocalID <= 0 {
				if warnings != nil {
					*warnings = append(*warnings, "Dropped Wall story placement with invalid localId")
				}
				continue
			}
			if _, duplicate := seenStories[story.LocalID]; duplicate {
				if warnings != nil {
					*warnings = append(*warnings, fmt.Sprintf("Dropped duplicate Wall placement for story #%d", story.LocalID))
				}
				continue
			}
			seenStories[story.LocalID] = struct{}{}
			var todoID int64
			if err := tx.QueryRowContext(ctx,
				`SELECT id FROM todos WHERE project_id = ? AND local_id = ?`,
				projectID, story.LocalID,
			).Scan(&todoID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					if warnings != nil {
						*warnings = append(*warnings, fmt.Sprintf("Dropped dangling Wall placement for story #%d", story.LocalID))
					}
					continue
				}
				return fmt.Errorf("resolve imported wall story #%d: %w", story.LocalID, err)
			}
			placementVersion := story.Version
			if placementVersion <= 0 {
				placementVersion = 1
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO wall_story_placements(project_id, todo_id, x, y, version)
VALUES (?, ?, ?, ?, ?)`, projectID, todoID, clampNoteCoord(story.X), clampNoteCoord(story.Y), placementVersion); err != nil {
				return fmt.Errorf("insert imported wall story #%d: %w", story.LocalID, err)
			}
			storyLocalIDs[story.LocalID] = struct{}{}
		}
	} else {
		rows, err := tx.QueryContext(ctx, `
SELECT t.local_id FROM wall_story_placements AS p
JOIN todos AS t ON t.id = p.todo_id
WHERE p.project_id = ?`, projectID)
		if err != nil {
			return fmt.Errorf("load preserved wall story placements for import: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var localID int64
			if err := rows.Scan(&localID); err != nil {
				rows.Close()
				return fmt.Errorf("decode preserved wall story placement for import: %w", err)
			}
			storyLocalIDs[localID] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read preserved wall story placements for import: %w", err)
		}
	}

	edges := payload.Edges
	if len(edges) > maxWallEdges {
		edges = edges[:maxWallEdges]
	}
	keptEdges := make([]WallEdge, 0, len(edges))
	seenEdges := make(map[string]struct{}, len(edges))
	resolves := func(endpoint WallEdgeEndpoint) bool {
		if endpoint.Kind() == WallEdgeEndpointStory {
			_, ok := storyLocalIDs[endpoint.TodoLocalID()]
			return ok
		}
		_, ok := seenIDs[endpoint.NoteID()]
		return ok
	}
	for _, e := range edges {
		from, err := ParseWallEdgeEndpoint(e.From)
		if err != nil {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("Dropped Wall edge with invalid endpoint %q", strings.TrimSpace(e.From)))
			}
			continue
		}
		to, err := ParseWallEdgeEndpoint(e.To)
		if err != nil {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("Dropped Wall edge with invalid endpoint %q", strings.TrimSpace(e.To)))
			}
			continue
		}
		if from.Canonical() == to.Canonical() {
			continue
		}
		if !resolves(from) {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("Dropped Wall edge with unknown endpoint %q", from.Canonical()))
			}
			continue
		}
		if !resolves(to) {
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("Dropped Wall edge with unknown endpoint %q", to.Canonical()))
			}
			continue
		}
		// Normalize to an undirected key so the same pair imported in either
		// direction is deduplicated; edge direction is not significant.
		a, b := from.Canonical(), to.Canonical()
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
		keptEdges = append(keptEdges, WallEdge{ID: id, From: from.Canonical(), To: to.Canonical()})
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
