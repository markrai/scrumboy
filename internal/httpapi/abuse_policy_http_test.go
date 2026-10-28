package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/abusepolicy"
	"scrumboy/internal/db"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

func tightAbusePolicy() abusepolicy.Policy {
	p := abusepolicy.Default()
	p.RateLimits.CreatePerIP = abusepolicy.WindowLimit{Limit: 2, Window: time.Hour}
	p.RateLimits.CreatePerIPDaily = abusepolicy.WindowLimit{Limit: 3, Window: 24 * time.Hour}
	p.RateLimits.CreateInstance = abusepolicy.WindowLimit{Limit: 50, Window: time.Hour}
	p.RateLimits.CreateInstanceDaily = abusepolicy.WindowLimit{Limit: 50, Window: 24 * time.Hour}
	p.RateLimits.MutationPerIP = abusepolicy.WindowLimit{Limit: 200, Window: time.Minute}
	p.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 40, Window: 10 * time.Second}
	p.RateLimits.MutationPerBoard = abusepolicy.WindowLimit{Limit: 200, Window: time.Minute}
	p.RateLimits.BulkImportPerIP = abusepolicy.WindowLimit{Limit: 1, Window: time.Hour}
	p.RateLimits.ExportPerIP = abusepolicy.WindowLimit{Limit: 2, Window: time.Hour}
	return p
}

func assertRateLimited(t *testing.T, resp *http.Response, body []byte) {
	t.Helper()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
	if apiErrorCode(t, body) != "RATE_LIMITED" {
		t.Fatalf("code=%q body=%s", apiErrorCode(t, body), body)
	}
}

