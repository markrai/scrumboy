package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newTestServer(t *testing.T, mode string) (*httptest.Server, *sql.DB, func()) {
	t.Helper()

	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	srv := httpapi.NewServer(st, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   mode,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: mode}),
	})
	ts := httptest.NewServer(srv)
	return ts, sqlDB, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func doMCP(t *testing.T, client *http.Client, url string, body any) (*http.Response, map[string]any) {
	t.Helper()

	var reqBody io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode mcp body: %v", err)
		}
		reqBody = &buf
	}

	req, err := http.NewRequest(http.MethodPost, url, reqBody)
	if err != nil {
		t.Fatalf("new mcp request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do mcp request: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	return resp, out
}

// postMCPWithBearer POSTs to /mcp with Authorization: Bearer <token> (full secret including sb_ prefix).
func postMCPWithBearer(t *testing.T, client *http.Client, baseURL, bearerToken string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode mcp body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", &buf)
	if err != nil {
		t.Fatalf("new mcp request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do mcp request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	return resp, out
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any, out any) *http.Response {
	t.Helper()

	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode json: %v", err)
		}
	}

	req, err := http.NewRequest(method, url, &buf)
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

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode json: %v", err)
		}
	}
	return resp
}

func newCookieClient(t *testing.T, ts *httptest.Server) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := ts.Client()
	client.Jar = jar
	return client
}

func newStatelessClient(ts *httptest.Server) *http.Client {
	base := ts.Client()
	return &http.Client{Transport: base.Transport}
}

func projectSlugByName(t *testing.T, sqlDB *sql.DB, name string) string {
	t.Helper()
	var slug string
	if err := sqlDB.QueryRow(`SELECT slug FROM projects WHERE name = ?`, name).Scan(&slug); err != nil {
		t.Fatalf("query project slug: %v", err)
	}
	return slug
}

func projectIDBySlug(t *testing.T, sqlDB *sql.DB, slug string) int64 {
	t.Helper()
	var projectID int64
	if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE slug = ?`, slug).Scan(&projectID); err != nil {
		t.Fatalf("query project id by slug: %v", err)
	}
	return projectID
}

func firstUserID(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	var userID int64
	if err := sqlDB.QueryRow(`SELECT id FROM users ORDER BY id ASC LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("query first user id: %v", err)
	}
	return userID
}

func todoLocalIDsByColumn(t *testing.T, sqlDB *sql.DB, slug, columnKey string) []int64 {
	t.Helper()
	rows, err := sqlDB.Query(`
SELECT t.local_id
FROM todos t
JOIN projects p ON p.id = t.project_id
WHERE p.slug = ? AND t.column_key = ?
ORDER BY t.rank ASC, t.id ASC
`, slug, columnKey)
	if err != nil {
		t.Fatalf("query todo order: %v", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var localID int64
		if err := rows.Scan(&localID); err != nil {
			t.Fatalf("scan todo order: %v", err)
		}
		out = append(out, localID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate todo order: %v", err)
	}
	return out
}

func boardColumnByKey(t *testing.T, cols []any, key string) map[string]any {
	t.Helper()
	for _, col := range cols {
		m := col.(map[string]any)
		if m["key"] == key {
			return m
		}
	}
	t.Fatalf("column %q not found in %#v", key, cols)
	return nil
}

func bootstrapUser(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	resp := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/bootstrap", map[string]any{
		"email":    "owner@example.com",
		"password": "password123",
		"name":     "Owner",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d", resp.StatusCode)
	}
}

func insertProjectScopedTag(t *testing.T, sqlDB *sql.DB, projectID int64, name string, color *string) int64 {
	t.Helper()
	nowMs := time.Now().UTC().UnixMilli()
	var colorArg any
	if color != nil {
		colorArg = *color
	}
	res, err := sqlDB.Exec(`INSERT INTO tags(user_id, project_id, name, created_at, color) VALUES (NULL, ?, ?, ?, ?)`, projectID, name, nowMs, colorArg)
	if err != nil {
		t.Fatalf("insert project-scoped tag: %v", err)
	}
	tagID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("project tag last insert id: %v", err)
	}
	return tagID
}

func newSessionClientForUser(t *testing.T, ts *httptest.Server, st *store.Store, userID int64) *http.Client {
	t.Helper()
	client := newCookieClient(t, ts)
	token, expiresAt, err := st.CreateSession(context.Background(), userID, 24*time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	client.Jar.SetCookies(u, []*http.Cookie{{
		Name:    "scrumboy_session",
		Value:   token,
		Path:    "/",
		Expires: expiresAt,
	}})
	return client
}

func TestMCPMountDoesNotBreakSPARoutes(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected root 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("expected html content type, got %q", got)
	}

	resp, err = http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected /mcp 200, got %d", resp.StatusCode)
	}
}

// Invalid Bearer must not fall back to a valid session cookie (strict Bearer semantics).
func TestMCP_InvalidBearerDoesNotFallBackToSessionCookie(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	bootstrapUser(t, newCookieClient(t, ts), ts.URL)
	st := store.New(sqlDB, nil)
	userID := firstUserID(t, sqlDB)
	client := newSessionClientForUser(t, ts, st, userID)

	authz := "Bearer sb_" + strings.Repeat("A", 43)

	t.Run("GET", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("GET status=%d want 401", resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "AUTH_REQUIRED" {
			t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
		}
	})

	t.Run("POST", func(t *testing.T) {
		body := map[string]any{"tool": "projects.list", "input": map[string]any{}}
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", &buf)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", authz)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("POST status=%d want 401", resp.StatusCode)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "AUTH_REQUIRED" {
			t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
		}
	})
}

func TestMCP_BearerTokenEndToEnd(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t, ts)
	bootstrapUser(t, cookieClient, ts.URL)

	var created map[string]any
	resp := doJSON(t, cookieClient, http.MethodPost, ts.URL+"/api/me/tokens", map[string]any{"name": "e2e"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/me/tokens: status=%d", resp.StatusCode)
	}
	token := created["token"].(string)
	id := int64(created["id"].(float64))

	mcpBody := map[string]any{"tool": "projects.list", "input": map[string]any{}}
	// No session cookie: Bearer alone must authenticate.
	bareClient := newStatelessClient(ts)
	resp, out := postMCPWithBearer(t, bareClient, ts.URL, token, mcpBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("MCP with valid bearer: status=%d %#v", resp.StatusCode, out)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("expected ok true, got %#v", out)
	}

	resp = doJSON(t, cookieClient, http.MethodDelete, fmt.Sprintf("%s/api/me/tokens/%d", ts.URL, id), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /api/me/tokens/{id}: status=%d", resp.StatusCode)
	}

	resp, out = postMCPWithBearer(t, bareClient, ts.URL, token, mcpBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("MCP after revoke: status=%d want 401", resp.StatusCode)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", out)
	}
}

