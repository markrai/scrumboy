package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestUserOwnedTags_CreationRequiresUserAccess(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create user and project
	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todo with tags - should succeed (user owns project)
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Verify tag was created with user_id
	var tagUserID int64
	err = st.db.QueryRowContext(ctx, `
SELECT user_id FROM tags WHERE name = 'bug'
`).Scan(&tagUserID)
	if err != nil {
		t.Fatalf("Query tag: %v", err)
	}
	if tagUserID != user.ID {
		t.Errorf("Expected tag user_id=%d, got %d", user.ID, tagUserID)
	}

	// Verify todo has tag
	if len(todo.Tags) != 1 || todo.Tags[0] != "bug" {
		t.Errorf("Expected todo to have tag 'bug', got %v", todo.Tags)
	}
}

func TestUserOwnedTags_NamesNormalizedToLowercase(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todo with mixed-case tag
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"Bug", "URGENT", "feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Verify tags are normalized to lowercase
	var names []string
	rows, err := st.db.QueryContext(ctx, `SELECT name FROM tags WHERE user_id = ? ORDER BY name`, user.ID)
	if err != nil {
		t.Fatalf("Query tags: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("Scan tag: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Rows error: %v", err)
	}

	expected := []string{"bug", "feature", "urgent"}
	if len(names) != len(expected) {
		t.Errorf("Expected %d tags, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range expected {
		if i >= len(names) || names[i] != name {
			t.Errorf("Expected tag[%d]=%q, got %v", i, name, names)
		}
	}
}

func TestUserOwnedTags_UniqueConstraintPreventsDuplicates(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todo with tag "bug"
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 1: %v", err)
	}

	// Create another todo with same tag "bug" (normalized)
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test 2",
		Tags:   []string{"Bug"}, // Different case, but normalized to "bug"
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 2: %v", err)
	}

	// Verify only one tag exists for this user
	var count int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tags WHERE user_id = ? AND name = 'bug'
`, user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 tag 'bug' for user, got %d", count)
	}
}

func TestUserOwnedTags_CanAttachToMultipleProjects(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p1, err := st.CreateProject(ctx, "Project 1")
	if err != nil {
		t.Fatalf("CreateProject 1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "Project 2")
	if err != nil {
		t.Fatalf("CreateProject 2: %v", err)
	}

	// Create tag in p1
	_, err = st.CreateTodo(ctx, p1.ID, CreateTodoInput{
		Title:  "Test 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p1: %v", err)
	}

	// Use same tag in p2
	_, err = st.CreateTodo(ctx, p2.ID, CreateTodoInput{
		Title:  "Test 2",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p2: %v", err)
	}

	// Verify tag is linked to both projects via project_tags
	var count1, count2 int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM project_tags pt
JOIN tags t ON pt.tag_id = t.id
WHERE pt.project_id = ? AND t.user_id = ? AND t.name = 'bug'
`, p1.ID, user.ID).Scan(&count1)
	if err != nil {
		t.Fatalf("Count project_tags p1: %v", err)
	}
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM project_tags pt
JOIN tags t ON pt.tag_id = t.id
WHERE pt.project_id = ? AND t.user_id = ? AND t.name = 'bug'
`, p2.ID, user.ID).Scan(&count2)
	if err != nil {
		t.Fatalf("Count project_tags p2: %v", err)
	}

	if count1 != 1 {
		t.Errorf("Expected 1 project_tags entry for p1, got %d", count1)
	}
	if count2 != 1 {
		t.Errorf("Expected 1 project_tags entry for p2, got %d", count2)
	}

	// Verify only one tag exists (same tag reused)
	var tagCount int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM tags WHERE user_id = ? AND name = 'bug'
`, user.ID).Scan(&tagCount)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if tagCount != 1 {
		t.Errorf("Expected 1 tag 'bug' for user, got %d", tagCount)
	}
}

