package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/abusepolicy"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newTestHTTPServerWithLimits(t *testing.T, mode string, maxBody, maxTrelloBody int64) (*httptest.Server, *sql.DB, func()) {
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
	disabled := abusepolicy.DisabledThrottling()
	srv := NewServer(st, Options{
		MaxRequestBody:      maxBody,
		MaxTrelloImportBody: maxTrelloBody,
		ScrumboyMode:        mode,
		AbusePolicy:         &disabled,
	})
	ts := httptest.NewServer(srv)
	return ts, sqlDB, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func doRawJSON(t *testing.T, client *http.Client, method, url, body string, out any) (*http.Response, []byte) {
	t.Helper()

	req, err := http.NewRequest(method, url, strings.NewReader(body))
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
		t.Fatalf("read body: %v", err)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			t.Fatalf("unmarshal: %v body=%s", err, string(payload))
		}
	}
	return resp, payload
}

func trelloBoardRawJSON() string {
	return `{
		"id":"http-board-1",
		"name":"HTTP Trello Import",
		"lists":[
			{"id":"list-doing","name":"Doing","pos":10,"closed":false},
			{"id":"list-done","name":"Done","pos":20,"closed":false},
			{"id":"list-closed","name":"Closed Lane","pos":30,"closed":true}
		],
		"labels":[{"id":"label-valid","name":"Café","color":"blue"}],
		"cards":[
			{"id":"card-open","name":"Open card","idList":"list-doing","pos":10,"closed":false,"idLabels":["label-valid"],"idMembers":[]},
			{"id":"card-closed","name":"Closed list card","idList":"list-closed","pos":20,"closed":true,"idLabels":[],"idMembers":[]}
		]
	}`
}

func TestAPI_TrelloPreviewAndImport(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithLimits(t, "full", 1<<20, 4<<20)
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", resp.StatusCode, string(body))
	}

	var preview struct {
		BoardName          string   `json:"boardName"`
		OpenLists          int      `json:"openLists"`
		ClosedLists        int      `json:"closedLists"`
		HardErrors         []string `json:"hardErrors"`
		Warnings           []string `json:"warnings"`
		DetectedDoneColumn string   `json:"detectedDoneColumn"`
	}
	resp, body = doRawJSON(t, client, http.MethodPost, ts.URL+"/api/import/trello/preview", trelloBoardRawJSON(), &preview)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", resp.StatusCode, string(body))
	}
	if preview.BoardName != "HTTP Trello Import" {
		t.Fatalf("boardName = %q", preview.BoardName)
	}
	if preview.OpenLists != 2 || preview.ClosedLists != 1 {
		t.Fatalf("unexpected list counts: %+v", preview)
	}
	if len(preview.HardErrors) != 0 {
		t.Fatalf("unexpected hard errors: %v", preview.HardErrors)
	}
	if preview.DetectedDoneColumn != "Done" {
		t.Fatalf("detectedDoneColumn = %q", preview.DetectedDoneColumn)
	}
	for _, warning := range preview.Warnings {
		if strings.Contains(warning, "label names will be adjusted") {
			t.Fatalf("valid label produced adjustment warning: %q", warning)
		}
	}

	var result struct {
		Project struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"project"`
	}
	resp, body = doRawJSON(t, client, http.MethodPost, ts.URL+"/api/import/trello", trelloBoardRawJSON(), &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status=%d body=%s", resp.StatusCode, string(body))
	}
	if result.Project.Name != "HTTP Trello Import" || result.Project.Slug == "" {
		t.Fatalf("unexpected import result: %+v", result)
	}

	var projectMetadata string
	if err := sqlDB.QueryRow(`SELECT import_metadata FROM projects WHERE id = ?`, result.Project.ID).Scan(&projectMetadata); err != nil {
		t.Fatalf("project import_metadata: %v", err)
	}
	if !strings.Contains(projectMetadata, `"source":"trello"`) {
		t.Fatalf("unexpected project metadata: %s", projectMetadata)
	}

	var todoCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM todos WHERE project_id = ?`, result.Project.ID).Scan(&todoCount); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if todoCount != 2 {
		t.Fatalf("expected 2 imported todos, got %d", todoCount)
	}
}