func TestMCPSystemGetCapabilities_FullPreBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", got)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
	data := out["data"].(map[string]any)
	if data["serverMode"] != "full" {
		t.Fatalf("expected serverMode full, got %#v", data["serverMode"])
	}
	if data["bootstrapAvailable"] != true {
		t.Fatalf("expected bootstrapAvailable true, got %#v", data["bootstrapAvailable"])
	}
	auth := data["auth"].(map[string]any)
	if auth["mode"] != "sessionCookie" {
		t.Fatalf("expected sessionCookie auth mode, got %#v", auth["mode"])
	}
	if auth["authenticatedToolsUsable"] != false {
		t.Fatalf("expected authenticatedToolsUsable false, got %#v", auth["authenticatedToolsUsable"])
	}
	tools := data["implementedTools"].([]any)
	if len(tools) != 28 || tools[0] != "system.getCapabilities" || tools[1] != "projects.list" || tools[2] != "todos.create" || tools[3] != "todos.get" || tools[4] != "todos.search" || tools[5] != "todos.update" || tools[6] != "todos.delete" || tools[7] != "todos.move" || tools[8] != "sprints.list" || tools[9] != "sprints.get" || tools[10] != "sprints.getActive" || tools[11] != "sprints.create" || tools[12] != "sprints.activate" || tools[13] != "sprints.close" || tools[14] != "sprints.update" || tools[15] != "sprints.delete" || tools[16] != "tags.listProject" || tools[17] != "tags.listMine" || tools[18] != "tags.updateMineColor" || tools[19] != "tags.deleteMine" || tools[20] != "tags.updateProjectColor" || tools[21] != "tags.deleteProject" || tools[22] != "members.list" || tools[23] != "members.listAvailable" || tools[24] != "members.add" || tools[25] != "members.updateRole" || tools[26] != "members.remove" || tools[27] != "board.get" {
		t.Fatalf("unexpected implementedTools: %#v", tools)
	}
	if _, ok := data["plannedTools"]; ok {
		t.Fatalf("expected plannedTools omitted once board.get is implemented, got %#v", data["plannedTools"])
	}
}

func TestMCPSystemGetCapabilities_AnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get /mcp: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	data := out["data"].(map[string]any)
	if data["serverMode"] != "anonymous" {
		t.Fatalf("expected anonymous mode, got %#v", data["serverMode"])
	}
	auth := data["auth"].(map[string]any)
	if auth["mode"] != "disabled" {
		t.Fatalf("expected disabled auth mode, got %#v", auth["mode"])
	}
	if auth["authenticatedToolsUsable"] != false {
		t.Fatalf("expected authenticatedToolsUsable false, got %#v", auth["authenticatedToolsUsable"])
	}
}

func TestMCPProjectsListRequiresAuthAfterBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	authClient := newCookieClient(t, ts)
	bootstrapUser(t, authClient, ts.URL)

	resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool":  "projects.list",
		"input": map[string]any{},
	})

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if out["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", out["ok"])
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPProjectsListSuccessWithAuthenticatedSession(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	var created map[string]any
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "MCP Project",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "projects.list",
		"input": map[string]any{},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}

	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 project, got %#v", items)
	}
	item := items[0].(map[string]any)
	if item["projectSlug"] == "" {
		t.Fatalf("expected projectSlug, got %#v", item["projectSlug"])
	}
	if item["name"] != "MCP Project" {
		t.Fatalf("expected project name, got %#v", item["name"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPProjectsListCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool":  "projects.list",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTodosCreateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Create Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Create Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Add MCP adapter",
			"body":        "Thin layer only",
			"tags":        []string{"mcp"},
			"columnKey":   "backlog",
			"position": map[string]any{
				"afterLocalId":  nil,
				"beforeLocalId": nil,
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
	data := out["data"].(map[string]any)
	todo := data["todo"].(map[string]any)
	if todo["projectSlug"] != slug {
		t.Fatalf("expected projectSlug %q, got %#v", slug, todo["projectSlug"])
	}
	if todo["localId"] == nil {
		t.Fatalf("expected localId, got %#v", todo["localId"])
	}
	if todo["columnKey"] != "backlog" {
		t.Fatalf("expected columnKey backlog, got %#v", todo["columnKey"])
	}
	if _, ok := todo["id"]; ok {
		t.Fatalf("did not expect global todo id in MCP todo object: %#v", todo)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPTodosCreateRequiresAuthAfterBootstrap(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Auth Required Todo Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Auth Required Todo Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Unauthed",
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosCreateCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": "demo",
			"title":       "Nope",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTodosCreateValidationErrorForMalformedInput(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug":  "demo",
			"title":        "Bad",
			"unknownField": true,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosGetSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Get Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Get Project")

	resp = doJSON(t, client, http.MethodPost, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Fetch me",
		},
	}, &map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("todos.create status=%d", resp.StatusCode)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.get",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	data := out["data"].(map[string]any)
	todo := data["todo"].(map[string]any)
	if todo["projectSlug"] != slug || todo["localId"] != float64(1) {
		t.Fatalf("unexpected canonical identity: %#v", todo)
	}
	if todo["title"] != "Fetch me" {
		t.Fatalf("expected title, got %#v", todo["title"])
	}
	if _, ok := todo["id"]; ok {
		t.Fatalf("did not expect global todo id in MCP todo object: %#v", todo)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPTodosGetNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Missing Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Missing Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.get",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     999,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPTodosGetRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Get Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Get Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos.get",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosSearchSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Search Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Search Project")

	doJSON(t, client, http.MethodPost, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Add MCP adapter",
		},
	}, &map[string]any{})
	doJSON(t, client, http.MethodPost, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Other task",
		},
	}, &map[string]any{})

	limit := 20
	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.search",
		"input": map[string]any{
			"projectSlug":     slug,
			"query":           "adapter",
			"limit":           limit,
			"excludeLocalIds": []int64{},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 result, got %#v", items)
	}
	item := items[0].(map[string]any)
	if item["projectSlug"] != slug || item["localId"] == nil {
		t.Fatalf("unexpected canonical search item: %#v", item)
	}
	if item["title"] != "Add MCP adapter" {
		t.Fatalf("unexpected title: %#v", item["title"])
	}
	if _, ok := item["id"]; ok {
		t.Fatalf("did not expect global todo id in search result: %#v", item)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
	if len(out["meta"].(map[string]any)) != 0 {
		t.Fatalf("expected empty meta for honest non-cursor search, got %#v", out["meta"])
	}
}

func TestMCPTodosSearchValidationErrorForMalformedInput(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.search",
		"input": map[string]any{
			"projectSlug": "demo",
			"limit":       0,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosUpdateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	userID := firstUserID(t, sqlDB)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Update Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Update Project")

	createResp, createOut := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug":      slug,
			"title":            "Original",
			"body":             "Original body",
			"tags":             []string{"mcp"},
			"estimationPoints": 3,
			"assigneeUserId":   userID,
		},
	})
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("todos.create status=%d", createResp.StatusCode)
	}
	localID := int(createOut["data"].(map[string]any)["todo"].(map[string]any)["localId"].(float64))

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     localID,
			"patch": map[string]any{
				"title": "Updated",
				"body":  "Updated body",
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	data := out["data"].(map[string]any)
	todo := data["todo"].(map[string]any)
	if todo["projectSlug"] != slug || todo["localId"] != float64(localID) {
		t.Fatalf("unexpected canonical identity: %#v", todo)
	}
	if todo["title"] != "Updated" || todo["body"] != "Updated body" {
		t.Fatalf("unexpected updated fields: %#v", todo)
	}
	if _, ok := todo["id"]; ok {
		t.Fatalf("did not expect global todo id in MCP todo object: %#v", todo)
	}
}

func TestMCPTodosUpdateOmittedFieldsRemainUnchanged(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Omit Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Omit Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug":      slug,
			"title":            "Keep title",
			"body":             "Keep body",
			"tags":             []string{"mcp", "backend"},
			"estimationPoints": 5,
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"body": "Changed only body",
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	todo := out["data"].(map[string]any)["todo"].(map[string]any)
	if todo["title"] != "Keep title" {
		t.Fatalf("expected title unchanged, got %#v", todo["title"])
	}
	if todo["body"] != "Changed only body" {
		t.Fatalf("expected body changed, got %#v", todo["body"])
	}
	tags := todo["tags"].([]any)
	if len(tags) != 2 || tags[0] != "backend" || tags[1] != "mcp" {
		t.Fatalf("expected tags unchanged, got %#v", todo["tags"])
	}
	if todo["estimationPoints"] != float64(5) {
		t.Fatalf("expected estimation unchanged, got %#v", todo["estimationPoints"])
	}
}

func TestMCPTodosUpdateNullClearsSupportedFields(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	userID := firstUserID(t, sqlDB)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Clear Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Clear Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug":      slug,
			"title":            "Clear me",
			"estimationPoints": 8,
			"assigneeUserId":   userID,
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"estimationPoints": nil,
				"assigneeUserId":   nil,
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	todo := out["data"].(map[string]any)["todo"].(map[string]any)
	if todo["estimationPoints"] != nil {
		t.Fatalf("expected estimationPoints cleared, got %#v", todo["estimationPoints"])
	}
	if todo["assigneeUserId"] != nil {
		t.Fatalf("expected assigneeUserId cleared, got %#v", todo["assigneeUserId"])
	}
}

func TestMCPTodosUpdatePatchSprintIdAssignAndClear(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Sprint Patch Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Sprint Patch Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint A", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Sprint patch todo",
		},
	})

	respAssign, outAssign := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"sprintId": sp.ID,
			},
		},
	})
	if respAssign.StatusCode != http.StatusOK {
		t.Fatalf("todos.update assign sprint status=%d body=%#v", respAssign.StatusCode, outAssign)
	}
	todo := outAssign["data"].(map[string]any)["todo"].(map[string]any)
	if todo["sprintId"] != float64(sp.ID) {
		t.Fatalf("expected sprintId %d in todo, got %#v", sp.ID, todo["sprintId"])
	}

	respClear, outClear := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"sprintId": nil,
			},
		},
	})
	if respClear.StatusCode != http.StatusOK {
		t.Fatalf("todos.update clear sprint status=%d body=%#v", respClear.StatusCode, outClear)
	}
	todo2 := outClear["data"].(map[string]any)["todo"].(map[string]any)
	if todo2["sprintId"] != nil {
		t.Fatalf("expected sprintId cleared (null), got %#v", todo2["sprintId"])
	}
}

