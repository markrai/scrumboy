package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	maxWallDrawingTombstones   = 1000
	MaxWallDrawingOpsPerCommit = 50
	maxWallDrawingEraseIDs     = MaxWallDrawingStrokes
)

type WallDrawingStroke struct {
	DrawingStroke
	Deleted bool `json:"deleted,omitempty"`
}

type WallDrawingOps struct {
	BaseVersion int64
	Add         []json.RawMessage
	Erase       []string
}

type WallDrawingResult struct {
	Wall    Wall
	Applied bool
}

func LiveWallDrawings(strokes []WallDrawingStroke) []DrawingStroke {
	out := make([]DrawingStroke, 0, len(strokes))
	for _, s := range strokes {
		if !s.Deleted {
			out = append(out, s.DrawingStroke)
		}
	}
	return out
}

func decodeWallDrawings(raw string) ([]WallDrawingStroke, error) {
	if raw == "" {
		return []WallDrawingStroke{}, nil
	}
	var out []WallDrawingStroke
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("decode wall drawings: %w", err)
	}
	if out == nil {
		out = []WallDrawingStroke{}
	}
	return out, nil
}

func parseWallStroke(raw []byte) (DrawingStroke, error) {
	var rs rawDrawingStroke
	if err := decodeStrict(raw, &rs); err != nil {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "invalid wall stroke json")
	}
	if rs.Deleted {
		return DrawingStroke{}, drawingError(ReasonInvalidDrawing, "unexpected stroke field")
	}
	return validateRawStroke(rs, strokeBounds{world: true})
}

