package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"testing"
	"time"
)

// These tests cover the canonical-name grouping projection for project tag views
// (issue #173). Personal cross-project tag ownership is unchanged; only the read/
// write projection groups by name.

func plAddMember(t *testing.T, st *Store, projectID, userID int64, role ProjectRole) {
	t.Helper()
	if _, err := st.db.ExecContext(context.Background(), `
INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		projectID, userID, role, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("add member: %v", err)
	}
}

func plInsertBoardScopedTag(t *testing.T, st *Store, projectID int64, name string, color *string) int64 {
	t.Helper()
	res, err := st.db.ExecContext(context.Background(), `
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES(NULL, ?, ?, ?, ?)`,
		name, time.Now().UTC().UnixMilli(), projectID, color)
	if err != nil {
		t.Fatalf("insert board-scoped tag: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func plTagRowID(t *testing.T, st *Store, name string, userID int64) int64 {
	t.Helper()
	var id int64
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT id FROM tags WHERE name = ? AND user_id = ?`, name, userID).Scan(&id); err != nil {
		t.Fatalf("lookup tag row %s/%d: %v", name, userID, err)
	}
	return id
}

func plFindTag(tags []TagCount, name string) *TagCount {
	for i := range tags {
		if tags[i].Name == name {
			return &tags[i]
		}
	}
	return nil
}