func TestMCPTodosUpdateValidationErrorForMalformedPatch(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Bad Patch Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Bad Patch Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Patch target",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"columnKey": "doing",
			},
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosUpdateRejectsInvalidNullField(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Invalid Null Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Invalid Null Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Patch target",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"title": nil,
			},
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosUpdateRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Update Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Update Auth Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Patch me",
		},
	})

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"patch": map[string]any{
				"title": "No auth",
			},
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosUpdateCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "todos.update",
		"input": map[string]any{
			"projectSlug": "demo",
			"localId":     1,
			"patch": map[string]any{
				"title": "Nope",
			},
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTodosDeleteSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Delete Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Delete Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Delete me",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
	data := out["data"].(map[string]any)
	if data["status"] != "deleted" {
		t.Fatalf("expected deleted status, got %#v", data["status"])
	}
	if data["projectSlug"] != slug || data["localId"] != float64(1) {
		t.Fatalf("unexpected delete identity echo: %#v", data)
	}
	if _, ok := data["id"]; ok {
		t.Fatalf("did not expect global todo id in delete response: %#v", data)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPTodosDeleteNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Delete Missing Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Delete Missing Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     999,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPTodosDeleteRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Delete Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Delete Auth Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Delete me",
		},
	})

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosDeleteCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "todos.delete",
		"input": map[string]any{
			"projectSlug": "demo",
			"localId":     1,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveSuccessToAnotherColumn(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Move me",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"toColumnKey": "in-progress",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	todo := out["data"].(map[string]any)["todo"].(map[string]any)
	if todo["projectSlug"] != slug || todo["localId"] != float64(1) {
		t.Fatalf("unexpected canonical identity: %#v", todo)
	}
	if todo["columnKey"] != "doing" {
		t.Fatalf("expected normalized columnKey doing, got %#v", todo["columnKey"])
	}
	if _, ok := todo["id"]; ok {
		t.Fatalf("did not expect global todo id in move response: %#v", todo)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPTodosMoveSuccessWithAfterLocalId(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move After Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move After Project")

	for i := 1; i <= 2; i++ {
		doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos.create",
			"input": map[string]any{
				"projectSlug": slug,
				"title":       "Task",
			},
		})
	}

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "todos.move",
		"input": map[string]any{"projectSlug": slug, "localId": 1, "toColumnKey": "doing"},
	})
	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":  slug,
			"localId":      2,
			"toColumnKey":  "doing",
			"afterLocalId": 1,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	todo := out["data"].(map[string]any)["todo"].(map[string]any)
	if todo["columnKey"] != "doing" {
		t.Fatalf("expected doing, got %#v", todo["columnKey"])
	}
	got := todoLocalIDsByColumn(t, sqlDB, slug, "doing")
	want := []int64{1, 2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected doing order: got=%v want=%v", got, want)
	}
}

func TestMCPTodosMoveSuccessWithBeforeLocalId(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Before Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Before Project")

	for i := 1; i <= 2; i++ {
		doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos.create",
			"input": map[string]any{
				"projectSlug": slug,
				"title":       "Task",
			},
		})
	}

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "todos.move",
		"input": map[string]any{"projectSlug": slug, "localId": 2, "toColumnKey": "doing"},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":   slug,
			"localId":       1,
			"toColumnKey":   "doing",
			"beforeLocalId": 2,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	todo := out["data"].(map[string]any)["todo"].(map[string]any)
	if todo["columnKey"] != "doing" {
		t.Fatalf("expected doing, got %#v", todo["columnKey"])
	}
	got := todoLocalIDsByColumn(t, sqlDB, slug, "doing")
	want := []int64{1, 2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected doing order: got=%v want=%v", got, want)
	}
}

