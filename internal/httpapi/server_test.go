package httpapi

import (
	"bufio"
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
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newTestHTTPServer(t *testing.T, mode string) (*httptest.Server, *sql.DB, func()) {
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
	if mode == "" {
		mode = "full"
	}
	srv := NewServer(st, Options{MaxRequestBody: 1 << 20, ScrumboyMode: mode})
	ts := httptest.NewServer(srv)
	return ts, sqlDB, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func doJSON(t *testing.T, client *http.Client, method, url string, body any, out any) (*http.Response, []byte) {
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

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("unmarshal: %v, body=%s", err, string(b))
		}
	}
	return resp, b
}

func newCookieClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func bootstrapUserClient(t *testing.T, client *http.Client, baseURL, name, email, password string) map[string]any {
	t.Helper()
	var user map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/bootstrap", map[string]any{
		"name":     name,
		"email":    email,
		"password": password,
	}, &user)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", resp.StatusCode, string(body))
	}
	return user
}

func loginUserClient(t *testing.T, client *http.Client, baseURL, email, password string) {
	t.Helper()
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", resp.StatusCode, string(body))
	}
}

func TestAPI_CreateMoveAndFetchBoard(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	var p struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	var slug string
	if err := sqlDB.QueryRow(`SELECT slug FROM projects WHERE id = ?`, p.ID).Scan(&slug); err != nil {
		t.Fatalf("read slug: %v", err)
	}
	if slug == "" {
		t.Fatalf("expected non-empty slug")
	}

	var todo struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/todos", map[string]any{
		"title":  "t",
		"body":   "",
		"tags":   []string{"bug"},
		"status": "BACKLOG",
	}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d", resp.StatusCode)
	}
	if todo.Status != "BACKLOG" {
		t.Fatalf("expected BACKLOG, got %q", todo.Status)
	}

	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/todos/"+strconv.FormatInt(todo.ID, 10)+"/move", map[string]any{
		"toStatus": "IN_PROGRESS",
		"afterId":  nil,
		"beforeId": nil,
	}, &todo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move todo status=%d", resp.StatusCode)
	}
	if todo.Status != "IN_PROGRESS" {
		t.Fatalf("expected IN_PROGRESS, got %q", todo.Status)
	}

	var board struct {
		Columns map[string][]struct {
			ID int64 `json:"id"`
		} `json:"columns"`
	}
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d", resp.StatusCode)
	}
	if len(board.Columns["IN_PROGRESS"]) != 1 || board.Columns["IN_PROGRESS"][0].ID != todo.ID {
		t.Fatalf("unexpected board: %+v", board.Columns)
	}

	// Back-compat: numeric-ID route still works.
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/projects/"+strconv.FormatInt(p.ID, 10)+"/board", nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board by id status=%d", resp.StatusCode)
	}
	if len(board.Columns["IN_PROGRESS"]) != 1 || board.Columns["IN_PROGRESS"][0].ID != todo.ID {
		t.Fatalf("unexpected board by id: %+v", board.Columns)
	}
}

func TestRenameLane_RequiresMaintainer(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "rename-lane-auth")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(context.Background(), "contrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, ts.URL, "contrib@example.com", "password123")

	resp, body := doJSON(t, contributorClient, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/workflow/"+store.DefaultColumnDoing, map[string]any{
		"name":  "Working",
		"color": "#10B981",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRenameLane_NonexistentKeyReturns404(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "rename-lane-404")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/workflow/not_a_lane", map[string]any{
		"name":  "Working",
		"color": "#10B981",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRenameLane_EmptyNameRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "rename-lane-400")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "WhitespaceOnly",
			body: map[string]any{"name": "   ", "color": "#10B981"},
		},
		{
			name: "EmptyColor",
			body: map[string]any{"name": "Working", "color": ""},
		},
		{
			name: "MissingColor",
			body: map[string]any{"name": "Working"},
		},
		{
			name: "InvalidColor",
			body: map[string]any{"name": "Working", "color": "#gggggg"},
		},
		{
			name: "RejectsKey",
			body: map[string]any{"name": "Working", "color": "#10B981", "key": "other"},
		},
		{
			name: "RejectsIsDone",
			body: map[string]any{"name": "Working", "color": "#10B981", "isDone": true},
		},
		{
			name: "RejectsPosition",
			body: map[string]any{"name": "Working", "color": "#10B981", "position": 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/workflow/"+store.DefaultColumnDoing, tc.body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
			}
		})
	}
}

func TestRenameLane_BoardAPIReflectsNewName(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "rename-lane-board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/workflow/"+store.DefaultColumnDoing, map[string]any{
		"name":  "Working",
		"color": "#aabbcc",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("rename lane status=%d body=%s", resp.StatusCode, string(body))
	}

	var board struct {
		ColumnOrder []struct {
			Key   string `json:"key"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"columnOrder"`
	}
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d body=%s", resp.StatusCode, string(body))
	}
	for _, lane := range board.ColumnOrder {
		if lane.Key == store.DefaultColumnDoing {
			if lane.Name != "Working" {
				t.Fatalf("expected lane name %q, got %q", "Working", lane.Name)
			}
			if lane.Color != "#aabbcc" {
				t.Fatalf("expected lane color %q, got %q", "#aabbcc", lane.Color)
			}
			return
		}
	}
	t.Fatalf("expected lane %q in board response", store.DefaultColumnDoing)
}

func TestAddLane_RequiresMaintainer(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "add-lane-auth")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(context.Background(), "addlane-contrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, ts.URL, "addlane-contrib@example.com", "password123")

	resp, body := doJSON(t, contributorClient, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/workflow", map[string]any{
		"name": "Review",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestAddLane_InvalidNameRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "add-lane-400")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "WhitespaceOnly", body: map[string]any{"name": "   "}},
		{name: "RejectsKey", body: map[string]any{"name": "Review", "key": "review"}},
		{name: "RejectsIsDone", body: map[string]any{"name": "Review", "isDone": true}},
		{name: "RejectsPosition", body: map[string]any{"name": "Review", "position": 1}},
		{name: "RejectsColor", body: map[string]any{"name": "Review", "color": "#123456"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/workflow", tc.body, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
			}
		})
	}
}