func plNewTodo(t *testing.T, st *Store, ctx context.Context, projectID int64, title string, tags ...string) {
	t.Helper()
	if _, err := st.CreateTodo(ctx, projectID, CreateTodoInput{
		Title:     title,
		Tags:      tags,
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull); err != nil {
		t.Fatalf("create todo %q: %v", title, err)
	}
}

func TestProjectLabels_TwoUsersBugCollapseToOneEntry(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, err := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	if err != nil {
		t.Fatalf("create u2: %v", err)
	}
	ctx1 := WithUserID(ctx, u1.ID)
	p, err := st.CreateProject(ctx1, "P")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	ctx2 := WithUserID(ctx, u2.ID)
	plNewTodo(t, st, ctx2, p.ID, "b", "bug")

	tags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if err != nil {
		t.Fatalf("listTagCounts: %v", err)
	}
	bug := plFindTag(tags, "bug")
	if bug == nil {
		t.Fatalf("expected one 'bug' entry, got %#v", tags)
	}
	// Exactly one grouped entry for the name.
	n := 0
	for _, tc := range tags {
		if tc.Name == "bug" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly one grouped 'bug', got %d", n)
	}
	if bug.Count != 2 {
		t.Errorf("expected distinct-todo count 2, got %d", bug.Count)
	}
	if bug.TagID != 0 {
		t.Errorf("expected no representative tagId for personal group, got %d", bug.TagID)
	}
	if !bug.CanDeleteMine {
		t.Errorf("expected CanDeleteMine true for owner")
	}
	if bug.CanDeleteProject {
		t.Errorf("personal group must never report CanDeleteProject")
	}
	if bug.DeleteScope() != "mine" {
		t.Errorf("expected deleteScope mine, got %q", bug.DeleteScope())
	}
}

func TestProjectLabels_LegacyConflictingViewerColorsResolveDeterministically(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	ctx2 := WithUserID(ctx, u2.ID)
	plNewTodo(t, st, ctx2, p.ID, "b", "bug")

	bug1 := plTagRowID(t, st, "bug", u1.ID) // u1's own row (lower id)
	bug2 := plTagRowID(t, st, "bug", u2.ID)

	// Viewer u1 has preferences on BOTH backing rows: owned row wins.
	if _, err := st.db.ExecContext(ctx, `INSERT INTO user_tag_colors(user_id, tag_id, color) VALUES(?,?,?)`, u1.ID, bug1, "#111111"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO user_tag_colors(user_id, tag_id, color) VALUES(?,?,?)`, u1.ID, bug2, "#222222"); err != nil {
		t.Fatal(err)
	}
	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != "#111111" {
		t.Fatalf("expected owned-row color #111111, got %#v", bug)
	}

	// Remove owned-row preference: lowest backing tag_id with a viewer color wins.
	if _, err := st.db.ExecContext(ctx, `DELETE FROM user_tag_colors WHERE user_id=? AND tag_id=?`, u1.ID, bug1); err != nil {
		t.Fatal(err)
	}
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != "#222222" {
		t.Fatalf("expected fallback color #222222, got %#v", bug)
	}

	// A name-based write converges all backing rows to one value.
	converged := "#333333"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &converged); err != nil {
		t.Fatalf("SetViewerTagColorByName: %v", err)
	}
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != converged {
		t.Fatalf("expected converged color %s, got %#v", converged, bug)
	}
}

// TestProjectLabels_ViewerColorSurvivesBackingRowRotation is the regression test for a
// silently-dropped color preference (issue #173, review comment 2): a viewer's color is
// written onto whichever backing row(s) exist for a canonical name at write time. If
// that row later falls out of the project's "used by a todo" read-inclusion set (e.g.
// the tag is removed from every todo carrying it) while a different member's row for the
// same canonical name becomes the sole backing row, the viewer's preference must still
// resolve for that name - not silently revert to no color just because the specific row
// it was recorded against is no longer part of the listing.
func TestProjectLabels_ViewerColorSurvivesBackingRowRotation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	// u1 tags a todo with "bug", creating u1's personal backing row, and sets a color.
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	bug1 := plTagRowID(t, st, "bug", u1.ID)
	color := "#654321"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &color); err != nil {
		t.Fatalf("SetViewerTagColorByName: %v", err)
	}
	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != color {
		t.Fatalf("expected initial color %s, got %#v", color, bug)
	}

	// u1's row stops being used in the project (untagged from its only todo), so it
	// drops out of the grouped listing's read-inclusion set entirely.
	if _, err := st.db.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, bug1); err != nil {
		t.Fatalf("untag bug1: %v", err)
	}
	if tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true); plFindTag(tags, "bug") != nil {
		t.Fatalf("expected 'bug' to disappear once its only backing row is unused, got %#v", tags)
	}

	// A different member creates a brand-new backing row for the same canonical name.
	ctx2 := WithUserID(ctx, u2.ID)
	plNewTodo(t, st, ctx2, p.ID, "b", "bug")
	bug2 := plTagRowID(t, st, "bug", u2.ID)

	// u1's color preference must still resolve for "bug", even though it lives on a tag_id
	// (bug1) that is no longer part of the listing's backing-row set.
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	bug := plFindTag(tags, "bug")
	if bug == nil {
		t.Fatalf("expected 'bug' to reappear via u2's new row, got %#v", tags)
	}
	if bug.Color == nil || *bug.Color != color {
		t.Errorf("expected u1's color preference %s to survive backing-row rotation, got %#v", color, bug.Color)
	}

	// A preference on the current row outranks the viewer-owned historical row.
	currentColor := "#123456"
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO user_tag_colors(user_id, tag_id, color) VALUES(?,?,?)`, u1.ID, bug2, currentColor); err != nil {
		t.Fatalf("insert current-row color: %v", err)
	}
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != currentColor {
		t.Fatalf("expected current-row color %s to outrank historical color, got %#v", currentColor, bug)
	}

	// A later name-based write converges both the current and historical rows.
	updatedColor := "#abcdef"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &updatedColor); err != nil {
		t.Fatalf("update after backing-row rotation: %v", err)
	}
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != updatedColor {
		t.Fatalf("expected updated color %s after rotation, got %#v", updatedColor, bug)
	}
	var convergedRows int
	if err := st.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM user_tag_colors
WHERE user_id = ? AND tag_id IN (?, ?) AND color = ?`,
		u1.ID, bug1, bug2, updatedColor).Scan(&convergedRows); err != nil {
		t.Fatalf("count converged color rows: %v", err)
	}
	if convergedRows != 2 {
		t.Fatalf("expected both current and historical rows to converge, got %d", convergedRows)
	}

	// Clearing by name also reaches both rows, so the historical preference cannot
	// immediately resurface.
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", nil); err != nil {
		t.Fatalf("clear after backing-row rotation: %v", err)
	}
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags, "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("expected no viewer color after clear, got %#v", bug)
	}
	var remainingRows int
	if err := st.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM user_tag_colors
WHERE user_id = ? AND tag_id IN (?, ?)`, u1.ID, bug1, bug2).Scan(&remainingRows); err != nil {
		t.Fatalf("count remaining color rows: %v", err)
	}
	if remainingRows != 0 {
		t.Fatalf("expected clear to remove current and historical preferences, got %d", remainingRows)
	}
}

