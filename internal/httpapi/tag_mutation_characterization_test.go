package httpapi

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
)

type tagMutationRESTFixture struct {
	ts        string
	db        *sql.DB
	client    *http.Client
	ownerID   int64
	server    *Server
	collector *capturingEventConsumer
}

func newTagMutationRESTFixture(t *testing.T) *tagMutationRESTFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)
	collector := &capturingEventConsumer{}
	ts.Config.Handler.(*Server).fanout = eventbus.NewFanout(collector)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Tag Owner", "tag-mutation-owner@example.com", "password123")
	return &tagMutationRESTFixture{
		ts: ts.URL, db: sqlDB, client: client,
		ownerID: int64(owner["id"].(float64)), server: ts.Config.Handler.(*Server), collector: collector,
	}
}

func (fx *tagMutationRESTFixture) resetEvents() {
	fx.collector.events = nil
}

func tagMutationPersonalID(t *testing.T, sqlDB *sql.DB, ownerID int64, name string) int64 {
	t.Helper()
	var tagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE user_id = ? AND name = ?`, ownerID, name).Scan(&tagID); err != nil {
		t.Fatalf("lookup personal tag %q: %v", name, err)
	}
	return tagID
}

func insertTagMutationBoardTag(t *testing.T, sqlDB *sql.DB, projectID int64, name string) int64 {
	t.Helper()
	result, err := sqlDB.Exec(`
INSERT INTO tags(user_id, name, created_at, project_id, color)
VALUES (NULL, ?, ?, ?, NULL)`, name, time.Now().UTC().UnixMilli(), projectID)
	if err != nil {
		t.Fatalf("insert board tag %q: %v", name, err)
	}
	tagID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("board tag id %q: %v", name, err)
	}
	return tagID
}

func assertTagMutationRESTStatus(t *testing.T, response *http.Response, body []byte, want int) {
	t.Helper()
	if response.StatusCode != want {
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, want, body)
	}
}

func assertTagMutationRefreshes(t *testing.T, events []eventbus.Event, reason string, wantProjects []int64, wantName string) {
	t.Helper()
	if len(events) != len(wantProjects) {
		t.Fatalf("events=%+v want exactly %d refreshes", events, len(wantProjects))
	}
	want := make(map[int64]int, len(wantProjects))
	for _, projectID := range wantProjects {
		want[projectID]++
	}
	for _, event := range events {
		if event.Type != "board.refresh_needed" {
			t.Fatalf("event type=%q want board.refresh_needed; events=%+v", event.Type, events)
		}
		var payload refreshNeededPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode refresh payload: %v", err)
		}
		if payload.Reason != reason || payload.Name != wantName || payload.LocalID != 0 || payload.Title != "" {
			t.Fatalf("payload=%+v want reason=%q name=%q and zero id/title", payload, reason, wantName)
		}
		if payload.ActorUserID == 0 {
			t.Fatalf("authenticated REST mutation omitted actor metadata: %+v", payload)
		}
		if want[event.ProjectID] == 0 {
			t.Fatalf("unexpected project %d in events=%+v; want=%v", event.ProjectID, events, wantProjects)
		}
		want[event.ProjectID]--
	}
	for projectID, remaining := range want {
		if remaining != 0 {
			t.Fatalf("project %d refresh count mismatch: remaining=%d events=%+v", projectID, remaining, events)
		}
	}
}

func doTagMutationRawJSON(t *testing.T, client *http.Client, method, url, body string, out any) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if out != nil && len(payload) != 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("decode response %s: %v", payload, err)
		}
	}
	return resp, payload
}

// TestTagMutationRESTSelectedSurface freezes all ten mutation routes, including the
// exact refresh reason, affected projects, entity projection, deduplication, and the
// deliberate absence of refresh publication for the mine color route.
func TestTagMutationRESTSelectedSurface(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectA, slugA := createProjectAPI(t, fx.client, fx.ts, "Tag Mutation A")
	projectB, slugB := createProjectAPI(t, fx.client, fx.ts, "Tag Mutation B")

	for _, name := range []string{
		"mine-color", "board-name-color", "numeric-name-color",
		"mine-delete", "board-name-delete", "numeric-name-delete",
	} {
		createTodoAPI(t, fx.client, fx.ts, slugA, "A "+name, name)
	}
	for _, name := range []string{"mine-delete", "board-name-delete", "numeric-name-delete"} {
		createTodoAPI(t, fx.client, fx.ts, slugB, "B "+name, name)
	}
	boardSlugColorID := insertTagMutationBoardTag(t, fx.db, projectA, "board-slug-color-id")
	projectColorID := insertTagMutationBoardTag(t, fx.db, projectA, "project-color-id")
	boardSlugDeleteID := insertTagMutationBoardTag(t, fx.db, projectA, "board-slug-delete-id")
	projectDeleteID := insertTagMutationBoardTag(t, fx.db, projectA, "project-delete-id")
	pid := strconv.FormatInt(projectA, 10)

	t.Run("PATCH /api/tags/mine/{tagId}/color", func(t *testing.T) {
		fx.resetEvents()
		tagID := tagMutationPersonalID(t, fx.db, fx.ownerID, "mine-color")
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/tags/mine/"+strconv.FormatInt(tagID, 10)+"/color", map[string]any{"color": "#111111"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", nil, "")
	})

	t.Run("PATCH /api/board/{slug}/tags/id/{tagId}/color", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/board/"+slugA+"/tags/id/"+strconv.FormatInt(boardSlugColorID, 10)+"/color", map[string]any{"color": "#222222"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectA}, "")
	})

	t.Run("PATCH /api/board/{slug}/tags/{tagName}/color", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/board/"+slugA+"/tags/board-name-color/color", map[string]any{"color": "#333333"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectA}, "board-name-color")
	})

	t.Run("PATCH /api/projects/{id}/tags/id/{tagId}/color", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/projects/"+pid+"/tags/id/"+strconv.FormatInt(projectColorID, 10)+"/color", map[string]any{"color": "#444444"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectA}, "")
	})

	t.Run("PATCH /api/projects/{id}/tags/{tagName}/color", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/projects/"+pid+"/tags/numeric-name-color/color", map[string]any{"color": "#555555"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectA}, "numeric-name-color")
	})

	t.Run("DELETE /api/tags/mine/{tagId}", func(t *testing.T) {
		fx.resetEvents()
		tagID := tagMutationPersonalID(t, fx.db, fx.ownerID, "mine-delete")
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/tags/mine/"+strconv.FormatInt(tagID, 10), nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectA, projectB}, "")
	})

	t.Run("DELETE /api/board/{slug}/tags/id/{tagId}", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/board/"+slugA+"/tags/id/"+strconv.FormatInt(boardSlugDeleteID, 10), nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectA}, "")
	})

	t.Run("DELETE /api/board/{slug}/tags/{tagName}", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/board/"+slugA+"/tags/board-name-delete", nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectA, projectB}, "board-name-delete")
	})

	t.Run("DELETE /api/projects/{id}/tags/id/{tagId}", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/projects/"+pid+"/tags/id/"+strconv.FormatInt(projectDeleteID, 10), nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectA}, "")
	})

	t.Run("DELETE /api/projects/{id}/tags/{tagName}", func(t *testing.T) {
		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/projects/"+pid+"/tags/numeric-name-delete", nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectA, projectB}, "numeric-name-delete")
	})
}

// TestTagMutationRESTColorInputContracts pins the currently inconsistent clear
// behavior at each distinct REST/store boundary. These differences are compatibility
// evidence for extraction, not an endorsement of convergence during Phase 23.0.
func TestTagMutationRESTColorInputContracts(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectID, slug := createProjectAPI(t, fx.client, fx.ts, "Tag Color Inputs")
	createTodoAPI(t, fx.client, fx.ts, slug, "personal", "personal-color")
	personalID := tagMutationPersonalID(t, fx.db, fx.ownerID, "personal-color")
	boardID := insertTagMutationBoardTag(t, fx.db, projectID, "board-color")
	mineURL := fx.ts + "/api/tags/mine/" + strconv.FormatInt(personalID, 10) + "/color"
	nameURL := fx.ts + "/api/projects/" + strconv.FormatInt(projectID, 10) + "/tags/personal-color/color"
	idURL := fx.ts + "/api/projects/" + strconv.FormatInt(projectID, 10) + "/tags/id/" + strconv.FormatInt(boardID, 10) + "/color"

	for _, tc := range []struct {
		name       string
		url        string
		value      any
		wantStatus int
		wantReason string
	}{
		{"mine trims valid hex", mineURL, "  #a1b2c3  ", http.StatusNoContent, ""},
		{"mine null clear", mineURL, nil, http.StatusNoContent, ""},
		{"mine missing preference null clear", mineURL, nil, http.StatusNoContent, ""},
		{"mine empty clear", mineURL, "", http.StatusNoContent, ""},
		{"mine whitespace invalid", mineURL, "   ", http.StatusBadRequest, "invalid_tag_color"},
		{"mine malformed invalid", mineURL, "blue", http.StatusBadRequest, "invalid_tag_color"},
		{"name trims valid hex", nameURL, "  #b1c2d3  ", http.StatusNoContent, ""},
		{"name null clear", nameURL, nil, http.StatusNoContent, ""},
		{"name missing preference null clear", nameURL, nil, http.StatusNoContent, ""},
		{"name empty clear", nameURL, "", http.StatusNoContent, ""},
		{"name whitespace clear", nameURL, "   ", http.StatusNoContent, ""},
		{"name malformed invalid", nameURL, "blue", http.StatusBadRequest, "invalid_tag_color"},
		{"board id trims valid hex", idURL, "  #c1d2e3  ", http.StatusNoContent, ""},
		{"board id null clear", idURL, nil, http.StatusNoContent, ""},
		{"board id missing preference null clear", idURL, nil, http.StatusNoContent, ""},
		{"board id empty clear", idURL, "", http.StatusNoContent, ""},
		{"board id whitespace invalid", idURL, "   ", http.StatusBadRequest, "invalid_tag_color"},
		{"board id malformed invalid", idURL, "blue", http.StatusBadRequest, "invalid_tag_color"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx.resetEvents()
			var out apiErrorEnvelope
			resp, body := doJSON(t, fx.client, http.MethodPatch, tc.url, map[string]any{"color": tc.value}, &out)
			assertTagMutationRESTStatus(t, resp, body, tc.wantStatus)
			if tc.wantReason != "" {
				assertAPIError(t, out, "VALIDATION_ERROR", "", tc.wantReason)
				if len(fx.collector.events) != 0 {
					t.Fatalf("failed mutation published events: %+v", fx.collector.events)
				}
			}
		})
	}
}

func TestTagMutationRESTPersonalColorCrossProjectEffects(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectA, slugA := createProjectAPI(t, fx.client, fx.ts, "Tag Color Cross Project A")
	projectB, slugB := createProjectAPI(t, fx.client, fx.ts, "Tag Color Cross Project B")
	createTodoAPI(t, fx.client, fx.ts, slugA, "A", "cross-project")
	createTodoAPI(t, fx.client, fx.ts, slugB, "B", "cross-project")
	tagID := tagMutationPersonalID(t, fx.db, fx.ownerID, "cross-project")

	fx.resetEvents()
	resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/projects/"+strconv.FormatInt(projectA, 10)+"/tags/cross-project/color", map[string]any{"color": "#123456"}, nil)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
	assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectA}, "cross-project")
	for _, projectID := range []int64{projectA, projectB} {
		tag := findWireTag(listProjectTags(t, fx.client, fx.ts, projectID), "cross-project")
		if tag == nil || tag.Color == nil || *tag.Color != "#123456" {
			t.Fatalf("project %d color=%+v want cross-project preference #123456", projectID, tag)
		}
	}

	fx.resetEvents()
	resp, body = doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/tags/mine/"+strconv.FormatInt(tagID, 10)+"/color", map[string]any{"color": "#654321"}, nil)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
	assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", nil, "")
	for _, projectID := range []int64{projectA, projectB} {
		tag := findWireTag(listProjectTags(t, fx.client, fx.ts, projectID), "cross-project")
		if tag == nil || tag.Color == nil || *tag.Color != "#654321" {
			t.Fatalf("project %d color=%+v want cross-project mine preference #654321", projectID, tag)
		}
	}
}

func TestTagMutationRESTDeleteUnusedMinePublishesNoRefresh(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	result, err := fx.db.Exec(`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'unused-mine', ?, NULL, NULL)`, fx.ownerID, time.Now().UTC().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	tagID, _ := result.LastInsertId()
	fx.resetEvents()
	resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/tags/mine/"+strconv.FormatInt(tagID, 10), nil, nil)
	assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
	assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", nil, "")
}

func TestTagMutationRESTFailedSelectedRoutesPublishNothing(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectID, slug := createProjectAPI(t, fx.client, fx.ts, "Tag Failed Routes")
	pid := strconv.FormatInt(projectID, 10)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"mine color", http.MethodPatch, "/api/tags/mine/999999/color", map[string]any{"color": "#123456"}},
		{"board id color", http.MethodPatch, "/api/board/" + slug + "/tags/id/999999/color", map[string]any{"color": "#123456"}},
		{"board name color", http.MethodPatch, "/api/board/" + slug + "/tags/missing/color", map[string]any{"color": "#123456"}},
		{"project id color", http.MethodPatch, "/api/projects/" + pid + "/tags/id/999999/color", map[string]any{"color": "#123456"}},
		{"project name color", http.MethodPatch, "/api/projects/" + pid + "/tags/missing/color", map[string]any{"color": "#123456"}},
		{"mine delete", http.MethodDelete, "/api/tags/mine/999999", nil},
		{"board id delete", http.MethodDelete, "/api/board/" + slug + "/tags/id/999999", nil},
		{"board name delete", http.MethodDelete, "/api/board/" + slug + "/tags/missing", nil},
		{"project id delete", http.MethodDelete, "/api/projects/" + pid + "/tags/id/999999", nil},
		{"project name delete", http.MethodDelete, "/api/projects/" + pid + "/tags/missing", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx.resetEvents()
			resp, body := doJSON(t, fx.client, tc.method, fx.ts+tc.path, tc.body, nil)
			assertTagMutationRESTStatus(t, resp, body, http.StatusNotFound)
			if len(fx.collector.events) != 0 {
				t.Fatalf("failed selected route published events: %+v", fx.collector.events)
			}
		})
	}
}

// TestTagMutationRESTErrorPrecedence freezes transport ordering that an application
// extraction must retain. Every failure is also asserted to publish no refresh.
func TestTagMutationRESTErrorPrecedence(t *testing.T) {
	fx := newTagMutationRESTFixture(t)
	projectA, _ := createProjectAPI(t, fx.client, fx.ts, "Tag Ordering A")
	projectB, _ := createProjectAPI(t, fx.client, fx.ts, "Tag Ordering B")
	foreignTagID := insertTagMutationBoardTag(t, fx.db, projectB, "foreign")
	pidA := strconv.FormatInt(projectA, 10)
	unauthenticated := &http.Client{}

	for _, tc := range []struct {
		name       string
		client     *http.Client
		method     string
		url        string
		body       string
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{"mine auth before malformed id", unauthenticated, http.MethodPatch, fx.ts + "/api/tags/mine/not-an-id/color", `{`, http.StatusUnauthorized, "UNAUTHORIZED", ""},
		{"numeric auth before malformed id", unauthenticated, http.MethodPatch, fx.ts + "/api/projects/" + pidA + "/tags/id/not-an-id/color", `{`, http.StatusUnauthorized, "UNAUTHORIZED", ""},
		{"inaccessible slug before malformed body", fx.client, http.MethodPatch, fx.ts + "/api/board/missing-board/tags/id/1/color", `{`, http.StatusNotFound, "NOT_FOUND", ""},
		{"wrong project before board role", fx.client, http.MethodPatch, fx.ts + "/api/projects/" + pidA + "/tags/id/" + strconv.FormatInt(foreignTagID, 10) + "/color", `{"color":"#123456"}`, http.StatusNotFound, "NOT_FOUND", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx.resetEvents()
			var out apiErrorEnvelope
			resp, body := doTagMutationRawJSON(t, tc.client, tc.method, tc.url, tc.body, &out)
			assertTagMutationRESTStatus(t, resp, body, tc.wantStatus)
			assertAPIError(t, out, tc.wantCode, "", tc.wantReason)
			if len(fx.collector.events) != 0 {
				t.Fatalf("failed mutation published events: %+v", fx.collector.events)
			}
		})
	}

	t.Run("creator temporary board name delete refused", func(t *testing.T) {
		slug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
		createTodoAPI(t, fx.client, fx.ts, slug, "temporary", "temporary-name")
		fx.resetEvents()
		var out apiErrorEnvelope
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/board/"+slug+"/tags/temporary-name", nil, &out)
		assertTagMutationRESTStatus(t, resp, body, http.StatusBadRequest)
		assertAPIError(t, out, "VALIDATION_ERROR", "", "name_based_tag_route_not_allowed")
		if len(fx.collector.events) != 0 {
			t.Fatalf("refused temporary name delete published events: %+v", fx.collector.events)
		}
	})

	t.Run("expired board hides missing tag", func(t *testing.T) {
		slug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
		projectID := projectIDBySlug(t, fx.db, slug)
		if _, err := fx.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), projectID); err != nil {
			t.Fatalf("expire board: %v", err)
		}
		fx.resetEvents()
		var out apiErrorEnvelope
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/board/"+slug+"/tags/id/999999", nil, &out)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNotFound)
		assertAPIError(t, out, "NOT_FOUND", "")
		if len(fx.collector.events) != 0 {
			t.Fatalf("expired-board failure published events: %+v", fx.collector.events)
		}
	})
}

// TestTagMutationRESTTemporaryAndAnonymousNameResolution distinguishes creator-owned
// temporary boards from unowned anonymous boards and freezes the anonymous name
// resolution order (board-scoped row first, signed-in personal fallback second).
func TestTagMutationRESTTemporaryAndAnonymousNameResolution(t *testing.T) {
	fx := newTagMutationRESTFixture(t)

	t.Run("creator-owned temporary id and name color", func(t *testing.T) {
		slug := createAnonBoardViaHTTP(t, fx.client, fx.ts)
		projectID := projectIDBySlug(t, fx.db, slug)
		createTodoAPI(t, fx.client, fx.ts, slug, "temporary", "creator-tag")
		tagID := tagMutationPersonalID(t, fx.db, fx.ownerID, "creator-tag")

		fx.resetEvents()
		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/board/"+slug+"/tags/id/"+strconv.FormatInt(tagID, 10)+"/color", map[string]any{"color": "#123456"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectID}, "")

		fx.resetEvents()
		resp, body = doJSON(t, fx.client, http.MethodPatch, fx.ts+"/api/board/"+slug+"/tags/creator-tag/color", map[string]any{"color": "#654321"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_color_updated", []int64{projectID}, "creator-tag")
	})

	t.Run("anonymous board-scoped name wins", func(t *testing.T) {
		anonymousClient := newCookieClient(t)
		slug := createAnonBoardViaHTTP(t, anonymousClient, fx.ts)
		projectID := projectIDBySlug(t, fx.db, slug)
		createTodoAPI(t, anonymousClient, fx.ts, slug, "anonymous", "board-name")

		fx.resetEvents()
		resp, body := doJSON(t, anonymousClient, http.MethodPatch, fx.ts+"/api/board/"+slug+"/tags/board-name/color", map[string]any{"color": "#abcdef"}, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		// Anonymous requests intentionally have no actor metadata, so assert the wire
		// dimensions directly instead of using the authenticated helper.
		if len(fx.collector.events) != 1 || fx.collector.events[0].ProjectID != projectID {
			t.Fatalf("anonymous color events=%+v", fx.collector.events)
		}

		fx.resetEvents()
		resp, body = doJSON(t, anonymousClient, http.MethodDelete, fx.ts+"/api/board/"+slug+"/tags/board-name", nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		if len(fx.collector.events) != 1 || fx.collector.events[0].ProjectID != projectID {
			t.Fatalf("anonymous delete events=%+v", fx.collector.events)
		}
	})

	t.Run("anonymous board personal fallback requires signed-in owner", func(t *testing.T) {
		anonymousClient := newCookieClient(t)
		slug := createAnonBoardViaHTTP(t, anonymousClient, fx.ts)
		projectID := projectIDBySlug(t, fx.db, slug)
		createTodoAPI(t, anonymousClient, fx.ts, slug, "empty")
		var todoID int64
		if err := fx.db.QueryRow(`SELECT id FROM todos WHERE project_id = ? ORDER BY id DESC LIMIT 1`, projectID).Scan(&todoID); err != nil {
			t.Fatalf("lookup anonymous todo: %v", err)
		}
		result, err := fx.db.Exec(`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'personal-fallback', ?, NULL, NULL)`, fx.ownerID, time.Now().UTC().UnixMilli())
		if err != nil {
			t.Fatalf("insert personal fallback tag: %v", err)
		}
		tagID, _ := result.LastInsertId()
		if _, err := fx.db.Exec(`INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagID); err != nil {
			t.Fatalf("link personal fallback tag: %v", err)
		}

		fx.resetEvents()
		var unauthErr apiErrorEnvelope
		resp, body := doJSON(t, anonymousClient, http.MethodDelete, fx.ts+"/api/board/"+slug+"/tags/personal-fallback", nil, &unauthErr)
		assertTagMutationRESTStatus(t, resp, body, http.StatusUnauthorized)
		assertAPIError(t, unauthErr, "UNAUTHORIZED", "")
		if len(fx.collector.events) != 0 {
			t.Fatalf("unauthenticated fallback published events: %+v", fx.collector.events)
		}

		resp, body = doJSON(t, fx.client, http.MethodDelete, fx.ts+"/api/board/"+slug+"/tags/personal-fallback", nil, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNoContent)
		assertTagMutationRefreshes(t, fx.collector.events, "tag_deleted", []int64{projectID}, "personal-fallback")
	})
}

func TestTagMutationRESTAnonymousModeNumericRoutesUnavailable(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPatch, "/api/projects/1/tags/id/1/color", map[string]any{"color": "#123456"}},
		{http.MethodPatch, "/api/projects/1/tags/name/color", map[string]any{"color": "#123456"}},
		{http.MethodDelete, "/api/projects/1/tags/id/1", nil},
		{http.MethodDelete, "/api/projects/1/tags/name", nil},
	} {
		resp, body := doJSON(t, ts.Client(), tc.method, ts.URL+tc.path, tc.body, nil)
		assertTagMutationRESTStatus(t, resp, body, http.StatusNotFound)
	}
}
