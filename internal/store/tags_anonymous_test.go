package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// TestAnonymousBoard_CreateTodoWithTags tests creating a todo with tags on an anonymous board
func TestAnonymousBoard_CreateTodoWithTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create anonymous board (no userID in context)
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Verify board is anonymous
	if project.ExpiresAt == nil || project.CreatorUserID != nil {
		t.Fatalf("expected anonymous board, got ExpiresAt=%v, CreatorUserID=%v", project.ExpiresAt, project.CreatorUserID)
	}

	// Create todo with tags (no userID in context)
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Body:   "Test body",
		Tags:   []string{"bug", "urgent"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo with tags: %v", err)
	}

	// Verify tags were created
	if len(todo.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(todo.Tags))
	}
	expectedTags := []string{"bug", "urgent"}
	for i, expected := range expectedTags {
		if i >= len(todo.Tags) || todo.Tags[i] != expected {
			t.Errorf("expected tag %q at index %d, got %v", expected, i, todo.Tags)
		}
	}
}

// TestAnonymousBoard_UpdateTodoTags tests updating todo tags on an anonymous board
func TestAnonymousBoard_UpdateTodoTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create anonymous board
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create todo with tags
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Body:   "Test body",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Update tags
	updated, err := st.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
		Title: "Test Todo",
		Body:  "Test body",
		Tags:  []string{"bug", "feature"},
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("update todo tags: %v", err)
	}

	// Verify tags were updated
	if len(updated.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(updated.Tags))
	}
	expectedTags := []string{"bug", "feature"}
	for i, expected := range expectedTags {
		if i >= len(updated.Tags) || updated.Tags[i] != expected {
			t.Errorf("expected tag %q at index %d, got %v", expected, i, updated.Tags)
		}
	}
}

// TestAnonymousBoard_TagIsolation tests that tags are isolated per anonymous board
func TestAnonymousBoard_TagIsolation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create two anonymous boards
	board1, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create board 1: %v", err)
	}
	board2, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create board 2: %v", err)
	}

	// Create todo with tag "bug" on board 1
	_, err = st.CreateTodo(ctx, board1.ID, CreateTodoInput{
		Title:  "Bug on Board 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo on board 1: %v", err)
	}

	// Create todo with tag "bug" on board 2
	_, err = st.CreateTodo(ctx, board2.ID, CreateTodoInput{
		Title:  "Bug on Board 2",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo on board 2: %v", err)
	}

	// Verify board 1 has all default tags (including "bug")
	tags1, err := st.ListBoardTagsForProject(ctx, board1.ID)
	if err != nil {
		t.Fatalf("list tags for board 1: %v", err)
	}
	// Should have 20 default tags (bug is one of them)
	if len(tags1) != 20 {
		t.Errorf("expected board 1 to have 20 default tags, got %d", len(tags1))
	}
	// Verify "bug" tag exists
	hasBug := false
	for _, tag := range tags1 {
		if tag.Name == "bug" {
			hasBug = true
			break
		}
	}
	if !hasBug {
		t.Errorf("expected board 1 to have 'bug' tag in default tags")
	}

	// Verify board 2 has all default tags (including "bug")
	tags2, err := st.ListBoardTagsForProject(ctx, board2.ID)
	if err != nil {
		t.Fatalf("list tags for board 2: %v", err)
	}
	// Should have 20 default tags (bug is one of them)
	if len(tags2) != 20 {
		t.Errorf("expected board 2 to have 20 default tags, got %d", len(tags2))
	}
	// Verify "bug" tag exists
	hasBug = false
	for _, tag := range tags2 {
		if tag.Name == "bug" {
			hasBug = true
			break
		}
	}
	if !hasBug {
		t.Errorf("expected board 2 to have 'bug' tag in default tags")
	}

	// Verify tags are actually different (different IDs, different project_ids)
	// Query tags directly to verify isolation
	var count int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT id) 
