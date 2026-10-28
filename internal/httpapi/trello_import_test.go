package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
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
		"cards":[
			{"id":"card-open","name":"Open card","idList":"list-doing","pos":10,"closed":false,"idLabels":[],"idMembers":[]},
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
