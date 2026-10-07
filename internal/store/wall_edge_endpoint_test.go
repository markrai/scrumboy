package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestParseWallEdgeEndpoint(t *testing.T) {
	t.Run("legacy note endpoint passes through verbatim", func(t *testing.T) {
		ep, err := ParseWallEdgeEndpoint("n_abc123")
		if err != nil {
			t.Fatalf("ParseWallEdgeEndpoint: %v", err)
		}
		if ep.Kind() != WallEdgeEndpointNote || ep.NoteID() != "n_abc123" || ep.TodoLocalID() != 0 {
			t.Fatalf("endpoint=%+v", ep)
		}
		if ep.Canonical() != "n_abc123" {
			t.Fatalf("Canonical=%q", ep.Canonical())
		}
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		ep, err := ParseWallEdgeEndpoint("  story:12  ")
		if err != nil {
			t.Fatalf("ParseWallEdgeEndpoint: %v", err)
		}
		if ep.Kind() != WallEdgeEndpointStory || ep.TodoLocalID() != 12 || ep.NoteID() != "" {
			t.Fatalf("endpoint=%+v", ep)
		}
		if ep.Canonical() != "story:12" {
			t.Fatalf("Canonical=%q", ep.Canonical())
		}
	})

	t.Run("story prefix is case sensitive", func(t *testing.T) {
		ep, err := ParseWallEdgeEndpoint("Story:12")
		if err != nil {
			t.Fatalf("ParseWallEdgeEndpoint: %v", err)
		}
		if ep.Kind() != WallEdgeEndpointNote || ep.NoteID() != "Story:12" {
			t.Fatalf("endpoint=%+v", ep)
		}
	})

	t.Run("non-canonical story input normalizes", func(t *testing.T) {
		ep, err := ParseWallEdgeEndpoint("story:007")
		if err != nil {
			t.Fatalf("ParseWallEdgeEndpoint: %v", err)
		}
		if ep.Kind() != WallEdgeEndpointStory || ep.TodoLocalID() != 7 || ep.Canonical() != "story:7" {
			t.Fatalf("endpoint=%+v canonical=%q", ep, ep.Canonical())
		}
	})

	for _, raw := range []string{"", "   ", "story:", "story:abc", "story:-1", "story:0", "story:1.5", "story:12:34", "story: 12", "story:99999999999999999999999"} {
		raw := raw
		t.Run("malformed "+raw, func(t *testing.T) {
			if _, err := ParseWallEdgeEndpoint(raw); !errors.Is(err, ErrValidation) {
				t.Fatalf("ParseWallEdgeEndpoint(%q) err=%v want ErrValidation", raw, err)
			}
		})
	}

	t.Run("malformed message", func(t *testing.T) {
		_, err := ParseWallEdgeEndpoint("story:abc")
		if err == nil || err.Error() != "validation: invalid story endpoint" {
			t.Fatalf("err=%v", err)
		}
	})

	if got := FormatWallStoryEndpoint(123); got != "story:123" {
		t.Fatalf("FormatWallStoryEndpoint(123)=%q", got)
	}
}

func mustWallEdgeStoryFixture(t *testing.T, st *Store, ctx context.Context, projectID int64, title string) int64 {
	t.Helper()
	todo, err := st.CreateTodo(ctx, projectID, CreateTodoInput{Title: title}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, _, err := st.PinWallStory(ctx, projectID, todo.LocalID, 10, 20); err != nil {
		t.Fatalf("PinWallStory: %v", err)
	}
	return todo.LocalID
}

func TestWallEdgeMixedEndpoints(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-endpoint@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithUserID(ctx, user.ID)
	p, err := st.CreateProject(ctx, "p1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	note, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	other, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	firstLocalID := mustWallEdgeStoryFixture(t, st, ctx, p.ID, "First")
	secondLocalID := mustWallEdgeStoryFixture(t, st, ctx, p.ID, "Second")
	first := FormatWallStoryEndpoint(firstLocalID)
	second := FormatWallStoryEndpoint(secondLocalID)

	t.Run("legacy note to note still works", func(t *testing.T) {
		edge, _, err := st.CreateEdge(ctx, p.ID, note.ID, other.ID)
		if err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
		if edge.From != note.ID || edge.To != other.ID {
			t.Fatalf("edge=%+v", edge)
		}
	})

	t.Run("note to story succeeds with canonical endpoints", func(t *testing.T) {
		edge, _, err := st.CreateEdge(ctx, p.ID, note.ID, fmt.Sprintf("story:%04d", firstLocalID))
		if err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
		if edge.From != note.ID || edge.To != first {
			t.Fatalf("edge=%+v want to=%q", edge, first)
		}
	})

	t.Run("story to note reverse duplicate dedupes without write", func(t *testing.T) {
		before, err := st.GetWall(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		dupe, after, err := st.CreateEdge(ctx, p.ID, first, note.ID)
		if err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
		if dupe.From != note.ID || dupe.To != first {
			t.Fatalf("dupe=%+v", dupe)
		}
		if after.Version != before.Version || len(after.Edges) != len(before.Edges) {
			t.Fatalf("reverse dupe bumped wall version %d->%d or edge count", before.Version, after.Version)
		}
	})

	t.Run("story to story succeeds", func(t *testing.T) {
		edge, _, err := st.CreateEdge(ctx, p.ID, first, second)
		if err != nil {
			t.Fatalf("CreateEdge: %v", err)
		}
		if edge.From != first || edge.To != second {
			t.Fatalf("edge=%+v", edge)
		}
	})

	t.Run("story self edge rejected", func(t *testing.T) {
		if _, _, err := st.CreateEdge(ctx, p.ID, first, first); !errors.Is(err, ErrValidation) {
			t.Fatalf("self edge err=%v want ErrValidation", err)
		}
		padded := fmt.Sprintf("story:%04d", firstLocalID)
		if _, _, err := st.CreateEdge(ctx, p.ID, padded, first); !errors.Is(err, ErrValidation) {
			t.Fatalf("canonically equal self edge err=%v want ErrValidation", err)
		}
	})

	t.Run("missing story placement rejected", func(t *testing.T) {
		if _, _, err := st.CreateEdge(ctx, p.ID, note.ID, "story:999999"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing placement err=%v want ErrNotFound", err)
		}
	})

	t.Run("malformed story endpoint rejected", func(t *testing.T) {
		for _, raw := range []string{"story:", "story:abc", "story:0", "story:-4"} {
			if _, _, err := st.CreateEdge(ctx, p.ID, note.ID, raw); !errors.Is(err, ErrValidation) {
				t.Fatalf("CreateEdge(%q) err=%v want ErrValidation", raw, err)
			}
		}
	})

	t.Run("other project placement local id does not resolve", func(t *testing.T) {
		targetProject, err := st.CreateProject(ctx, "p-target")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		targetNote, _, err := st.CreateNote(ctx, targetProject.ID, CreateNoteInput{Color: "#ffd966"})
		if err != nil {
			t.Fatalf("CreateNote: %v", err)
		}
		sourceProject, err := st.CreateProject(ctx, "p-source")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		sourceLocalID := mustWallEdgeStoryFixture(t, st, ctx, sourceProject.ID, "Elsewhere")
		// The target wall holds notes but no story placements at all, so the
		// source project's local ID cannot resolve there by construction.
		if _, _, err := st.CreateEdge(ctx, targetProject.ID, targetNote.ID, FormatWallStoryEndpoint(sourceLocalID)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("cross-project err=%v want ErrNotFound", err)
		}
	})
}
