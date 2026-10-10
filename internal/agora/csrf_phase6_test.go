package agora_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"scrumboy/internal/agora"
	"scrumboy/internal/mcp"
)

func TestPhase6AgoraRejectsHostileBrowserOriginBeforeToolExecution(t *testing.T) {
	mcpHandler := mcp.New(nil, mcp.Options{Mode: "full"})
	handler := agora.New(mcpHandler, agora.Options{MaxRequestBytes: 1 << 20})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/agora/v1/invoke", strings.NewReader(`{"tool":"projects_create","arguments":{"name":"must not run"}}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected hostile Origin to be rejected with 403, got %d: %s", rec.Code, rec.Body.String())
	}
}