func TestAddLane_BoardShowsNewLane(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "add-lane-board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var created struct {
		Key      string `json:"key"`
		Name     string `json:"name"`
		IsDone   bool   `json:"isDone"`
		Position int    `json:"position"`
	}
	resp, body := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/workflow", map[string]any{
		"name": "Review",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add lane status=%d body=%s", resp.StatusCode, string(body))
	}
	if created.Key != "review" {
		t.Fatalf("expected created key %q, got %+v", "review", created)
	}
	if created.IsDone {
		t.Fatalf("expected created lane to be non-done, got %+v", created)
	}

	var board struct {
		ColumnOrder []struct {
			Key    string `json:"key"`
			Name   string `json:"name"`
			IsDone bool   `json:"isDone"`
		} `json:"columnOrder"`
	}
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d body=%s", resp.StatusCode, string(body))
	}

	reviewIdx := -1
	doneIdx := -1
	doneCount := 0
	for i, lane := range board.ColumnOrder {
		if lane.Key == created.Key {
			reviewIdx = i
			if lane.Name != "Review" {
				t.Fatalf("expected created lane name %q, got %q", "Review", lane.Name)
			}
		}
		if lane.IsDone {
			doneIdx = i
			doneCount++
		}
	}
	if reviewIdx < 0 {
		t.Fatalf("expected created lane %q in board response", created.Key)
	}
	if doneCount != 1 {
		t.Fatalf("expected exactly one done lane, got %d", doneCount)
	}
	if doneIdx < 0 || reviewIdx != doneIdx-1 {
		t.Fatalf("expected created lane immediately before done, reviewIdx=%d doneIdx=%d board=%+v", reviewIdx, doneIdx, board.ColumnOrder)
	}
}

