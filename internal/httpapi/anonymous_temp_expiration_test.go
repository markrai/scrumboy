package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

func TestAnonymousMode_DeleteProjectRoute_Returns404(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()
	resp, _ := doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/1", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for DELETE /api/projects/{id} in anonymous mode, got %d", resp.StatusCode)
	}
}

func TestExpiredAnonymousTempBoard_BoardGET_Returns404(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	pastMs := time.Now().UTC().Add(-24 * time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, pastMs, p.ID); err != nil {
		t.Fatalf("expire project: %v", err)
	}

	client := ts.Client()
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+p.Slug, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for expired board GET, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestExpiredAnonymousTempBoard_TodoCreate_Returns404(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	pastMs := time.Now().UTC().Add(-24 * time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, pastMs, p.ID); err != nil {
		t.Fatalf("expire project: %v", err)
	}

	client := ts.Client()
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+p.Slug+"/todos", map[string]any{
		"title":     "late",
		"columnKey": "backlog",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for todo create on expired board, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestAnonymousMode_ImportReplace_Forbidden(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	client := ts.Client()
	data := store.ExportData{
		Version: version.ExportFormatVersion,
		Scope:   "single",
		Projects: []store.ProjectExport{
			{Slug: "imp", Name: "Import"},
		},
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", map[string]any{
		"data":         data,
		"importMode":   "replace",
		"confirmation": "REPLACE",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for replace import in anonymous mode, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestAnonymousMode_PatchRename_ExpiredBoard_Returns404(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	pastMs := time.Now().UTC().Add(-24 * time.Hour).UnixMilli()
	if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, pastMs, p.ID); err != nil {
		t.Fatalf("expire project: %v", err)
	}

	client := ts.Client()
	resp, _ := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(p.ID, 10), map[string]any{
		"name": "Too Late",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for PATCH rename on expired board, got %d", resp.StatusCode)
	}
}

func TestAnonymousMode_PatchRename_DurableProject_Returns404(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	st := store.New(sqlDB, nil)
	p, err := st.CreateProject(context.Background(), "Durable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	client := ts.Client()
	resp, _ := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(p.ID, 10), map[string]any{
		"name": "Nope",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for PATCH on durable project in anonymous mode, got %d", resp.StatusCode)
	}
}

func TestAnonymousMode_PatchRename_ImageRejected(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "anonymous")
	defer cleanup()

	st := store.New(sqlDB, nil)
	p, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	client := ts.Client()
	resp, _ := doJSON(t, client, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(p.ID, 10), map[string]any{
		"image": "data:image/png;base64,aaaa",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for numeric image PATCH in anonymous mode, got %d", resp.StatusCode)
	}
}