func TestAPI_TrelloProblematicLabelPreviewAndImport(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithLimits(t, "full", 1<<20, 4<<20)
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Label Importer",
		"email":    "labels@example.com",
		"password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", resp.StatusCode, string(body))
	}

	raw := `{
		"id":"http-problem-labels",
		"name":"HTTP Problem Labels",
		"lists":[
			{"id":"doing","name":"Doing","pos":10,"closed":false},
			{"id":"done","name":"Done","pos":20,"closed":false}
		],
		"labels":[
			{"id":"punctuation","name":"Bug/Fix","color":"blue"},
			{"id":"bug-one","name":"Bug","color":"red"},
			{"id":"bug-two","name":"Bug","color":"yellow"},
			{"id":"genuine-suffix","name":"Bug 2","color":"green"},
			{"id":"modern-fallback","name":"","color":"green_light"}
		],
		"cards":[
			{"id":"one","name":"Card One","idList":"doing","pos":10,"closed":false,"idLabels":["punctuation","bug-one","modern-fallback"],"idMembers":[]},
			{"id":"two","name":"Card Two","idList":"doing","pos":20,"closed":false,"idLabels":["bug-two","genuine-suffix","punctuation"],"idMembers":[]}
		]
	}`
	var preview struct {
		HardErrors []string `json:"hardErrors"`
		Warnings   []string `json:"warnings"`
	}
	resp, body = doRawJSON(t, client, http.MethodPost, ts.URL+"/api/import/trello/preview", raw, &preview)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(preview.HardErrors) != 0 {
		t.Fatalf("preview hard errors: %v", preview.HardErrors)
	}
	var adjustmentWarning string
	for _, warning := range preview.Warnings {
		if strings.Contains(warning, "label names will be adjusted") {
			adjustmentWarning = warning
		}
	}
	if !strings.Contains(adjustmentWarning, "(3 adjusted)") {
		t.Fatalf("adjustment warning = %q", adjustmentWarning)
	}

	var result struct {
		Project struct {
			ID int64 `json:"id"`
		} `json:"project"`
	}
	resp, body = doRawJSON(t, client, http.MethodPost, ts.URL+"/api/import/trello", raw, &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status=%d body=%s", resp.StatusCode, string(body))
	}
	if result.Project.ID == 0 {
		t.Fatalf("missing imported project: %+v", result)
	}

	rows, err := sqlDB.Query(`
SELECT td.title, tg.name
FROM todos td
JOIN todo_tags tt ON tt.todo_id = td.id
JOIN tags tg ON tg.id = tt.tag_id
WHERE td.project_id = ?
ORDER BY td.title, tg.name`, result.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string][]string)
	for rows.Next() {
		var title, name string
		if err := rows.Scan(&title, &name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		got[title] = append(got[title], name)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"Card One": {"bug", "bug-fix", "trello-green-light-label"},
		"Card Two": {"bug-2", "bug-3", "bug-fix"},
	}
	if len(got) != len(want) {
		t.Fatalf("stored card tags = %#v", got)
	}
	for title, wantTags := range want {
		gotTags := got[title]
		if strings.Join(gotTags, "|") != strings.Join(wantTags, "|") {
			t.Fatalf("%s tags = %#v, want %#v", title, gotTags, wantTags)
		}
	}

	var tagCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM project_tags WHERE project_id = ?`, result.Project.ID).Scan(&tagCount); err != nil {
		t.Fatal(err)
	}
	if tagCount != 5 {
		t.Fatalf("stored %d project tags, want 5 distinct source labels", tagCount)
	}
}

func TestAPI_TrelloPreviewRejectsOversizedUpload(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithLimits(t, "full", 16, 64)
	defer cleanup()

	client := ts.Client()
	oversized := `{"data":"` + strings.Repeat("x", 128) + `"}`

	resp, body := doRawJSON(t, client, http.MethodPost, ts.URL+"/api/import/trello/preview", oversized, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", resp.StatusCode, string(body))
	}
}

// TestAPI_TrelloInvalidJSONNoRawParserDump asserts that invalid Trello JSON produces
// a validation error with no raw parser output in details (finding 3 fix: c).
func TestAPI_TrelloInvalidJSONNoRawParserDump(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithLimits(t, "full", 1<<20, 4<<20)
	defer cleanup()

	client := ts.Client()

	for _, endpoint := range []string{"/api/import/trello/preview", "/api/import/trello"} {
		t.Run(endpoint, func(t *testing.T) {
			resp, body := doRawJSON(t, client, http.MethodPost, ts.URL+endpoint, `{not valid json`, nil)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
			}
			var out map[string]any
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatalf("unmarshal: %v body=%s", err, string(body))
			}
			errObj, _ := out["error"].(map[string]any)
			if errObj == nil {
				t.Fatalf("missing error object: %s", string(body))
			}
			if errObj["code"] != "VALIDATION_ERROR" {
				t.Fatalf("code = %v, want VALIDATION_ERROR", errObj["code"])
			}
			details, _ := errObj["details"].(map[string]any)
			// No raw parser dump: details must not contain "detail" with parser text
			if detail, ok := details["detail"]; ok && detail != nil {
				t.Fatalf("response leaks raw parser output in details.detail: %v", detail)
			}
		})
	}
}

// TestAPI_BackupImportValidationErrorHasRuleAndProject asserts that a store-level
// validation error (project-scoped) includes rule and project in details, with no SQL (finding 3 fix: a).
func TestAPI_BackupImportValidationErrorHasRuleAndProject(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name": "Alice", "email": "alice-valid@example.com", "password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", resp.StatusCode, string(body))
	}

	// A backup with an empty workflow column key triggers import_record_invalid
	// with details{project: ..., rule: "workflow_column_empty_key"}.
	payload := map[string]any{
		"importMode": "copy",
		"data": map[string]any{
			"version":    "1.2",
			"exportedAt": "2026-01-01T00:00:00Z",
			"mode":       "full",
			"scope":      "full",
			"projects": []any{map[string]any{
				"slug":      "test-wf",
				"name":      "WF Test",
				"createdAt": "2026-01-01T00:00:00Z",
				"updatedAt": "2026-01-01T00:00:00Z",
				"workflowColumns": []any{
					map[string]any{"key": "", "name": "To Do", "color": "#94a3b8", "position": 0, "isDone": false},
					map[string]any{"key": "done", "name": "Done", "color": "#ef4444", "position": 1, "isDone": true},
				},
				"todos": []any{},
				"tags":  []any{},
			}},
		},
	}
	var out map[string]any
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, &out)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("missing error object: %s", string(body))
	}
	details, _ := errObj["details"].(map[string]any)
	if details["rule"] != "workflow_column_empty_key" {
		t.Fatalf("details.rule = %v, want workflow_column_empty_key (body=%s)", details["rule"], string(body))
	}
	if details["project"] != "WF Test" {
		t.Fatalf("details.project = %v, want WF Test", details["project"])
	}
	// Must not expose SQL text
	raw := string(body)
	for _, sqlLeak := range []string{"sqlite", "SELECT", "INSERT", "FROM projects", "syntax error"} {
		if strings.Contains(raw, sqlLeak) {
			t.Fatalf("response leaks SQL %q: %s", sqlLeak, raw)
		}
	}
}

// TestAPI_BackupImportTagPathRedactedDoesNotLeak asserts transport-boundary
// redaction for an unexpected failure outside the custom-field import path
// (tags INSERT), including that no project is left partially imported.
// The injected abort text deliberately contains "too large" so a string-matching
// payload exception cannot mis-classify the failure as PAYLOAD_TOO_LARGE.
func TestAPI_BackupImportTagPathRedactedDoesNotLeak(t *testing.T) {
	var logs strings.Builder
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		Logger:         log.New(&logs, "", 0),
	})
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name": "Tag", "email": "tag-internal@example.com", "password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", resp.StatusCode, string(body))
	}

	_, err := sqlDB.Exec(`CREATE TRIGGER trg_import_tag_fail BEFORE INSERT ON tags
		BEGIN SELECT RAISE(ABORT, 'injected: value too large SELECT * FROM secret_credentials'); END`)
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	payload := map[string]any{
		"importMode": "copy",
		"data": map[string]any{
			"version":    "1.2",
			"exportedAt": "2026-01-01T00:00:00Z",
			"mode":       "full",
			"scope":      "full",
			"projects": []any{map[string]any{
				"slug":      "tag-internal-test",
				"name":      "Tag Internal Test",
				"createdAt": "2026-01-01T00:00:00Z",
				"updatedAt": "2026-01-01T00:00:00Z",
				"todos":     []any{},
				"tags":      []any{map[string]any{"name": "alpha", "color": "#112233"}},
			}},
		},
	}
	var out map[string]any
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, &out)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", resp.StatusCode, string(body))
	}
	raw := string(body)
	for _, leak := range []string{"secret_credentials", "injected:", "SELECT *", "too large"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("response leaks internal detail %q: %s", leak, raw)
		}
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "INTERNAL" {
		t.Fatalf("code = %v, want INTERNAL (not PAYLOAD_TOO_LARGE); body=%s", errObj["code"], raw)
	}
	details, _ := errObj["details"].(map[string]any)
	if details != nil {
		if detail, ok := details["detail"]; ok {
			t.Fatalf("leaked details.detail=%v", detail)
		}
	}
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE slug = ?`, "tag-internal-test").Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if n != 0 {
		t.Fatalf("import rolled back incompletely: projects=%d", n)
	}
	logText := logs.String()
	if !strings.Contains(logText, "secret_credentials") && !strings.Contains(logText, "too large") {
		t.Fatalf("server log missing underlying diagnostic cause; logs=%q", logText)
	}
	if !strings.Contains(logText, "BACKUP IMPORT: ERROR from ImportProjects:") {
		t.Fatalf("server log missing import error line; logs=%q", logText)
	}
}