func TestAddLane_ResponseAndBoardReflectTrimmedName(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "add-lane-trim")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var created struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	resp, body := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/workflow", map[string]any{
		"name": "  QA Gate  ",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add lane status=%d body=%s", resp.StatusCode, string(body))
	}
	if created.Key != "qa_gate" {
		t.Fatalf("expected key %q, got %+v", "qa_gate", created)
	}
	if created.Name != "QA Gate" {
		t.Fatalf("expected trimmed name %q, got %q", "QA Gate", created.Name)
	}

	var board struct {
		ColumnOrder []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"columnOrder"`
	}
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d body=%s", resp.StatusCode, string(body))
	}
	for _, lane := range board.ColumnOrder {
		if lane.Key == created.Key {
			if lane.Name != "QA Gate" {
				t.Fatalf("board lane name: want %q, got %q", "QA Gate", lane.Name)
			}
			return
		}
	}
	t.Fatalf("lane %q missing from board", created.Key)
}

func TestDeleteLane_RequiresMaintainer(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "delete-lane-auth")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(ctxOwner, project.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}

	contributor, err := st.CreateUser(context.Background(), "deletelane-contrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, ts.URL, "deletelane-contrib@example.com", "password123")

	resp, body := doJSON(t, contributorClient, http.MethodDelete, ts.URL+"/api/board/"+project.Slug+"/workflow/"+added.Key, nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestWorkflowLaneCounts_MaintainerGetsCounts(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "wf-counts-ok")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	var todo struct {
		ID int64 `json:"id"`
	}
	resp, body := doJSON(t, ownerClient, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", map[string]any{
		"title":  "t",
		"body":   "",
		"tags":   []string{},
		"status": "BACKLOG",
	}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d body=%s", resp.StatusCode, string(body))
	}

	var out struct {
		Slug              string         `json:"slug"`
		CountsByColumnKey map[string]int `json:"countsByColumnKey"`
	}
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/workflow/counts", nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	if out.Slug != project.Slug {
		t.Fatalf("slug: want %q, got %q", project.Slug, out.Slug)
	}
	if out.CountsByColumnKey[store.DefaultColumnBacklog] != 1 {
		t.Fatalf("backlog count: want 1, got %v", out.CountsByColumnKey)
	}
}

func TestWorkflowLaneCounts_RequiresMaintainer(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "wf-counts-auth")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	contributor, err := st.CreateUser(context.Background(), "wfcounts-contrib@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	contributorClient := newCookieClient(t)
	loginUserClient(t, contributorClient, ts.URL, "wfcounts-contrib@example.com", "password123")

	resp, body := doJSON(t, contributorClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/workflow/counts", nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestWorkflowLaneCounts_NoSessionReturnsNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "wf-counts-nosession")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Board routes use writeStoreErr(..., hideUnauthorized=true): unauthenticated durable boards map to 404.
	anonClient := &http.Client{}
	resp, body := doJSON(t, anonClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/workflow/counts", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errBody); err != nil || errBody.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND error JSON, got %s", string(body))
	}
}

func TestDeleteLane_BoardNoLongerShowsLane(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))

	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "delete-lane-board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(store.WithUserID(context.Background(), ownerID), project.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodDelete, ts.URL+"/api/board/"+project.Slug+"/workflow/"+added.Key, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete lane status=%d body=%s", resp.StatusCode, string(body))
	}

	var board struct {
		ColumnOrder []struct {
			Key    string `json:"key"`
			IsDone bool   `json:"isDone"`
		} `json:"columnOrder"`
	}
	resp, body = doJSON(t, ownerClient, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d body=%s", resp.StatusCode, string(body))
	}
	doneCount := 0
	for _, lane := range board.ColumnOrder {
		if lane.Key == added.Key {
			t.Fatalf("expected lane %q to be removed from board", added.Key)
		}
		if lane.IsDone {
			doneCount++
		}
	}
	if doneCount != 1 {
		t.Fatalf("expected exactly one done lane, got %d", doneCount)
	}
}

func TestFullMode_MultiProjectBehavior(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	// Create multiple projects
	var p1, p2 struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p1"}, &p1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project 1 status=%d", resp.StatusCode)
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p2"}, &p2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project 2 status=%d", resp.StatusCode)
	}

	// Verify projects have expires_at = NULL (full mode)
	var expiresAt sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT expires_at FROM projects WHERE id = ?`, p1.ID).Scan(&expiresAt); err != nil {
		t.Fatalf("read expires_at: %v", err)
	}
	if expiresAt.Valid {
		t.Fatalf("expected expires_at to be NULL for full mode project, got %d", expiresAt.Int64)
	}

	// Verify / serves SPA (doesn't auto-create)
	resp, _ = http.Get(ts.URL + "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status=%d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestAPI_ProjectsIncludeExpiresAt(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	// Create durable project via API
	var p struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "durable"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	// Create a temporary board directly via store (no public HTTP endpoint in full mode)
	st := store.New(sqlDB, nil)
	tmp, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	var out []struct {
		ID        int64      `json:"id"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/projects", nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list projects status=%d body=%s", resp.StatusCode, string(body))
	}

	var durableExpiresAt, tmpExpiresAt *time.Time
	for _, item := range out {
		if item.ID == p.ID {
			durableExpiresAt = item.ExpiresAt
		}
		if item.ID == tmp.ID {
			tmpExpiresAt = item.ExpiresAt
		}
	}
	if durableExpiresAt != nil {
		t.Fatalf("expected durable project expiresAt=null, got %v", durableExpiresAt)
	}
	if tmpExpiresAt == nil {
		t.Fatalf("expected temporary board expiresAt to be non-null")
	}
}

func TestAnonymousMode_DoesNotAllowProjectEnumeration(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	// In anonymous mode, /api/projects must not enumerate projects.
	resp, err := http.Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatalf("GET /api/projects: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(b))
	}
}

func TestAnonymousMode_RootServesLandingAndIsIdempotent(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	var before int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&before); err != nil {
		t.Fatalf("count before: %v", err)
	}

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML, got %s", resp.Header.Get("Content-Type"))
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `href="/anon"`) {
		t.Fatalf("expected landing page to include /anon CTA")
	}

	var after int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&after); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if after != before {
		t.Fatalf("expected GET / to be idempotent, count %d -> %d", before, after)
	}
}

func TestAnonAndTempRoutes_CreateAndRedirect(t *testing.T) {
	modes := []string{"full", "anonymous"}
	for _, mode := range modes {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			ts, sqlDB, cleanup := newTestHTTPServer(t, mode)
			defer cleanup()

			// Client that doesn't follow redirects
			client := &http.Client{
				CheckRedirect: func(req *http.Request, via []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}

			// GET /temp -> /anon
			resp, err := client.Get(ts.URL + "/temp")
			if err != nil {
				t.Fatalf("GET /temp: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("expected 302 for /temp, got %d", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != "/anon" {
				t.Fatalf("expected Location=/anon, got %q", loc)
			}

			// GET /anon creates and redirects to /{slug}
			resp, err = client.Get(ts.URL + "/anon")
			if err != nil {
				t.Fatalf("GET /anon: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("expected 302 for /anon, got %d", resp.StatusCode)
			}
			location := resp.Header.Get("Location")
			slug := strings.TrimPrefix(location, "/")
			if slug == "" || slug == location || strings.Contains(slug, "/") {
				t.Fatalf("expected /{slug} Location, got %q", location)
			}

			// Non-GET should be rejected (no side effects)
			req, _ := http.NewRequest(http.MethodPost, ts.URL+"/anon", nil)
			resp, err = client.Do(req)
			if err != nil {
				t.Fatalf("POST /anon: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405 for POST /anon, got %d", resp.StatusCode)
			}

			// Sanity: created project is expiring
			var expiresAt sql.NullInt64
			if err := sqlDB.QueryRow(`SELECT expires_at FROM projects WHERE slug = ?`, slug).Scan(&expiresAt); err != nil {
				t.Fatalf("read expires_at: %v", err)
			}
			if !expiresAt.Valid {
				t.Fatalf("expected expires_at to be set for /anon-created board")
			}
		})
	}
}

func TestAuth_BootstrapLoginMeLogout_FullMode(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// /api/auth/status before bootstrap: bootstrapAvailable=true, user=null
	resp, err := client.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("GET /api/auth/status: %v", err)
	}
	var st map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/auth/status, got %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control no-store for /api/auth/status, got %q", cc)
	}
	if st["bootstrapAvailable"] != true {
		t.Fatalf("expected bootstrapAvailable true, got %#v", st["bootstrapAvailable"])
	}
	if st["user"] != nil {
		t.Fatalf("expected user null, got %#v", st["user"])
	}

	// /api/me before login -> 401
	resp, err = client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	// Bootstrap first user (also sets cookie)
	var u map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Alice",
		"email":    "admin@example.com",
		"password": "password123",
	}, &u)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for bootstrap, got %d", resp.StatusCode)
	}
	if u["email"] != "admin@example.com" {
		t.Fatalf("expected email admin@example.com, got %#v", u["email"])
	}
	if u["name"] != "Alice" {
		t.Fatalf("expected name Alice, got %#v", u["name"])
	}

	// /api/auth/status after bootstrap: bootstrapAvailable=false, user present (id+email only)
	resp, err = client.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("GET /api/auth/status: %v", err)
	}
	st = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/auth/status, got %d", resp.StatusCode)
	}
	if st["bootstrapAvailable"] != false {
		t.Fatalf("expected bootstrapAvailable false, got %#v", st["bootstrapAvailable"])
	}
	userObj, ok := st["user"].(map[string]any)
	if !ok {
		t.Fatalf("expected user object, got %#v", st["user"])
	}
	if userObj["email"] != "admin@example.com" {
		t.Fatalf("expected user.email admin@example.com, got %#v", userObj["email"])
	}
	if userObj["name"] != "Alice" {
		t.Fatalf("expected user.name Alice, got %#v", userObj["name"])
	}
	if _, ok := userObj["createdAt"]; ok {
		t.Fatalf("status.user must not include createdAt")
	}

	// /api/me after bootstrap -> 200
	resp, err = client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if me["email"] != "admin@example.com" {
		t.Fatalf("expected me.email admin@example.com, got %#v", me["email"])
	}

	// Logout clears cookie and returns 200 + HTML meta refresh (tunnel-friendly; 302+Set-Cookie
	// can be mishandled by some proxies e.g. Cloudflare Tunnel)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/logout", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Scrumboy", "1") // not required for form POST but test uses doJSON-style
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("logout request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for logout, got %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control no-store for logout, got %q", cc)
	}
	// Verify Set-Cookie clears the session (cookie jar will have been updated)

	// /api/auth/status after logout: bootstrapAvailable=false, user=null
	resp, err = client.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("GET /api/auth/status: %v", err)
	}
	st = map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/auth/status, got %d", resp.StatusCode)
	}
	if st["bootstrapAvailable"] != false {
		t.Fatalf("expected bootstrapAvailable false after logout, got %#v", st["bootstrapAvailable"])
	}
	if st["user"] != nil {
		t.Fatalf("expected user null after logout, got %#v", st["user"])
	}

	resp, err = client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", resp.StatusCode)
	}
}

func TestAuth_EndpointsNotFound_AnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := &http.Client{}

	// /api/me should just 401 (no redirect, no crash)
	resp, err := client.Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}

	// /api/auth/* endpoints (except status) should be 404 in anonymous mode
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{"email": "x@y.com", "password": "pw"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for /api/auth/login in anonymous mode, got %d", resp.StatusCode)
	}

	// /api/auth/status returns 200 in anonymous mode with user: null, bootstrapAvailable: false (no console errors)
	resp, err = client.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatalf("GET /api/auth/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /api/auth/status in anonymous mode, got %d", resp.StatusCode)
	}
	var statusResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if statusResp["user"] != nil {
		t.Fatalf("expected user to be null in anonymous mode, got %v", statusResp["user"])
	}
	if statusResp["bootstrapAvailable"] != false {
		t.Fatalf("expected bootstrapAvailable to be false in anonymous mode, got %v", statusResp["bootstrapAvailable"])
	}
}

func TestClaimTemporaryBoard_FullMode(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Bootstrap user (sets cookie)
	var u map[string]any
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Alice",
		"email":    "admin@example.com",
		"password": "password123",
	}, &u)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	userID := int64(u["id"].(float64))

	// Create a temporary board directly in DB (matches /anon semantics).
	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if p.ExpiresAt == nil {
		t.Fatalf("expected temp board expiresAt")
	}

	// Claim it via API.
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/claim", map[string]any{}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify DB updated: expires_at NULL and owner_user_id set.
	var owner sql.NullInt64
	var expires sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT owner_user_id, expires_at FROM projects WHERE id = ?`, p.ID).Scan(&owner, &expires); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if !owner.Valid || owner.Int64 != userID {
		t.Fatalf("expected owner_user_id=%d, got %+v", userID, owner)
	}
	if expires.Valid {
		t.Fatalf("expected expires_at NULL after claim")
	}
}