func TestMCPTodosMoveValidationErrorWhenBothNeighborsSet(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":   "demo",
			"localId":       1,
			"toColumnKey":   "doing",
			"afterLocalId":  2,
			"beforeLocalId": 3,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveValidationErrorForNonexistentNeighbor(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Missing Neighbor Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Missing Neighbor Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Task",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":  slug,
			"localId":      1,
			"toColumnKey":  "doing",
			"afterLocalId": 999,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveValidationErrorForWrongColumnNeighbor(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Wrong Column Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Wrong Column Project")

	for i := 1; i <= 2; i++ {
		doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos.create",
			"input": map[string]any{
				"projectSlug": slug,
				"title":       "Task",
			},
		})
	}
	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "todos.move",
		"input": map[string]any{"projectSlug": slug, "localId": 1, "toColumnKey": "doing"},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":  slug,
			"localId":      2,
			"toColumnKey":  "testing",
			"afterLocalId": 1,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveValidationErrorForAmbiguousAfterPlacement(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Ambiguous Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Ambiguous Project")

	for i := 1; i <= 3; i++ {
		doMCP(t, client, ts.URL+"/mcp", map[string]any{
			"tool": "todos.create",
			"input": map[string]any{
				"projectSlug": slug,
				"title":       "Task",
			},
		})
	}

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "todos.move",
		"input": map[string]any{"projectSlug": slug, "localId": 1, "toColumnKey": "doing"},
	})
	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "todos.move",
		"input": map[string]any{"projectSlug": slug, "localId": 2, "toColumnKey": "doing"},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug":  slug,
			"localId":      3,
			"toColumnKey":  "doing",
			"afterLocalId": 1,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveRequiresAuth(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Todo Move Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Todo Move Auth Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Move me",
		},
	})

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug": slug,
			"localId":     1,
			"toColumnKey": "doing",
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTodosMoveCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "todos.move",
		"input": map[string]any{
			"projectSlug": "demo",
			"localId":     1,
			"toColumnKey": "doing",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPSprintsListSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint List Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint List Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp1, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint 1: %v", err)
	}
	if _, err := st.CreateSprint(context.Background(), projectID, "Sprint 2", time.UnixMilli(3000), time.UnixMilli(4000)); err != nil {
		t.Fatalf("create sprint 2: %v", err)
	}

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Backlog todo",
		},
	})
	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Sprint todo",
			"sprintId":    sp1.ID,
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 sprints, got %#v", items)
	}
	first := items[0].(map[string]any)
	if first["projectSlug"] != slug || first["sprintId"] == nil {
		t.Fatalf("unexpected canonical sprint identity: %#v", first)
	}
	meta := out["meta"].(map[string]any)
	if meta["unscheduledCount"] != float64(1) {
		t.Fatalf("expected unscheduledCount=1, got %#v", meta["unscheduledCount"])
	}
}

func TestMCPSprintsGetSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Get Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Get Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.get",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["projectSlug"] != slug || sprint["sprintId"] != float64(sp.ID) {
		t.Fatalf("unexpected canonical sprint identity: %#v", sprint)
	}
}

func TestMCPSprintsGetActiveSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Active Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Active Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Active Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := st.ActivateSprint(context.Background(), projectID, sp.ID); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.getActive",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["projectSlug"] != slug || sprint["sprintId"] != float64(sp.ID) {
		t.Fatalf("unexpected active sprint identity: %#v", sprint)
	}
}

func TestMCPSprintsGetActiveNoActiveSprintReturnsNull(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint No Active Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint No Active Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	if _, err := st.CreateSprint(context.Background(), projectID, "Planned Sprint", time.UnixMilli(1000), time.UnixMilli(2000)); err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.getActive",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["data"].(map[string]any)["sprint"] != nil {
		t.Fatalf("expected sprint null, got %#v", out["data"])
	}
}

func TestMCPSprintsAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.list",
		"input": map[string]any{
			"projectSlug": "demo",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCreateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Create Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Create Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.create",
		"input": map[string]any{
			"projectSlug":    slug,
			"name":           "Sprint 1",
			"plannedStartAt": "2026-04-01T00:00:00Z",
			"plannedEndAt":   "2026-04-14T23:59:59Z",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["projectSlug"] != slug || sprint["sprintId"] == nil {
		t.Fatalf("unexpected sprint identity: %#v", sprint)
	}
	if sprint["name"] != "Sprint 1" {
		t.Fatalf("expected sprint name, got %#v", sprint["name"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPSprintsCreateValidationErrorForMalformedInput(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Bad Input Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Bad Input Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.create",
		"input": map[string]any{
			"projectSlug":    slug,
			"name":           "Sprint 1",
			"plannedStartAt": "not-a-time",
			"plannedEndAt":   "2026-04-14T23:59:59Z",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCreateValidationErrorForInvalidDateRange(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Bad Range Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Bad Range Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.create",
		"input": map[string]any{
			"projectSlug":    slug,
			"name":           "Sprint 1",
			"plannedStartAt": "2026-04-14T23:59:59Z",
			"plannedEndAt":   "2026-04-01T00:00:00Z",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCreateAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Create Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Create Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.create",
		"input": map[string]any{
			"projectSlug":    slug,
			"name":           "Sprint 1",
			"plannedStartAt": "2026-04-01T00:00:00Z",
			"plannedEndAt":   "2026-04-14T23:59:59Z",
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCreateCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.create",
		"input": map[string]any{
			"projectSlug":    "demo",
			"name":           "Sprint 1",
			"plannedStartAt": "2026-04-01T00:00:00Z",
			"plannedEndAt":   "2026-04-14T23:59:59Z",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPSprintsActivateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Activate Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Activate Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Planned Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.activate",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["projectSlug"] != slug || sprint["sprintId"] != float64(sp.ID) {
		t.Fatalf("unexpected sprint identity: %#v", sprint)
	}
	if sprint["state"] != "ACTIVE" {
		t.Fatalf("expected ACTIVE, got %#v", sprint["state"])
	}
}

func TestMCPSprintsCloseSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Close Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Close Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := st.ActivateSprint(context.Background(), projectID, sp.ID); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.close",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["state"] != "CLOSED" {
		t.Fatalf("expected CLOSED, got %#v", sprint["state"])
	}
}

func TestMCPSprintsActivateValidationErrorFromWrongState(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Activate Wrong State Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Activate Wrong State Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := st.ActivateSprint(context.Background(), projectID, sp.ID); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.activate",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsCloseValidationErrorFromWrongState(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Close Wrong State Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Close Wrong State Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.close",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintActionsAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Action Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Action Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.activate",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPSprintActionsCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.activate",
		"input": map[string]any{
			"projectSlug": "demo",
			"sprintId":    1,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPSprintsUpdateSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Update Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Update Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"name":           "Sprint 1 revised",
				"plannedStartAt": 3000,
				"plannedEndAt":   4000,
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["projectSlug"] != slug || sprint["sprintId"] != float64(sp.ID) {
		t.Fatalf("unexpected sprint identity: %#v", sprint)
	}
	if sprint["name"] != "Sprint 1 revised" {
		t.Fatalf("expected updated name, got %#v", sprint["name"])
	}
	if sprint["plannedStartAt"] != float64(3000) || sprint["plannedEndAt"] != float64(4000) {
		t.Fatalf("expected updated dates, got %#v", sprint)
	}
}

func TestMCPSprintsUpdateOmissionLeavesFieldsUnchanged(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Omit Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Omit Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"name": "Sprint renamed",
			},
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	sprint := out["data"].(map[string]any)["sprint"].(map[string]any)
	if sprint["name"] != "Sprint renamed" {
		t.Fatalf("expected updated name, got %#v", sprint["name"])
	}
	if sprint["plannedStartAt"] != float64(1000) || sprint["plannedEndAt"] != float64(2000) {
		t.Fatalf("expected dates unchanged, got %#v", sprint)
	}
}

func TestMCPSprintsUpdateValidationErrorForStateFieldCombo(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint State Update Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint State Update Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := st.ActivateSprint(context.Background(), projectID, sp.ID); err != nil {
		t.Fatalf("activate sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"name": "Nope",
			},
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsUpdateMalformedTimestampValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Bad Timestamp Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Bad Timestamp Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"plannedStartAt": "not-a-number",
			},
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsUpdateInvalidDateRangeValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Bad Update Range Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Bad Update Range Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"plannedStartAt": 3000,
				"plannedEndAt":   2000,
			},
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPSprintsUpdateAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Update Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Update Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
			"patch": map[string]any{
				"name": "No auth",
			},
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPSprintsUpdateCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.update",
		"input": map[string]any{
			"projectSlug": "demo",
			"sprintId":    1,
			"patch": map[string]any{
				"name": "Nope",
			},
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPSprintsDeleteSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Delete Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Delete Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
	data := out["data"].(map[string]any)
	if data["status"] != "deleted" {
		t.Fatalf("expected deleted status, got %#v", data["status"])
	}
	if data["projectSlug"] != slug || data["sprintId"] != float64(sp.ID) {
		t.Fatalf("unexpected delete identity echo: %#v", data)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPSprintsDeleteNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Delete Missing Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Delete Missing Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "sprints.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    999,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPSprintsDeleteAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Sprint Delete Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Sprint Delete Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	sp, err := st.CreateSprint(context.Background(), projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.delete",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPSprintsDeleteCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "sprints.delete",
		"input": map[string]any{
			"projectSlug": "demo",
			"sprintId":    1,
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTagsListProjectSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"mcp"},
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.listProject",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected project tags, got %#v", items)
	}
	tag := items[0].(map[string]any)
	if tag["tagId"] == nil || tag["name"] != "mcp" {
		t.Fatalf("unexpected project tag shape: %#v", tag)
	}
	if tag["count"] != float64(1) {
		t.Fatalf("expected count 1, got %#v", tag["count"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPTagsListMineSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Mine Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Mine Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"backend", "mcp"},
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("expected mine tags, got %#v", items)
	}
	tag := items[0].(map[string]any)
	if tag["tagId"] == nil || tag["name"] == nil {
		t.Fatalf("unexpected mine tag shape: %#v", tag)
	}
	if _, hasCount := tag["count"]; hasCount {
		t.Fatalf("did not expect count in tags.listMine shape: %#v", tag)
	}
}

func TestMCPTagsAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "tags.listProject",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTagsCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateMineColorSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Color Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Color Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"backend"},
		},
	})

	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	tagID := mine["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tagId"]

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": tagID,
			"color": "#7c3aed",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	tag := out["data"].(map[string]any)["tag"].(map[string]any)
	if tag["tagId"] != tagID || tag["name"] != "backend" {
		t.Fatalf("unexpected tag response: %#v", tag)
	}
	if tag["color"] != "#7c3aed" {
		t.Fatalf("expected updated color, got %#v", tag["color"])
	}
}

func TestMCPTagsDeleteMineSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Delete Mine Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Delete Mine Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"deleteme"},
		},
	})

	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	var tagID int64
	for _, it := range mine["data"].(map[string]any)["items"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "deleteme" {
			tagID = int64(m["tagId"].(float64))
			break
		}
	}
	if tagID == 0 {
		t.Fatalf("tag deleteme not in listMine: %#v", mine)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": tagID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true")
	}
	del := out["data"].(map[string]any)["deleted"].(map[string]any)
	if int64(del["tagId"].(float64)) != tagID {
		t.Fatalf("deleted tagId: %#v", del["tagId"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object")
	}

	_, mine2 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	for _, it := range mine2["data"].(map[string]any)["items"].([]any) {
		m := it.(map[string]any)
		if int64(m["tagId"].(float64)) == tagID {
			t.Fatalf("tag still in listMine after delete")
		}
	}
}

func TestMCPTagsDeleteMineValidationInvalidTagId(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Del Val Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": float64(0),
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR")
	}
}

