package httpapi

import (
	"net/http"
	"strconv"
	"testing"
)

// TestHTTPTagsMine_ListEnablesColorEditing pins that the global personal library
// reports canUpdateColor so Settings → Tag Colors does not disable every picker.
func TestHTTPTagsMine_ListEnablesColorEditing(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	_, slug := createProjectAPI(t, client, ts.URL, "P")
	createTodoAPI(t, client, ts.URL, slug, "a", "bug")

	var tags []projectTagWire
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/tags/mine", nil, &tags)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tags/mine status=%d body=%s", resp.StatusCode, string(body))
	}
	bug := findWireTag(tags, "bug")
	if bug == nil {
		t.Fatalf("expected bug in mine list, got %#v", tags)
	}
	if bug.TagID == 0 {
		t.Fatalf("mine list must include a real tagId, got %#v", bug)
	}
	if !bug.CanUpdateColor {
		t.Fatalf("mine list must report canUpdateColor true, got %#v", bug)
	}
	if bug.DeleteScope != "mine" || !bug.CanDelete {
		t.Fatalf("mine list should be deletable by owner, got %#v", bug)
	}
}

// TestHTTPTagsMine_ColorIgnoresUnrelatedSettingsProject proves a personal tag used
// only in Project B can still be colored via the mine route when an unrelated
// settingsProjectId (Project A) would have made the project-aware ID route 404.
func TestHTTPTagsMine_ColorIgnoresUnrelatedSettingsProject(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "owner@example.com", "password123")

	pidA, _ := createProjectAPI(t, client, ts.URL, "A")
	_, slugB := createProjectAPI(t, client, ts.URL, "B")
	createTodoAPI(t, client, ts.URL, slugB, "only-in-b", "solo")

	var tagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='solo' AND user_id IS NOT NULL`).Scan(&tagID); err != nil {
		t.Fatalf("lookup solo tag: %v", err)
	}

	// Project-aware durable ID route for A correctly rejects a tag unused there.
	resp, body := doJSON(t, client, http.MethodPatch,
		ts.URL+"/api/projects/"+strconv.FormatInt(pidA, 10)+"/tags/id/"+strconv.FormatInt(tagID, 10)+"/color",
		map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("project A id color for B-only tag: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}

	// Mine route succeeds regardless of which project Settings selected for charts.
	resp, body = doJSON(t, client, http.MethodPatch,
		ts.URL+"/api/tags/mine/"+strconv.FormatInt(tagID, 10)+"/color",
		map[string]any{"color": "#abcdef"}, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mine color status=%d body=%s", resp.StatusCode, string(body))
	}

	var tags []projectTagWire
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/tags/mine", nil, &tags)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relist mine status=%d body=%s", resp.StatusCode, string(body))
	}
	solo := findWireTag(tags, "solo")
	if solo == nil || solo.Color == nil || *solo.Color != "#abcdef" {
		t.Fatalf("expected mine color persisted, got %#v", solo)
	}
}

// TestHTTPTagsMine_DeleteOwnerOnlyAndCrossProject pins owner-only delete and that
// removing a personal library tag clears it from every project that used it.
func TestHTTPTagsMine_DeleteOwnerOnlyAndCrossProject(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	owner := newCookieClient(t)
	bootstrapUserClient(t, owner, ts.URL, "Owner", "owner@example.com", "password123")

	pa, slugA := createProjectAPI(t, owner, ts.URL, "A")
	pb, slugB := createProjectAPI(t, owner, ts.URL, "B")
	createTodoAPI(t, owner, ts.URL, slugA, "a", "shared")
	createTodoAPI(t, owner, ts.URL, slugB, "b", "shared")

	var tagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='shared' AND user_id IS NOT NULL`).Scan(&tagID); err != nil {
		t.Fatalf("lookup shared tag: %v", err)
	}

	// Non-owner cannot delete through the mine route.
	_, outsider := createUserAPI(t, owner, ts.URL, "Outsider", "outsider@example.com", "password123")
	resp, body := doJSON(t, outsider, http.MethodDelete,
		ts.URL+"/api/tags/mine/"+strconv.FormatInt(tagID, 10), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-owner mine delete: expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var stillThere int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&stillThere); err != nil {
		t.Fatalf("count tag after outsider delete: %v", err)
	}
	if stillThere != 1 {
		t.Fatalf("outsider must not delete the tag")
	}

	resp, body = doJSON(t, owner, http.MethodDelete,
		ts.URL+"/api/tags/mine/"+strconv.FormatInt(tagID, 10), nil, &map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("owner mine delete status=%d body=%s", resp.StatusCode, string(body))
	}

	var remaining int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&remaining); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected tag deleted")
	}
	if findWireTag(listProjectTags(t, owner, ts.URL, pa), "shared") != nil {
		t.Errorf("expected shared gone from project A")
	}
	if findWireTag(listProjectTags(t, owner, ts.URL, pb), "shared") != nil {
		t.Errorf("expected shared gone from project B")
	}
}