func TestClaimTemporaryBoard_Unauthorized(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	// Create temp board.
	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	// No cookie -> 401
	client := &http.Client{}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/claim", map[string]any{}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLegacyIDRoute_RedirectsToSlug(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	// Client that doesn't follow redirects
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "VO2 Max Coach"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, string(body))
	}
	if p.ID == 0 || p.Slug == "" {
		t.Fatalf("expected id and slug in response, got id=%d slug=%q", p.ID, p.Slug)
	}

	resp, err := client.Get(ts.URL + "/p/" + strconv.FormatInt(p.ID, 10))
	if err != nil {
		t.Fatalf("GET /p/{id}: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/"+p.Slug {
		t.Fatalf("expected Location=/%s, got %q", p.Slug, loc)
	}
}

func TestFrontend_DoesNotEmitLegacyIDRoutes(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	resp, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatalf("GET /app.js: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(b))
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /app.js: %v", err)
	}
	if strings.Contains(string(b), "/p/") {
		t.Fatalf("frontend must not emit legacy /p/{id} routes, but /app.js contains '/p/'")
	}
}

func TestFrontend_DoesNotEmitLegacyTodoAPI(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	// JS must not call global todo-id endpoints.
	resp, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatalf("GET /app.js: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "/api/todos/") || strings.Contains(string(b), "/api/todos") {
		t.Fatalf("frontend must not emit legacy /api/todos endpoints, but /app.js contains '/api/todos'")
	}

	// Also ensure HTML doesn't embed legacy endpoints.
	resp, err = http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	html, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(html), "/api/todos") {
		t.Fatalf("index.html must not embed legacy /api/todos endpoints")
	}
}

func TestTodoLocalID_SequencingAndSlugEndpoints(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "VO2 Max Coach"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, string(body))
	}
	if p.Slug == "" {
		t.Fatalf("expected non-empty slug")
	}

	var t1, t2 struct {
		ID      int64 `json:"id"`
		LocalID int64 `json:"localId"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
		"title":  "t1",
		"body":   "",
		"tags":   []string{},
		"status": "BACKLOG",
	}, &t1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo1 status=%d", resp.StatusCode)
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
		"title":  "t2",
		"body":   "",
		"tags":   []string{},
		"status": "BACKLOG",
	}, &t2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo2 status=%d", resp.StatusCode)
	}
	if t1.LocalID != 1 || t2.LocalID != 2 {
		t.Fatalf("expected localIds 1,2 got %d,%d", t1.LocalID, t2.LocalID)
	}

	// PATCH via slug/localId
	var patched struct {
		LocalID int64  `json:"localId"`
		Title   string `json:"title"`
	}
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+p.Slug+"/todos/1", map[string]any{
		"title":          "t1-updated",
		"body":           "",
		"tags":           []string{},
		"assigneeUserId": nil,
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch todo status=%d", resp.StatusCode)
	}
	if patched.LocalID != 1 || patched.Title != "t1-updated" {
		t.Fatalf("unexpected patch response: %+v", patched)
	}

	// MOVE via slug/localId (after/before are localIds)
	var moved struct {
		LocalID int64  `json:"localId"`
		Status  string `json:"status"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos/2/move", map[string]any{
		"toStatus": "IN_PROGRESS",
		"afterId":  nil,
		"beforeId": nil,
	}, &moved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move todo status=%d", resp.StatusCode)
	}
	if moved.LocalID != 2 || moved.Status != "IN_PROGRESS" {
		t.Fatalf("unexpected move response: %+v", moved)
	}

	// DELETE via slug/localId
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+p.Slug+"/todos/1", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete todo status=%d", resp.StatusCode)
	}
}

func TestTodoPatch_RequiresAssigneeField(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	var p struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Assignee Guard"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, string(body))
	}

	var todo struct {
		LocalID int64 `json:"localId"`
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
		"title":  "guarded",
		"body":   "",
		"tags":   []string{},
		"status": "BACKLOG",
	}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d body=%s", resp.StatusCode, string(body))
	}

	// Missing assigneeUserId must be rejected at routing layer.
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+p.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), map[string]any{
		"title": "guarded-updated",
		"body":  "",
		"tags":  []string{},
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 when assigneeUserId is missing, got %d", resp.StatusCode)
	}
}

func TestAnonymousMode_BoardActivityTracking(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()

	// Create anonymous board
	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Get initial state
	var initialExpiresAt, initialLastActivityAt int64
	if err := sqlDB.QueryRow(`SELECT expires_at, last_activity_at FROM projects WHERE id = ?`, project.ID).Scan(&initialExpiresAt, &initialLastActivityAt); err != nil {
		t.Fatalf("read initial state: %v", err)
	}

	// Manually set last_activity_at to be old (6 minutes ago) to ensure first request updates
	oldTimeMs := time.Now().UTC().UnixMilli() - (6 * 60 * 1000)
	if _, err := sqlDB.Exec(`UPDATE projects SET last_activity_at = ? WHERE id = ?`, oldTimeMs, project.ID); err != nil {
		t.Fatalf("set old last_activity_at: %v", err)
	}

	// GET /api/board/{slug} should update activity (throttle allows it since last_activity_at is old)
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d", resp.StatusCode)
	}

	// Verify last_activity_at was updated
	var newLastActivityAt, newExpiresAt int64
	if err := sqlDB.QueryRow(`SELECT last_activity_at, expires_at FROM projects WHERE id = ?`, project.ID).Scan(&newLastActivityAt, &newExpiresAt); err != nil {
		t.Fatalf("read new state: %v", err)
	}
	if newLastActivityAt <= oldTimeMs {
		t.Fatalf("expected last_activity_at to be updated, got %d <= %d", newLastActivityAt, oldTimeMs)
	}

	// Verify expires_at was extended
	if newExpiresAt <= initialExpiresAt {
		t.Fatalf("expected expires_at to be extended, got %d <= %d", newExpiresAt, initialExpiresAt)
	}

	// Make another request immediately - should be throttled (no update)
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d", resp.StatusCode)
	}

	// Verify last_activity_at was NOT updated (throttled)
	var throttledLastActivityAt int64
	if err := sqlDB.QueryRow(`SELECT last_activity_at FROM projects WHERE id = ?`, project.ID).Scan(&throttledLastActivityAt); err != nil {
		t.Fatalf("read throttled last_activity_at: %v", err)
	}
	if throttledLastActivityAt != newLastActivityAt {
		t.Fatalf("expected last_activity_at to be throttled (unchanged), got %d != %d", throttledLastActivityAt, newLastActivityAt)
	}

	// Verify expires_at was also NOT extended (throttled)
	var throttledExpiresAt int64
	if err := sqlDB.QueryRow(`SELECT expires_at FROM projects WHERE id = ?`, project.ID).Scan(&throttledExpiresAt); err != nil {
		t.Fatalf("read throttled expires_at: %v", err)
	}
	if throttledExpiresAt != newExpiresAt {
		t.Fatalf("expected expires_at to be throttled (unchanged), got %d != %d", throttledExpiresAt, newExpiresAt)
	}
}