FROM tags 
WHERE name = 'bug' AND user_id IS NULL`).Scan(&count)
	if err != nil {
		t.Fatalf("count bug tags: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 separate 'bug' tags (one per board), got %d", count)
	}
}

// TestAnonymousBoard_TagColors tests that board-scoped tag colors persist
func TestAnonymousBoard_TagColors(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create anonymous board
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create todo with tag
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Get tag ID
	var tagID int64
	err = st.db.QueryRowContext(ctx, `
SELECT id FROM tags WHERE project_id = ? AND name = 'bug' AND user_id IS NULL`, project.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("get tag ID: %v", err)
	}

	// Update tag color (board-wide color, stored in tags.color)
	// Anonymous users can update board-scoped tag colors without authentication
	color := "#FF0000"
	err = st.UpdateTagColorForProject(ctx, project.ID, nil, "bug", &color, true)
	if err != nil {
		t.Fatalf("update tag color: %v", err)
	}

	// Verify color persists in tags table
	var storedColor sql.NullString
	err = st.db.QueryRowContext(ctx, `SELECT color FROM tags WHERE id = ?`, tagID).Scan(&storedColor)
	if err != nil {
		t.Fatalf("get tag color: %v", err)
	}
	if !storedColor.Valid || storedColor.String != color {
		t.Errorf("expected color %q, got %v", color, storedColor)
	}

	// Verify color appears in listTagCounts
	// Note: listTagCounts now returns all board-scoped tags (including default tags)
	tags, err := st.listTagCounts(ctx, project.ID, nil, nil)
	if err != nil {
		t.Fatalf("list tag counts: %v", err)
	}
	// Should have all 20 default tags, but only "bug" has been used (count > 0)
	if len(tags) != 20 {
		t.Fatalf("expected 20 tags (all default tags), got %d", len(tags))
	}
	// Find the "bug" tag and verify its color
	var bugTag *TagCount
	for i := range tags {
		if tags[i].Name == "bug" {
			bugTag = &tags[i]
			break
		}
	}
	if bugTag == nil {
		t.Fatalf("bug tag not found in tags")
	}
	if bugTag.Color == nil || *bugTag.Color != color {
		t.Errorf("expected bug tag color %q, got %v", color, bugTag.Color)
	}
	// listTagCounts is tag_id-driven: each row has TagID
	if bugTag.TagID == 0 {
		t.Error("expected bug tag to have non-zero TagID")
	}
	// Anonymous viewer: no canDelete for project-scoped (no auth)
	if bugTag.CanDelete {
		t.Error("expected CanDelete false when viewer is nil for project-scoped tag")
	}
}

// TestUpdateTagColor_RejectsInvalidColor tests that UpdateTagColor returns ErrValidation for invalid colors
func TestUpdateTagColor_RejectsInvalidColor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "T", Tags: []string{"bug"}, ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	tagID, err := st.GetBoardScopedTagIDByName(ctx, project.ID, "bug")
	if err != nil {
		t.Fatalf("get tag ID: %v", err)
	}

	invalidColors := []string{"red", "#gggggg", "#abc", "#ff0000\");}</style><script>alert(1)</script>"}
	for _, c := range invalidColors {
		err = st.UpdateTagColor(ctx, nil, tagID, &c)
		if err == nil {
			t.Errorf("expected ErrValidation for color %q, got nil", c)
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("expected ErrValidation for color %q, got: %v", c, err)
		}
	}
}

// TestAnonymousBoard_UpdateUserOwnedTagColorFails tests that anonymous users cannot update user-owned tag colors
func TestAnonymousBoard_UpdateUserOwnedTagColorFails(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create authenticated user and project
	user, err := st.CreateUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ctxWithUser := WithUserID(ctx, user.ID)

	// Create durable project
	durableProject, err := st.CreateProject(ctxWithUser, "Durable Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create todo with user-owned tag
	_, err = st.CreateTodo(ctxWithUser, durableProject.ID, CreateTodoInput{
		Title:  "Test Todo",
		Tags:   []string{"user-tag"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Anonymous user attempts to update user-owned tag color (should fail)
	color := "#FF0000"
	err = st.UpdateTagColorForProject(ctx, durableProject.ID, nil, "user-tag", &color, false)
	if err == nil {
		t.Fatalf("expected error when anonymous user updates user-owned tag color, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") && !strings.Contains(err.Error(), "authentication") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}

// TestAnonymousBoard_TagDeletion tests deleting board-scoped tags
func TestAnonymousBoard_TagDeletion(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create anonymous board
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create todo with tag
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Get tag ID
	var tagID int64
	err = st.db.QueryRowContext(ctx, `
