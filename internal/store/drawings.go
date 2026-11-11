package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"

	"scrumboy/internal/drawrefs"
)

const (
	MaxDrawingStrokes            = 50
	MaxWallDrawingStrokes        = 500
	MaxDrawingStrokePoints       = 512
	MaxDrawingBytes              = 256 * 1024
	MaxSubmittedDrawingBytes     = 768 * 1024
	MaxStoredDrawingBytesPerTodo = 4 * 1024 * 1024
	MaxDrawingsPerTodo           = 50
	MaxWallDrawingBytes          = 1 << 20
	maxDrawingDimension          = 20000
	maxDrawingStrokeSize         = 20000
	maxWallDrawingCoordinate     = 100000
)

const (
	ReasonDrawingOwnedElsewhere      = "drawing_owned_elsewhere"
	ReasonUnresolvedDrawingReference = "unresolved_drawing_reference"
	ReasonConflictingDrawingRecord   = "conflicting_drawing_record"
	ReasonInvalidDrawing             = "invalid_drawing"
	ReasonDrawingTooLarge            = "drawing_too_large"
	ReasonDrawingLimit               = "drawing_limit_reached"
	ReasonDrawingsDisabled           = "drawings_disabled"
	ReasonWallDrawingVersionConflict = "wall_drawing_version_conflict"
)

var (
	strokeIDRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	strokeColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

type DrawingPoint [3]float64

type DrawingStroke struct {
	ID        string         `json:"id"`
	Tool      string         `json:"tool"`
	Color     string         `json:"color"`
	Size      float64        `json:"size"`
	Opacity   float64        `json:"opacity"`
	Points    []DrawingPoint `json:"points"`
	CreatedAt int64          `json:"createdAt"`
	By        *int64         `json:"by,omitempty"`
}

type DrawingDocument struct {
	Version int             `json:"version"`
	Width   float64         `json:"width"`
	Height  float64         `json:"height"`
	Strokes []DrawingStroke `json:"strokes"`
}

type rawDrawingStroke struct {
	ID        string      `json:"id"`
	Tool      string      `json:"tool"`
	Color     string      `json:"color"`
	Size      float64     `json:"size"`
	Opacity   float64     `json:"opacity"`
	Points    [][]float64 `json:"points"`
	CreatedAt int64       `json:"createdAt"`
	By        *int64      `json:"by,omitempty"`
	Deleted   bool        `json:"deleted,omitempty"`
}

type rawDrawingDocument struct {
	Version int                `json:"version"`
	Width   float64            `json:"width"`
	Height  float64            `json:"height"`
	Strokes []rawDrawingStroke `json:"strokes"`
}

func drawingError(reason, msg string) error {
	return &reasonedStoreError{base: ErrValidation, reason: reason, msg: ErrValidation.Error() + ": " + msg}
}

func drawingConflict(reason, msg string) error {
	return &reasonedStoreError{base: ErrConflict, reason: reason, msg: ErrConflict.Error() + ": " + msg}
}

func finiteNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type strokeBounds struct {
	world         bool
	width, height float64
}

func validateRawStroke(s rawDrawingStroke, b strokeBounds) (DrawingStroke, error) {
	if !strokeIDRe.MatchString(s.ID) {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke id")
	}
	if s.Tool != "pen" && s.Tool != "highlighter" {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke tool")
	}
	if !strokeColorRe.MatchString(s.Color) {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke color")
	}
	if !finiteNumber(s.Size) || s.Size <= 0 || s.Size > maxDrawingStrokeSize {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke size")
	}
	if !finiteNumber(s.Opacity) || s.Opacity < 0 || s.Opacity > 1 {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke opacity")
	}
	if len(s.Points) == 0 || len(s.Points) > MaxDrawingStrokePoints {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke point count")
	}
	if s.CreatedAt < 0 {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke timestamp")
	}
	points := make([]DrawingPoint, 0, len(s.Points))
	for _, p := range s.Points {
		if len(p) != 3 {
			return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke point")
		}
		x, y, pr := p[0], p[1], p[2]
		if !finiteNumber(x) || !finiteNumber(y) || !finiteNumber(pr) || pr < 0 || pr > 1 {
			return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid stroke point")
		}
		if b.world {
			if math.Abs(x) > maxWallDrawingCoordinate || math.Abs(y) > maxWallDrawingCoordinate {
				return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "stroke point out of bounds")
			}
		} else if x < 0 || y < 0 || x > b.width || y > b.height {
			return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "stroke point out of bounds")
		}
		points = append(points, DrawingPoint{x, y, pr})
	}
	return DrawingStroke{
		ID: s.ID, Tool: s.Tool, Color: s.Color, Size: s.Size, Opacity: s.Opacity,
		Points: points, CreatedAt: s.CreatedAt, By: s.By,
	}, nil
}

func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	return nil
}

type drawingLimits struct {
	maxBytes     int
	maxStrokes   int
	maxDimension float64
}