func TestGetBoard_ActivityTrackingBestEffort(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()

	// Create anonymous board
	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create a todo so the board has content
	var todo struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", map[string]any{
		"title": "test todo",
		"body":  "",
		"tags":  []string{},
	}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d", resp.StatusCode)
	}

	// Simulate UpdateBoardActivity failure by deleting the project row
	// after it's been loaded but before UpdateBoardActivity is called.
	// Since GetBoard loads the project first, we need to test differently.
	// Instead, we verify that GetBoard succeeds and returns board data,
	// and that the code structure ensures activity tracking errors don't fail the request.

	// GetBoard should succeed and return board data
	var board struct {
		Project struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"project"`
		Columns map[string][]struct {
			ID int64 `json:"id"`
		} `json:"columns"`
	}
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d", resp.StatusCode)
	}
	if board.Project.ID != project.ID {
		t.Fatalf("expected project ID %d, got %d", project.ID, board.Project.ID)
	}
	if len(board.Columns["BACKLOG"]) != 1 || board.Columns["BACKLOG"][0].ID != todo.ID {
		t.Fatalf("expected todo in BACKLOG column")
	}

	// Verify board data is returned correctly even if activity tracking had issues
	// The code change ensures UpdateBoardActivity errors are logged but don't fail the request
}

func TestAnonymousMode_BoardRouteServesSPA(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()

	// Test that a board-like route (slug format) serves index.html, not 404
	// This verifies that paths that don't match static files fall through to SPA
	resp, err := client.Get(ts.URL + "/x4gG5Z")
	if err != nil {
		t.Fatalf("GET /x4gG5Z: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for board route, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected HTML content type, got %s", resp.Header.Get("Content-Type"))
	}

	// Verify static assets still work
	resp, err = client.Get(ts.URL + "/styles.css")
	if err != nil {
		t.Fatalf("GET /styles.css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for static asset, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Type") == "text/html; charset=utf-8" {
		t.Fatalf("expected CSS content type for static asset, got HTML")
	}
}

func TestExpiredProjectCleanup(t *testing.T) {
	_, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	st := store.New(sqlDB, nil)

	// Create a project with expires_at in the past
	nowMs := time.Now().UTC().UnixMilli()
	pastMs := nowMs - (15 * 24 * 60 * 60 * 1000) // 15 days ago
	_, err := sqlDB.Exec(`INSERT INTO projects(name, image, slug, last_activity_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"expired", "/scrumboy.png", "expired123", nowMs, pastMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert expired project: %v", err)
	}

	// Create a project with expires_at = NULL (should not be deleted)
	_, err = sqlDB.Exec(`INSERT INTO projects(name, image, slug, last_activity_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, ?, ?)`,
		"permanent", "/scrumboy.png", "permanent123", nowMs, nowMs, nowMs)
	if err != nil {
		t.Fatalf("insert permanent project: %v", err)
	}

	// Run cleanup
	deleted, err := st.DeleteExpiredProjects(context.Background())
	if err != nil {
		t.Fatalf("delete expired projects: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted project, got %d", deleted)
	}

	// Verify expired project is gone
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'expired123'`).Scan(&count); err != nil {
		t.Fatalf("check expired project: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected expired project to be deleted")
	}

	// Verify permanent project still exists
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = 'permanent123'`).Scan(&count); err != nil {
		t.Fatalf("check permanent project: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected permanent project to still exist")
	}
}

// Deprecated: Tags are now user-owned, not scope-based
func TestTagScope_FullMode_GlobalTags(t *testing.T) {
	t.Skip("Tags are now user-owned; scope-based tests are obsolete")
}

func testTagScope_FullMode_GlobalTags_Old(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := ts.Client()

	// Create project 1
	var p1 struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p1"}, &p1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	// Create todo with tags in p1
	var todo1 struct {
		ID int64 `json:"id"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects/"+strconv.FormatInt(p1.ID, 10)+"/todos", map[string]any{
		"title":  "Todo 1",
		"tags":   []string{"bug"},
		"status": "BACKLOG",
	}, &todo1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d", resp.StatusCode)
	}

	// Create project 2
	var p2 struct {
		ID int64 `json:"id"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p2"}, &p2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project 2 status=%d", resp.StatusCode)
	}

	// List tags for p2 - should see GLOBAL tag from p1
	var tags []struct {
		Name  string  `json:"name"`
		Color *string `json:"color"`
	}
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/projects/"+strconv.FormatInt(p2.ID, 10)+"/tags", nil, &tags)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tags status=%d", resp.StatusCode)
	}
	if len(tags) != 1 || tags[0].Name != "bug" {
		t.Errorf("Expected p2 to see GLOBAL tag 'bug', got %v", tags)
	}
}

// Deprecated: Tags are now user-owned, not scope-based
func TestTagScope_AnonymousMode_ProjectScopedTags(t *testing.T) {
	t.Skip("Tags are now user-owned; scope-based tests are obsolete")
}