func ValidateWallDrawingsForImport(strokes []DrawingStroke) ([]WallDrawingStroke, error) {
	if len(strokes) > MaxWallDrawingStrokes {
		return nil, drawingError(ReasonDrawingLimit, "too many wall strokes")
	}
	out := make([]WallDrawingStroke, 0, len(strokes))
	seen := make(map[string]struct{}, len(strokes))
	for _, s := range strokes {
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		v, err := parseWallStroke(raw)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[v.ID]; dup {
			return nil, drawingError(ReasonInvalidDrawing, "duplicate wall stroke id")
		}
		seen[v.ID] = struct{}{}
		v.By = nil
		out = append(out, WallDrawingStroke{DrawingStroke: v})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxWallDrawingBytes {
		return nil, drawingError(ReasonDrawingTooLarge, "wall drawings too large")
	}
	return out, nil
}

func sameStrokeContent(a, b DrawingStroke) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

func (s *Store) ApplyWallDrawingOps(ctx context.Context, projectID, actorID int64, ops WallDrawingOps) (WallDrawingResult, error) {
	if len(ops.Add) == 0 && len(ops.Erase) == 0 {
		return WallDrawingResult{}, drawingError(ReasonInvalidDrawing, "no drawing operations")
	}
	if len(ops.Add) > MaxWallDrawingOpsPerCommit || len(ops.Erase) > maxWallDrawingEraseIDs {
		return WallDrawingResult{}, drawingError(ReasonDrawingLimit, "too many drawing operations")
	}
	adds := make([]DrawingStroke, 0, len(ops.Add))
	addIDs := make(map[string]struct{}, len(ops.Add))
	for _, raw := range ops.Add {
		st, err := parseWallStroke(raw)
		if err != nil {
			return WallDrawingResult{}, err
		}
		if _, dup := addIDs[st.ID]; dup {
			return WallDrawingResult{}, drawingError(ReasonInvalidDrawing, "duplicate wall stroke id")
		}
		addIDs[st.ID] = struct{}{}
		by := actorID
		st.By = &by
		adds = append(adds, st)
	}
	eraseIDs := make([]string, 0, len(ops.Erase))
	seenErase := map[string]struct{}{}
	for _, id := range ops.Erase {
		if !strokeIDRe.MatchString(id) {
			return WallDrawingResult{}, drawingError(ReasonInvalidDrawing, "invalid stroke id")
		}
		if _, dup := seenErase[id]; dup {
			continue
		}
		seenErase[id] = struct{}{}
		eraseIDs = append(eraseIDs, id)
	}

	mu := lockWall(projectID)
	defer mu.Unlock()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return WallDrawingResult{}, fmt.Errorf("begin wall drawings: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var drawingsJSON string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT drawings, version FROM project_walls WHERE project_id = ?`, projectID).Scan(&drawingsJSON, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return WallDrawingResult{}, fmt.Errorf("read wall drawings: %w", err)
	}
	current, err := decodeWallDrawings(drawingsJSON)
	if err != nil {
		return WallDrawingResult{}, err
	}
	index := make(map[string]int, len(current))
	for i, st := range current {
		index[st.ID] = i
	}

	alreadyApplied := true
	for _, add := range adds {
		i, ok := index[add.ID]
		if !ok {
			alreadyApplied = false
			break
		}
		if !current[i].Deleted && !sameStrokeContent(current[i].DrawingStroke, add) {
			alreadyApplied = false
			break
		}
	}
	if alreadyApplied {
		for _, id := range eraseIDs {
			if i, ok := index[id]; !ok || !current[i].Deleted {
				alreadyApplied = false
				break
			}
		}
	}
	if alreadyApplied {
		if err := tx.Commit(); err != nil {
			return WallDrawingResult{}, fmt.Errorf("commit wall drawings no-op: %w", err)
		}
		wall, err := s.GetWall(ctx, projectID)
		if err != nil {
			return WallDrawingResult{}, err
		}
		return WallDrawingResult{Wall: wall, Applied: false}, nil
	}
	if ops.BaseVersion != version {
		return WallDrawingResult{}, drawingConflict(ReasonWallDrawingVersionConflict, "wall changed since the drawing began")
	}

	for _, add := range adds {
		if i, ok := index[add.ID]; ok {
			if current[i].Deleted {
				continue
			}
			if !sameStrokeContent(current[i].DrawingStroke, add) {
				return WallDrawingResult{}, drawingError(ReasonInvalidDrawing, "stroke id already used")
			}
			continue
		}
		index[add.ID] = len(current)
		current = append(current, WallDrawingStroke{DrawingStroke: add})
	}
	nowMs := time.Now().UTC().UnixMilli()
	for _, id := range eraseIDs {
		if i, ok := index[id]; ok {
			if !current[i].Deleted {
				current[i] = WallDrawingStroke{DrawingStroke: DrawingStroke{ID: id, CreatedAt: nowMs}, Deleted: true}
			}
			continue
		}
		index[id] = len(current)
		current = append(current, WallDrawingStroke{DrawingStroke: DrawingStroke{ID: id, CreatedAt: nowMs}, Deleted: true})
	}

	live := 0
	tombstones := 0
	for _, st := range current {
		if st.Deleted {
			tombstones++
		} else {
			live++
		}
	}
	if live > MaxWallDrawingStrokes {
		return WallDrawingResult{}, drawingError(ReasonDrawingLimit, "wall stroke limit reached")
	}
	if tombstones > maxWallDrawingTombstones {
		drop := tombstones - maxWallDrawingTombstones
		kept := current[:0]
		for _, st := range current {
			if st.Deleted && drop > 0 {
				drop--
				continue
			}
			kept = append(kept, st)
		}
		current = kept
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		return WallDrawingResult{}, fmt.Errorf("encode wall drawings: %w", err)
	}
	if liveBytes, _ := json.Marshal(LiveWallDrawings(current)); len(liveBytes) > MaxWallDrawingBytes {
		return WallDrawingResult{}, drawingError(ReasonDrawingTooLarge, "wall drawings too large")
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO project_walls (project_id, notes, edges, drawings, version, updated_at)
VALUES (?, '[]', '[]', ?, 1, ?)
ON CONFLICT (project_id) DO UPDATE SET drawings = excluded.drawings, version = project_walls.version + 1, updated_at = excluded.updated_at`,
		projectID, string(encoded), nowMs); err != nil {
		return WallDrawingResult{}, fmt.Errorf("write wall drawings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return WallDrawingResult{}, fmt.Errorf("commit wall drawings: %w", err)
	}
	wall, err := s.GetWall(ctx, projectID)
	if err != nil {
		return WallDrawingResult{}, err
	}
	return WallDrawingResult{Wall: wall, Applied: true}, nil
}