func TestUserOwnedTags_BoardShowsAllTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create two users
	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	// Create project owned by user1
	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Add user2 as member
	nowMs := time.Now().UTC().UnixMilli()
	_, err = st.db.ExecContext(ctx, `
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, p.ID, user2.ID, RoleMaintainer, nowMs)
	if err != nil {
		t.Fatalf("Add member: %v", err)
	}

	// User1 creates todo with tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Todo 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user1: %v", err)
	}

	// User2 creates todo with tag "feature"
	ctx2 := WithUserID(ctx, user2.ID)
	_, err = st.CreateTodo(ctx2, p.ID, CreateTodoInput{
		Title:  "Todo 2",
		Tags:   []string{"feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user2: %v", err)
	}

	// View board as user1 - should see ALL tags (both "bug" and "feature")
	pc, _ := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	_, tags, _, _, err := st.GetBoard(ctx1, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	tagNames := make(map[string]bool)
	for _, tc := range tags {
		tagNames[tc.Name] = true
	}

	if !tagNames["bug"] {
		t.Error("Expected to see 'bug' tag on board")
	}
	if !tagNames["feature"] {
		t.Error("Expected to see 'feature' tag on board")
	}
	if len(tags) != 2 {
		t.Errorf("Expected 2 tags on board, got %d: %v", len(tags), tags)
	}
}

func TestUserOwnedTags_AutocompleteShowsUserTagsOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create two users
	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	// Create project
	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Add user2 as member
	nowMs := time.Now().UTC().UnixMilli()
	_, err = st.db.ExecContext(ctx, `
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, p.ID, user2.ID, RoleMaintainer, nowMs)
	if err != nil {
		t.Fatalf("Add member: %v", err)
	}

	// User1 creates todo with tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Todo 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user1: %v", err)
	}

	// User2 creates todo with tag "feature"
	ctx2 := WithUserID(ctx, user2.ID)
	_, err = st.CreateTodo(ctx2, p.ID, CreateTodoInput{
		Title:  "Todo 2",
		Tags:   []string{"feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user2: %v", err)
	}

	// User1's autocomplete should only show user1's tags
	tags1, err := st.ListUserTagsForProject(ctx1, user1.ID, p.ID)
	if err != nil {
		t.Fatalf("ListUserTagsForProject user1: %v", err)
	}

	tagNames1 := make(map[string]bool)
	for _, tag := range tags1 {
		tagNames1[tag.Name] = true
	}

	if !tagNames1["bug"] {
		t.Error("Expected user1 to see 'bug' tag in autocomplete")
	}
	if tagNames1["feature"] {
		t.Error("Expected user1 NOT to see 'feature' tag in autocomplete (owned by user2)")
	}

	// User2's autocomplete should only show user2's tags
	tags2, err := st.ListUserTagsForProject(ctx2, user2.ID, p.ID)
	if err != nil {
		t.Fatalf("ListUserTagsForProject user2: %v", err)
	}

	tagNames2 := make(map[string]bool)
	for _, tag := range tags2 {
		tagNames2[tag.Name] = true
	}

	if !tagNames2["feature"] {
		t.Error("Expected user2 to see 'feature' tag in autocomplete")
	}
	if tagNames2["bug"] {
		t.Error("Expected user2 NOT to see 'bug' tag in autocomplete (owned by user1)")
	}
}

func TestUserOwnedTags_ColorPerViewer(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// User1 creates tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Get tag ID
	var tagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user1.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("Get tag ID: %v", err)
	}

	// User1 sets color preference
	color1 := "#FF0000"
	err = st.UpdateTagColor(ctx1, &user1.ID, tagID, &color1)
	if err != nil {
		t.Fatalf("UpdateTagColor user1: %v", err)
	}

	// User2 sets different color preference for same tag
	ctx2 := WithUserID(ctx, user2.ID)
	color2 := "#00FF00"
	err = st.UpdateTagColor(ctx2, &user2.ID, tagID, &color2)
	if err != nil {
		t.Fatalf("UpdateTagColor user2: %v", err)
	}

	// Verify user1 sees their color
	color, err := st.GetTagColor(ctx1, user1.ID, tagID)
	if err != nil {
		t.Fatalf("GetTagColor user1: %v", err)
	}
	if color == nil || *color != color1 {
		t.Errorf("Expected user1 color %q, got %v", color1, color)
	}

	// Verify user2 sees their color
	color, err = st.GetTagColor(ctx2, user2.ID, tagID)
	if err != nil {
		t.Fatalf("GetTagColor user2: %v", err)
	}
	if color == nil || *color != color2 {
		t.Errorf("Expected user2 color %q, got %v", color2, color)
	}
}

func TestUserOwnedTags_DeleteRequiresZeroReferences(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todo with tag
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Get tag ID
	var tagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("Get tag ID: %v", err)
	}

	// Atomic delete: delete tag while referenced - should succeed
	err = st.DeleteTag(ctx, user.ID, tagID, false)
	if err != nil {
		t.Errorf("Expected DeleteTag to succeed (atomic delete removes references), got %v", err)
	}

	// Verify tag is deleted
	var count int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&count)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected tag to be deleted, but %d tags found", count)
	}
}