func TestProjectLabels_ViewerColorIgnoresUnrelatedProjectRows(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	ctx2 := WithUserID(ctx, u2.ID)
	projectA, _ := st.CreateProject(ctx1, "A")
	projectB, _ := st.CreateProject(ctx1, "B")
	plAddMember(t, st, projectB.ID, u2.ID, RoleMaintainer)

	// u1's viewer-owned "bug" row is associated only with Project A.
	plNewTodo(t, st, ctx1, projectA.ID, "a", "bug")
	bugA := plTagRowID(t, st, "bug", u1.ID)
	colorA := "#aa0000"
	if err := st.SetViewerTagColorByName(ctx, projectA.ID, u1.ID, "bug", &colorA); err != nil {
		t.Fatalf("set Project A color: %v", err)
	}

	// Project B is backed only by u2's separate personal row.
	plNewTodo(t, st, ctx2, projectB.ID, "b", "bug")
	bugB := plTagRowID(t, st, "bug", u2.ID)
	tagsB, _ := st.listTagCounts(ctx1, projectB.ID, &u1.ID, nil, true)
	if bug := plFindTag(tagsB, "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("unrelated Project A preference must not color Project B, got %#v", bug)
	}

	colorB := "#0000bb"
	if err := st.SetViewerTagColorByName(ctx, projectB.ID, u1.ID, "bug", &colorB); err != nil {
		t.Fatalf("set Project B color: %v", err)
	}
	tagsB, _ = st.listTagCounts(ctx1, projectB.ID, &u1.ID, nil, true)
	if bug := plFindTag(tagsB, "bug"); bug == nil || bug.Color == nil || *bug.Color != colorB {
		t.Fatalf("expected Project B color %s, got %#v", colorB, bug)
	}
	tagsA, _ := st.listTagCounts(ctx1, projectA.ID, &u1.ID, nil, true)
	if bug := plFindTag(tagsA, "bug"); bug == nil || bug.Color == nil || *bug.Color != colorA {
		t.Fatalf("Project B write must not change Project A color %s, got %#v", colorA, bug)
	}

	var storedA, storedB string
	if err := st.db.QueryRowContext(ctx,
		`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`,
		u1.ID, bugA).Scan(&storedA); err != nil {
		t.Fatalf("read Project A stored color: %v", err)
	}
	if err := st.db.QueryRowContext(ctx,
		`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`,
		u1.ID, bugB).Scan(&storedB); err != nil {
		t.Fatalf("read Project B stored color: %v", err)
	}
	if storedA != colorA || storedB != colorB {
		t.Fatalf("expected isolated stored colors A=%s B=%s, got A=%s B=%s", colorA, colorB, storedA, storedB)
	}
}

func TestProjectLabels_HistoricalPersonalColorDoesNotOverridePureBoardGroup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")

	// Leave a personal preference in the project's history, but remove its row from
	// the current personal backing set.
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	personalBug := plTagRowID(t, st, "bug", u1.ID)
	personalColor := "#aa0000"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &personalColor); err != nil {
		t.Fatalf("set personal color: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM todo_tags WHERE tag_id = ?`, personalBug); err != nil {
		t.Fatalf("untag personal bug: %v", err)
	}

	// Once the visible group is pure board-scoped, its shared color is authoritative.
	boardColor := "#00bb00"
	boardBug := plInsertBoardScopedTag(t, st, p.ID, "bug", &boardColor)
	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	bug := plFindTag(tags, "bug")
	if bug == nil || bug.TagID != boardBug || bug.Color == nil || *bug.Color != boardColor {
		t.Fatalf("expected pure board-scoped color %s, got %#v", boardColor, bug)
	}
}

func TestProjectLabels_NonOwnerCanSetColorByNameWithoutAffectingOthers(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)
	// Only u1 owns a "bug" row; u2 owns none but still sees the label.
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")

	color := "#abcdef"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u2.ID, "bug", &color); err != nil {
		t.Fatalf("SetViewerTagColorByName by non-owner: %v", err)
	}

	// u2 sees their chosen color.
	tags2, _ := st.listTagCounts(WithUserID(ctx, u2.ID), p.ID, &u2.ID, nil, true)
	if bug := plFindTag(tags2, "bug"); bug == nil || bug.Color == nil || *bug.Color != color {
		t.Fatalf("expected u2 to see %s, got %#v", color, bug)
	}
	// u1 is unaffected (no preference).
	tags1, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags1, "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("expected u1 color unaffected (nil), got %#v", bug)
	}
}

func TestProjectLabels_DeleteScopeByViewer(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u3, _ := st.CreateUser(ctx, "u3@example.com", "password", "U3")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u3.ID, RoleViewer)
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	plInsertBoardScopedTag(t, st, p.ID, "board", nil)

	// Owner: personal "bug" -> mine; board-scoped "board" -> project (maintainer/admin).
	tags1, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if bug := plFindTag(tags1, "bug"); bug == nil || bug.DeleteScope() != "mine" {
		t.Fatalf("owner bug scope expected mine, got %#v", bug)
	}
	if bd := plFindTag(tags1, "board"); bd == nil || bd.DeleteScope() != "project" || bd.TagID == 0 {
		t.Fatalf("owner board scope expected project with tagId, got %#v", bd)
	}

	// Non-owner viewer: no delete rights on either.
	tags3, _ := st.listTagCounts(WithUserID(ctx, u3.ID), p.ID, &u3.ID, nil, true)
	if bug := plFindTag(tags3, "bug"); bug == nil || bug.DeleteScope() != "none" {
		t.Fatalf("viewer bug scope expected none, got %#v", bug)
	}
	if bd := plFindTag(tags3, "board"); bd == nil || bd.DeleteScope() != "none" {
		t.Fatalf("viewer board scope expected none, got %#v", bd)
	}
}

