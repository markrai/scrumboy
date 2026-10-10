package mcp_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newPhase6MCPFixture(t *testing.T) (*sql.DB, *store.Store, *mcp.Adapter) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "phase6.db"), db.Options{
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
	return sqlDB, st, mcp.New(st, mcp.Options{Mode: "full"})
}

func TestPhase6LegacyMCPCookieMutationsRequireDeliberateRequestHeader(t *testing.T) {
	sqlDB, st, handler := newPhase6MCPFixture(t)
	user, err := st.CreateUser(context.Background(), "phase6@example.com", "password123", "Phase Six")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessionToken, _, err := st.CreateSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, apiToken, _, err := st.CreateUserAPIToken(context.Background(), user.ID, nil, false)
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}

	body := map[string]any{
		"tool":  "projects_create",
		"input": map[string]any{"name": "Phase 6 CSRF Probe"},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	request := func(t *testing.T, cookie, bearer string, addHeader bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: cookie})
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if addHeader {
			req.Header.Set("X-Scrumboy", "1")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	blocked := request(t, sessionToken, "", false)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("expected cookie-authenticated POST without header to return 403, got %d: %s", blocked.Code, blocked.Body.String())
	}
	var count int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&count); err != nil {
		t.Fatalf("count projects after blocked request: %v", err)
	}
	if count != 0 {
		t.Fatalf("blocked request mutated projects: count=%d", count)
	}

	allowed := request(t, sessionToken, "", true)
	if allowed.Code != http.StatusOK {
		t.Fatalf("expected cookie-authenticated POST with header to succeed, got %d: %s", allowed.Code, allowed.Body.String())
	}

	bearerAllowed := request(t, "", apiToken, false)
	if bearerAllowed.Code != http.StatusOK {
		t.Fatalf("expected bearer integration to remain header-exempt, got %d: %s", bearerAllowed.Code, bearerAllowed.Body.String())
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&count); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected exactly two authorized mutations, got project count=%d", count)
	}
}

func TestPhase6PublishedBoardDoesNotAuthorizePrivateMCPReads(t *testing.T) {
	_, st, handler := newPhase6MCPFixture(t)
	ctx := context.Background()
	owner, err := st.CreateUser(ctx, "phase6-owner@example.com", "password123", "Phase Six Owner")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	outsider, err := st.CreateUser(ctx, "phase6-outsider@example.com", "password123", "Phase Six Outsider")
	if err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	project, err := st.CreateProject(store.WithUserID(ctx, owner.ID), "Published But Private MCP")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := st.UpdateProjectPublicViewing(ctx, project.ID, owner.ID, true); err != nil {
		t.Fatalf("publish project: %v", err)
	}
	ownerSession, _, err := st.CreateSession(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatalf("create owner session: %v", err)
	}
	outsiderSession, _, err := st.CreateSession(ctx, outsider.ID, time.Hour)
	if err != nil {
		t.Fatalf("create outsider session: %v", err)
	}
	_, outsiderAPIToken, _, err := st.CreateUserAPIToken(ctx, outsider.ID, nil, false)
	if err != nil {
		t.Fatalf("create outsider API token: %v", err)
	}

	encoded, err := json.Marshal(map[string]any{
		"tool":  "board_get",
		"input": map[string]any{"projectSlug": project.Slug},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	call := func(t *testing.T, cookie, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: cookie})
			req.Header.Set("X-Scrumboy", "1")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	ownerResponse := call(t, ownerSession, "")
	if ownerResponse.Code != http.StatusOK {
		t.Fatalf("expected owner MCP board read to succeed, got %d: %s", ownerResponse.Code, ownerResponse.Body.String())
	}
	for name, response := range map[string]*httptest.ResponseRecorder{
		"session cookie": call(t, outsiderSession, ""),
		"API token":      call(t, "", outsiderAPIToken),
	} {
		if response.Code == http.StatusOK {
			t.Fatalf("published state authorized outsider private MCP read via %s: %s", name, response.Body.String())
		}
	}
}