func TestAnonymousCreationHourlyBudget(t *testing.T) {
	policy := tightAbusePolicy()
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	client := ts.Client()
	prev := client.CheckRedirect
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { client.CheckRedirect = prev }()

	for i := 0; i < 2; i++ {
		resp, err := client.Get(ts.URL + "/anon")
		if err != nil {
			t.Fatalf("GET /anon %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("GET /anon %d status=%d", i, resp.StatusCode)
		}
	}
	resp, err := client.Get(ts.URL + "/anon")
	if err != nil {
		t.Fatalf("GET /anon overflow: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assertRateLimited(t, resp, body)
}

func TestAnonymousNumericTodoMutationsAreClosed(t *testing.T) {
	policy := tightAbusePolicy()
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := st.CreateTodo(context.Background(), project.ID, store.CreateTodoInput{
			Title:     fmt.Sprintf("n%d", i),
			ColumnKey: store.DefaultColumnBacklog,
		}, store.ModeAnonymous); err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	client := ts.Client()
	rows, err := sqlDB.Query(`SELECT id FROM todos WHERE project_id = ? ORDER BY id`, project.ID)
	if err != nil {
		t.Fatalf("list todos: %v", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	for i, id := range ids {
		resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", ts.URL, id), nil, nil)
		if resp.StatusCode != http.StatusNotFound || apiErrorCode(t, body) != "NOT_FOUND" {
			t.Fatalf("numeric DELETE %d status=%d body=%s", i, resp.StatusCode, body)
		}
	}
	var remaining int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 50 {
		t.Fatalf("remaining=%d want 50", remaining)
	}
}

func TestAnonymousSlugMutationBurst(t *testing.T) {
	policy := tightAbusePolicy()
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	for i := 0; i < 45; i++ {
		if _, err := st.CreateTodo(context.Background(), project.ID, store.CreateTodoInput{
			Title:     fmt.Sprintf("s%d", i),
			ColumnKey: store.DefaultColumnBacklog,
		}, store.ModeAnonymous); err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	client := ts.Client()
	ok := 0
	limited := 0
	for localID := 1; localID <= 45; localID++ {
		resp, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/board/%s/todos/%d", ts.URL, project.Slug, localID), nil, nil)
		switch resp.StatusCode {
		case http.StatusNoContent:
			ok++
		case http.StatusTooManyRequests:
			limited++
			assertRateLimited(t, resp, body)
		default:
			t.Fatalf("slug DELETE localID=%d status=%d body=%s", localID, resp.StatusCode, body)
		}
	}
	if ok != 40 || limited != 5 {
		t.Fatalf("ok=%d limited=%d want 40/5", ok, limited)
	}
}

func TestDurableNumericMutationsUnaffected(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 1, Window: 10 * time.Second}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "durable-abuse@example.com", "password123")
	var created struct {
		ID int64 `json:"id"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Durable"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, body)
	}
	st := store.New(sqlDB, nil)
	project, err := st.GetProject(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	var todo struct {
		ID int64 `json:"id"`
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", map[string]any{"title": "d"}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create todo status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", ts.URL, todo.ID), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("durable numeric DELETE status=%d body=%s", resp.StatusCode, body)
	}
}

func TestAnonymousSlugRenameAndNumericClose(t *testing.T) {
	policy := tightAbusePolicy()
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	client := ts.Client()
	resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/projects/%d", ts.URL, project.ID), map[string]any{"name": "Nope"}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("numeric rename status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug, map[string]any{"name": "Renamed"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slug rename status=%d body=%s", resp.StatusCode, body)
	}
}

func TestImportRecordCeilingWhenThrottlingDisabled(t *testing.T) {
	policy := abusepolicy.DisabledThrottling()
	policy.Safety.MaxTodosPerImport = 3
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	now := time.Now().UTC()
	todos := make([]store.TodoExport, 4)
	for i := range todos {
		todos[i] = store.TodoExport{
			LocalID:   int64(i + 1),
			Title:     fmt.Sprintf("t%d", i),
			Status:    store.DefaultColumnBacklog,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	payload := map[string]any{
		"importMode": "copy",
		"targetSlug": project.Slug,
		"data": store.ExportData{
			Version:    version.ExportFormatVersion,
			ExportedAt: now,
			Mode:       "anonymous",
			Scope:      "anonymous",
			Projects: []store.ProjectExport{{
				Name:      "Imported",
				Slug:      "imported-board",
				Todos:     todos,
				CreatedAt: now,
				UpdatedAt: now,
			}},
		},
	}
	client := ts.Client()
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("import status=%d body=%s", resp.StatusCode, body)
	}
	if apiErrorCode(t, body) != "VALIDATION_ERROR" {
		t.Fatalf("code=%q body=%s", apiErrorCode(t, body), body)
	}
}

func TestAnonymousBackupExportBudget(t *testing.T) {
	policy := tightAbusePolicy()
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	client := ts.Client()
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/backup/export", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Scrumboy", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("export %d status=%d", i, resp.StatusCode)
		}
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/backup/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scrumboy", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assertRateLimited(t, resp, body)
}

func TestMCPUnauthorizedDoesNotConsumeExpiringBudget(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 1, Window: 10 * time.Second}

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
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	defer sqlDB.Close()

	if _, err := st.BootstrapUser(context.Background(), "mcp-abuse@example.com", "password123", "Owner"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	owner, err := st.GetUserByEmail(context.Background(), "mcp-abuse@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), owner.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if _, err := st.CreateTodo(store.WithUserID(context.Background(), owner.ID), board.ID, store.CreateTodoInput{
		Title:     "keep",
		ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	client := ts.Client()
	payload, err := json.Marshal(map[string]any{
		"tool": "todos_delete",
		"input": map[string]any{
			"projectSlug": board.Slug,
			"localId":     1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scrumboy", "1")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("unauthorized MCP consumed budget: status=%d body=%s", resp.StatusCode, body)
		}
		if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
			t.Fatalf("unauthorized MCP status=%d body=%s", resp.StatusCode, body)
		}
	}
}

func TestRetryAfterSecondsCeiling(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want int
	}{
		{0, 1},
		{-time.Second, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Nanosecond, 2},
		{1500 * time.Millisecond, 2},
		{10 * time.Second, 10},
	}
	for _, tc := range cases {
		if got := retryAfterSeconds(tc.d); got != tc.want {
			t.Fatalf("retryAfterSeconds(%v)=%d want %d", tc.d, got, tc.want)
		}
	}
}

func anonymousImportPayload(now time.Time, todos int) store.ExportData {
	items := make([]store.TodoExport, todos)
	for i := range items {
		items[i] = store.TodoExport{
			LocalID:   int64(i + 1),
			Title:     fmt.Sprintf("t%d", i),
			Status:    store.DefaultColumnBacklog,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	return store.ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "anonymous",
		Scope:      "anonymous",
		Projects: []store.ProjectExport{{
			Name:      "Imported",
			Slug:      "imported-board",
			Todos:     items,
			CreatedAt: now,
			UpdatedAt: now,
		}},
	}
}

func doJSONForwarded(t *testing.T, client *http.Client, url, ip string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func TestDurableRestoreAboveAnonymousImportLimits(t *testing.T) {
	policy := abusepolicy.DisabledThrottling()
	policy.Safety.MaxProjectsPerImport = 2
	policy.Safety.MaxTodosPerImport = 3
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "durable-import@example.com", "password123")

	now := time.Now().UTC()
	projects := make([]store.ProjectExport, 3)
	for i := range projects {
		todos := make([]store.TodoExport, 4)
		for j := range todos {
			todos[j] = store.TodoExport{
				LocalID:   int64(j + 1),
				Title:     fmt.Sprintf("p%d-t%d", i, j),
				Status:    store.DefaultColumnBacklog,
				CreatedAt: now,
				UpdatedAt: now,
			}
		}
		projects[i] = store.ProjectExport{
			Name:      fmt.Sprintf("Lib %d", i),
			Slug:      fmt.Sprintf("lib-%d", i),
			Todos:     todos,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", map[string]any{
		"importMode": "copy",
		"data": store.ExportData{
			Version:    version.ExportFormatVersion,
			ExportedAt: now,
			Mode:       "full",
			Scope:      "full",
			Projects:   projects,
		},
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("durable restore status=%d body=%s", resp.StatusCode, body)
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE name LIKE 'Lib %'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("imported projects=%d want 3", n)
	}
}

func TestTargetedAnonymousImportAuthorizesBeforeAdmission(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.BulkImportPerIP = abusepolicy.WindowLimit{Limit: 1, Window: time.Hour}
	policy.RateLimits.MutationPerBoard = abusepolicy.WindowLimit{Limit: 50, Window: time.Minute}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	now := time.Now().UTC()
	payload := map[string]any{
		"importMode": "copy",
		"targetSlug": "missing-target-board",
		"data":       anonymousImportPayload(now, 1),
	}
	client := ts.Client()
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing target status=%d body=%s", resp.StatusCode, body)
	}
	var todos int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID).Scan(&todos); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if todos != 0 {
		t.Fatalf("rejected import wrote todos=%d", todos)
	}

	payload["targetSlug"] = project.Slug
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized targeted import status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTargetedAnonymousImportSharesPerBoardBudget(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.BulkImportPerIP = abusepolicy.WindowLimit{Limit: 10, Window: time.Hour}
	policy.RateLimits.MutationPerIP = abusepolicy.WindowLimit{Limit: 50, Window: time.Minute}
	policy.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 50, Window: 10 * time.Second}
	policy.RateLimits.MutationPerBoard = abusepolicy.WindowLimit{Limit: 1, Window: time.Minute}
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		TrustProxy:     true,
		AbusePolicy:    &policy,
	})
	defer cleanup()

	st := store.New(sqlDB, nil)
	project, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	now := time.Now().UTC()
	payload := map[string]any{
		"importMode": "copy",
		"targetSlug": project.Slug,
		"data":       anonymousImportPayload(now, 1),
	}
	client := ts.Client()
	resp, body := doJSONForwarded(t, client, ts.URL+"/api/backup/import", "203.0.113.10", payload)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first IP import status=%d body=%s", resp.StatusCode, body)
	}
	var todosBefore int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID).Scan(&todosBefore); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	var auditsBefore int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, project.ID).Scan(&auditsBefore); err != nil {
		t.Fatalf("count audit: %v", err)
	}

	resp, body = doJSONForwarded(t, client, ts.URL+"/api/backup/import", "203.0.113.11", payload)
	assertRateLimited(t, resp, body)

	var todosAfter int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, project.ID).Scan(&todosAfter); err != nil {
		t.Fatalf("count todos after: %v", err)
	}
	if todosAfter != todosBefore {
		t.Fatalf("rejected import wrote todos %d -> %d", todosBefore, todosAfter)
	}
	var auditsAfter int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, project.ID).Scan(&auditsAfter); err != nil {
		t.Fatalf("count audit after: %v", err)
	}
	if auditsAfter != auditsBefore {
		t.Fatalf("rejected import wrote audit %d -> %d", auditsBefore, auditsAfter)
	}
}

func TestMCPForbiddenCustomFieldDoesNotConsumeExpiringBudget(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 1, Window: 10 * time.Second}

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
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	defer sqlDB.Close()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "mcp-cf-owner@example.com", "password123")
	owner, err := st.GetUserByEmail(context.Background(), "mcp-cf-owner@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	board, err := st.CreateAnonymousBoard(store.WithUserID(context.Background(), owner.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	if _, err := st.CreateUser(context.Background(), "mcp-cf-other@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "mcp-cf-other@example.com", "password123")

	payload, err := json.Marshal(map[string]any{
		"tool": "customFields_create",
		"input": map[string]any{
			"projectSlug": board.Slug,
			"name":        "Ticket",
			"type":        "text",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scrumboy", "1")
		resp, err := otherClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("forbidden MCP consumed budget: status=%d body=%s", resp.StatusCode, body)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("forbidden MCP status=%d body=%s", resp.StatusCode, body)
		}
	}

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+board.Slug, map[string]any{
		"name": "Still Allowed",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner mutation after forbidden MCP status=%d body=%s", resp.StatusCode, body)
	}
}

func TestDeferExpiringMutationAdmissionRoutes(t *testing.T) {
	cases := []struct {
		method string
		rest   []string
		want   bool
	}{
		{http.MethodPatch, []string{"slug"}, true},
		{http.MethodPatch, []string{"slug", "settings"}, true},
		{http.MethodPost, []string{"slug", "claim"}, true},
		{http.MethodPost, []string{"slug", "workflow"}, true},
		{http.MethodPost, []string{"slug", "custom-fields"}, true},
		{http.MethodDelete, []string{"slug", "todos", "1"}, false},
		{http.MethodPatch, []string{"slug", "todos", "1"}, false},
		{http.MethodGet, []string{"slug", "settings"}, false},
	}
	for _, tc := range cases {
		if got := deferExpiringMutationAdmission(tc.method, tc.rest); got != tc.want {
			t.Fatalf("%s %v: got %v want %v", tc.method, tc.rest, got, tc.want)
		}
	}
}

func TestRESTForbiddenImageDoesNotConsumeExpiringBudget(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.MutationPerIPBurst = abusepolicy.WindowLimit{Limit: 1, Window: 10 * time.Second}
	policy.RateLimits.MutationPerBoard = abusepolicy.WindowLimit{Limit: 1, Window: time.Minute}

	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-image-budget@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-image-budget@example.com", "password123")

	var nameBefore string
	var auditsBefore int
	if err := sqlDB.QueryRow(`SELECT name FROM projects WHERE id = ?`, tempBoard.ID).Scan(&nameBefore); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, tempBoard.ID).Scan(&auditsBefore); err != nil {
		t.Fatalf("count audit: %v", err)
	}

	for i := 0; i < 5; i++ {
		resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug, map[string]any{
			"image": "data:image/png;base64,aaaa",
		}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("forbidden image consumed budget: status=%d body=%s", resp.StatusCode, body)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("forbidden image status=%d body=%s", resp.StatusCode, body)
		}
		if apiErrorCode(t, body) != "FORBIDDEN" {
			t.Fatalf("code=%q body=%s", apiErrorCode(t, body), body)
		}
	}

	resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug+"/settings", map[string]any{
		"sprintsEnabled": false,
	}, nil)
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("forbidden settings consumed budget: status=%d body=%s", resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("forbidden settings status=%d body=%s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, otherClient, http.MethodPost, ts.URL+"/api/board/"+tempBoard.Slug+"/claim", map[string]any{}, nil)
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("forbidden claim consumed budget: status=%d body=%s", resp.StatusCode, body)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-creator claim status=%d body=%s", resp.StatusCode, body)
	}

	var nameMid, stored string
	var auditsMid, imageAudits int
	if err := sqlDB.QueryRow(`SELECT name, COALESCE(image, '') FROM projects WHERE id = ?`, tempBoard.ID).Scan(&nameMid, &stored); err != nil {
		t.Fatalf("read project after forbidden: %v", err)
	}
	if nameMid != nameBefore {
		t.Fatalf("forbidden requests changed name %q -> %q", nameBefore, nameMid)
	}
	if stored == "data:image/png;base64,aaaa" {
		t.Fatalf("unauthorized image write persisted")
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, tempBoard.ID).Scan(&auditsMid); err != nil {
		t.Fatalf("count audit after forbidden: %v", err)
	}
	if auditsMid != auditsBefore {
		t.Fatalf("forbidden requests wrote audit %d -> %d", auditsBefore, auditsMid)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action = 'project_image_updated'`, tempBoard.ID).Scan(&imageAudits); err != nil {
		t.Fatalf("count image audit: %v", err)
	}
	if imageAudits != 0 {
		t.Fatalf("unauthorized image audit rows=%d", imageAudits)
	}

	resp, body = doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug, map[string]any{
		"name": "Owner Still Allowed",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner mutation after forbidden REST status=%d body=%s", resp.StatusCode, body)
	}
}

func TestRESTForbiddenNameDoesNotConsumeExpiringBudget(t *testing.T) {
	policy := tightAbusePolicy()
	policy.RateLimits.MutationPerBoard = abusepolicy.WindowLimit{Limit: 1, Window: time.Minute}

	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		AbusePolicy:    &policy,
	})
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-rename-budget@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-rename-budget@example.com", "password123")

	var nameBefore string
	var auditsBefore int
	if err := sqlDB.QueryRow(`SELECT name FROM projects WHERE id = ?`, tempBoard.ID).Scan(&nameBefore); err != nil {
		t.Fatalf("read name: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, tempBoard.ID).Scan(&auditsBefore); err != nil {
		t.Fatalf("count audit: %v", err)
	}

	for i := 0; i < 5; i++ {
		resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug, map[string]any{
			"name": "Should Not Stick",
		}, nil)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("forbidden name consumed budget: status=%d body=%s", resp.StatusCode, body)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("forbidden name status=%d body=%s", resp.StatusCode, body)
		}
		if apiErrorCode(t, body) != "FORBIDDEN" {
			t.Fatalf("code=%q body=%s", apiErrorCode(t, body), body)
		}
	}

	var nameMid string
	var auditsMid int
	if err := sqlDB.QueryRow(`SELECT name FROM projects WHERE id = ?`, tempBoard.ID).Scan(&nameMid); err != nil {
		t.Fatalf("read name after forbidden: %v", err)
	}
	if nameMid != nameBefore {
		t.Fatalf("forbidden requests changed name %q -> %q", nameBefore, nameMid)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ?`, tempBoard.ID).Scan(&auditsMid); err != nil {
		t.Fatalf("count audit after forbidden: %v", err)
	}
	if auditsMid != auditsBefore {
		t.Fatalf("forbidden requests wrote audit %d -> %d", auditsBefore, auditsMid)
	}

	image := "data:image/png;base64,aaaa"
	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug, map[string]any{
		"name":  "Owner Renamed",
		"image": image,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner name+image after forbidden rename status=%d body=%s", resp.StatusCode, body)
	}
	var storedName, storedImage string
	if err := sqlDB.QueryRow(`SELECT name, COALESCE(image, '') FROM projects WHERE id = ?`, tempBoard.ID).Scan(&storedName, &storedImage); err != nil {
		t.Fatalf("read owner update: %v", err)
	}
	if storedName != "Owner Renamed" || storedImage != image {
		t.Fatalf("owner update name=%q image=%q", storedName, storedImage)
	}

	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	anonClient := ts.Client()
	resp, body = doJSON(t, anonClient, http.MethodPatch, ts.URL+"/api/board/"+anonBoard.Slug, map[string]any{
		"name": "Anonymous Renamed",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous slug rename status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, anonClient, http.MethodPatch, ts.URL+"/api/board/"+anonBoard.Slug, map[string]any{
		"name": "Anonymous Renamed Again",
	}, nil)
	assertRateLimited(t, resp, body)
	var anonName string
	if err := sqlDB.QueryRow(`SELECT name FROM projects WHERE id = ?`, anonBoard.ID).Scan(&anonName); err != nil {
		t.Fatalf("read anonymous name: %v", err)
	}
	if anonName != "Anonymous Renamed" {
		t.Fatalf("rejected anonymous rename persisted %q", anonName)
	}
}
