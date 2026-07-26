package httpapi

import (
	"net/http"
	"strconv"
	"testing"
)

type projectTagWire struct {
	TagID          int64   `json:"tagId"`
	Name           string  `json:"name"`
	Color          *string `json:"color"`
	DeleteScope    string  `json:"deleteScope"`
	CanDelete      bool    `json:"canDelete"`
	CanUpdateColor bool    `json:"canUpdateColor"`
}

func createProjectAPI(t *testing.T, client *http.Client, baseURL, name string) (int64, string) {
	t.Helper()
	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/projects", map[string]any{"name": name}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, string(body))
	}
	return p.ID, p.Slug
}

func createTodoAPI(t *testing.T, client *http.Client, baseURL string, slug, title string, tags ...string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/board/"+slug+"/todos", map[string]any{
		"title":  title,
		"tags":   tags,
		"status": "BACKLOG",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d body=%s", resp.StatusCode, string(body))
	}
}

func listProjectTags(t *testing.T, client *http.Client, baseURL string, projectID int64) []projectTagWire {
	t.Helper()
	var tags []projectTagWire
	resp, body := doJSON(t, client, http.MethodGet, baseURL+"/api/projects/"+strconv.FormatInt(projectID, 10)+"/tags", nil, &tags)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tags status=%d body=%s", resp.StatusCode, string(body))
	}
	return tags
}

func findWireTag(tags []projectTagWire, name string) *projectTagWire {
	for i := range tags {
		if tags[i].Name == name {
			return &tags[i]
		}
	}
	return nil
}

func TestHTTPProjectTags_GroupedNameColorAndDelete(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	pid, slug := createProjectAPI(t, client, ts.URL, "P")
	createTodoAPI(t, client, ts.URL, slug, "a", "bug")

	// Grouped GET: personal label has no tagId, deleteScope "mine".
	tags := listProjectTags(t, client, ts.URL, pid)
	bug := findWireTag(tags, "bug")
	if bug == nil {
		t.Fatalf("expected 'bug' entry, got %#v", tags)
	}
	if bug.TagID != 0 {
		t.Errorf("expected no tagId for grouped personal label, got %d", bug.TagID)
	}
	if bug.DeleteScope != "mine" || !bug.CanDelete {
		t.Errorf("expected deleteScope mine + canDelete true, got %#v", bug)
	}

	// Name-based color PATCH (previously rejected for durable projects) -> 204.
	resp, body := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/bug/color", map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("name color status=%d body=%s", resp.StatusCode, string(body))
	}
	tags = listProjectTags(t, client, ts.URL, pid)
	if bug = findWireTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != "#abcdef" {
		t.Fatalf("expected color to persist via name route, got %#v", bug)
	}

	// Name-based DELETE (previously rejected) -> 204, then gone.
	resp, body = doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/bug", nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("name delete status=%d body=%s", resp.StatusCode, string(body))
	}
	tags = listProjectTags(t, client, ts.URL, pid)
	if findWireTag(tags, "bug") != nil {
		t.Errorf("expected 'bug' gone after delete-mine, got %#v", tags)
	}
}