func TestProjectLabels_DeleteMineIsCrossProject(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	ctx1 := WithUserID(ctx, u1.ID)
	pa, _ := st.CreateProject(ctx1, "A")
	pb, _ := st.CreateProject(ctx1, "B")
	plNewTodo(t, st, ctx1, pa.ID, "a", "bug") // same user reuses one "bug" row
	plNewTodo(t, st, ctx1, pb.ID, "b", "bug")

	affected, err := st.DeleteMyTagByName(ctx, pa.ID, u1.ID, "bug")
	if err != nil {
		t.Fatalf("DeleteMyTagByName: %v", err)
	}
	sawA, sawB := false, false
	for _, id := range affected {
		if id == pa.ID {
			sawA = true
		}
		if id == pb.ID {
			sawB = true
		}
	}
	if !sawA || !sawB {
		t.Fatalf("expected affected projects to include A(%d) and B(%d), got %v", pa.ID, pb.ID, affected)
	}
	// Removed from both projects.
	tagsA, _ := st.listTagCounts(ctx1, pa.ID, &u1.ID, nil, true)
	if plFindTag(tagsA, "bug") != nil {
		t.Errorf("bug should be gone from A")
	}
	tagsB, _ := st.listTagCounts(ctx1, pb.ID, &u1.ID, nil, true)
	if plFindTag(tagsB, "bug") != nil {
		t.Errorf("bug should be gone from B (cross-project delete)")
	}
}

func TestProjectLabels_DeleteMineLeavesGroupWhenAnotherMemberUsesName(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	ctx2 := WithUserID(ctx, u2.ID)
	plNewTodo(t, st, ctx2, p.ID, "b", "bug")

	if _, err := st.DeleteMyTagByName(ctx, p.ID, u1.ID, "bug"); err != nil {
		t.Fatalf("DeleteMyTagByName: %v", err)
	}
	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	bug := plFindTag(tags, "bug")
	if bug == nil {
		t.Fatalf("grouped 'bug' should remain (u2 still uses it)")
	}
	if bug.Count != 1 {
		t.Errorf("expected count 1 after deleting mine, got %d", bug.Count)
	}
	// u1 no longer owns a backing row.
	if bug.CanDeleteMine {
		t.Errorf("u1 should not report CanDeleteMine after deleting own row")
	}
}

func TestProjectLabels_BoardScopedAndMixedGroups(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")

	// Pure board-scoped group keeps a real tagId and shared color.
	bcColor := "#0a0b0c"
	bcID := plInsertBoardScopedTag(t, st, p.ID, "shared", &bcColor)

	// Mixed group: personal "bug" + a board-scoped "bug" row -> personal-label group.
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	mixColor := "#0d0e0f"
	plInsertBoardScopedTag(t, st, p.ID, "bug", &mixColor)

	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)

	shared := plFindTag(tags, "shared")
	if shared == nil || shared.TagID != bcID || shared.Color == nil || *shared.Color != bcColor {
		t.Fatalf("board-scoped 'shared' expected tagId %d and color %s, got %#v", bcID, bcColor, shared)
	}

	bug := plFindTag(tags, "bug")
	if bug == nil {
		t.Fatalf("mixed 'bug' entry missing")
	}
	if bug.TagID != 0 {
		t.Errorf("mixed group must collapse to no representative tagId, got %d", bug.TagID)
	}
	// No viewer color -> falls back to the board-scoped shared color.
	if bug.Color == nil || *bug.Color != mixColor {
		t.Errorf("mixed group should fall back to board color %s, got %#v", mixColor, bug.Color)
	}

	// Name-based color write must never touch tags.color of the board-scoped row.
	viewerColor := "#101112"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &viewerColor); err != nil {
		t.Fatalf("SetViewerTagColorByName: %v", err)
	}
	var storedBoardColor *string
	if err := st.db.QueryRowContext(ctx, `SELECT color FROM tags WHERE name='bug' AND user_id IS NULL`).Scan(&storedBoardColor); err != nil {
		t.Fatalf("read board bug color: %v", err)
	}
	if storedBoardColor == nil || *storedBoardColor != mixColor {
		t.Errorf("board-scoped tags.color must be untouched (%s), got %#v", mixColor, storedBoardColor)
	}
}