func TestMCPTagsDeleteMineValidationUnknownField(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Del UF Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId":         float64(1),
			"projectSlug":   "nope",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR")
	}
}

func TestMCPTagsDeleteMineAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Del Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Del Auth Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "t",
			"tags":        []string{"x"},
		},
	})
	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	tagID := mine["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tagId"]

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": tagID,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED")
	}
}

func TestMCPTagsDeleteMineCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPTagsDeleteMineCapabilityUnavailableBeforeBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPTagsDeleteMineNotInViewerLibraryNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Del Other Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Del Other Project")

	doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "t",
			"tags":        []string{"owneronly"},
		},
	})
	_, mine := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	var tagID int64
	for _, it := range mine["data"].(map[string]any)["items"].([]any) {
		m := it.(map[string]any)
		if m["name"] == "owneronly" {
			tagID = int64(m["tagId"].(float64))
			break
		}
	}
	if tagID == 0 {
		t.Fatalf("expected owner tag")
	}

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "tagdelother@example.com", "password123", "O")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	otherClient := newSessionClientForUser(t, ts, st, other.ID)

	resp2, out := doMCP(t, otherClient, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteMine",
		"input": map[string]any{
			"tagId": tagID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for non-owner mine-scope precheck, got %#v", out["error"])
	}
}

func TestMCPTagsUpdateMineColorClearSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Clear Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Clear Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"backend"},
		},
	})

	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	tagID := mine["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tagId"]

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": tagID,
			"color": "#7c3aed",
		},
	})

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": tagID,
			"color": nil,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	tag := out["data"].(map[string]any)["tag"].(map[string]any)
	if tag["color"] != nil {
		t.Fatalf("expected cleared color, got %#v", tag["color"])
	}
}

