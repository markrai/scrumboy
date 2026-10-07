package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// wallStoryEndpointPrefix marks a wall edge endpoint that refers to a pinned
// Todo (wall story) by portable project-scoped local ID, e.g. "story:123".
// Anything without this prefix is a legacy raw note endpoint and is matched
// verbatim against wall note IDs.
const wallStoryEndpointPrefix = "story:"

// WallEdgeEndpointKind identifies what a wall edge endpoint refers to.
type WallEdgeEndpointKind int

const (
	// WallEdgeEndpointNote is a legacy raw note endpoint matched against
	// wall note IDs exactly as persisted.
	WallEdgeEndpointNote WallEdgeEndpointKind = iota + 1
	// WallEdgeEndpointStory is a pinned-Todo endpoint identified by the
	// portable project-scoped Todo local ID.
	WallEdgeEndpointStory
)

// WallEdgeEndpoint is one parsed wall edge endpoint. Callers inspect Kind,
// NoteID, and TodoLocalID instead of parsing endpoint strings themselves.
// Canonical is the persisted/API form: raw note IDs verbatim, story endpoints
// in canonical "story:<localID>" form.
type WallEdgeEndpoint struct {
	raw         string
	kind        WallEdgeEndpointKind
	todoLocalID int64
}

// ParseWallEdgeEndpoint parses one wall edge endpoint string. Legacy raw note
// endpoints are accepted verbatim. The "story:" prefix selects a pinned-Todo
// endpoint whose remainder must be a positive Todo local ID; anything else
// with that prefix is malformed and rejected with ErrValidation.
func ParseWallEdgeEndpoint(raw string) (WallEdgeEndpoint, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return WallEdgeEndpoint{}, fmt.Errorf("%w: invalid edge endpoint", ErrValidation)
	}
	if rest, ok := strings.CutPrefix(trimmed, wallStoryEndpointPrefix); ok {
		localID, err := strconv.ParseInt(rest, 10, 64)
		if err != nil || localID <= 0 {
			return WallEdgeEndpoint{}, fmt.Errorf("%w: invalid story endpoint", ErrValidation)
		}
		return WallEdgeEndpoint{raw: FormatWallStoryEndpoint(localID), kind: WallEdgeEndpointStory, todoLocalID: localID}, nil
	}
	return WallEdgeEndpoint{raw: trimmed, kind: WallEdgeEndpointNote}, nil
}

// FormatWallStoryEndpoint renders the canonical persisted/API form of a
// pinned-Todo edge endpoint for a portable project-scoped Todo local ID.
func FormatWallStoryEndpoint(todoLocalID int64) string {
	return wallStoryEndpointPrefix + strconv.FormatInt(todoLocalID, 10)
}

// Kind reports whether the endpoint refers to a note or a pinned Todo.
func (e WallEdgeEndpoint) Kind() WallEdgeEndpointKind {
	return e.kind
}

// NoteID returns the raw note ID for note endpoints and "" otherwise.
func (e WallEdgeEndpoint) NoteID() string {
	if e.kind != WallEdgeEndpointNote {
		return ""
	}
	return e.raw
}

// TodoLocalID returns the portable project-scoped Todo local ID for story
// endpoints and 0 otherwise.
func (e WallEdgeEndpoint) TodoLocalID() int64 {
	if e.kind != WallEdgeEndpointStory {
		return 0
	}
	return e.todoLocalID
}

// Canonical returns the persisted/API form of the endpoint.
func (e WallEdgeEndpoint) Canonical() string {
	return e.raw
}

// removeWallStoryEdgesTx removes every edge incident to one canonical story
// endpoint from the project's wall row. Survivor order is preserved. It
// reports whether any edge was removed and writes only in that case; a
// missing wall row simply reports no change. It never commits and never bumps
// the wall version: the caller owns version-bump policy and the surrounding
// transaction, so cleanup either commits or rolls back atomically with the
// placement or Todo deletion it accompanies.
func removeWallStoryEdgesTx(ctx context.Context, tx *sql.Tx, projectID int64, endpoint string) (bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT edges FROM project_walls WHERE project_id = ?`, projectID).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("load wall edges for story cleanup: %w", err)
	}
	var edges []WallEdge
	if err := json.Unmarshal([]byte(raw), &edges); err != nil {
		return false, fmt.Errorf("decode wall edges for story cleanup: %w", err)
	}
	kept := make([]WallEdge, 0, len(edges))
	changed := false
	for _, e := range edges {
		if e.From == endpoint || e.To == endpoint {
			changed = true
			continue
		}
		kept = append(kept, e)
	}
	if !changed {
		return false, nil
	}
	out, err := json.Marshal(kept)
	if err != nil {
		return false, fmt.Errorf("encode wall edges for story cleanup: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE project_walls SET edges = ? WHERE project_id = ?`, string(out), projectID); err != nil {
		return false, fmt.Errorf("write wall edges for story cleanup: %w", err)
	}
	return true, nil
}

// resolveWallEdgeEndpoint reports whether an endpoint refers to something
// present on the given wall: a note ID for note endpoints, a pinned Todo
// placement with the same project-scoped local ID for story endpoints.
func resolveWallEdgeEndpoint(wall Wall, endpoint WallEdgeEndpoint) bool {
	if endpoint.Kind() == WallEdgeEndpointStory {
		for _, p := range wall.Stories {
			if p.TodoLocalID == endpoint.TodoLocalID() {
				return true
			}
		}
		return false
	}
	for _, n := range wall.Notes {
		if n.ID == endpoint.NoteID() {
			return true
		}
	}
	return false
}