SELECT id FROM tags WHERE project_id = ? AND name = 'bug' AND user_id IS NULL`, project.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("get tag ID: %v", err)
	}

	// Atomic delete: delete tag while referenced - should succeed (todo_tags removed then tag)
	err = st.DeleteTag(ctx, 0, tagID, true) // userID=0 for anonymous board
	if err != nil {
		t.Errorf("expected successful atomic deletion even with references, got: %v", err)
	}

	// Verify tag was deleted
	var count int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&count)
	if err != nil {
		t.Fatalf("check tag deleted: %v", err)
	}
	if count != 0 {
		t.Errorf("expected tag to be deleted, but it still exists")
	}
	// Verify todo_tags no longer reference this tag
	var refCount int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&refCount)
	if err != nil {
		t.Fatalf("check todo_tags: %v", err)
	}
	if refCount != 0 {
		t.Errorf("expected no todo_tags rows for deleted tag, got %d", refCount)
	}
}

// TestDurableBoard_TagsRequireAuth tests that durable boards still require auth for tags
func TestDurableBoard_TagsRequireAuth(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Bootstrap user
	user, err := st.BootstrapUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}

	// Create durable project (with userID in context)
	ctxWithUser := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ctxWithUser, "Durable Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Try to create todo with tags (no userID in context - simulating anonymous access) - should fail
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err == nil {
		t.Errorf("expected error when creating todo with tags without userID on durable board, got nil")
	}
	if !strings.Contains(err.Error(), "userID required") && !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected 'userID required' or 'unauthorized' error, got: %v", err)
	}
}

// TestAuthenticatedUser_ViewsAnonymousBoard tests authenticated user viewing anonymous board
func TestAuthenticatedUser_ViewsAnonymousBoard(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Bootstrap user
	user, err := st.BootstrapUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}

	// Create anonymous board (no userID context)
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create todo with board-scoped tag (no userID)
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:  "Anonymous Todo",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Authenticated user views board (with userID context)
	ctxWithUser := WithUserID(ctx, user.ID)
	pc, _ := st.GetProjectContextForRead(ctxWithUser, project.ID, ModeFull)
	_, tags, _, cols, err := st.GetBoard(ctxWithUser, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("get board: %v", err)
	}

	// Verify board-scoped tags are visible
	// Note: Anonymous boards now have 20 default tags (including "bug")
	if len(tags) != 20 {
		t.Errorf("expected 20 default tags, got %d", len(tags))
	}
	// Verify "bug" tag is present
	var bugFound bool
	for _, tag := range tags {
		if tag.Name == "bug" {
			bugFound = true
			break
		}
	}
	if !bugFound {
		t.Errorf("expected 'bug' tag in default tags")
	}

	// Verify todos have the tag
	backlog := cols[DefaultColumnBacklog]
	if len(backlog) != 1 {
		t.Fatalf("expected 1 todo in backlog, got %d", len(backlog))
	}
	if len(backlog[0].Tags) != 1 || backlog[0].Tags[0] != "bug" {
		t.Errorf("expected todo to have [bug] tag, got %v", backlog[0].Tags)
	}
}

// TestTagColorResolution tests color resolution for both tag types
func TestTagColorResolution(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Bootstrap user
	user, err := st.BootstrapUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	ctxWithUser := WithUserID(ctx, user.ID)

	// Create anonymous board with board-scoped tag
	anonProject, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}
	_, err = st.CreateTodo(ctx, anonProject.ID, CreateTodoInput{
		Title:  "Anonymous Todo",
		Tags:   []string{"anon-tag"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("create anonymous todo: %v", err)
	}

	// Set color on board-scoped tag (directly in tags.color)
	var anonTagID int64
	err = st.db.QueryRowContext(ctx, `