// plInsertPersonalTagRow inserts a user-owned tag row verbatim, bypassing
// CanonicalizeTag, to reproduce legacy rows whose stored name is not canonical.
func plInsertPersonalTagRow(t *testing.T, st *Store, userID int64, name string) int64 {
	t.Helper()
	res, err := st.db.ExecContext(context.Background(), `
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES(?, ?, ?, NULL, NULL)`,
		userID, name, time.Now().UTC().UnixMilli())
	if err != nil {
		t.Fatalf("insert personal tag row %q: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// plAttachTagToTodo links an existing tag row to the newest todo in a project, so a
// legacy row participates in the read inclusion rule (used by a todo in the project).
func plAttachTagToTodo(t *testing.T, st *Store, projectID, tagID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var todoID int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT id FROM todos WHERE project_id = ? ORDER BY id DESC LIMIT 1`, projectID).Scan(&todoID); err != nil {
		t.Fatalf("find todo in project %d: %v", projectID, err)
	}
	if _, err := st.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO todo_tags(todo_id, tag_id) VALUES(?, ?)`, todoID, tagID); err != nil {
		t.Fatalf("attach tag %d to todo %d: %v", tagID, todoID, err)
	}
	return todoID
}

// TestProjectLabels_CrossOwnerCanonicalEquivalence is the regression test for grouping
// on raw names. A legacy row stored as "make space" and a canonical "make-space" row
// owned by a different member describe the same logical label and must collapse into
// one entry, and the name-based write paths must resolve both.
func TestProjectLabels_CrossOwnerCanonicalEquivalence(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	// u1 owns the canonical row through the normal write path.
	plNewTodo(t, st, ctx1, p.ID, "a", "make-space")
	canonicalID := plTagRowID(t, st, "make-space", u1.ID)

	// u2 owns a legacy row whose stored name never went through CanonicalizeTag.
	legacyID := plInsertPersonalTagRow(t, st, u2.ID, "make space")
	ctx2 := WithUserID(ctx, u2.ID)
	plNewTodo(t, st, ctx2, p.ID, "b")
	legacyTodoID := plAttachTagToTodo(t, st, p.ID, legacyID)

	tags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if err != nil {
		t.Fatalf("listTagCounts: %v", err)
	}
	if raw := plFindTag(tags, "make space"); raw != nil {
		t.Fatalf("legacy raw name must not surface as its own entry: %#v", raw)
	}
	grouped := plFindTag(tags, "make-space")
	if grouped == nil {
		t.Fatalf("expected one canonical 'make-space' entry, got %#v", tags)
	}
	if grouped.Count != 2 {
		t.Errorf("expected distinct-todo count 2 across both backing rows, got %d", grouped.Count)
	}
	if grouped.TagID != 0 {
		t.Errorf("cross-owner personal group must omit tagId, got %d", grouped.TagID)
	}

	// A todo carrying BOTH spellings must count once, not twice.
	plAttachTagToTodo(t, st, p.ID, canonicalID)
	tags, _ = st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	grouped = plFindTag(tags, "make-space")
	if grouped == nil || grouped.Count != 2 {
		t.Fatalf("a todo tagged with both spellings must count once (expected 2), got %#v", grouped)
	}

	// Name-based color write reaches the legacy row too.
	color := "#123456"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "make-space", &color); err != nil {
		t.Fatalf("SetViewerTagColorByName: %v", err)
	}
	var legacyColor string
	if err := st.db.QueryRowContext(ctx,
		`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, u1.ID, legacyID).Scan(&legacyColor); err != nil {
		t.Fatalf("expected viewer color on the legacy backing row: %v", err)
	}
	if legacyColor != color {
		t.Errorf("expected legacy row color %s, got %s", color, legacyColor)
	}

	// Delete-mine by canonical name removes u2's legacy row, not u1's canonical row.
	affected, err := st.DeleteMyTagByName(ctx, p.ID, u2.ID, "make-space")
	if err != nil {
		t.Fatalf("DeleteMyTagByName for legacy row: %v", err)
	}
	if len(affected) == 0 {
		t.Errorf("expected at least the current project in affected ids, got %v", affected)
	}
	var legacyRemaining, canonicalRemaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, legacyID).Scan(&legacyRemaining)
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, canonicalID).Scan(&canonicalRemaining)
	if legacyRemaining != 0 {
		t.Errorf("legacy row should be deleted by canonical name")
	}
	if canonicalRemaining != 1 {
		t.Errorf("another member's backing row must survive delete-mine")
	}
	_ = legacyTodoID
}

// TestProjectLabels_TemporaryBoardKeepsRowLevelProjection pins the exclusion of
// temporary boards from grouping: their entries must keep addressable tag IDs because
// their color and delete routes are still tag_id-based.
func TestProjectLabels_TemporaryBoardKeepsRowLevelProjection(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)

	// A creator-owned temporary board in full mode.
	p, err := st.CreateProject(ctx1, "Temp")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	expires := time.Now().UTC().Add(24 * time.Hour).UnixMilli()
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET expires_at = ? WHERE id = ?`, expires, p.ID); err != nil {
		t.Fatalf("make project temporary: %v", err)
	}
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	// Temporary boards create board-scoped tags, so add a personal "bug" row on top to
	// build the mixed group that grouping would collapse into a single tagId-less entry.
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	personalID := plInsertPersonalTagRow(t, st, u2.ID, "bug")
	plAttachTagToTodo(t, st, p.ID, personalID)

	tags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, false)
	if err != nil {
		t.Fatalf("listTagCounts: %v", err)
	}
	n := 0
	for _, tc := range tags {
		if tc.Name != "bug" {
			continue
		}
		n++
		if tc.TagID == 0 {
			t.Errorf("temporary-board entries must keep a real tagId, got 0")
		}
	}
	if n != 2 {
		t.Fatalf("expected two row-level 'bug' entries on a temporary board, got %d (%#v)", n, tags)
	}

	// The same data would collapse under the durable projection; that difference is
	// exactly why temporary boards must not be grouped.
	if groupedTags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true); err != nil {
		t.Fatalf("listTagCounts grouped: %v", err)
	} else if bug := plFindTag(groupedTags, "bug"); bug == nil || bug.TagID != 0 {
		t.Fatalf("expected the grouped projection to collapse to a tagId-less entry, got %#v", bug)
	}

	// Name-based writes are rejected outright on temporary boards.
	color := "#abcdef"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "bug", &color); !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for name-based color on a temporary board, got %v", err)
	}
	if _, err := st.DeleteMyTagByName(ctx, p.ID, u1.ID, "bug"); !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for name-based delete on a temporary board, got %v", err)
	}
}