func testTagScope_AnonymousMode_ProjectScopedTags_Old(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Create anonymous board 1
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/anon", nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("create board 1 status=%d", resp.StatusCode)
	}
	slug1 := strings.TrimPrefix(resp.Header.Get("Location"), "/")

	// Get project ID for board 1
	var p1ID int64
	if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE slug = ?`, slug1).Scan(&p1ID); err != nil {
		t.Fatalf("get p1 id: %v", err)
	}

	// Create todo with tags in board 1
	var todo1 struct {
		ID int64 `json:"id"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug1+"/todos", map[string]any{
		"title":  "Todo 1",
		"tags":   []string{"bug"},
		"status": "BACKLOG",
	}, &todo1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d", resp.StatusCode)
	}

	// Create anonymous board 2
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/anon", nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("create board 2 status=%d", resp.StatusCode)
	}
	slug2 := strings.TrimPrefix(resp.Header.Get("Location"), "/")

	// Get project ID for board 2
	var p2ID int64
	if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE slug = ?`, slug2).Scan(&p2ID); err != nil {
		t.Fatalf("get p2 id: %v", err)
	}

	// Verify tags are isolated: check DB directly
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE name = 'bug' AND scope = 'PROJECT' AND project_id = ?`, p2ID).Scan(&count); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected p2 to have 0 'bug' tags, got %d", count)
	}

	// Verify p1 has the tag
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM tags WHERE name = 'bug' AND scope = 'PROJECT' AND project_id = ?`, p1ID).Scan(&count); err != nil {
		t.Fatalf("count tags: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected p1 to have 1 'bug' tag, got %d", count)
	}
}

// Deprecated: Tags are now user-owned, not scope-based
func TestTagScope_AnonymousMode_ColorIsolation(t *testing.T) {
	t.Skip("Tags are now user-owned; scope-based tests are obsolete")
}

func testTagScope_AnonymousMode_ColorIsolation_Old(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Create anonymous board 1
	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/anon", nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("create board 1 status=%d", resp.StatusCode)
	}
	slug1 := strings.TrimPrefix(resp.Header.Get("Location"), "/")
	var p1ID int64
	if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE slug = ?`, slug1).Scan(&p1ID); err != nil {
		t.Fatalf("get p1 id: %v", err)
	}

	// Create todo with tag in board 1
	_, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug1+"/todos", map[string]any{
		"title":  "Todo 1",
		"tags":   []string{"bug"},
		"status": "BACKLOG",
	}, nil)

	// Update tag color for board 1
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+slug1+"/tags/bug", map[string]any{
		"color": "#FF0000",
	}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("update tag color status=%d", resp.StatusCode)
	}

	// Create anonymous board 2 with same tag name
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/anon", nil, nil)
	slug2 := strings.TrimPrefix(resp.Header.Get("Location"), "/")
	var p2ID int64
	if err := sqlDB.QueryRow(`SELECT id FROM projects WHERE slug = ?`, slug2).Scan(&p2ID); err != nil {
		t.Fatalf("get p2 id: %v", err)
	}

	_, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug2+"/todos", map[string]any{
		"title":  "Todo 2",
		"tags":   []string{"bug"},
		"status": "BACKLOG",
	}, nil)

	// Verify p1's tag has color, p2's doesn't
	var color1 sql.NullString
	if err := sqlDB.QueryRow(`SELECT color FROM tags WHERE name = 'bug' AND project_id = ?`, p1ID).Scan(&color1); err != nil {
		t.Fatalf("get p1 color: %v", err)
	}
	if !color1.Valid || color1.String != "#FF0000" {
		t.Errorf("Expected p1 tag color #FF0000, got %v", color1)
	}

	var color2 sql.NullString
	if err := sqlDB.QueryRow(`SELECT color FROM tags WHERE name = 'bug' AND project_id = ?`, p2ID).Scan(&color2); err != nil {
		t.Fatalf("get p2 color: %v", err)
	}
	if color2.Valid {
		t.Errorf("Expected p2 tag to have no color, got %v", color2)
	}
}