SELECT id FROM tags WHERE project_id = ? AND name = 'anon-tag' AND user_id IS NULL`, anonProject.ID).Scan(&anonTagID)
	if err != nil {
		t.Fatalf("get anon tag ID: %v", err)
	}
	boardColor := "#FF0000"
	_, err = st.db.ExecContext(ctx, `UPDATE tags SET color = ? WHERE id = ?`, boardColor, anonTagID)
	if err != nil {
		t.Fatalf("set board tag color: %v", err)
	}

	// Verify board-scoped tag color in listTagCounts
	// Note: Anonymous boards now have 20 default tags + any user-created tags
	anonTags, err := st.listTagCounts(ctx, anonProject.ID, nil, nil)
	if err != nil {
		t.Fatalf("list anon tags: %v", err)
	}
	// Should have 20 default tags + 1 user-created "anon-tag"
	if len(anonTags) != 21 {
		t.Fatalf("expected 21 tags (20 default + 1 custom), got %d", len(anonTags))
	}
	// Find anon-tag and verify its color
	var anonTagFound bool
	for _, tag := range anonTags {
		if tag.Name == "anon-tag" {
			anonTagFound = true
			if tag.Color == nil || *tag.Color != boardColor {
				t.Errorf("expected anon-tag color %q, got %v", boardColor, tag.Color)
			}
			break
		}
	}
	if !anonTagFound {
		t.Errorf("anon-tag not found in tags")
	}

	// Create durable project with user-owned tag
	durableProject, err := st.CreateProject(ctxWithUser, "Durable Project")
	if err != nil {
		t.Fatalf("create durable project: %v", err)
	}
	_, err = st.CreateTodo(ctxWithUser, durableProject.ID, CreateTodoInput{
		Title:  "Durable Todo",
		Tags:   []string{"user-tag"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("create durable todo: %v", err)
	}

	// Set color preference for user-owned tag (in user_tag_colors)
	userColor := "#00FF00"
	var userTagID int64
	err = st.db.QueryRowContext(ctx, `