// TestProjectLabels_NameWritesRequireMembership verifies that authentication alone is
// not enough: a signed-in non-member must not be able to reach another project's
// backing tag rows through the name-based write paths.
func TestProjectLabels_NameWritesRequireMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	outsider, _ := st.CreateUser(ctx, "outsider@example.com", "password", "Outsider")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	bugID := plTagRowID(t, st, "bug", u1.ID)

	color := "#abcdef"
	if err := st.SetViewerTagColorByName(ctx, p.ID, outsider.ID, "bug", &color); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for non-member color write, got %v", err)
	}
	var prefs int
	_ = st.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, outsider.ID, bugID).Scan(&prefs)
	if prefs != 0 {
		t.Errorf("non-member must not create a color preference on another project's tag row")
	}

	if _, err := st.DeleteMyTagByName(ctx, p.ID, outsider.ID, "bug"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for non-member delete, got %v", err)
	}

	// A viewer-role member may still set their own color.
	plAddMember(t, st, p.ID, outsider.ID, RoleViewer)
	if err := st.SetViewerTagColorByName(ctx, p.ID, outsider.ID, "bug", &color); err != nil {
		t.Errorf("viewer member should be able to set their own color: %v", err)
	}
}

func TestProjectLabels_ReadInclusionUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")

	// Unused board-scoped tag still lists.
	plInsertBoardScopedTag(t, st, p.ID, "unusedboard", nil)

	// Personal row linked only via project_tags (no todo) must NOT list.
	res, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES(?, 'orphan', ?)`, u1.ID, time.Now().UTC().UnixMilli())
	if err != nil {
		t.Fatalf("insert orphan tag: %v", err)
	}
	orphanID, _ := res.LastInsertId()
	if _, err := st.db.ExecContext(ctx, `INSERT INTO project_tags(project_id, tag_id, created_at) VALUES(?,?,?)`, p.ID, orphanID, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("link orphan project_tag: %v", err)
	}

	tags, _ := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if plFindTag(tags, "unusedboard") == nil {
		t.Errorf("unused board-scoped tag should still be listed")
	}
	if plFindTag(tags, "orphan") != nil {
		t.Errorf("personal tag linked only via project_tags (no todo) must not be listed")
	}
}

// plBoardTitles returns the titles of every todo the board returned, across lanes.
func plBoardTitles(cols map[string][]Todo) []string {
	var out []string
	for _, lane := range cols {
		for _, td := range lane {
			out = append(out, td.Title)
		}
	}
	sort.Strings(out)
	return out
}

func plTitlesOf(todos []Todo) []string {
	out := make([]string, 0, len(todos))
	for _, td := range todos {
		out = append(out, td.Title)
	}
	sort.Strings(out)
	return out
}

// TestProjectLabels_CanonicalFilterMatchesGroupedCount is the regression test for the
// chip/board disagreement: the grouped listing counts "make space" and "make-space" as
// one label, so every board filter path must return the todos behind both backing rows.
// A filter that resolved only the exact canonical row would render a board that
// contradicts the count on the chip the user just clicked.
func TestProjectLabels_CanonicalFilterMatchesGroupedCount(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	ctx2 := WithUserID(ctx, u2.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	// One todo on the canonical row, one on a legacy row stored as "make space".
	plNewTodo(t, st, ctx1, p.ID, "canonical", "make-space")
	plNewTodo(t, st, ctx2, p.ID, "legacy")
	legacyID := plInsertPersonalTagRow(t, st, u2.ID, "make space")
	plAttachTagToTodo(t, st, p.ID, legacyID)

	// A third todo on an unrelated tag must never appear under the filter.
	plNewTodo(t, st, ctx1, p.ID, "other", "bug")

	pc, err := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}

	chip := plFindTag(func() []TagCount {
		tags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
		if err != nil {
			t.Fatalf("listTagCounts: %v", err)
		}
		return tags
	}(), "make-space")
	if chip == nil || chip.Count != 2 {
		t.Fatalf("expected a grouped 'make-space' chip counting 2 todos, got %#v", chip)
	}

	want := []string{"canonical", "legacy"}

	// Filtering by the label the chip renders, and by the legacy spelling a stale
	// client may still send, must both resolve to the same two todos.
	for _, filter := range []string{"make-space", "make space"} {
		_, _, _, cols, err := st.GetBoard(ctx1, &pc, filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("GetBoard(%q): %v", filter, err)
		}
		if got := plBoardTitles(cols); !plEqualStrings(got, want) {
			t.Errorf("GetBoard(%q) returned %v, want %v", filter, got, want)
		}

		_, _, _, pagedCols, meta, err := st.GetBoardPaged(ctx1, &pc, filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"}, 10)
		if err != nil {
			t.Fatalf("GetBoardPaged(%q): %v", filter, err)
		}
		if got := plBoardTitles(pagedCols); !plEqualStrings(got, want) {
			t.Errorf("GetBoardPaged(%q) returned %v, want %v", filter, got, want)
		}
		if total := meta[DefaultColumnBacklog].TotalCount; total != 2 {
			t.Errorf("GetBoardPaged(%q) lane total = %d, want 2 (must match the chip count)", filter, total)
		}

		// Lane pagination path, used directly by the lane endpoint and by the
		// per-lane fallback above the board soft cap.
		items, _, _, err := st.ListTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, 10, math.MinInt64, 0, filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("ListTodosForBoardLane(%q): %v", filter, err)
		}
		if got := plTitlesOf(items); !plEqualStrings(got, want) {
			t.Errorf("ListTodosForBoardLane(%q) returned %v, want %v", filter, got, want)
		}

		count, err := st.CountTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("CountTodosForBoardLane(%q): %v", filter, err)
		}
		if count != 2 {
			t.Errorf("CountTodosForBoardLane(%q) = %d, want 2", filter, count)
		}
	}

	// Paging through the filtered lane must still see both spellings.
	page1, cursor, hasMore, err := st.ListTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, 1, math.MinInt64, 0, "make-space", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane page 1: %v", err)
	}
	if len(page1) != 1 || !hasMore {
		t.Fatalf("expected a first page of 1 with more to come, got %d items hasMore=%v", len(page1), hasMore)
	}
	afterRank, afterID := ParseLaneCursor(cursor)
	page2, _, _, err := st.ListTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, 1, afterRank, afterID, "make-space", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane page 2: %v", err)
	}
	if got := plTitlesOf(append(append([]Todo{}, page1...), page2...)); !plEqualStrings(got, want) {
		t.Errorf("paged lane returned %v across pages, want %v", got, want)
	}

	// A filter matching no backing row must return an empty board, never fall back
	// to the unfiltered query.
	_, _, _, emptyCols, err := st.GetBoard(ctx1, &pc, "no-such-tag", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard(no-such-tag): %v", err)
	}
	if got := plBoardTitles(emptyCols); len(got) != 0 {
		t.Errorf("unmatched filter must return no todos, got %v", got)
	}
	emptyCount, err := st.CountTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, "no-such-tag", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("CountTodosForBoardLane(no-such-tag): %v", err)
	}
	if emptyCount != 0 {
		t.Errorf("unmatched filter count = %d, want 0", emptyCount)
	}
}

// TestProjectLabels_TemporaryBoardFilterStaysRowLevel pins that temporary boards do not
// inherit durable TagGroupKey filter expansion. Their chips are still one entry per tag
// row, so each spelling selects only its own backing row — including when the chip is
// the raw legacy label "make space" (which must not be rewritten to "make-space" first).
func TestProjectLabels_TemporaryBoardFilterStaysRowLevel(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	ctx2 := WithUserID(ctx, u2.ID)
	p, _ := st.CreateProject(ctx1, "Temp")
	expires := time.Now().UTC().Add(24 * time.Hour).UnixMilli()
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET expires_at = ? WHERE id = ?`, expires, p.ID); err != nil {
		t.Fatalf("make project temporary: %v", err)
	}
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)

	plNewTodo(t, st, ctx1, p.ID, "canonical", "make-space")
	plNewTodo(t, st, ctx2, p.ID, "legacy")
	legacyID := plInsertPersonalTagRow(t, st, u2.ID, "make space")
	plAttachTagToTodo(t, st, p.ID, legacyID)

	pc, err := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}

	// Each chip spelling is its own filter on a temporary board.
	cases := []struct {
		filter string
		want   []string
	}{
		{filter: "make-space", want: []string{"canonical"}},
		{filter: "make space", want: []string{"legacy"}},
	}
	for _, tc := range cases {
		_, _, _, cols, err := st.GetBoard(ctx1, &pc, tc.filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("GetBoard(%q): %v", tc.filter, err)
		}
		if got := plBoardTitles(cols); !plEqualStrings(got, tc.want) {
			t.Errorf("GetBoard(%q) returned %v, want %v", tc.filter, got, tc.want)
		}

		_, _, _, pagedCols, meta, err := st.GetBoardPaged(ctx1, &pc, tc.filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"}, 10)
		if err != nil {
			t.Fatalf("GetBoardPaged(%q): %v", tc.filter, err)
		}
		if got := plBoardTitles(pagedCols); !plEqualStrings(got, tc.want) {
			t.Errorf("GetBoardPaged(%q) returned %v, want %v", tc.filter, got, tc.want)
		}
		if total := meta[DefaultColumnBacklog].TotalCount; total != 1 {
			t.Errorf("GetBoardPaged(%q) lane total = %d, want 1", tc.filter, total)
		}

		items, _, _, err := st.ListTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, 10, math.MinInt64, 0, tc.filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("ListTodosForBoardLane(%q): %v", tc.filter, err)
		}
		if got := plTitlesOf(items); !plEqualStrings(got, tc.want) {
			t.Errorf("ListTodosForBoardLane(%q) returned %v, want %v", tc.filter, got, tc.want)
		}

		count, err := st.CountTodosForBoardLane(ctx1, p.ID, DefaultColumnBacklog, tc.filter, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("CountTodosForBoardLane(%q): %v", tc.filter, err)
		}
		if count != 1 {
			t.Errorf("CountTodosForBoardLane(%q) = %d, want 1", tc.filter, count)
		}
	}
}

