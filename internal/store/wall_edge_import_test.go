package store

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func mustWallEdgeImportFixture(t *testing.T, st *Store, ctx context.Context, name string) (Project, WallNote, WallNote, int64, int64) {
	t.Helper()
	p, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	second, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#ffd966"})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	firstTodo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "First"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	secondTodo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "Second"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, _, err := st.PinWallStory(ctx, p.ID, firstTodo.LocalID, 10, 20); err != nil {
		t.Fatalf("PinWallStory: %v", err)
	}
	if _, _, err := st.PinWallStory(ctx, p.ID, secondTodo.LocalID, 30, 40); err != nil {
		t.Fatalf("PinWallStory: %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, first.ID, FormatWallStoryEndpoint(firstTodo.LocalID)); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, FormatWallStoryEndpoint(firstTodo.LocalID), FormatWallStoryEndpoint(secondTodo.LocalID)); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	if _, _, err := st.CreateEdge(ctx, p.ID, first.ID, second.ID); err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	return p, first, second, firstTodo.LocalID, secondTodo.LocalID
}

func wallEdgeStrings(edges []WallEdge) []string {
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.From+"->"+e.To)
	}
	return out
}

func wallEdgeWarnings(warnings []string) []string {
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if strings.Contains(w, "Wall edge") {
			out = append(out, w)
		}
	}
	return out
}

