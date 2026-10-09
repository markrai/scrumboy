package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/store"
)

type publicBoardHTTPFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	st      *store.Store
	owner   store.User
	project store.Project
	sprint  store.Sprint
}

func newPublicBoardHTTPFixture(t *testing.T, options ...func(*Options)) *publicBoardHTTPFixture {
	t.Helper()
	opts := Options{ScrumboyMode: "full", PublicProjectsEnabled: true}
	for _, apply := range options {
		apply(&opts)
	}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, opts)
	t.Cleanup(cleanup)
	st := store.New(sqlDB, nil)
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "phase2-owner@example.com", "password123", "Private Owner Sentinel")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(store.WithUserID(ctx, owner.ID), "Phase Two Public")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectPublicViewing(ctx, project.ID, owner.ID, true); err != nil {
		t.Fatalf("publish project: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `UPDATE projects SET image = 'private-image-sentinel' WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("set private project image: %v", err)
	}
	sprint, err := st.CreateSprint(store.WithUserID(ctx, owner.ID), project.ID, "Public Sprint", time.Now().UTC(), time.Now().UTC().Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	seedPublicBoardHTTPData(t, sqlDB, owner.ID, project.ID, sprint.ID)
	return &publicBoardHTTPFixture{ts: ts, db: sqlDB, st: st, owner: owner, project: project, sprint: sprint}
}

func seedPublicBoardHTTPData(t *testing.T, db *sql.DB, ownerID, projectID, sprintID int64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	other, err := store.New(db, nil).CreateProject(store.WithUserID(ctx, ownerID), "Other Project Secret Sentinel")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, sprint_id, created_at, updated_at, priority_key, created_by_user_id, archived_at)
VALUES
 (?, 1, 'Visible Card', 'Visible body', 'backlog', 10, 3, ?, ?, ?, 'high', ?, NULL),
 (?, 2, 'Linked Card', 'Linked body', 'backlog', 20, NULL, NULL, ?, ?, NULL, NULL, NULL),
 (?, 3, 'Archived Card Sentinel', 'archived-body-sentinel', 'backlog', 30, NULL, NULL, ?, ?, NULL, NULL, ?),
 (?, 99, 'Cross Project Sentinel', 'cross-project-body-sentinel', 'backlog', 5, NULL, NULL, ?, ?, NULL, NULL, NULL)`,
		projectID, sprintID, now, now, ownerID,
		projectID, now, now,
		projectID, now, now, now,
		other.ID, now, now,
	); err != nil {
		t.Fatalf("insert public HTTP todo fixtures: %v", err)
	}
	var todoID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = 1`, projectID).Scan(&todoID); err != nil {
		t.Fatalf("load todo id: %v", err)
	}
	result, err := db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'public-tag', ?, NULL, NULL)`, ownerID, now)
	if err != nil {
		t.Fatalf("insert public HTTP tag: %v", err)
	}
	tagID, _ := result.LastInsertId()
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{name: "todo tag", sql: `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, args: []any{todoID, tagID}},
		{name: "project tag", sql: `INSERT INTO project_tags(project_id, tag_id, created_at) VALUES (?, ?, ?)`, args: []any{projectID, tagID, now}},
		{name: "personal color", sql: `INSERT INTO user_tag_colors(user_id, tag_id, color) VALUES (?, ?, '#personal-color-sentinel')`, args: []any{ownerID, tagID}},
		{name: "links", sql: `INSERT INTO todo_links(project_id, from_local_id, to_local_id, link_type, created_at)
VALUES (?, 1, 2, 'relates_to', ?), (?, 1, 3, 'blocks', ?), (?, 1, 99, 'blocks', ?)`, args: []any{projectID, now, projectID, now, projectID, now}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("insert public HTTP %s fixture: %v", statement.name, err)
		}
	}
}

func publicHTTP(t *testing.T, client *http.Client, method, target string, cookie *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, body
}

func publicHTTPWithHeaders(t *testing.T, client *http.Client, method, target string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, body
}

func decodeJSONObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode JSON: %v; body=%s", err, body)
	}
	return value
}

func assertPublicHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if got := response.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	if got := response.Header.Get("X-Robots-Tag"); got != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag=%q", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options=%q", got)
	}
}

func TestPublicBoardPhase2ResponseContractAndPrivacy(t *testing.T) {
	fixture := newPublicBoardHTTPFixture(t)
	base := fixture.ts.URL + "/api/public/board/" + fixture.project.Slug
	response, anonymousBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.StatusCode, anonymousBody)
	}
	assertPublicHeaders(t, response)
	if got := response.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type=%q", got)
	}
	for _, private := range []string{
		"phase2-owner@example.com", "Private Owner Sentinel", "private-image-sentinel",
		"Archived Card Sentinel", "archived-body-sentinel", "Cross Project Sentinel",
		"cross-project-body-sentinel", "personal-color-sentinel",
	} {
		if bytes.Contains(anonymousBody, []byte(private)) {
			t.Fatalf("snapshot leaked %q: %s", private, anonymousBody)
		}
	}

	snapshot := decodeJSONObject(t, anonymousBody)
	assertExactJSONKeys(t, snapshot, "access", "project", "workflow", "priorities", "tags", "columns", "columnsMeta")
	assertExactJSONKeys(t, snapshot["access"].(map[string]any), "kind", "readOnly")
	assertExactJSONKeys(t, snapshot["project"].(map[string]any), "slug", "name", "dominantColor", "estimationMode", "sprintsEnabled")
	workflow := snapshot["workflow"].([]any)
	if len(workflow) == 0 {
		t.Fatal("snapshot workflow is empty")
	}
	assertExactJSONKeys(t, workflow[0].(map[string]any), "key", "name", "color", "isDone", "position")
	priorities := snapshot["priorities"].([]any)
	if len(priorities) == 0 {
		t.Fatal("snapshot priorities are empty")
	}
	assertExactJSONKeys(t, priorities[0].(map[string]any), "key", "name", "color", "position")
	tags := snapshot["tags"].([]any)
	if len(tags) != 1 {
		t.Fatalf("snapshot tags=%+v", tags)
	}
	assertExactJSONKeys(t, tags[0].(map[string]any), "name", "color", "activeCount")
	columns := snapshot["columns"].(map[string]any)
	backlog := columns["backlog"].([]any)
	if len(backlog) != 2 {
		t.Fatalf("public backlog=%+v", backlog)
	}
	card := backlog[0].(map[string]any)
	assertExactJSONKeys(t, card, "localId", "title", "body", "columnKey", "estimationPoints", "priorityKey", "sprintNumber", "tags")
	cardTags := card["tags"].([]any)
	assertExactJSONKeys(t, cardTags[0].(map[string]any), "name", "color")
	meta := snapshot["columnsMeta"].(map[string]any)["backlog"].(map[string]any)
	assertExactJSONKeys(t, meta, "hasMore", "nextCursor", "totalCount")

	checks := []struct {
		path string
		keys []string
	}{
		{path: "/lanes/backlog?limit=1", keys: []string{"items", "hasMore", "nextCursor", "totalCount"}},
		{path: "/todos/1", keys: []string{"todo"}},
		{path: "/todos/1/links", keys: []string{"links"}},
		{path: "/sprints", keys: []string{"sprints"}},
	}
	for _, check := range checks {
		resp, body := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base+check.path, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d body=%s", check.path, resp.StatusCode, body)
		}
		assertPublicHeaders(t, resp)
		assertExactJSONKeys(t, decodeJSONObject(t, body), check.keys...)
	}
	_, linksBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base+"/todos/1/links", nil)
	links := decodeJSONObject(t, linksBody)["links"].([]any)
	if len(links) != 1 {
		t.Fatalf("public links=%+v", links)
	}
	assertExactJSONKeys(t, links[0].(map[string]any), "direction", "localId", "title")
	_, sprintsBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base+"/sprints", nil)
	sprints := decodeJSONObject(t, sprintsBody)["sprints"].([]any)
	if len(sprints) != 1 {
		t.Fatalf("public sprints=%+v", sprints)
	}
	assertExactJSONKeys(t, sprints[0].(map[string]any), "number", "name", "state")
	_, detailBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base+"/todos/1", nil)
	detail := decodeJSONObject(t, detailBody)["todo"].(map[string]any)
	assertExactJSONKeys(t, detail, "localId", "title", "body", "columnKey", "estimationPoints", "priorityKey", "sprintNumber", "tags")

	outsider, err := fixture.st.CreateUser(context.Background(), "phase2-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}
	token, expires, err := fixture.st.CreateSession(context.Background(), outsider.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession outsider: %v", err)
	}
	cookie := &http.Cookie{Name: "scrumboy_session", Value: token, Path: "/", Expires: expires}
	_, signedInBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base, cookie)
	if !bytes.Equal(signedInBody, anonymousBody) {
		t.Fatalf("public response changed for signed-in nonmember\nanonymous=%s\nsigned-in=%s", anonymousBody, signedInBody)
	}

	eventsResp, eventsBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, base+"/events", nil)
	if eventsResp.StatusCode != http.StatusNotFound {
		t.Fatalf("events status=%d body=%s", eventsResp.StatusCode, eventsBody)
	}
	privateResp, _ := publicHTTP(t, fixture.ts.Client(), http.MethodGet, fixture.ts.URL+"/api/board/"+fixture.project.Slug, cookie)
	if privateResp.StatusCode == http.StatusOK {
		t.Fatal("publication authorized signed-in nonmember on private board route")
	}
}