// TestProjectLabels_UncanonicalizableRowStaysAddressable covers the raw-name fallback:
// the listing shows a legacy row whose name cannot be canonicalized under its stored
// label, so that label must also work for filtering, coloring and deleting. Otherwise
// the entry advertises controls that every write path rejects.
func TestProjectLabels_UncanonicalizableRowStaysAddressable(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")

	// Lowercase (so the tags.name CHECK accepts it) but not canonicalizable: the "!"
	// fails the canonical name pattern, which is how such rows predate that rule.
	const legacyName = "wont-canonicalize!"
	if CanonicalizeTag(legacyName) != "" {
		t.Fatalf("test fixture must be uncanonicalizable, got %q", CanonicalizeTag(legacyName))
	}
	plNewTodo(t, st, ctx1, p.ID, "legacy")
	legacyID := plInsertPersonalTagRow(t, st, u1.ID, legacyName)
	plAttachTagToTodo(t, st, p.ID, legacyID)

	tags, err := st.listTagCounts(ctx1, p.ID, &u1.ID, nil, true)
	if err != nil {
		t.Fatalf("listTagCounts: %v", err)
	}
	entry := plFindTag(tags, legacyName)
	if entry == nil {
		t.Fatalf("expected the legacy row to list under its raw name, got %#v", tags)
	}
	if !entry.CanDeleteMine {
		t.Fatalf("owner should see a mine-scope delete for their own legacy row")
	}

	// Board filtering resolves the raw label instead of degrading to an unfiltered board.
	pc, err := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	_, _, _, cols, err := st.GetBoard(ctx1, &pc, legacyName, "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard(%q): %v", legacyName, err)
	}
	if got := plBoardTitles(cols); !plEqualStrings(got, []string{"legacy"}) {
		t.Errorf("filtering by the raw legacy label returned %v, want [legacy]", got)
	}

	// The color control the entry advertises must actually work.
	color := "#445566"
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, legacyName, &color); err != nil {
		t.Fatalf("SetViewerTagColorByName on a legacy row: %v", err)
	}
	var stored string
	if err := st.db.QueryRowContext(ctx,
		`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, u1.ID, legacyID).Scan(&stored); err != nil {
		t.Fatalf("expected a viewer color on the legacy row: %v", err)
	}
	if stored != color {
		t.Errorf("expected color %s on the legacy row, got %s", color, stored)
	}

	// So must the delete control.
	if _, err := st.DeleteMyTagByName(ctx, p.ID, u1.ID, legacyName); err != nil {
		t.Fatalf("DeleteMyTagByName on a legacy row: %v", err)
	}
	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, legacyID).Scan(&remaining)
	if remaining != 0 {
		t.Errorf("legacy row should be deleted by its displayed label")
	}

	// A blank label is still not a tag.
	if err := st.SetViewerTagColorByName(ctx, p.ID, u1.ID, "   ", &color); !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for a blank tag name, got %v", err)
	}
	if _, err := st.DeleteMyTagByName(ctx, p.ID, u1.ID, ""); !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for an empty tag name, got %v", err)
	}
}

func plEqualStrings(a, b []string) bool {
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