SELECT id FROM tags WHERE user_id = ? AND name = 'user-tag'`, user.ID).Scan(&userTagID)
	if err != nil {
		t.Fatalf("get user tag ID: %v", err)
	}
	err = st.UpdateTagColor(ctxWithUser, &user.ID, userTagID, &userColor)
	if err != nil {
		t.Fatalf("set user tag color: %v", err)
	}

	// Verify user-owned tag color in listTagCounts
	durableTags, err := st.listTagCounts(ctx, durableProject.ID, &user.ID, nil)
	if err != nil {
		t.Fatalf("list durable tags: %v", err)
	}
	if len(durableTags) != 1 {
		t.Fatalf("expected 1 durable tag, got %d", len(durableTags))
	}
	if durableTags[0].Color == nil || *durableTags[0].Color != userColor {
		t.Errorf("expected user color %q, got %v", userColor, durableTags[0].Color)
	}
}

// TestDurableBoard_UserOwnedTagsUnchanged tests that user-owned tag behavior is unchanged
func TestDurableBoard_UserOwnedTagsUnchanged(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Bootstrap user
	user, err := st.BootstrapUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	ctxWithUser := WithUserID(ctx, user.ID)

	// Create durable project
	project, err := st.CreateProject(ctxWithUser, "Durable Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Create todo with user-owned tags
	todo, err := st.CreateTodo(ctxWithUser, project.ID, CreateTodoInput{
		Title:  "Test Todo",
		Tags:   []string{"bug", "feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	// Verify tags are user-owned (user_id IS NOT NULL, project_id IS NULL)
	var count int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tags WHERE user_id = ? AND name IN ('bug', 'feature') AND project_id IS NULL`, user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count user tags: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 user-owned tags, got %d", count)
	}

	// Verify tags appear on todo
	if len(todo.Tags) != 2 {
		t.Errorf("expected 2 tags on todo, got %d", len(todo.Tags))
	}

	// Verify tags are reusable across projects
	project2, err := st.CreateProject(ctxWithUser, "Project 2")
	if err != nil {
		t.Fatalf("create project 2: %v", err)
	}
	_, err = st.CreateTodo(ctxWithUser, project2.ID, CreateTodoInput{
		Title:  "Todo in Project 2",
		Tags:   []string{"bug"}, // Reuse existing user-owned tag
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("create todo in project 2: %v", err)
	}

	// Verify still only 2 user-owned tags (reused, not duplicated)
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tags WHERE user_id = ?`, user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("count user tags after reuse: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 user-owned tags (reused), got %d", count)
	}
}

// TestAnonymousBoard_DefaultTagsAutoPopulated tests that anonymous boards are auto-populated with default tags
func TestAnonymousBoard_DefaultTagsAutoPopulated(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create anonymous board
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Verify default tags were created
	var tagCount int
	err = st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tags 
		WHERE project_id = ? AND user_id IS NULL`, project.ID).Scan(&tagCount)
	if err != nil {
		t.Fatalf("count tags: %v", err)
	}

	expectedCount := 20 // Number of default tags
	if tagCount != expectedCount {
		t.Errorf("expected %d default tags, got %d", expectedCount, tagCount)
	}

	// Verify all tags have colors
	var coloredTagCount int
	err = st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tags 
		WHERE project_id = ? AND user_id IS NULL AND color IS NOT NULL`, project.ID).Scan(&coloredTagCount)
	if err != nil {
		t.Fatalf("count colored tags: %v", err)
	}

	expectedColoredCount := 20 // All tags have colors
	if coloredTagCount != expectedColoredCount {
		t.Errorf("expected %d colored tags, got %d", expectedColoredCount, coloredTagCount)
	}

	// Verify specific tag colors
	testCases := []struct {
		tagName       string
		expectedColor string
	}{
		{"bug", "#FF0000"},
		{"feature", "#00FF00"},
		{"enhancement", "#800080"},
		{"testing", "#00CED1"},
		{"devops", "#708090"},
		{"blocking", "#FF4500"},
		{"regression", "#8B0000"},
	}

	for _, tc := range testCases {
		var color sql.NullString
		err = st.db.QueryRowContext(ctx, `
			SELECT color FROM tags 
			WHERE project_id = ? AND user_id IS NULL AND name = ?`,
			project.ID, tc.tagName).Scan(&color)
		if err != nil {
			t.Fatalf("get tag %q color: %v", tc.tagName, err)
		}

		if !color.Valid || color.String != tc.expectedColor {
			t.Errorf("tag %q expected color %q, got %v", tc.tagName, tc.expectedColor, color)
		}
	}
}

// TestAuthenticatedProject_NoDefaultTags verifies that creating a durable project does NOT auto-populate default tags.
// This test catches accidental reuse of default tag logic for authenticated projects.
func TestAuthenticatedProject_NoDefaultTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create authenticated user
	user, err := st.CreateUser(ctx, "test@example.com", "password", "Test User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	ctxWithUser := WithUserID(ctx, user.ID)

	// Create durable project (authenticated)
	project, err := st.CreateProject(ctxWithUser, "Durable Project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Verify NO default tags were created
	var tagCount int
	err = st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tags 
		WHERE project_id = ?`, project.ID).Scan(&tagCount)
	if err != nil {
		t.Fatalf("count tags: %v", err)
	}

	if tagCount != 0 {
		t.Errorf("expected 0 tags on durable project, got %d (default tags should NOT be created)", tagCount)
	}
}