func TestPublicBoardPhase2GenericNotFoundAndValidationPrecedence(t *testing.T) {
	fixture := newPublicBoardHTTPFixture(t)
	ctx := context.Background()
	ownerCtx := store.WithUserID(ctx, fixture.owner.ID)
	privateProject, err := fixture.st.CreateProject(ownerCtx, "Private Fixture")
	if err != nil {
		t.Fatalf("CreateProject private: %v", err)
	}
	staging, err := fixture.st.CreateProject(ownerCtx, "Staging Fixture")
	if err != nil {
		t.Fatalf("CreateProject staging: %v", err)
	}
	reserved, err := fixture.st.CreateProject(ownerCtx, "Reserved Fixture")
	if err != nil {
		t.Fatalf("CreateProject reserved: %v", err)
	}
	temporary, err := fixture.st.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if _, err := fixture.db.ExecContext(ctx, `
UPDATE projects SET public_view_enabled = 1, import_batch_id = 'pending' WHERE id = ?;
UPDATE projects SET public_view_enabled = 1, slug = 'dashboard' WHERE id = ?;
UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, staging.ID, reserved.ID, temporary.ID); err != nil {
		t.Fatalf("seed ineligible projects: %v", err)
	}

	slugs := []string{privateProject.Slug, staging.Slug, "dashboard", temporary.Slug, "missing-public-board"}
	var notFoundBody []byte
	for _, slug := range slugs {
		resp, body := publicHTTP(t, fixture.ts.Client(), http.MethodGet, fixture.ts.URL+"/api/public/board/"+slug, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("slug %q status=%d body=%s", slug, resp.StatusCode, body)
		}
		assertPublicHeaders(t, resp)
		if notFoundBody == nil {
			notFoundBody = body
		} else if !bytes.Equal(body, notFoundBody) {
			t.Fatalf("slug %q returned distinguishable 404: %s vs %s", slug, body, notFoundBody)
		}
	}
	privateBadResp, privateBadBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, fixture.ts.URL+"/api/public/board/"+privateProject.Slug+"?limitPerLane=nope", nil)
	if privateBadResp.StatusCode != http.StatusNotFound || !bytes.Equal(privateBadBody, notFoundBody) {
		t.Fatalf("private malformed query status=%d body=%s", privateBadResp.StatusCode, privateBadBody)
	}
	publicBadResp, publicBadBody := publicHTTP(t, fixture.ts.Client(), http.MethodGet, fixture.ts.URL+"/api/public/board/"+fixture.project.Slug+"?limitPerLane=nope", nil)
	if publicBadResp.StatusCode != http.StatusBadRequest || !bytes.Contains(publicBadBody, []byte(`"code":"INVALID_REQUEST"`)) {
		t.Fatalf("published malformed query status=%d body=%s", publicBadResp.StatusCode, publicBadBody)
	}

	for name, opts := range map[string]Options{
		"operator gate off": {ScrumboyMode: "full", PublicProjectsEnabled: false},
		"anonymous mode":    {ScrumboyMode: "anonymous", PublicProjectsEnabled: true},
	} {
		t.Run(name, func(t *testing.T) {
			server := NewServer(fixture.st, opts)
			defer server.Close(context.Background())
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/public/board/"+fixture.project.Slug, nil)
			server.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound || !bytes.Equal(recorder.Body.Bytes(), notFoundBody) {
				t.Fatalf("status=%d body=%s, want generic 404 %s", recorder.Code, recorder.Body.Bytes(), notFoundBody)
			}
		})
	}
}

func TestPublicBoardPhase2RejectsMutationMethodsWithoutStateChange(t *testing.T) {
	fixture := newPublicBoardHTTPFixture(t)
	base := fixture.ts.URL + "/api/public/board/" + fixture.project.Slug
	var before int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, fixture.project.ID).Scan(&before); err != nil {
		t.Fatalf("count todos before: %v", err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		resp, body := publicHTTP(t, fixture.ts.Client(), method, base, nil)
		wantStatus := http.StatusNotFound
		csrfProtected := method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete
		if csrfProtected {
			wantStatus = http.StatusForbidden
		}
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s without X-Scrumboy status=%d body=%s, want %d", method, resp.StatusCode, body, wantStatus)
		}
		assertPublicHeaders(t, resp)
		if csrfProtected {
			withHeader, withHeaderBody := publicHTTPWithHeaders(t, fixture.ts.Client(), method, base, map[string]string{"X-Scrumboy": "1"})
			if withHeader.StatusCode != http.StatusNotFound {
				t.Fatalf("%s with X-Scrumboy status=%d body=%s", method, withHeader.StatusCode, withHeaderBody)
			}
			assertPublicHeaders(t, withHeader)
		}
	}
	var after int
	if err := fixture.db.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, fixture.project.ID).Scan(&after); err != nil {
		t.Fatalf("count todos after: %v", err)
	}
	if before != after {
		t.Fatalf("public non-GET requests changed todo count %d -> %d", before, after)
	}
}

func TestPublicBoardPhase2RateLimitUsesTrustedClientIPBoundary(t *testing.T) {
	limiter := ratelimit.New(2, time.Minute)
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		ScrumboyMode:          "full",
		PublicProjectsEnabled: true,
		PublicReadRateLimit:   limiter,
		TrustProxy:            false,
	})
	defer cleanup()
	st := store.New(sqlDB, nil)
	owner, err := st.BootstrapUser(context.Background(), "rate-owner@example.com", "password123", "Rate Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	project, err := st.CreateProject(store.WithUserID(context.Background(), owner.ID), "Rate Private Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	token, expires, err := st.CreateSession(context.Background(), owner.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	paths := []string{
		"/api/public/not-a-route",
		"/api/public/board/missing-before-limit",
		"/api/public/board/" + project.Slug,
		"/api/public/board/missing-after-limit",
	}
	var limitedBody []byte
	for i, path := range paths {
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i+1))
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if i < 2 && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("request %d status=%d body=%s", i+1, resp.StatusCode, body)
		}
		if i >= 2 {
			if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" || !bytes.Contains(body, []byte(`"code":"RATE_LIMITED"`)) {
				t.Fatalf("rate-limited response status=%d retry=%q body=%s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
			}
			if limitedBody == nil {
				limitedBody = body
			} else if !bytes.Equal(body, limitedBody) {
				t.Fatalf("private and missing projects returned distinguishable rate-limit bodies: %s vs %s", body, limitedBody)
			}
			assertPublicHeaders(t, resp)
		}
	}
	member, body := publicHTTP(t, ts.Client(), http.MethodGet, ts.URL+"/api/board/"+project.Slug, &http.Cookie{Name: "scrumboy_session", Value: token, Expires: expires})
	if member.StatusCode != http.StatusOK {
		t.Fatalf("public limiter affected authenticated route: status=%d body=%s", member.StatusCode, body)
	}
}

func TestPublicBoardPhase2LogsRedactSlugs(t *testing.T) {
	var logs bytes.Buffer
	fixture := newPublicBoardHTTPFixture(t, func(opts *Options) {
		opts.Logger = log.New(&logs, "", 0)
	})
	secretSlug := "private-slug-sentinel"
	resp, _ := publicHTTP(t, fixture.ts.Client(), http.MethodGet, fixture.ts.URL+"/api/public/board/"+secretSlug+"?search=secret-query-sentinel", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got := logs.String()
	if strings.Contains(got, secretSlug) || strings.Contains(got, "secret-query-sentinel") {
		t.Fatalf("public request log leaked path/query: %s", got)
	}
	if !strings.Contains(got, "/api/public/board/:slug") {
		t.Fatalf("public request log lacks redacted route: %s", got)
	}
}

func TestPublishedProjectNumericAliasStillRequiresMembership(t *testing.T) {
	fixture := newPublicBoardHTTPFixture(t)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	target := fixture.ts.URL + "/p/" + strconv.FormatInt(fixture.project.ID, 10)
	for name, cookie := range map[string]*http.Cookie{"anonymous": nil} {
		t.Run(name, func(t *testing.T) {
			resp, body := publicHTTP(t, client, http.MethodGet, target, cookie)
			if resp.StatusCode != http.StatusNotFound || resp.Header.Get("Location") != "" {
				t.Fatalf("status=%d location=%q body=%s", resp.StatusCode, resp.Header.Get("Location"), body)
			}
		})
	}
	outsider, err := fixture.st.CreateUser(context.Background(), "numeric-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}
	outsiderToken, outsiderExpiry, err := fixture.st.CreateSession(context.Background(), outsider.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession outsider: %v", err)
	}
	outsiderResp, _ := publicHTTP(t, client, http.MethodGet, target, &http.Cookie{Name: "scrumboy_session", Value: outsiderToken, Expires: outsiderExpiry})
	if outsiderResp.StatusCode != http.StatusNotFound || outsiderResp.Header.Get("Location") != "" {
		t.Fatalf("outsider status=%d location=%q", outsiderResp.StatusCode, outsiderResp.Header.Get("Location"))
	}
	ownerToken, ownerExpiry, err := fixture.st.CreateSession(context.Background(), fixture.owner.ID, time.Hour)
	if err != nil {
		t.Fatalf("CreateSession owner: %v", err)
	}
	ownerResp, _ := publicHTTP(t, client, http.MethodGet, target, &http.Cookie{Name: "scrumboy_session", Value: ownerToken, Expires: ownerExpiry})
	if ownerResp.StatusCode != http.StatusFound || ownerResp.Header.Get("Location") != "/"+url.PathEscape(fixture.project.Slug) {
		t.Fatalf("owner status=%d location=%q", ownerResp.StatusCode, ownerResp.Header.Get("Location"))
	}
	temporary, err := fixture.st.CreateAnonymousBoard(store.WithUserID(context.Background(), fixture.owner.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	temporaryTarget := fixture.ts.URL + "/p/" + strconv.FormatInt(temporary.ID, 10)
	temporaryResp, _ := publicHTTP(t, client, http.MethodGet, temporaryTarget, nil)
	if temporaryResp.StatusCode != http.StatusFound || temporaryResp.Header.Get("Location") != "/"+url.PathEscape(temporary.Slug) {
		t.Fatalf("temporary status=%d location=%q", temporaryResp.StatusCode, temporaryResp.Header.Get("Location"))
	}
}