func TestHTTPProjectTags_IdRouteCompatibility(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	pid, slug := createProjectAPI(t, client, ts.URL, "P")
	createTodoAPI(t, client, ts.URL, slug, "a", "bug")

	var tagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='bug' AND user_id IS NOT NULL`).Scan(&tagID); err != nil {
		t.Fatalf("lookup tag id: %v", err)
	}

	// The id route still works against the real row id for personal tags.
	resp, body := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/id/"+strconv.FormatInt(tagID, 10)+"/color", map[string]any{"color": "#123456"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("id color status=%d body=%s", resp.StatusCode, string(body))
	}
	tags := listProjectTags(t, client, ts.URL, pid)
	if bug := findWireTag(tags, "bug"); bug == nil || bug.Color == nil || *bug.Color != "#123456" {
		t.Fatalf("expected color via id route, got %#v", bug)
	}

	resp, body = doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/id/"+strconv.FormatInt(tagID, 10), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("id delete status=%d body=%s", resp.StatusCode, string(body))
	}
	tags = listProjectTags(t, client, ts.URL, pid)
	if findWireTag(tags, "bug") != nil {
		t.Errorf("expected 'bug' gone after id delete, got %#v", tags)
	}
}

// createUserAPI creates a second account through the admin API and signs it in,
// returning its user id and an authenticated client.
func createUserAPI(t *testing.T, owner *http.Client, baseURL, name, email, password string) (int64, *http.Client) {
	t.Helper()
	var created map[string]any
	resp, body := doJSON(t, owner, http.MethodPost, baseURL+"/api/admin/users", map[string]any{
		"name":     name,
		"email":    email,
		"password": password,
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user %s status=%d body=%s", email, resp.StatusCode, string(body))
	}
	client := newCookieClient(t)
	loginUserClient(t, client, baseURL, email, password)
	return int64(created["id"].(float64)), client
}

// TestHTTPProjectTags_NameRoutesRequireMembership pins that the numeric-id name routes
// authorize on project membership, not merely on being signed in. A grouped label is
// backed by other members' tag rows, so an authenticated outsider must not reach them.
func TestHTTPProjectTags_NameRoutesRequireMembership(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	pid, slug := createProjectAPI(t, owner, ts.URL, "P")
	createTodoAPI(t, owner, ts.URL, slug, "a", "bug")
	pidStr := strconv.FormatInt(pid, 10)

	outsiderID, outsider := createUserAPI(t, owner, ts.URL, "Outsider", "outsider@example.com", "password123")

	// Signed in, but not a member: both name routes hide the project entirely.
	resp, body := doJSON(t, outsider, http.MethodPatch, ts.URL+"/api/projects/"+pidStr+"/tags/bug/color", map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member color: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, outsider, http.MethodDelete, ts.URL+"/api/projects/"+pidStr+"/tags/bug", nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member delete: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	// No preference row leaked onto the owner's backing tag.
	var prefs int
	if err := sqlDB.QueryRow(`
SELECT COUNT(*) FROM user_tag_colors utc
JOIN tags g ON g.id = utc.tag_id
WHERE utc.user_id = ? AND g.name = 'bug'`, outsiderID).Scan(&prefs); err != nil {
		t.Fatalf("count outsider prefs: %v", err)
	}
	if prefs != 0 {
		t.Errorf("non-member must not create a color preference, found %d", prefs)
	}

	// The owner's tag is untouched.
	if bug := findWireTag(listProjectTags(t, owner, ts.URL, pid), "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("owner's tag should be unchanged, got %#v", bug)
	}

	// Unauthenticated requests are rejected before any store work.
	anon := &http.Client{}
	resp, _ = doJSON(t, anon, http.MethodPatch, ts.URL+"/api/projects/"+pidStr+"/tags/bug/color", map[string]any{"color": "#abcdef"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous color: expected 401, got %d", resp.StatusCode)
	}

	// Once added as a viewer, the same user may set their own display color.
	resp, body = doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects/"+pidStr+"/members", map[string]any{
		"user_id": outsiderID,
		"role":    "viewer",
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("add viewer status=%d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, outsider, http.MethodPatch, ts.URL+"/api/projects/"+pidStr+"/tags/bug/color", map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("viewer member color: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	if bug := findWireTag(listProjectTags(t, outsider, ts.URL, pid), "bug"); bug == nil || bug.Color == nil || *bug.Color != "#abcdef" {
		t.Fatalf("viewer should see their own color, got %#v", bug)
	}
	// ...and it stays personal: the owner still sees no color.
	if bug := findWireTag(listProjectTags(t, owner, ts.URL, pid), "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("owner's color must not change, got %#v", bug)
	}
}

// TestHTTPBoardTags_TemporaryBoardKeepsRowLevelTagIDs pins that a Full-mode temporary
// board is excluded from grouping: its tag entries keep real tagIds so the tag_id
// routes stay usable, and the durable name-based delete is still refused there.
func TestHTTPBoardTags_TemporaryBoardKeepsRowLevelTagIDs(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	slug := createAnonBoardViaHTTP(t, client, ts.URL)
	createTodoAPI(t, client, ts.URL, slug, "a", "bug")

	var tags []projectTagWire
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+slug+"/tags", nil, &tags)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("board tags status=%d body=%s", resp.StatusCode, string(body))
	}
	bug := findWireTag(tags, "bug")
	if bug == nil {
		t.Fatalf("expected 'bug' entry, got %#v", tags)
	}
	if bug.TagID == 0 {
		t.Fatalf("temporary-board tags must keep a real tagId so tag_id routes stay usable, got %#v", bug)
	}

	// The tag_id color route still works on the temporary board.
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+slug+"/tags/id/"+strconv.FormatInt(bug.TagID, 10)+"/color", map[string]any{"color": "#123456"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("temporary board id color status=%d body=%s", resp.StatusCode, string(body))
	}

	// The tag_id delete route still uses DeleteTag (not the durable project-aware path).
	resp, body = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+slug+"/tags/id/"+strconv.FormatInt(bug.TagID, 10), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("temporary board id delete status=%d body=%s", resp.StatusCode, string(body))
	}
}

// TestHTTPProjectTags_IdColorRouteAuthz pins the durable ID-based color routes:
// membership, project ownership of the tag, Viewer preference vs Maintainer shared
// color, and unauthenticated rejection. Both /api/projects/{id} and /api/board/{slug}
// durable paths share UpdateTagColorForDurableProjectByID.
func TestHTTPProjectTags_IdColorRouteAuthz(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	pid, slug := createProjectAPI(t, owner, ts.URL, "P")
	pidStr := strconv.FormatInt(pid, 10)
	createTodoAPI(t, owner, ts.URL, slug, "a", "bug")

	var personalTagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='bug' AND user_id IS NOT NULL`).Scan(&personalTagID); err != nil {
		t.Fatalf("lookup personal tag id: %v", err)
	}

	// Board-scoped tag on this project (listed with a real tagId).
	res, err := sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'shared', ?, ?, NULL)`,
		1, pid)
	if err != nil {
		t.Fatalf("insert board-scoped tag: %v", err)
	}
	boardTagID, _ := res.LastInsertId()

	otherPID, _ := createProjectAPI(t, owner, ts.URL, "Other")
	res, err = sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'foreign', ?, ?, NULL)`,
		1, otherPID)
	if err != nil {
		t.Fatalf("insert foreign board-scoped tag: %v", err)
	}
	foreignTagID, _ := res.LastInsertId()

	viewerID, viewer := createUserAPI(t, owner, ts.URL, "Viewer", "viewer@example.com", "password123")
	outsiderID, outsider := createUserAPI(t, owner, ts.URL, "Outsider", "outsider@example.com", "password123")
	_ = outsiderID

	colorURL := func(tagID int64) string {
		return ts.URL + "/api/projects/" + pidStr + "/tags/id/" + strconv.FormatInt(tagID, 10) + "/color"
	}
	boardColorURL := func(tagID int64) string {
		return ts.URL + "/api/board/" + slug + "/tags/id/" + strconv.FormatInt(tagID, 10) + "/color"
	}

	// Unauthenticated numeric ID update is rejected. The projects route returns 401
	// before store work; the board slug route hides unauthenticated durable access as 404.
	anon := &http.Client{}
	resp, _ := doJSON(t, anon, http.MethodPatch, colorURL(personalTagID), map[string]any{"color": "#111111"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous id color: expected 401, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, anon, http.MethodPatch, boardColorURL(boardTagID), map[string]any{"color": "#111111"}, nil)
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anonymous board id color: expected 401 or 404, got %d", resp.StatusCode)
	}

	// Non-member cannot update either personal or board-scoped rows (404).
	resp, body := doJSON(t, outsider, http.MethodPatch, colorURL(personalTagID), map[string]any{"color": "#222222"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member personal id color: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, outsider, http.MethodPatch, colorURL(boardTagID), map[string]any{"color": "#222222"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member board-scoped id color: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	// Tag ID from another project returns 404.
	resp, body = doJSON(t, owner, http.MethodPatch, colorURL(foreignTagID), map[string]any{"color": "#333333"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign project tag id: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	// Add viewer as Viewer.
	resp, body = doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects/"+pidStr+"/members", map[string]any{
		"user_id": viewerID,
		"role":    "viewer",
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("add viewer status=%d body=%s", resp.StatusCode, string(body))
	}

	// Viewer may update a user-owned compatibility ID only as their personal preference.
	resp, body = doJSON(t, viewer, http.MethodPatch, colorURL(personalTagID), map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("viewer personal id color: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	var viewerPref string
	if err := sqlDB.QueryRow(`SELECT color FROM user_tag_colors WHERE user_id=? AND tag_id=?`, viewerID, personalTagID).Scan(&viewerPref); err != nil {
		t.Fatalf("viewer preference missing: %v", err)
	}
	if viewerPref != "#abcdef" {
		t.Errorf("viewer preference = %q, want #abcdef", viewerPref)
	}
	// Owner's display color for the grouped personal label stays unchanged.
	if bug := findWireTag(listProjectTags(t, owner, ts.URL, pid), "bug"); bug == nil || bug.Color != nil {
		t.Fatalf("owner personal color must be unchanged, got %#v", bug)
	}

	// Viewer cannot update a board-scoped shared color (mapped to 404 via hideUnauthorized).
	resp, body = doJSON(t, viewer, http.MethodPatch, colorURL(boardTagID), map[string]any{"color": "#445566"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer board-scoped id color: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var sharedColor *string
	if err := sqlDB.QueryRow(`SELECT color FROM tags WHERE id=?`, boardTagID).Scan(&sharedColor); err != nil {
		t.Fatalf("read shared color: %v", err)
	}
	if sharedColor != nil {
		t.Fatalf("viewer must not change shared tags.color, got %#v", sharedColor)
	}

	// Listing advertises canUpdateColor=false for the board-scoped tag to a Viewer.
	tags := listProjectTags(t, viewer, ts.URL, pid)
	shared := findWireTag(tags, "shared")
	if shared == nil || shared.TagID != boardTagID {
		t.Fatalf("expected board-scoped 'shared' with tagId, got %#v", tags)
	}
	if shared.CanUpdateColor {
		t.Errorf("viewer must not see canUpdateColor for shared board-scoped tag")
	}
	if bug := findWireTag(tags, "bug"); bug == nil || !bug.CanUpdateColor {
		t.Errorf("viewer should see canUpdateColor for personal label, got %#v", bug)
	}

	// Maintainer (owner) can update that shared color on both durable routes.
	resp, body = doJSON(t, owner, http.MethodPatch, colorURL(boardTagID), map[string]any{"color": "#445566"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("maintainer projects id color: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	if err := sqlDB.QueryRow(`SELECT color FROM tags WHERE id=?`, boardTagID).Scan(&sharedColor); err != nil {
		t.Fatalf("read shared color after maintainer update: %v", err)
	}
	if sharedColor == nil || *sharedColor != "#445566" {
		t.Fatalf("expected shared color #445566, got %#v", sharedColor)
	}
	resp, body = doJSON(t, owner, http.MethodPatch, boardColorURL(boardTagID), map[string]any{"color": "#778899"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("maintainer board id color: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	if err := sqlDB.QueryRow(`SELECT color FROM tags WHERE id=?`, boardTagID).Scan(&sharedColor); err != nil {
		t.Fatalf("read shared color after board route: %v", err)
	}
	if sharedColor == nil || *sharedColor != "#778899" {
		t.Fatalf("expected shared color #778899 via board route, got %#v", sharedColor)
	}

	ownerTags := listProjectTags(t, owner, ts.URL, pid)
	if shared = findWireTag(ownerTags, "shared"); shared == nil || !shared.CanUpdateColor {
		t.Errorf("maintainer should see canUpdateColor for board-scoped tag, got %#v", shared)
	}
}

// TestHTTPProjectTags_IdDeleteRouteAuthz pins durable ID-based DELETE routes:
// project association, membership, Viewer vs Maintainer for board-scoped tags,
// owner-only personal deletes, and multi-project refresh after cross-project
// personal deletion. Temporary boards keep the previous DeleteTag path.
func TestHTTPProjectTags_IdDeleteRouteAuthz(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	pidA, slugA := createProjectAPI(t, owner, ts.URL, "A")
	pidB, slugB := createProjectAPI(t, owner, ts.URL, "B")
	pidAStr := strconv.FormatInt(pidA, 10)
	createTodoAPI(t, owner, ts.URL, slugA, "a", "shared")
	createTodoAPI(t, owner, ts.URL, slugB, "b", "shared")
	createTodoAPI(t, owner, ts.URL, slugB, "only-b", "solo")

	var sharedTagID, soloTagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='shared' AND user_id IS NOT NULL`).Scan(&sharedTagID); err != nil {
		t.Fatalf("lookup shared tag: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='solo' AND user_id IS NOT NULL`).Scan(&soloTagID); err != nil {
		t.Fatalf("lookup solo tag: %v", err)
	}

	res, err := sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'board-a', ?, ?, NULL)`,
		1, pidA)
	if err != nil {
		t.Fatalf("insert board-scoped tag: %v", err)
	}
	boardTagID, _ := res.LastInsertId()

	res, err = sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'foreign', ?, ?, NULL)`,
		1, pidB)
	if err != nil {
		t.Fatalf("insert foreign board-scoped tag: %v", err)
	}
	foreignTagID, _ := res.LastInsertId()

	viewerID, viewer := createUserAPI(t, owner, ts.URL, "Viewer", "viewer@example.com", "password123")
	_, outsider := createUserAPI(t, owner, ts.URL, "Outsider", "outsider@example.com", "password123")

	deleteURL := func(projectID, tagID int64) string {
		return ts.URL + "/api/projects/" + strconv.FormatInt(projectID, 10) + "/tags/id/" + strconv.FormatInt(tagID, 10)
	}
	boardDeleteURL := func(slug string, tagID int64) string {
		return ts.URL + "/api/board/" + slug + "/tags/id/" + strconv.FormatInt(tagID, 10)
	}

	// Tag used only in B cannot be deleted through A's ID route.
	resp, body := doJSON(t, owner, http.MethodDelete, deleteURL(pidA, soloTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("A-route delete of B-only tag: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var stillSolo int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, soloTagID).Scan(&stillSolo); err != nil {
		t.Fatalf("count solo: %v", err)
	}
	if stillSolo != 1 {
		t.Fatalf("solo tag must still exist after cross-project reject")
	}

	// Board-scoped tag from another project returns 404.
	resp, body = doJSON(t, owner, http.MethodDelete, deleteURL(pidA, foreignTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign board-scoped delete: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	// Non-member cannot use the route.
	resp, body = doJSON(t, outsider, http.MethodDelete, deleteURL(pidA, boardTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-member delete: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	resp, body = doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects/"+pidAStr+"/members", map[string]any{
		"user_id": viewerID,
		"role":    "viewer",
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("add viewer status=%d body=%s", resp.StatusCode, string(body))
	}

	// Viewer cannot delete a board-scoped tag (mapped to 404 via hideUnauthorized).
	resp, body = doJSON(t, viewer, http.MethodDelete, deleteURL(pidA, boardTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("viewer board-scoped delete: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var boardStill int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, boardTagID).Scan(&boardStill); err != nil {
		t.Fatalf("count board tag: %v", err)
	}
	if boardStill != 1 {
		t.Fatalf("viewer must not delete board-scoped tag")
	}

	// Maintainer can delete a board-scoped tag belonging to that project (both routes).
	resp, body = doJSON(t, owner, http.MethodDelete, deleteURL(pidA, boardTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("maintainer projects id delete: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, boardTagID).Scan(&boardStill); err != nil {
		t.Fatalf("count after maintainer delete: %v", err)
	}
	if boardStill != 0 {
		t.Fatalf("board-scoped tag should be gone")
	}

	// Re-insert a board-scoped tag and delete via durable board slug route.
	res, err = sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (NULL, 'board-a2', ?, ?, NULL)`,
		1, pidA)
	if err != nil {
		t.Fatalf("reinsert board-scoped tag: %v", err)
	}
	boardTag2, _ := res.LastInsertId()
	resp, body = doJSON(t, owner, http.MethodDelete, boardDeleteURL(slugA, boardTag2), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("maintainer board id delete: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}

	// Member cannot delete another member's personal tag by ID.
	memberID, member := createUserAPI(t, owner, ts.URL, "Member", "member@example.com", "password123")
	resp, body = doJSON(t, owner, http.MethodPost, ts.URL+"/api/projects/"+pidAStr+"/members", map[string]any{
		"user_id": memberID,
		"role":    "maintainer",
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("add member status=%d body=%s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, member, http.MethodDelete, deleteURL(pidA, sharedTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("member deleting owner's personal tag: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var sharedStill int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, sharedTagID).Scan(&sharedStill); err != nil {
		t.Fatalf("count shared: %v", err)
	}
	if sharedStill != 1 {
		t.Fatalf("owner personal tag must survive member delete attempt")
	}

	// Owner deleting own cross-project personal tag through its actual project
	// removes it from every affected project.
	resp, body = doJSON(t, owner, http.MethodDelete, deleteURL(pidA, sharedTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner cross-project personal delete: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, sharedTagID).Scan(&sharedStill); err != nil {
		t.Fatalf("count shared after delete: %v", err)
	}
	if sharedStill != 0 {
		t.Fatalf("shared personal tag should be deleted")
	}
	if findWireTag(listProjectTags(t, owner, ts.URL, pidA), "shared") != nil {
		t.Errorf("expected shared gone from project A")
	}
	if findWireTag(listProjectTags(t, owner, ts.URL, pidB), "shared") != nil {
		t.Errorf("expected shared gone from project B after multi-project refresh path")
	}

	// Solo still exists on B and can be deleted through B's route.
	resp, body = doJSON(t, owner, http.MethodDelete, deleteURL(pidB, soloTagID), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("B-route solo delete: expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestHTTPProjectTags_DeleteMineIsCrossProject(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	pa, slugA := createProjectAPI(t, client, ts.URL, "A")
	pb, slugB := createProjectAPI(t, client, ts.URL, "B")
	createTodoAPI(t, client, ts.URL, slugA, "a", "bug")
	createTodoAPI(t, client, ts.URL, slugB, "b", "bug")

	resp, body := doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(pa, 10)+"/tags/bug", nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete-mine status=%d body=%s", resp.StatusCode, string(body))
	}
	// Cross-project: personal tag row is shared, so it disappears from B too.
	if findWireTag(listProjectTags(t, client, ts.URL, pb), "bug") != nil {
		t.Errorf("expected 'bug' removed from project B after cross-project delete")
	}
}
