package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

// newWallTestServer spins up an httptest server with the wall feature flag on.
// Returns the test server, a cookie-jarred client, the underlying store, and
// a slug for a durable project the bootstrapped user owns.
func newWallTestServer(t *testing.T, wallEnabled bool) (*httptest.Server, *http.Client, *store.Store, string) {
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
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	srv := NewServer(st, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full", WallEnabled: wallEnabled})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newCookieClient(t)
	var u struct {
		ID int64 `json:"id"`
	}
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "A",
		"email":    "a@b.com",
		"password": "password123",
	}, &u)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d", resp.StatusCode)
	}

	var p struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "proj"}, &p)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, string(body))
	}

	return ts, client, st, p.Slug
}

func TestWallGetReturnsEmptyWithoutCreatingRow(t *testing.T) {
	ts, client, _, slug := newWallTestServer(t, true)

	var wall struct {
		Notes   []map[string]any `json:"notes"`
		Version int64            `json:"version"`
	}
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+slug+"/wall", nil, &wall)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET wall status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(wall.Notes) != 0 {
		t.Fatalf("expected empty notes, got %v", wall.Notes)
	}
	if wall.Version != 0 {
		t.Fatalf("expected synthetic version 0, got %d", wall.Version)
	}
}

func TestWallNoteScopedLifecycle(t *testing.T) {
	ts, client, _, slug := newWallTestServer(t, true)

	var created struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/wall/notes", map[string]any{
		"x":      10.0,
		"y":      20.0,
		"width":  180,
		"height": 140,
		"color":  "#ffd966",
		"text":   "hi",
	}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST note status=%d body=%s", resp.StatusCode, string(body))
	}
	if created.ID == "" || created.Version != 1 {
		t.Fatalf("unexpected created note: %+v", created)
	}

	// PATCH with correct version.
	var patched struct {
		Version int64 `json:"version"`
	}
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+slug+"/wall/notes/"+created.ID, map[string]any{
		"ifVersion": created.Version,
		"text":      "updated",
	}, &patched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH note status=%d body=%s", resp.StatusCode, string(body))
	}
	if patched.Version != 2 {
		t.Fatalf("expected version 2, got %d", patched.Version)
	}

	// Stale PATCH should conflict.
	resp, _ = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+slug+"/wall/notes/"+created.ID, map[string]any{
		"ifVersion": created.Version,
		"text":      "stale",
	}, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on stale PATCH, got %d", resp.StatusCode)
	}

	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+slug+"/wall/notes/"+created.ID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 on DELETE, got %d", resp.StatusCode)
	}
}

func TestWallEdgeRoutes(t *testing.T) {
	ts, client, _, slug := newWallTestServer(t, true)

	// Two notes to connect.
	var a, b struct {
		ID string `json:"id"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/wall/notes", map[string]any{
		"x": 0, "y": 0, "width": 180, "height": 140, "color": "#ffd966", "text": "a",
	}, &a)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create note A: %d %s", resp.StatusCode, string(body))
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/wall/notes", map[string]any{
		"x": 200, "y": 0, "width": 180, "height": 140, "color": "#ffd966", "text": "b",
	}, &b)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create note B: %d %s", resp.StatusCode, string(body))
	}

	// Self-loop must be rejected at the route layer.
	resp, _ = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/wall/edges", map[string]any{
		"from": a.ID, "to": a.ID,
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for self-loop, got %d", resp.StatusCode)
	}

	// Create succeeds.
	var edge struct {
		ID   string `json:"id"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+slug+"/wall/edges", map[string]any{
		"from": a.ID, "to": b.ID,
	}, &edge)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create edge: %d %s", resp.StatusCode, string(body))
	}
	if edge.ID == "" || edge.From != a.ID || edge.To != b.ID {
		t.Fatalf("unexpected edge payload: %+v", edge)
	}

	// GET wall must include the edge alongside notes.
	var wall struct {
		Notes []map[string]any `json:"notes"`
		Edges []map[string]any `json:"edges"`
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+slug+"/wall", nil, &wall)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET wall: %d %s", resp.StatusCode, string(body))
	}
	if len(wall.Edges) != 1 || wall.Edges[0]["id"] != edge.ID {
		t.Fatalf("expected 1 edge in wall doc, got %v", wall.Edges)
	}

	// DELETE the edge.
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+slug+"/wall/edges/"+edge.ID, nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 on edge delete, got %d", resp.StatusCode)
	}
	resp, _ = doJSON(t, client, http.MethodDelete, ts.URL+"/api/board/"+slug+"/wall/edges/"+edge.ID, nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on second delete, got %d", resp.StatusCode)
	}
}

func TestWallRejectsAnonymousTempBoard(t *testing.T) {
	ts, client, st, _ := newWallTestServer(t, true)

	// Anonymous/temp boards (expires_at != NULL) are out of Scrumbaby scope.
	tmp, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+tmp.Slug+"/wall", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on anonymous temp wall, got %d", resp.StatusCode)
	}
}

func TestWallReturns404WhenFlagOff(t *testing.T) {
	ts, client, _, slug := newWallTestServer(t, false)

	resp, _ := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+slug+"/wall", nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when flag off, got %d", resp.StatusCode)
	}

	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("auth/status status=%d", resp.StatusCode)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := m["wallEnabled"].(bool); !ok || v {
		t.Fatalf("expected wallEnabled=false in auth/status, got %v", m["wallEnabled"])
	}
}
