package agora_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"scrumboy/internal/agora"
	"scrumboy/internal/db"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func TestPhase6AgoraRejectsHostileBrowserOriginBeforeToolExecution(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "phase6-agora.db"), db.Options{
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
	user, err := st.BootstrapUser(context.Background(), "phase6-agora@example.com", "Password123!", "Phase Six Agora")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	_, apiToken, _, err := st.CreateUserAPIToken(context.Background(), user.ID, nil, false)
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	handler := agora.New(mcp.New(st, mcp.Options{Mode: "full"}), agora.Options{MaxRequestBytes: 1 << 20})

	projectCount := func() int {
		t.Helper()
		var count int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&count); err != nil {
			t.Fatalf("count projects: %v", err)
		}
		return count
	}
	invoke := func(name string, origin *string) (int, string) {
		t.Helper()
		body := []byte(`{"tool":"projects_create","arguments":{"name":"` + name + `"}}`)
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/agora/v1/invoke", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiToken)
		if origin != nil {
			req.Header.Set("Origin", *origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	hostileOrigin := "https://attacker.example"
	status, body := invoke("must not run", &hostileOrigin)
	if status != http.StatusForbidden {
		t.Fatalf("expected hostile Origin to be rejected with 403, got %d: %s", status, body)
	}
	if got := projectCount(); got != 0 {
		t.Fatalf("hostile Origin reached the MCP mutation path: project count=%d", got)
	}

	sameOrigin := "http://127.0.0.1"
	status, body = invoke("same origin works", &sameOrigin)
	if status != http.StatusOK {
		t.Fatalf("same-origin Agora request failed with %d: %s", status, body)
	}
	if got := projectCount(); got != 1 {
		t.Fatalf("same-origin request did not execute exactly once: project count=%d", got)
	}

	status, body = invoke("non-browser works", nil)
	if status != http.StatusOK {
		t.Fatalf("Origin-less Agora request failed with %d: %s", status, body)
	}
	if got := projectCount(); got != 2 {
		t.Fatalf("Origin-less request did not execute exactly once: project count=%d", got)
	}
}