func TestMCPTagsUpdateMineColorMalformedColorValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Bad Color Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Bad Color Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"backend"},
		},
	})

	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	tagID := mine["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tagId"]

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": tagID,
			"color": "purple",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateMineColorRejectsEmptyStringClear(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Tag Empty Clear Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Tag Empty Clear Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "Tagged todo",
			"tags":        []string{"backend"},
		},
	})

	_, mine := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool":  "tags.listMine",
		"input": map[string]any{},
	})
	tagID := mine["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["tagId"]

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": tagID,
			"color": "",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateMineColorMalformedInputValidation(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId":        1,
			"color":        "#7c3aed",
			"unknownField": true,
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateMineColorAuthFailure(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": 1,
			"color": "#7c3aed",
		},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateMineColorCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateMineColor",
		"input": map[string]any{
			"tagId": 1,
			"color": "#7c3aed",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateProjectColorSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Color Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Color Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", nil)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       "#7c3aed",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	tag := out["data"].(map[string]any)["tag"].(map[string]any)
	if tag["tagId"] != float64(tagID) || tag["name"] != "backend" {
		t.Fatalf("unexpected tag response: %#v", tag)
	}
	if tag["color"] != "#7c3aed" {
		t.Fatalf("expected updated color, got %#v", tag["color"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

// TestMCPTagsUpdateProjectColorVisibleToOtherMemberViaListProject checks that a project-scoped
// color change is stored on the shared tag row (tags.color), not as a per-viewer preference:
// a different project member sees the same color via tags.listProject / ListTagCounts.
// The maintainer updates color before adding the viewer so the write path is unambiguously the owner session.
func TestMCPTagsUpdateProjectColorVisibleToOtherMemberViaListProject(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Shared Color",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Shared Color")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", nil)

	wantColor := "#aabbcc"
	resp2, _ := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       wantColor,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update project color status=%d", resp2.StatusCode)
	}

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "viewer2@example.com", "password123", "Viewer2")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp3, listOut := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "tags.listProject",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("tags.listProject status=%d", resp3.StatusCode)
	}
	items := listOut["data"].(map[string]any)["items"].([]any)
	var found bool
	for _, it := range items {
		m := it.(map[string]any)
		if int64(m["tagId"].(float64)) != tagID {
			continue
		}
		found = true
		if m["color"] != wantColor {
			t.Fatalf("viewer expected shared project color %q, got %#v", wantColor, m["color"])
		}
	}
	if !found {
		t.Fatalf("tag %d not in listProject items: %#v", tagID, items)
	}
}

func TestMCPTagsUpdateProjectColorPermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Permission Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Permission Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", nil)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer membership: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       "#7c3aed",
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateProjectColorWrongProjectNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag First",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Second",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create second project status=%d", resp.StatusCode)
	}
	firstSlug := projectSlugByName(t, sqlDB, "Project Tag First")
	secondSlug := projectSlugByName(t, sqlDB, "Project Tag Second")
	secondProjectID := projectIDBySlug(t, sqlDB, secondSlug)
	tagID := insertProjectScopedTag(t, sqlDB, secondProjectID, "backend", nil)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": firstSlug,
			"tagId":       tagID,
			"color":       "#7c3aed",
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPTagsDeleteProjectSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del Proj Tag Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del Proj Tag Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "scoped-del", nil)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true")
	}
	del := out["data"].(map[string]any)["deleted"].(map[string]any)
	if del["projectSlug"] != slug || int64(del["tagId"].(float64)) != tagID {
		t.Fatalf("deleted: %#v", del)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object")
	}
}

func TestMCPTagsDeleteProjectWrongProjectNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT First",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	resp = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT Second",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create second project status=%d", resp.StatusCode)
	}
	firstSlug := projectSlugByName(t, sqlDB, "Del PT First")
	secondSlug := projectSlugByName(t, sqlDB, "Del PT Second")
	secondProjectID := projectIDBySlug(t, sqlDB, secondSlug)
	tagID := insertProjectScopedTag(t, sqlDB, secondProjectID, "xdel", nil)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": firstSlug,
			"tagId":       tagID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND")
	}
}

func TestMCPTagsDeleteProjectUserOwnedTagNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT User Tag Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del PT User Tag Project")

	doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "todos.create",
		"input": map[string]any{
			"projectSlug": slug,
			"title":       "t",
			"tags":        []string{"userownedtag"},
		},
	})

	var userTagID int64
	if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name = 'userownedtag' AND user_id IS NOT NULL`).Scan(&userTagID); err != nil {
		t.Fatalf("query user-owned tag: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       userTagID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND for user-owned tag id")
	}
}

func TestMCPTagsDeleteProjectValidationInvalidTagId(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT Val Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del PT Val Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       float64(0),
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR")
	}
}

func TestMCPTagsDeleteProjectValidationUnknownField(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT UF Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del PT UF Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       float64(1),
			"tagName":     "nope",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR")
	}
}

func TestMCPTagsDeleteProjectAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del PT Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "authdel", nil)

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED")
	}
}

func TestMCPTagsDeleteProjectCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": "demo",
			"tagId":       float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPTagsDeleteProjectCapabilityUnavailableBeforeBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": "demo",
			"tagId":       float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPTagsDeleteProjectPermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Del PT Perm Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Del PT Perm Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "perm-del", nil)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "delptview@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "tags.deleteProject",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN")
	}
}

func TestMCPTagsUpdateProjectColorMalformedColorValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Bad Color",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Bad Color")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", nil)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       "purple",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateProjectColorClearSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	initialColor := "#7c3aed"
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Clear",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Clear")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", &initialColor)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       nil,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	tag := out["data"].(map[string]any)["tag"].(map[string]any)
	if tag["color"] != nil {
		t.Fatalf("expected cleared project color, got %#v", tag["color"])
	}
}

func TestMCPTagsUpdateProjectColorAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Project Tag Auth Failure",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Project Tag Auth Failure")
	projectID := projectIDBySlug(t, sqlDB, slug)
	tagID := insertProjectScopedTag(t, sqlDB, projectID, "backend", nil)

	resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": slug,
			"tagId":       tagID,
			"color":       "#7c3aed",
		},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPTagsUpdateProjectColorCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "tags.updateProjectColor",
		"input": map[string]any{
			"projectSlug": "demo",
			"tagId":       1,
			"color":       "#7c3aed",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPMembersListSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members List Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members List Project")
	ownerID := firstUserID(t, sqlDB)

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	data := out["data"].(map[string]any)
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one member, got %d", len(items))
	}
	m := items[0].(map[string]any)
	if m["projectSlug"] != slug {
		t.Fatalf("projectSlug: %#v", m["projectSlug"])
	}
	if int64(m["userId"].(float64)) != ownerID {
		t.Fatalf("userId: %#v", m["userId"])
	}
	if m["email"] != "owner@example.com" {
		t.Fatalf("email: %#v", m["email"])
	}
	if m["role"] != "maintainer" {
		t.Fatalf("role: %#v", m["role"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPMembersListNormalizesLegacyStoredRoles(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members List Legacy Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members List Legacy Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "listleg@example.com", "password123", "Leg")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?`, "owner", projectID, ownerID); err != nil {
		t.Fatalf("legacy owner: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?`, "editor", projectID, other.ID); err != nil {
		t.Fatalf("legacy editor: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 members, got %d", len(items))
	}
	byUser := make(map[int64]string)
	for _, it := range items {
		m := it.(map[string]any)
		byUser[int64(m["userId"].(float64))] = m["role"].(string)
	}
	if byUser[ownerID] != "maintainer" {
		t.Fatalf("owner row: want maintainer, got %q", byUser[ownerID])
	}
	if byUser[other.ID] != "contributor" {
		t.Fatalf("editor row: want contributor, got %q", byUser[other.ID])
	}
}

func TestMCPMembersListAvailableSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Avail Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Avail Project")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "other@example.com", "password123", "Other")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.listAvailable",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	items := out["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one available user, got %d", len(items))
	}
	u := items[0].(map[string]any)
	if int64(u["userId"].(float64)) != other.ID {
		t.Fatalf("userId: %#v", u["userId"])
	}
	if u["email"] != "other@example.com" {
		t.Fatalf("email: %#v", u["email"])
	}
}

func TestMCPMembersListAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "members.list",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPMembersListCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.list",
		"input": map[string]any{
			"projectSlug": "demo",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPMembersListAvailableCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.listAvailable",
		"input": map[string]any{
			"projectSlug": "demo",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPMembersListAvailablePermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Perm Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Perm Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "vmem@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.listAvailable",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Add Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Add Project")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "memberadd@example.com", "password123", "Member Add")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true, got %#v", out["ok"])
	}
	data := out["data"].(map[string]any)
	m := data["member"].(map[string]any)
	if m["projectSlug"] != slug {
		t.Fatalf("projectSlug: %#v", m["projectSlug"])
	}
	if int64(m["userId"].(float64)) != other.ID {
		t.Fatalf("userId: %#v", m["userId"])
	}
	if m["email"] != "memberadd@example.com" {
		t.Fatalf("email: %#v", m["email"])
	}
	if m["role"] != "contributor" {
		t.Fatalf("role: %#v", m["role"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPMembersAddDuplicateConflict(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Dup Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Dup Project")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "memberdup@example.com", "password123", "Dup")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	body := map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "viewer",
		},
	}
	resp2, _ := doMCP(t, client, ts.URL+"/mcp", body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("first add: expected 200, got %d", resp2.StatusCode)
	}
	resp3, out := doMCP(t, client, ts.URL+"/mcp", body)
	if resp3.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp3.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CONFLICT" {
		t.Fatalf("expected CONFLICT, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddUnsupportedRole(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Role Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Role Project")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "badrole@example.com", "password123", "Bad")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "owner",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddUserNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members NF User Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members NF User Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      int64(999999999),
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Add Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Add Auth Project")

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "addauth@example.com", "password123", "A")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
			"role":        "contributor",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddCapabilityUnavailableBeforeBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
			"role":        "contributor",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPMembersAddPermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Add Perm Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Add Perm Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "vadd@example.com", "password123", "VAdd")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	target, err := st.CreateUser(context.Background(), "targetadd@example.com", "password123", "T")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.add",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      target.ID,
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %#v", errObj["code"])
	}
}

func TestMCPMembersUpdateRoleSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Update Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Update Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "uprole@example.com", "password123", "UpRole")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "viewer",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true")
	}
	m := out["data"].(map[string]any)["member"].(map[string]any)
	if m["role"] != "viewer" {
		t.Fatalf("role: %#v", m["role"])
	}
	if int64(m["userId"].(float64)) != other.ID {
		t.Fatalf("userId: %#v", m["userId"])
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object")
	}
}

func TestMCPMembersUpdateRoleUnchangedNoOp(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members NoOp Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members NoOp Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "noop@example.com", "password123", "NoOp")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	m := out["data"].(map[string]any)["member"].(map[string]any)
	if m["role"] != "contributor" {
		t.Fatalf("role: %#v", m["role"])
	}
}

func TestMCPMembersUpdateRoleUnsupportedRole(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members UR Role Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members UR Role Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "urrole@example.com", "password123", "U")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "owner",
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR")
	}
}

func TestMCPMembersUpdateRoleTargetNotMember(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members UR NF Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members UR NF Project")

	st := store.New(sqlDB, nil)
	loner, err := st.CreateUser(context.Background(), "notmember@example.com", "password123", "N")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      loner.ID,
			"role":        "viewer",
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND")
	}
}

func TestMCPMembersUpdateRoleAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members UR Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members UR Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "urauth@example.com", "password123", "U")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), firstUserID(t, sqlDB), projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "viewer",
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED")
	}
}

func TestMCPMembersUpdateRoleCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
			"role":        "contributor",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPMembersUpdateRoleCapabilityUnavailableBeforeBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
			"role":        "contributor",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPMembersUpdateRolePermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members UR Perm Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members UR Perm Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "urview@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	target, err := st.CreateUser(context.Background(), "urtarget@example.com", "password123", "T")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, target.ID, store.RoleContributor); err != nil {
		t.Fatalf("add target: %v", err)
	}

	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      target.ID,
			"role":        "viewer",
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN")
	}
}

func TestMCPMembersUpdateRoleSelfDemotionConflict(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Self Demo Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Self Demo Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      ownerID,
			"role":        "contributor",
		},
	})
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CONFLICT" {
		t.Fatalf("expected CONFLICT")
	}
}

func TestMCPMembersUpdateRoleLastMaintainerDemotionConflict(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Last M Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Last M Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	m2, err := st.CreateUser(context.Background(), "lastm2@example.com", "password123", "M2")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, m2.ID, store.RoleContributor); err != nil {
		t.Fatalf("add m2: %v", err)
	}
	// Align with store TestUpdateProjectMemberRole_LastMaintainerCannotDemoteToViewer setup.
	r1, _ := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{"projectSlug": slug, "userId": m2.ID, "role": "maintainer"},
	})
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("mcp promote m2: %d", r1.StatusCode)
	}
	r2, _ := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{"projectSlug": slug, "userId": m2.ID, "role": "contributor"},
	})
	if r2.StatusCode != http.StatusOK {
		t.Fatalf("mcp demote m2: %d", r2.StatusCode)
	}
	r3, _ := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{"projectSlug": slug, "userId": m2.ID, "role": "maintainer"},
	})
	if r3.StatusCode != http.StatusOK {
		t.Fatalf("mcp re-promote m2: %d", r3.StatusCode)
	}
	m2Client := newSessionClientForUser(t, ts, st, m2.ID)
	r4, _ := doMCP(t, m2Client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{"projectSlug": slug, "userId": ownerID, "role": "contributor"},
	})
	if r4.StatusCode != http.StatusOK {
		t.Fatalf("mcp demote owner: %d", r4.StatusCode)
	}
	// m2 is now the only maintainer; self-demotion to viewer must fail (ErrConflict, last-maintainer path in store).
	resp2, out := doMCP(t, m2Client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      m2.ID,
			"role":        "viewer",
		},
	})
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CONFLICT" {
		t.Fatalf("expected CONFLICT, got %#v", out["error"])
	}
}

func TestMCPMembersUpdateRoleLegacyOutputNormalization(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members UR Legacy Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members UR Legacy Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "urlegacy@example.com", "password123", "L")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := sqlDB.Exec(`UPDATE project_members SET role = ? WHERE project_id = ? AND user_id = ?`, "owner", projectID, other.ID); err != nil {
		t.Fatalf("legacy role: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
			"role":        "maintainer",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	m := out["data"].(map[string]any)["member"].(map[string]any)
	if m["role"] != "maintainer" {
		t.Fatalf("expected normalized maintainer, got %#v", m["role"])
	}
}

func TestMCPMembersRemoveSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members Remove Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members Remove Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "rmuser@example.com", "password123", "Rm")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if out["ok"] != true {
		t.Fatalf("expected ok=true")
	}
	rem := out["data"].(map[string]any)["removed"].(map[string]any)
	if rem["projectSlug"] != slug || int64(rem["userId"].(float64)) != other.ID {
		t.Fatalf("removed: %#v", rem)
	}
	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object")
	}
}

func TestMCPMembersRemoveTargetNotMember(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members RM NF Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members RM NF Project")

	st := store.New(sqlDB, nil)
	loner, err := st.CreateUser(context.Background(), "rmnf@example.com", "password123", "N")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      loner.ID,
		},
	})
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND")
	}
}

func TestMCPMembersRemoveAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members RM Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members RM Auth Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	other, err := st.CreateUser(context.Background(), "rmauth@example.com", "password123", "A")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), firstUserID(t, sqlDB), projectID, other.ID, store.RoleContributor); err != nil {
		t.Fatalf("add member: %v", err)
	}

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      other.ID,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED")
	}
}

func TestMCPMembersRemoveCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPMembersRemoveCapabilityUnavailableBeforeBootstrap(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": "demo",
			"userId":      float64(1),
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE")
	}
}

func TestMCPMembersRemovePermissionFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members RM Perm Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members RM Perm Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	viewer, err := st.CreateUser(context.Background(), "rmview@example.com", "password123", "V")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	target, err := st.CreateUser(context.Background(), "rmtarget@example.com", "password123", "T")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, target.ID, store.RoleContributor); err != nil {
		t.Fatalf("add target: %v", err)
	}

	viewerClient := newSessionClientForUser(t, ts, st, viewer.ID)

	resp2, out := doMCP(t, viewerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      target.ID,
		},
	})
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN")
	}
}

func TestMCPMembersRemoveLastMaintainerValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members RM Last M Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members RM Last M Project")

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      ownerID,
		},
	})
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp2.StatusCode)
	}
	if out["error"].(map[string]any)["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", out["error"])
	}
}

func TestMCPMembersRemoveSelfSuccessWhenNotLastMaintainer(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	resp := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Members RM Self Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Members RM Self Project")
	projectID := projectIDBySlug(t, sqlDB, slug)

	st := store.New(sqlDB, nil)
	m2, err := st.CreateUser(context.Background(), "rmself2@example.com", "password123", "S2")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddProjectMember(context.Background(), ownerID, projectID, m2.ID, store.RoleContributor); err != nil {
		t.Fatalf("add m2: %v", err)
	}
	r1, _ := doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "members.updateRole",
		"input": map[string]any{"projectSlug": slug, "userId": m2.ID, "role": "maintainer"},
	})
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("promote m2: %d", r1.StatusCode)
	}

	m2Client := newSessionClientForUser(t, ts, st, m2.ID)
	resp2, out := doMCP(t, m2Client, ts.URL+"/mcp", map[string]any{
		"tool": "members.remove",
		"input": map[string]any{
			"projectSlug": slug,
			"userId":      m2.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	rem := out["data"].(map[string]any)["removed"].(map[string]any)
	if int64(rem["userId"].(float64)) != m2.ID {
		t.Fatalf("removed userId: %#v", rem["userId"])
	}
}

func TestMCPBoardGetSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Get Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Get Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Backlog todo",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"bug"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create backlog todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Doing todo",
		ColumnKey: store.DefaultColumnDoing,
	}, store.ModeFull); err != nil {
		t.Fatalf("create doing todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	data := out["data"].(map[string]any)
	project := data["project"].(map[string]any)
	if project["projectSlug"] != slug || project["name"] != "Board Get Project" || project["role"] != "maintainer" {
		t.Fatalf("unexpected project shape: %#v", project)
	}
	if _, ok := project["projectId"]; ok {
		t.Fatalf("board project should not expose projectId: %#v", project)
	}

	columns := data["columns"].([]any)
	backlog := boardColumnByKey(t, columns, store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one backlog item, got %#v", items)
	}
	item := items[0].(map[string]any)
	if item["projectSlug"] != slug || item["title"] != "Backlog todo" || item["columnKey"] != store.DefaultColumnBacklog {
		t.Fatalf("unexpected board todo item: %#v", item)
	}
	if _, ok := item["id"]; ok {
		t.Fatalf("board item should not expose global todo id: %#v", item)
	}

	if _, ok := out["meta"].(map[string]any); !ok {
		t.Fatalf("expected meta object, got %#v", out["meta"])
	}
}

func TestMCPBoardGetPerColumnPagination(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Page Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Page Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	for i := 0; i < 3; i++ {
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "Paged todo",
			ColumnKey: store.DefaultColumnBacklog,
		}, store.ModeFull); err != nil {
			t.Fatalf("create todo %d: %v", i, err)
		}
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
			"limit":       2,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	meta := out["meta"].(map[string]any)
	hasMoreByColumn := meta["hasMoreByColumn"].(map[string]any)
	nextCursorByColumn := meta["nextCursorByColumn"].(map[string]any)
	totalCountByColumn := meta["totalCountByColumn"].(map[string]any)
	if hasMoreByColumn[store.DefaultColumnBacklog] != true {
		t.Fatalf("expected backlog hasMore=true, got %#v", hasMoreByColumn)
	}
	cursor, ok := nextCursorByColumn[store.DefaultColumnBacklog].(string)
	if !ok || cursor == "" {
		t.Fatalf("expected opaque backlog cursor, got %#v", nextCursorByColumn[store.DefaultColumnBacklog])
	}
	if int(totalCountByColumn[store.DefaultColumnBacklog].(float64)) != 3 {
		t.Fatalf("expected totalCount 3, got %#v", totalCountByColumn[store.DefaultColumnBacklog])
	}

	resp3, out2 := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
			"limit":       2,
			"cursorByColumn": map[string]any{
				store.DefaultColumnBacklog: cursor,
			},
		},
	})
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on follow-up page, got %d", resp3.StatusCode)
	}
	backlog := boardColumnByKey(t, out2["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	if len(backlog["items"].([]any)) != 1 {
		t.Fatalf("expected one remaining backlog item, got %#v", backlog["items"])
	}
}

func TestMCPBoardGetFilters(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Filter Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Filter Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Fix login bug",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"bug"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create matching todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Fix login copy",
		ColumnKey: store.DefaultColumnBacklog,
		Tags:      []string{"docs"},
	}, store.ModeFull); err != nil {
		t.Fatalf("create non-matching todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
			"tag":         "bug",
			"search":      "login",
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "Fix login bug" {
		t.Fatalf("unexpected filtered items: %#v", items)
	}
}

func TestMCPBoardGetSprintFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Sprint Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "Board Sprint Project")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	sp, err := st.CreateSprint(ctx, projectID, "Sprint 1", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "In sprint",
		ColumnKey: store.DefaultColumnBacklog,
		SprintID:  &sp.ID,
	}, store.ModeFull); err != nil {
		t.Fatalf("create sprint todo: %v", err)
	}
	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
		Title:     "Outside sprint",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull); err != nil {
		t.Fatalf("create unscheduled todo: %v", err)
	}

	resp2, out := doMCP(t, client, ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
			"sprintId":    sp.ID,
		},
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	backlog := boardColumnByKey(t, out["data"].(map[string]any)["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["title"] != "In sprint" {
		t.Fatalf("unexpected sprint-filtered items: %#v", items)
	}
}

func TestMCPBoardGetAuthFailure(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "Board Auth Project",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "Board Auth Project")

	resp2, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": slug,
		},
	})
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp2.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", errObj["code"])
	}
}

func TestMCPBoardGetCapabilityUnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool": "board.get",
		"input": map[string]any{
			"projectSlug": "demo",
		},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "CAPABILITY_UNAVAILABLE" {
		t.Fatalf("expected CAPABILITY_UNAVAILABLE, got %#v", errObj["code"])
	}
}

func TestMCPUnknownToolReturnsNotFoundEnvelope(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{
		"tool":  "missing.tool",
		"input": map[string]any{},
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if out["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", out["ok"])
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %#v", errObj["code"])
	}
	if _, ok := errObj["details"].(map[string]any); !ok {
		t.Fatalf("expected details object, got %#v", errObj["details"])
	}
}

func TestMCPInvalidJSONReturnsValidationError(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewBufferString(`{"tool":"projects.list"} {"extra":true}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if out["ok"] != false {
		t.Fatalf("expected ok=false, got %#v", out["ok"])
	}
	errObj := out["error"].(map[string]any)
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Fatalf("expected VALIDATION_ERROR, got %#v", errObj["code"])
	}
}