var referenceDrawingLimits = drawingLimits{maxBytes: MaxDrawingBytes, maxStrokes: MaxDrawingStrokes, maxDimension: maxDrawingDimension}

func ParseDrawingDocument(raw []byte) (DrawingDocument, []byte, error) {
	return parseDrawingDocumentWithLimits(raw, referenceDrawingLimits)
}

func parseDrawingDocumentWithLimits(raw []byte, limits drawingLimits) (DrawingDocument, []byte, error) {
	if len(raw) == 0 || len(raw) > limits.maxBytes*2 {
		return DrawingDocument{}, nil, drawingError(ReasonDrawingTooLarge, "drawing too large")
	}
	var in rawDrawingDocument
	if err := decodeStrict(raw, &in); err != nil {
		return DrawingDocument{}, nil, drawingError(ReasonInvalidDrawing, "invalid drawing json")
	}
	if in.Version != 1 {
		return DrawingDocument{}, nil, drawingError(ReasonInvalidDrawing, "unsupported drawing version")
	}
	if !finiteNumber(in.Width) || !finiteNumber(in.Height) || in.Width <= 0 || in.Height <= 0 || in.Width > limits.maxDimension || in.Height > limits.maxDimension {
		return DrawingDocument{}, nil, drawingError(ReasonInvalidDrawing, "invalid drawing dimensions")
	}
	if len(in.Strokes) > limits.maxStrokes {
		return DrawingDocument{}, nil, drawingError(ReasonDrawingLimit, "too many strokes in drawing")
	}
	doc := DrawingDocument{Version: 1, Width: in.Width, Height: in.Height, Strokes: make([]DrawingStroke, 0, len(in.Strokes))}
	seen := make(map[string]struct{}, len(in.Strokes))
	for _, rs := range in.Strokes {
		if rs.Deleted {
			return DrawingDocument{}, nil, drawingError(ReasonInvalidDrawing, "unexpected stroke field")
		}
		s, err := validateRawStroke(rs, strokeBounds{width: in.Width, height: in.Height})
		if err != nil {
			return DrawingDocument{}, nil, err
		}
		if _, dup := seen[s.ID]; dup {
			return DrawingDocument{}, nil, drawingError(ReasonInvalidDrawing, "duplicate stroke id")
		}
		seen[s.ID] = struct{}{}
		doc.Strokes = append(doc.Strokes, s)
	}
	canonical, err := json.Marshal(doc)
	if err != nil {
		return DrawingDocument{}, nil, fmt.Errorf("encode drawing: %w", err)
	}
	if len(canonical) > limits.maxBytes {
		return DrawingDocument{}, nil, drawingError(ReasonDrawingTooLarge, "drawing too large")
	}
	return doc, canonical, nil
}

type ownedDrawing struct {
	data string
}

func loadTodoDrawingsTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64) (map[string]ownedDrawing, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM todo_drawings WHERE todo_id = ? AND project_id = ?`, todoID, projectID)
	if err != nil {
		return nil, fmt.Errorf("load todo drawings: %w", err)
	}
	defer rows.Close()
	out := map[string]ownedDrawing{}
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, fmt.Errorf("scan todo drawing: %w", err)
		}
		out[id] = ownedDrawing{data: data}
	}
	return out, rows.Err()
}

type SyncTodoDrawingsInput struct {
	Enabled   bool
	Submitted map[string]json.RawMessage
}

func (s *Store) DrawingsEnabled() bool { return !s.drawingsDisabled }

func (s *Store) syncTodoDrawingsTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64, body string, submitted map[string]json.RawMessage, nowMs int64) error {
	return syncTodoDrawingsTx(ctx, tx, projectID, todoID, body, SyncTodoDrawingsInput{Enabled: !s.drawingsDisabled, Submitted: submitted}, nowMs)
}

func syncTodoDrawingsTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64, body string, in SyncTodoDrawingsInput, nowMs int64) error {
	if !in.Enabled && len(in.Submitted) > 0 {
		return drawingError(ReasonDrawingsDisabled, "drawings are disabled")
	}
	refs := drawrefs.Extract(body)
	if len(refs) > MaxDrawingsPerTodo {
		return drawingError(ReasonDrawingLimit, "too many drawings in one story")
	}
	submitted := make(map[string][]byte, len(in.Submitted))
	total := 0
	for id, raw := range in.Submitted {
		if !drawrefs.IsDrawingID(id) {
			return drawingError(ReasonInvalidDrawing, "invalid drawing id")
		}
		total += len(raw)
		if total > MaxSubmittedDrawingBytes {
			return drawingError(ReasonDrawingTooLarge, "submitted drawings too large")
		}
	}
	referenced := make(map[string]struct{}, len(refs))
	for _, id := range refs {
		referenced[id] = struct{}{}
	}
	for id, raw := range in.Submitted {
		if _, ok := referenced[id]; !ok {
			continue
		}
		_, canonical, err := ParseDrawingDocument(raw)
		if err != nil {
			return err
		}
		submitted[id] = canonical
	}
	owned, err := loadTodoDrawingsTx(ctx, tx, projectID, todoID)
	if err != nil {
		return err
	}
	stored := 0
	for _, id := range refs {
		if data, ok := submitted[id]; ok {
			stored += len(data)
		} else if row, ok := owned[id]; ok {
			stored += len(row.data)
		}
	}
	if stored > MaxStoredDrawingBytesPerTodo {
		return drawingError(ReasonDrawingTooLarge, "story drawings exceed the stored budget")
	}
	for _, id := range refs {
		row, isOwned := owned[id]
		data, isSubmitted := submitted[id]
		switch {
		case isOwned && !isSubmitted:
			continue
		case isOwned && isSubmitted:
			if row.data == string(data) {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE todo_drawings SET data = ?, version = version + 1, updated_at = ? WHERE id = ? AND todo_id = ? AND project_id = ?`,
				string(data), nowMs, id, todoID, projectID); err != nil {
				return fmt.Errorf("update drawing: %w", err)
			}
		case !isOwned && isSubmitted:
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM todo_drawings WHERE id = ?)`, id).Scan(&exists); err != nil {
				return fmt.Errorf("check drawing owner: %w", err)
			}
			if exists {
				return drawingError(ReasonDrawingOwnedElsewhere, "drawing is owned by another story")
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO todo_drawings (id, todo_id, project_id, data, version, created_at, updated_at) VALUES (?, ?, ?, ?, 1, ?, ?)`,
				id, todoID, projectID, string(data), nowMs, nowMs); err != nil {
				return fmt.Errorf("insert drawing: %w", err)
			}
		default:
			return drawingError(ReasonUnresolvedDrawingReference, "story references a drawing that does not exist")
		}
	}
	if !in.Enabled {
		return nil
	}
	for id := range owned {
		if _, keep := referenced[id]; keep {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM todo_drawings WHERE id = ? AND todo_id = ? AND project_id = ?`, id, todoID, projectID); err != nil {
			return fmt.Errorf("prune drawing: %w", err)
		}
	}
	return nil
}

func referencedOwnedDrawingsTx(ctx context.Context, tx *sql.Tx, projectID, todoID int64, body string) (map[string]json.RawMessage, error) {
	refs := drawrefs.Extract(body)
	out := make(map[string]json.RawMessage, len(refs))
	if len(refs) == 0 {
		return out, nil
	}
	owned, err := loadTodoDrawingsTx(ctx, tx, projectID, todoID)
	if err != nil {
		return nil, err
	}
	for _, id := range refs {
		if row, ok := owned[id]; ok {
			out[id] = json.RawMessage(row.data)
		}
	}
	return out, nil
}

func (s *Store) ListTodoDrawingsByLocalID(ctx context.Context, projectID, localID int64) (map[string]json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin list drawings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var todoID int64
	var body string
	err = tx.QueryRowContext(ctx, `SELECT id, body FROM todos WHERE project_id = ? AND local_id = ?`, projectID, localID).Scan(&todoID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load todo for drawings: %w", err)
	}
	return referencedOwnedDrawingsTx(ctx, tx, projectID, todoID, body)
}

func (s *Store) GetProjectDrawing(ctx context.Context, projectID int64, drawingID string, includeArchived bool) (json.RawMessage, error) {
	if !drawrefs.IsDrawingID(drawingID) {
		return nil, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin get drawing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return getProjectDrawingTx(ctx, tx, projectID, drawingID, includeArchived)
}

func getProjectDrawingTx(ctx context.Context, tx *sql.Tx, projectID int64, drawingID string, includeArchived bool) (json.RawMessage, error) {
	var data, body string
	var archivedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, `
SELECT d.data, t.body, t.archived_at
FROM todo_drawings d
JOIN todos t ON t.id = d.todo_id AND t.project_id = d.project_id
WHERE d.id = ? AND d.project_id = ?`, drawingID, projectID).Scan(&data, &body, &archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get drawing: %w", err)
	}
	if archivedAt.Valid && !includeArchived {
		return nil, ErrNotFound
	}
	for _, id := range drawrefs.Extract(body) {
		if id == drawingID {
			return json.RawMessage(data), nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) GetPublicDrawing(ctx context.Context, projectID int64, expectedSlug, drawingID string) (json.RawMessage, error) {
	if !drawrefs.IsDrawingID(drawingID) {
		return nil, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin public drawing: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return nil, err
	}
	return getProjectDrawingTx(ctx, tx, projectID, drawingID, false)
}

func (s *Store) ListPublicTodoDrawings(ctx context.Context, projectID int64, expectedSlug string, localID int64) (map[string]json.RawMessage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin public drawings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := loadEligiblePublicProjectTx(ctx, tx, projectID, expectedSlug); err != nil {
		return nil, err
	}
	var todoID int64
	var body string
	err = tx.QueryRowContext(ctx, `SELECT id, body FROM todos WHERE project_id = ? AND local_id = ? AND archived_at IS NULL`, projectID, localID).Scan(&todoID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load public todo for drawings: %w", err)
	}
	return referencedOwnedDrawingsTx(ctx, tx, projectID, todoID, body)
}