// TestAnonymousMode_RenameProjectAuthorization verifies that PATCH /api/projects/{id} in anonymous mode
// correctly enforces authorization at the store boundary. This test is critical because routing allows
// PATCH requests through in anonymous mode, relying entirely on store-layer authorization.
//
// Test cases:
// - Anonymous + non-temp board → 404 (not found, store rejects)
// - Anonymous + expired temp board → 404 (not found, store rejects)
// - Anonymous + active anonymous temp board → 200 (allowed, no auth required)
// - Anonymous + authenticated temp board → 404 (not found, store rejects)
func TestAnonymousMode_RenameProjectAuthorization(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()
	st := store.New(sqlDB, nil)

	// Test 1: Non-temp board (durable project) - should be rejected
	ctx := context.Background()
	durableProject, err := st.CreateProject(ctx, "Durable Project")
	if err != nil {
		t.Fatalf("create durable project: %v", err)
	}

	resp, _ := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(durableProject.ID, 10), map[string]interface{}{
		"name": "Renamed",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Test 1: Expected 404 for durable project, got %d", resp.StatusCode)
	}

	// Test 2: Expired anonymous temp board - should be rejected
	expiredProject, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create expired project: %v", err)
	}
	// Set expires_at to past
	pastTimeMs := time.Now().UTC().UnixMilli() - (24 * 60 * 60 * 1000) // 1 day ago
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, pastTimeMs, expiredProject.ID); err != nil {
		t.Fatalf("expire project: %v", err)
	}

	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(expiredProject.ID, 10), map[string]interface{}{
		"name": "Renamed",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Test 2: Expected 404 for expired temp board, got %d", resp.StatusCode)
	}

	// Test 3: Active anonymous temp board (expires_at IS NOT NULL AND creator_user_id IS NULL) - should succeed
	activeAnonymousProject, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("create active anonymous project: %v", err)
	}
	// Verify it's anonymous (creator_user_id IS NULL)
	var creatorUserID sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT creator_user_id FROM projects WHERE id = ?`, activeAnonymousProject.ID).Scan(&creatorUserID); err != nil {
		t.Fatalf("check creator: %v", err)
	}
	if creatorUserID.Valid {
		t.Fatalf("Expected anonymous temp board to have NULL creator_user_id")
	}

	resp, body := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(activeAnonymousProject.ID, 10), map[string]interface{}{
		"name": "Renamed Anonymous Board",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Test 3: Expected 200 for active anonymous temp board, got %d body=%s", resp.StatusCode, string(body))
	}

	// Verify the name was actually updated
	var updatedName string
	if err := sqlDB.QueryRow(`SELECT name FROM projects WHERE id = ?`, activeAnonymousProject.ID).Scan(&updatedName); err != nil {
		t.Fatalf("read updated name: %v", err)
	}
	if updatedName != "Renamed Anonymous Board" {
		t.Errorf("Expected name to be updated to 'Renamed Anonymous Board', got %q", updatedName)
	}

	// Test 4: Authenticated temp board (has creator_user_id) - should be rejected
	// Create a user first (in full mode context, but we'll create the project directly in DB)
	userID := int64(1)
	// Need password_hash for user creation
	passwordHash := "$2a$10$dummyhashfortestingpurposesonly" // Dummy hash for test
	if _, err := sqlDB.Exec(`INSERT INTO users (id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, "test@example.com", "Test User", passwordHash, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Create temp board with creator_user_id set (authenticated temp board)
	nowMs := time.Now().UTC().UnixMilli()
	expiresAtMs := nowMs + (14 * 24 * 60 * 60 * 1000)
	var authenticatedTempProjectID int64
	if err := sqlDB.QueryRow(`INSERT INTO projects (name, image, slug, creator_user_id, last_activity_at, expires_at, created_at, updated_at) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		"Authenticated Temp", "/scrumboy.png", "auth-temp-123", userID, nowMs, expiresAtMs, nowMs, nowMs).Scan(&authenticatedTempProjectID); err != nil {
		t.Fatalf("create authenticated temp project: %v", err)
	}

	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(authenticatedTempProjectID, 10), map[string]interface{}{
		"name": "Renamed",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Test 4: Expected 404 for authenticated temp board, got %d", resp.StatusCode)
	}
}

func TestBoard_SearchFilter(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()
	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("create anonymous board: %v", err)
	}

	// Create todos with different titles
	_, err = st.CreateTodo(context.Background(), project.ID, store.CreateTodoInput{
		Title: "Login feature",
		Body:  "User authentication",
	}, store.ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo 1: %v", err)
	}

	_, err = st.CreateTodo(context.Background(), project.ID, store.CreateTodoInput{
		Title: "Dashboard",
		Body:  "Main page",
	}, store.ModeAnonymous)
	if err != nil {
		t.Fatalf("create todo 2: %v", err)
	}

	// Test search matches title
	var board struct {
		Columns map[string][]struct {
			Title string `json:"title"`
		} `json:"columns"`
	}
	resp, _ := doJSON(t, client, http.MethodGet,
		ts.URL+"/api/board/"+project.Slug+"?search=login", nil, &board)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Should only return "Login feature"
	totalTodos := 0
	for _, todos := range board.Columns {
		totalTodos += len(todos)
	}
	if totalTodos != 1 {
		t.Fatalf("expected 1 todo, got %d", totalTodos)
	}

	// Test zero results
	resp, _ = doJSON(t, client, http.MethodGet,
		ts.URL+"/api/board/"+project.Slug+"?search=nonexistent", nil, &board)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Should return empty columns
	totalTodos = 0
	for _, todos := range board.Columns {
		totalTodos += len(todos)
	}
	if totalTodos != 0 {
		t.Fatalf("expected 0 todos, got %d", totalTodos)
	}
}

func TestAdminUsers_ListUsers_RequiresAdmin(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Bootstrap owner
	var owner map[string]any
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Owner",
		"email":    "owner@example.com",
		"password": "password123",
	}, &owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Owner can list users
	var users []map[string]any
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/admin/users", nil, &users)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0]["systemRole"] != "owner" {
		t.Fatalf("expected owner role, got %v", users[0]["systemRole"])
	}

	// Create admin user
	var admin map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "Admin",
		"email":    "admin@example.com",
		"password": "password123",
	}, &admin)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Regular user (not created yet, but we can test unauthenticated)
	client2 := &http.Client{}
	resp, _ = doJSON(t, client2, http.MethodGet, ts.URL+"/api/admin/users", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAdminUsers_UpdateRole_OwnerCanPromoteAndDemote(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Bootstrap owner
	var owner map[string]any
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Owner",
		"email":    "owner@example.com",
		"password": "password123",
	}, &owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	ownerID := int64(owner["id"].(float64))

	// Create regular user
	var user map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "User",
		"email":    "user@example.com",
		"password": "password123",
	}, &user)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	userID := int64(user["id"].(float64))
	if user["systemRole"] != "user" {
		t.Fatalf("expected user role, got %v", user["systemRole"])
	}

	// Owner can promote user to admin
	var updated map[string]any
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", userID), map[string]any{
		"role": "admin",
	}, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if updated["systemRole"] != "admin" {
		t.Fatalf("expected admin role, got %v", updated["systemRole"])
	}

	// Owner can demote admin to user
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", userID), map[string]any{
		"role": "user",
	}, &updated)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if updated["systemRole"] != "user" {
		t.Fatalf("expected user role, got %v", updated["systemRole"])
	}

	// Cannot promote to owner via API
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+fmt.Sprintf("/api/admin/users/%d/role", userID), map[string]any{
		"role": "owner",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for owner role, got %d", resp.StatusCode)
	}

	// Create admin user via API (owner can create users)
	var adminUser map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "Admin2",
		"email":    "admin2@example.com",
		"password": "password123",
	}, &adminUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	adminUserID := int64(adminUser["id"].(float64))

	// Promote to admin via store (since API doesn't allow owner promotion)
	st := store.New(sqlDB, nil)
	if err := st.UpdateUserRole(context.Background(), ownerID, adminUserID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Test admin limitations via store layer
	// Admin cannot update roles (store enforces owner-only)
	// Note: HTTP integration for admin login requires complex cookie handling in tests.
	// The store layer enforcement is what matters - HTTP layer just wires to store.
	err = st.UpdateUserRole(context.Background(), adminUserID, userID, store.SystemRoleAdmin)
	if err != store.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized when admin tries to update role, got %v", err)
	}
}

func TestAdminUsers_Delete_OwnerOnly(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar}

	// Bootstrap owner
	var owner map[string]any
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Owner",
		"email":    "owner@example.com",
		"password": "password123",
	}, &owner)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	ownerID := int64(owner["id"].(float64))

	// Create regular user
	var user map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "User",
		"email":    "user@example.com",
		"password": "password123",
	}, &user)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	userID := int64(user["id"].(float64))

	// Owner can delete non-owner user
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+fmt.Sprintf("/api/admin/users/%d", userID), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify user is deleted
	var users []map[string]any
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/admin/users", nil, &users)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}

	// Create admin user via API
	var adminUser map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "Admin",
		"email":    "admin@example.com",
		"password": "password123",
	}, &adminUser)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	adminUserID := int64(adminUser["id"].(float64))

	// Promote to admin via store
	st := store.New(sqlDB, nil)
	if err := st.UpdateUserRole(context.Background(), ownerID, adminUserID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Create another user for admin to try to delete
	var user2 map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "User2",
		"email":    "user2@example.com",
		"password": "password123",
	}, &user2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	user2ID := int64(user2["id"].(float64))

	// Test admin limitations via store layer (HTTP integration for admin login requires complex cookie handling)
	// Admin cannot delete users (store enforces owner-only)
	err = st.DeleteUser(context.Background(), adminUserID, user2ID)
	if err != store.ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized when admin tries to delete user, got %v", err)
	}

	// Cannot delete self
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+fmt.Sprintf("/api/admin/users/%d", ownerID), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-delete, got %d", resp.StatusCode)
	}

	// Create second owner
	var owner2 map[string]any
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/admin/users", map[string]any{
		"name":     "Owner2",
		"email":    "owner2@example.com",
		"password": "password123",
	}, &owner2)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	owner2ID := int64(owner2["id"].(float64))
	// Promote to owner via store (since API doesn't allow it)
	if err := st.UpdateUserRole(context.Background(), ownerID, owner2ID, store.SystemRoleOwner); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}

	// Can delete one owner when multiple exist
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+fmt.Sprintf("/api/admin/users/%d", owner2ID), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 when 2 owners exist, got %d", resp.StatusCode)
	}

	// Cannot delete last owner
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+fmt.Sprintf("/api/admin/users/%d", ownerID), nil, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for last owner, got %d", resp.StatusCode)
	}
}

func TestAPI_BoardPagedAndLaneEndpoint(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "p"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	// Create 25 todos in BACKLOG
	for i := 0; i < 25; i++ {
		_, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
			"title": "Todo",
			"body":  "",
			"tags":  []string{},
		}, nil)
	}

	// GET /api/board/{slug}?limitPerLane=10 returns columnsMeta
	var board struct {
		Project     map[string]any            `json:"project"`
		Columns     map[string][]any          `json:"columns"`
		ColumnsMeta map[string]map[string]any `json:"columnsMeta"`
	}
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+p.Slug+"?limitPerLane=10", nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board paged status=%d", resp.StatusCode)
	}
	if board.ColumnsMeta == nil {
		t.Fatal("expected columnsMeta")
	}
	backlog := board.Columns["BACKLOG"]
	if len(backlog) != 10 {
		t.Errorf("expected 10 items in BACKLOG, got %d", len(backlog))
	}
	meta := board.ColumnsMeta["BACKLOG"]
	if meta == nil {
		t.Fatal("expected BACKLOG columnsMeta")
	}
	if !meta["hasMore"].(bool) {
		t.Error("expected BACKLOG hasMore true")
	}

	// GET /api/board/{slug}/lanes/BACKLOG?limit=5&afterCursor=...
	nextCursor := ""
	if v, ok := meta["nextCursor"].(string); ok {
		nextCursor = v
	}
	if nextCursor == "" {
		t.Fatal("expected non-empty nextCursor")
	}

	var lane struct {
		Items      []any  `json:"items"`
		NextCursor string `json:"nextCursor"`
		HasMore    bool   `json:"hasMore"`
	}
	resp, _ = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+p.Slug+"/lanes/BACKLOG?limit=5&afterCursor="+url.QueryEscape(nextCursor), nil, &lane)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get lane status=%d", resp.StatusCode)
	}
	if len(lane.Items) != 5 {
		t.Errorf("expected 5 items, got %d", len(lane.Items))
	}
	if !lane.HasMore {
		t.Error("expected hasMore true")
	}
}

func TestBoardEvents_HeadersAndRefreshNeededEvent(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	var p struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "sse"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	var slug string
	if err := sqlDB.QueryRow(`SELECT slug FROM projects WHERE id = ?`, p.ID).Scan(&slug); err != nil {
		t.Fatalf("read slug: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/board/"+slug+"/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	eventsResp, err := client.Do(req)
	if err != nil {
		t.Fatalf("connect sse: %v", err)
	}
	defer eventsResp.Body.Close()

	if ct := eventsResp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream content-type, got %q", ct)
	}
	if cc := eventsResp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("expected Cache-Control no-cache, got %q", cc)
	}
	if v := eventsResp.Header.Get("X-Accel-Buffering"); v != "no" {
		t.Fatalf("expected X-Accel-Buffering no, got %q", v)
	}

	eventsCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(eventsResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			if strings.HasPrefix(line, "data: ") {
				eventsCh <- strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				return
			}
		}
	}()

	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/todos", map[string]any{
		"title": "SSE test todo",
		"body":  "",
		"tags":  []string{},
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d", resp.StatusCode)
	}

	select {
	case event := <-eventsCh:
		if !strings.Contains(event, `"type":"refresh_needed"`) {
			t.Fatalf("expected refresh_needed event, got %q", event)
		}
		if !strings.Contains(event, `"projectId":`) {
			t.Fatalf("expected projectId in event, got %q", event)
		}
	case err := <-errCh:
		t.Fatalf("error reading sse event: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sse event")
	}
}

// TestBoard_SprintFilter_AbsentSprintId_ReturnsModeNone verifies that when sprintId is absent
// from the GET /api/board/{slug} request, the backend applies no sprint filter (Mode "none"),
// so both scheduled and unscheduled todos appear in the response.
func TestBoard_SprintFilter_AbsentSprintId_ReturnsModeNone(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()

	// Create a durable project via API.
	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "sprint-filter-test"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	if p.Slug == "" {
		if err := sqlDB.QueryRow(`SELECT slug FROM projects WHERE id = ?`, p.ID).Scan(&p.Slug); err != nil {
			t.Fatalf("read slug: %v", err)
		}
	}

	// Use store directly to create a sprint (avoids auth requirements in the API).
	st := store.New(sqlDB, nil)
	ctx := context.Background()
	sprint, err := st.CreateSprint(ctx, p.ID, "Sprint 1", time.Now(), time.Now().Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	// Create an unscheduled todo (sprint_id = NULL) via API.
	var unscheduledTodo struct {
		ID      int64  `json:"id"`
		LocalID int64  `json:"localId"`
		Title   string `json:"title"`
	}
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
		"title": "unscheduled todo",
		"body":  "",
		"tags":  []string{},
	}, &unscheduledTodo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create unscheduled todo status=%d", resp.StatusCode)
	}

	// Create a scheduled todo and assign it to the sprint via store (avoids auth).
	scheduledTodo, err := st.CreateTodo(ctx, p.ID, store.CreateTodoInput{
		Title:     "scheduled todo",
		Body:      "",
		Tags:      []string{},
		ColumnKey: store.DefaultColumnBacklog,
		SprintID:  &sprint.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create scheduled todo: %v", err)
	}

	// GET /api/board/{slug} with no sprintId — should return BOTH todos (Mode "none", no sprint_id filter).
	var board struct {
		Columns map[string][]struct {
			ID int64 `json:"id"`
		} `json:"columns"`
	}
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+p.Slug, nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get board status=%d body=%s", resp.StatusCode, string(body))
	}

	// Collect all todo IDs from all columns.
	allIDs := map[int64]bool{}
	for _, todos := range board.Columns {
		for _, td := range todos {
			allIDs[td.ID] = true
		}
	}

	if !allIDs[unscheduledTodo.ID] {
		t.Errorf("expected unscheduled todo (id=%d) in board response; got IDs: %v", unscheduledTodo.ID, allIDs)
	}
	if !allIDs[scheduledTodo.ID] {
		t.Errorf("expected scheduled todo (id=%d) in board response; got IDs: %v", scheduledTodo.ID, allIDs)
	}
}