func TestWallEdgeImportRoundTripReplace(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-import@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, firstLocalID, secondLocalID := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Export")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}

	st2, cleanup2 := newTestStore(t)
	defer cleanup2()
	user2, err := st2.BootstrapUser(ctx, "wall-edge-import-2@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st2.ImportProjects(WithUserID(ctx, user2.ID), exported, ModeFull, "replace"); err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}
	restoredProject, err := st2.GetProjectBySlug(WithUserID(ctx, user2.ID), p.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	restored, err := st2.GetWall(WithUserID(ctx, user2.ID), restoredProject.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	want := []string{
		first.ID + "->" + FormatWallStoryEndpoint(firstLocalID),
		FormatWallStoryEndpoint(firstLocalID) + "->" + FormatWallStoryEndpoint(secondLocalID),
		first.ID + "->" + second.ID,
	}
	if got := wallEdgeStrings(restored.Edges); !equalStrings(got, want) {
		t.Fatalf("edges=%q want %q", got, want)
	}
	if len(restored.Stories) != 2 {
		t.Fatalf("stories=%d want 2", len(restored.Stories))
	}
}

func TestWallEdgeImportCopyCarriesMixedEdges(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-copy@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, firstLocalID, secondLocalID := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Copy")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if _, err := st.ImportProjects(ownerCtx, exported, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}
	projects, err := st.ListProjects(ownerCtx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var copyID int64
	for _, entry := range projects {
		if entry.Project.ID != p.ID && strings.HasPrefix(entry.Project.Slug, p.Slug) {
			copyID = entry.Project.ID
			break
		}
	}
	if copyID == 0 {
		t.Fatalf("copied project not found; projects=%+v", projects)
	}
	copied, err := st.GetWall(ownerCtx, copyID)
	if err != nil {
		t.Fatalf("GetWall copy: %v", err)
	}
	want := []string{
		first.ID + "->" + FormatWallStoryEndpoint(firstLocalID),
		FormatWallStoryEndpoint(firstLocalID) + "->" + FormatWallStoryEndpoint(secondLocalID),
		first.ID + "->" + second.ID,
	}
	if got := wallEdgeStrings(copied.Edges); !equalStrings(got, want) {
		t.Fatalf("copy edges=%q want %q", got, want)
	}
	if len(copied.Stories) != 2 {
		t.Fatalf("copy stories=%d want 2", len(copied.Stories))
	}
}

func TestWallEdgeImportMergeExplicitPlacements(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-merge@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, firstLocalID, secondLocalID := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Merge")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var payload *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == p.Slug {
			payload = &exported.Projects[i]
			break
		}
	}
	if payload == nil || payload.Wall == nil {
		t.Fatalf("exported wall=%+v", payload)
	}
	payload.Wall.Edges = append(payload.Wall.Edges, WallEdge{
		ID:   "e_reverse_dupe",
		From: FormatWallStoryEndpoint(firstLocalID),
		To:   first.ID,
	})
	if _, err := st.ImportProjects(ownerCtx, exported, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}
	merged, err := st.GetWall(ownerCtx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	want := []string{
		first.ID + "->" + FormatWallStoryEndpoint(firstLocalID),
		FormatWallStoryEndpoint(firstLocalID) + "->" + FormatWallStoryEndpoint(secondLocalID),
		first.ID + "->" + second.ID,
	}
	if got := wallEdgeStrings(merged.Edges); !equalStrings(got, want) {
		t.Fatalf("merge edges=%q want %q (reverse duplicate deduped)", got, want)
	}
}

func TestWallEdgeImportMergeOmittedStoriesPreservesCompatiblePlacements(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-merge-legacy@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, _, firstLocalID, _ := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Merge Legacy")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var payload *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == p.Slug {
			payload = &exported.Projects[i]
			break
		}
	}
	if payload == nil || payload.Wall == nil {
		t.Fatalf("exported wall=%+v", payload)
	}
	payload.Wall.Stories = nil
	payload.Wall.StoriesPresent = false
	if _, err := st.ImportProjects(ownerCtx, exported, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}
	merged, err := st.GetWall(ownerCtx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	if len(merged.Stories) != 2 {
		t.Fatalf("stories=%d want preserved 2", len(merged.Stories))
	}
	found := false
	for _, e := range merged.Edges {
		if e.From == first.ID && e.To == FormatWallStoryEndpoint(firstLocalID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("note story edge missing after legacy merge; edges=%q", wallEdgeStrings(merged.Edges))
	}
}

func TestWallEdgeImportExplicitEmptyStoriesDropsStoryEdges(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-empty@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, _, _ := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Empty")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var payload *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == p.Slug {
			payload = &exported.Projects[i]
			break
		}
	}
	if payload == nil || payload.Wall == nil {
		t.Fatalf("exported wall=%+v", payload)
	}
	payload.Wall.Stories = []WallStoryPlacementExport{}
	payload.Wall.StoriesPresent = true
	result, err := st.ImportProjects(ownerCtx, exported, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}
	merged, err := st.GetWall(ownerCtx, p.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	want := []string{first.ID + "->" + second.ID}
	if got := wallEdgeStrings(merged.Edges); !equalStrings(got, want) {
		t.Fatalf("edges=%q want %q", got, want)
	}
	if len(wallEdgeWarnings(result.Warnings)) == 0 {
		t.Fatalf("want edge warnings, got %v", result.Warnings)
	}
}

func TestWallEdgeImportDropsDanglingMalformedAndCanonicalizes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-filter@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, firstLocalID, _ := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Filter")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var payload *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == p.Slug {
			payload = &exported.Projects[i]
			break
		}
	}
	if payload == nil || payload.Wall == nil {
		t.Fatalf("exported wall=%+v", payload)
	}
	padded := "story:00" + strconv.FormatInt(firstLocalID, 10)
	payload.Wall.Edges = []WallEdge{
		{ID: "e_padded", From: padded, To: second.ID},
		{ID: "e_dangling", From: "story:999999", To: first.ID},
		{ID: "e_malformed", From: "story:abc", To: first.ID},
		{ID: "e_self", From: FormatWallStoryEndpoint(firstLocalID), To: padded},
		{ID: "e_note", From: first.ID, To: second.ID},
		{ID: "e_note_dupe", From: second.ID, To: first.ID},
	}
	result, err := st.ImportProjects(ownerCtx, exported, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}
	restoredProject, err := st.GetProjectBySlug(ownerCtx, p.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	restored, err := st.GetWall(ownerCtx, restoredProject.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	want := []string{
		FormatWallStoryEndpoint(firstLocalID) + "->" + second.ID,
		first.ID + "->" + second.ID,
	}
	if got := wallEdgeStrings(restored.Edges); !equalStrings(got, want) {
		t.Fatalf("edges=%q want %q", got, want)
	}
	joined := strings.Join(wallEdgeWarnings(result.Warnings), "\n")
	for _, wantWarning := range []string{"unknown endpoint", "invalid endpoint"} {
		if !strings.Contains(joined, wantWarning) {
			t.Fatalf("warnings=%v want %q", result.Warnings, wantWarning)
		}
	}
}

func TestWallEdgeImportLegacyNoteOnlyUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-edge-legacy@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	p, first, second, _, _ := mustWallEdgeImportFixture(t, st, ownerCtx, "Edge Legacy")

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var payload *ProjectExport
	for i := range exported.Projects {
		if exported.Projects[i].Slug == p.Slug {
			payload = &exported.Projects[i]
			break
		}
	}
	if payload == nil || payload.Wall == nil {
		t.Fatalf("exported wall=%+v", payload)
	}
	payload.Wall.Edges = []WallEdge{{ID: "e1", From: first.ID, To: second.ID}}
	payload.Wall.Stories = nil
	payload.Wall.StoriesPresent = false
	result, err := st.ImportProjects(ownerCtx, exported, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}
	restoredProject, err := st.GetProjectBySlug(ownerCtx, p.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	restored, err := st.GetWall(ownerCtx, restoredProject.ID)
	if err != nil {
		t.Fatalf("GetWall: %v", err)
	}
	want := []string{first.ID + "->" + second.ID}
	if got := wallEdgeStrings(restored.Edges); !equalStrings(got, want) {
		t.Fatalf("edges=%q want %q", got, want)
	}
	if got := wallEdgeWarnings(result.Warnings); len(got) != 0 {
		t.Fatalf("edge warnings=%q want none", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
