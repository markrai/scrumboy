package store

import (
	"context"
	"errors"
	"testing"
)

func TestWallGetIsSideEffectFree(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	wall, err := st.GetWall(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if len(wall.Notes) != 0 {
		t.Fatalf("expected synthetic empty wall, got %d notes", len(wall.Notes))
	}
	if wall.Version != 0 {
		t.Fatalf("expected version 0 for synthetic wall, got %d", wall.Version)
	}

	// Second GET must still not create a row.
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_walls WHERE project_id = ?`, p.ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("GET should not materialize row, got count=%d", count)
	}
}

func TestWallCreateMaterializesRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	note, wall, err := st.CreateNote(ctx, p.ID, CreateNoteInput{
		X:      10, Y: 20, Width: 180, Height: 140,
		Color: "#ffd966", Text: "hello",
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if note.ID == "" {
		t.Fatal("expected note id")
	}
	if note.Version != 1 {
		t.Fatalf("expected note.Version=1, got %d", note.Version)
	}
	if wall.Version != 1 {
		t.Fatalf("expected wall.Version=1, got %d", wall.Version)
	}

	reloaded, err := st.GetWall(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if len(reloaded.Notes) != 1 || reloaded.Notes[0].ID != note.ID {
		t.Fatalf("expected persisted note, got %#v", reloaded)
	}
}

func TestWallPatchConflictsOnVersionMismatch(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	note, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	text := "updated"
	if _, _, err := st.PatchNote(ctx, p.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &text}); err != nil {
		t.Fatalf("PatchNote first: %v", err)
	}

	// Second patch using the stale version must conflict.
	text2 := "stale"
	_, _, err = st.PatchNote(ctx, p.ID, note.ID, PatchNoteInput{IfVersion: note.Version, Text: &text2})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestWallPatchOnDifferentNotesDoesNotConflict(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}
	b, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote b: %v", err)
	}

	ta := "a text"
	tb := "b text"
	if _, _, err := st.PatchNote(ctx, p.ID, a.ID, PatchNoteInput{IfVersion: a.Version, Text: &ta}); err != nil {
		t.Fatalf("PatchNote a: %v", err)
	}
	if _, _, err := st.PatchNote(ctx, p.ID, b.ID, PatchNoteInput{IfVersion: b.Version, Text: &tb}); err != nil {
		t.Fatalf("PatchNote b: %v", err)
	}
}

func TestWallDeleteNote(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	note, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	wall, err := st.DeleteNote(ctx, p.ID, note.ID)
	if err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if len(wall.Notes) != 0 {
		t.Fatalf("expected 0 notes after delete, got %d", len(wall.Notes))
	}

	// Deleting again yields ErrNotFound.
	_, err = st.DeleteNote(ctx, p.ID, note.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWallEdgeCreateRejectsSelfLoopAndUnknownNote(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}

	if _, _, err := st.CreateEdge(ctx, p.ID, a.ID, a.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for self-loop, got %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, a.ID, "n_does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown endpoint, got %v", err)
	}
}

func TestWallEdgeCreateIsIdempotent(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote a: %v", err)
	}
	b, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote b: %v", err)
	}

	first, w1, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge first: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected edge id")
	}
	if len(w1.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(w1.Edges))
	}

	// Same direction.
	again, w2, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge dupe forward: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("expected idempotent dupe, got new edge %s vs %s", again.ID, first.ID)
	}
	if w2.Version != w1.Version {
		t.Fatalf("dupe must not bump wall version (was %d, got %d)", w1.Version, w2.Version)
	}

	// Reverse direction is the same undirected edge.
	again2, _, err := st.CreateEdge(ctx, p.ID, b.ID, a.ID)
	if err != nil {
		t.Fatalf("CreateEdge dupe reverse: %v", err)
	}
	if again2.ID != first.ID {
		t.Fatalf("expected reverse to dedupe, got %s vs %s", again2.ID, first.ID)
	}
}

func TestWallDeleteEdgeAndCascadeOnNoteDelete(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	a, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	b, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	c, _, _ := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	eAB, _, err := st.CreateEdge(ctx, p.ID, a.ID, b.ID)
	if err != nil {
		t.Fatalf("CreateEdge AB: %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, b.ID, c.ID); err != nil {
		t.Fatalf("CreateEdge BC: %v", err)
	}

	w, err := st.DeleteEdge(ctx, p.ID, eAB.ID)
	if err != nil {
		t.Fatalf("DeleteEdge: %v", err)
	}
	if len(w.Edges) != 1 {
		t.Fatalf("expected 1 edge remaining, got %d", len(w.Edges))
	}
	if _, err := st.DeleteEdge(ctx, p.ID, eAB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second DeleteEdge, got %v", err)
	}

	// Deleting note B must cascade and remove the BC edge.
	w2, err := st.DeleteNote(ctx, p.ID, b.ID)
	if err != nil {
		t.Fatalf("DeleteNote b: %v", err)
	}
	if len(w2.Edges) != 0 {
		t.Fatalf("expected dependent edges removed, got %d", len(w2.Edges))
	}
}

func TestWallRejectsInvalidColor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	_, _, err = st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "not-a-color"})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}