func TestUserOwnedTags_OwnershipPermanent(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctx, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create tag in project
	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Verify tag exists and is owned by user
	var tagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("Get tag: %v", err)
	}

	// Remove user from project (simulate leaving project)
	_, err = st.db.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, p.ID, user.ID)
	if err != nil {
		t.Fatalf("Remove member: %v", err)
	}

	// Verify tag still exists and is still owned by user (ownership is permanent)
	var count int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE user_id = ? AND name = 'bug'`, user.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected tag to still exist after leaving project, but found %d tags", count)
	}

	// User can still use their tag in other projects
	p2, err := st.CreateProject(ctx, "Project 2")
	if err != nil {
		t.Fatalf("CreateProject 2: %v", err)
	}
	_, err = st.CreateTodo(ctx, p2.ID, CreateTodoInput{
		Title:  "Test 2",
		Tags:   []string{"bug"}, // Reuse same tag
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p2: %v", err)
	}

	// Verify same tag ID is used (reused, not recreated)
	var tagID2 int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user.ID).Scan(&tagID2)
	if err != nil {
		t.Fatalf("Get tag 2: %v", err)
	}
	if tagID != tagID2 {
		t.Errorf("Expected same tag ID to be reused, got %d vs %d", tagID, tagID2)
	}
}

func TestUserOwnedTags_UsersCanOnlyAttachOwnTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Add user2 as member
	nowMs := time.Now().UTC().UnixMilli()
	_, err = st.db.ExecContext(ctx, `
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, p.ID, user2.ID, RoleMaintainer, nowMs)
	if err != nil {
		t.Fatalf("Add member: %v", err)
	}

	// User1 creates tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Todo 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user1: %v", err)
	}

	// User2 creates todo - can only use their own tags, so "bug" will create a new tag for user2
	ctx2 := WithUserID(ctx, user2.ID)
	_, err = st.CreateTodo(ctx2, p.ID, CreateTodoInput{
		Title:  "Todo 2",
		Tags:   []string{"bug"}, // Same name, but will be user2's tag
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user2: %v", err)
	}

	// Verify two separate tags exist (one per user)
	var count int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE name = 'bug'`).Scan(&count)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 tags with name 'bug' (one per user), got %d", count)
	}

	// Verify each user owns their tag
	var user1TagID, user2TagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user1.ID).Scan(&user1TagID)
	if err != nil {
		t.Fatalf("Get user1 tag: %v", err)
	}
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user2.ID).Scan(&user2TagID)
	if err != nil {
		t.Fatalf("Get user2 tag: %v", err)
	}
	if user1TagID == user2TagID {
		t.Error("Expected different tag IDs for user1 and user2")
	}
}

func TestUserOwnedTags_FilteringMatchesByNameAcrossOwners(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Add user2 as member
	nowMs := time.Now().UTC().UnixMilli()
	_, err = st.db.ExecContext(ctx, `
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, p.ID, user2.ID, RoleMaintainer, nowMs)
	if err != nil {
		t.Fatalf("Add member: %v", err)
	}

	// User1 creates todo with tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Todo 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user1: %v", err)
	}

	// User2 creates todo with tag "bug" (their own tag)
	ctx2 := WithUserID(ctx, user2.ID)
	_, err = st.CreateTodo(ctx2, p.ID, CreateTodoInput{
		Title:  "Todo 2",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user2: %v", err)
	}

	// Filter by "bug" - should match both todos (filtering by name, not owner)
	pc, _ := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx1, &pc, "bug", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	if len(cols[DefaultColumnBacklog]) != 2 {
		t.Errorf("Expected 2 todos when filtering by 'bug', got %d", len(cols[DefaultColumnBacklog]))
	}
}

func TestUserOwnedTags_ProjectTagsIsProjectWide(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Add user2 as member
	nowMs := time.Now().UTC().UnixMilli()
	_, err = st.db.ExecContext(ctx, `
INSERT INTO project_members(project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, p.ID, user2.ID, RoleMaintainer, nowMs)
	if err != nil {
		t.Fatalf("Add member: %v", err)
	}

	// User1 creates todo with tag "bug"
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Todo 1",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user1: %v", err)
	}

	// User2 creates todo with tag "feature"
	ctx2 := WithUserID(ctx, user2.ID)
	_, err = st.CreateTodo(ctx2, p.ID, CreateTodoInput{
		Title:  "Todo 2",
		Tags:   []string{"feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo user2: %v", err)
	}

	// Verify project_tags contains both tags (project-wide tag set)
	var count int
	err = st.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM project_tags WHERE project_id = ?
`, p.ID).Scan(&count)
	if err != nil {
		t.Fatalf("Count project_tags: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 tags in project_tags (project-wide), got %d", count)
	}
}