// TestAPI_BackupImportInternalRedactedDoesNotLeak asserts that a forced internal
// failure through the custom-field path (which wraps with redactImportInternal)
// returns INTERNAL without leaking SQL text (finding 3 fix: b).
func TestAPI_BackupImportInternalRedactedDoesNotLeak(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name": "Bob", "email": "bob-internal@example.com", "password": "password123",
	}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", resp.StatusCode, string(body))
	}

	// Inject a SQL trigger that fires when inserting a project custom field,
	// which is in the importCustomFieldsTx path wrapped with redactImportInternal.
	_, err := sqlDB.Exec(`CREATE TRIGGER trg_import_cf_fail BEFORE INSERT ON project_custom_fields
		BEGIN SELECT RAISE(ABORT, 'injected: SELECT * FROM secret_credentials'); END`)
	if err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	// Backup with a custom field definition — forces importCustomFieldsTx to INSERT.
	payload := map[string]any{
		"importMode": "copy",
		"data": map[string]any{
			"version":    "1.2",
			"exportedAt": "2026-01-01T00:00:00Z",
			"mode":       "full",
			"scope":      "full",
			"projects": []any{map[string]any{
				"slug":         "cf-internal-test",
				"name":         "CF Internal Test",
				"createdAt":    "2026-01-01T00:00:00Z",
				"updatedAt":    "2026-01-01T00:00:00Z",
				"customFields": []any{map[string]any{"key": "pts", "name": "Points", "type": "number", "position": 0}},
				"todos":        []any{},
				"tags":         []any{},
			}},
		},
	}
	var out map[string]any
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", payload, &out)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", resp.StatusCode, string(body))
	}
	raw := string(body)
	for _, leak := range []string{"secret_credentials", "injected:"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("response leaks internal detail %q: %s", leak, raw)
		}
	}
	// The error code must be INTERNAL
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "INTERNAL" {
		t.Fatalf("code = %v, want INTERNAL", errObj["code"])
	}
}