func TestUserOwnedTags_CannotDeleteOtherUsersTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser 1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser 2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	p, err := st.CreateProject(ctx1, "Test Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// User1 creates tag
	_, err = st.CreateTodo(ctx1, p.ID, CreateTodoInput{
		Title:  "Test",
		Tags:   []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Get tag ID
	var tagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'bug'`, user1.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("Get tag ID: %v", err)
	}

	// User2 tries to delete user1's tag - should fail
	ctx2 := WithUserID(ctx, user2.ID)
	err = st.DeleteTag(ctx2, user2.ID, tagID, false)
	if err == nil {
		t.Error("Expected DeleteTag to fail (user2 cannot delete user1's tag), but it succeeded")
	}
	// Error should contain "not owned by user" or be ErrUnauthorized
	if !strings.Contains(err.Error(), "not owned by user") && err != ErrUnauthorized {
		t.Errorf("Expected error about ownership or ErrUnauthorized, got %v", err)
	}

	// User1 can delete their own tag (if no references)
	// First delete the todo
	pc, _ := st.GetProjectContextForRead(ctx1, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx1, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	if len(cols[DefaultColumnBacklog]) == 0 {
		t.Fatal("Expected at least one todo")
	}
	todoID := cols[DefaultColumnBacklog][0].ID
	err = st.DeleteTodo(ctx1, todoID, ModeFull)
	if err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}

	// Now user1 can delete their tag
	err = st.DeleteTag(ctx1, user1.ID, tagID, false)
	if err != nil {
		t.Errorf("Expected DeleteTag to succeed (user1 deleting own tag), got %v", err)
	}
}

func TestUserOwnedTags_MaintainerOverrideRequiresAll(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user1, err := st.BootstrapUser(ctx, "user1@example.com", "password", "User 1")
	if err != nil {
		t.Fatalf("BootstrapUser user1: %v", err)
	}
	user2, err := st.CreateUser(ctx, "user2@example.com", "password", "User 2")
	if err != nil {
		t.Fatalf("CreateUser user2: %v", err)
	}

	ctx1 := WithUserID(ctx, user1.ID)
	ctx2 := WithUserID(ctx, user2.ID)

	p1, err := st.CreateProject(ctx1, "Project 1")
	if err != nil {
		t.Fatalf("CreateProject 1: %v", err)
	}
	p2, err := st.CreateProject(ctx1, "Project 2")
	if err != nil {
		t.Fatalf("CreateProject 2: %v", err)
	}

	// User1 creates a tag used in both projects
	_, err = st.CreateTodo(ctx1, p1.ID, CreateTodoInput{Title: "P1 Todo", Tags: []string{"shared-tag"}, ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p1: %v", err)
	}
	_, err = st.CreateTodo(ctx1, p2.ID, CreateTodoInput{Title: "P2 Todo", Tags: []string{"shared-tag"}, ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p2: %v", err)
	}

	var tagID int64
	err = st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'shared-tag'`, user1.ID).Scan(&tagID)
	if err != nil {
		t.Fatalf("Get tag ID: %v", err)
	}

	// Add user2 as maintainer of p1 only (not p2)
	err = st.AddProjectMember(ctx1, user1.ID, p1.ID, user2.ID, RoleMaintainer)
	if err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	// User2 tries to delete user1's tag - should fail (maintainer of p1 but not p2)
	err = st.DeleteTag(ctx2, user2.ID, tagID, false)
	if err == nil {
		t.Error("Expected DeleteTag to fail (user2 is maintainer of p1 but not p2), but it succeeded")
	}
	if !strings.Contains(err.Error(), "not owned by user") && err != ErrUnauthorized {
		t.Errorf("Expected unauthorized error, got %v", err)
	}

	// Verify tag still exists
	var count int
	err = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&count)
	if err != nil {
		t.Fatalf("Count tags: %v", err)
	}
	if count == 0 {
		t.Error("Expected tag to still exist after failed delete")
	}
}

func TestUserOwnedTags_ListUserTags(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "user@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)

	p1, err := st.CreateProject(ctx, "Project 1")
	if err != nil {
		t.Fatalf("CreateProject 1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "Project 2")
	if err != nil {
		t.Fatalf("CreateProject 2: %v", err)
	}

	// Create tags in both projects
	_, err = st.CreateTodo(ctx, p1.ID, CreateTodoInput{
		Title:  "Test 1",
		Tags:   []string{"bug", "urgent"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p1: %v", err)
	}
	_, err = st.CreateTodo(ctx, p2.ID, CreateTodoInput{
		Title:  "Test 2",
		Tags:   []string{"feature"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo p2: %v", err)
	}

	// List user's tags (cross-project)
	tags, err := st.ListUserTags(ctx, user.ID)
	if err != nil {
		t.Fatalf("ListUserTags: %v", err)
	}

	tagNames := make(map[string]bool)
	for _, tag := range tags {
		tagNames[tag.Name] = true
	}

	if !tagNames["bug"] {
		t.Error("Expected 'bug' tag in user's tag library")
	}
	if !tagNames["urgent"] {
		t.Error("Expected 'urgent' tag in user's tag library")
	}
	if !tagNames["feature"] {
		t.Error("Expected 'feature' tag in user's tag library")
	}
	if len(tags) != 3 {
		t.Errorf("Expected 3 tags in user's library, got %d: %v", len(tags), tags)
	}
}
