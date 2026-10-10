package mcp_test

import (
	"net/http"
	"testing"
)

func TestMCPMeGet_BearerReturnsTokenOwner(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t, ts)
	bootstrapUser(t, cookieClient, ts.URL)

	var created map[string]any
	resp := doJSON(t, cookieClient, http.MethodPost, ts.URL+"/api/me/tokens", map[string]any{"name": "me_get"}, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/me/tokens: status=%d", resp.StatusCode)
	}
	token := created["token"].(string)

	resp, out := postMCPWithBearer(t, newStatelessClient(ts), ts.URL, token, map[string]any{"tool": "me_get", "input": map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me_get status=%d %#v", resp.StatusCode, out)
	}
	data, _ := out["data"].(map[string]any)
	if data["email"] != "owner@example.com" || data["name"] != "Owner" {
		t.Fatalf("me_get data = %#v, want owner@example.com / Owner", data)
	}
	if id, _ := data["userId"].(float64); id <= 0 {
		t.Fatalf("me_get userId = %#v, want > 0", data["userId"])
	}
}

func TestMCPMeGet_RequiresSignIn(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	bootstrapUser(t, newCookieClient(t, ts), ts.URL)

	resp, out := doMCP(t, newStatelessClient(ts), ts.URL+"/mcp", map[string]any{"tool": "me_get", "input": map[string]any{}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me_get status=%d want 401 (%#v)", resp.StatusCode, out)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj == nil || errObj["code"] != "AUTH_REQUIRED" {
		t.Fatalf("expected AUTH_REQUIRED, got %#v", out)
	}
}

func TestMCPMeGet_UnavailableInAnonymousMode(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "anonymous")
	defer cleanup()

	resp, out := doMCP(t, ts.Client(), ts.URL+"/mcp", map[string]any{"tool": "me_get", "input": map[string]any{}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anonymous me_get status=%d want 403 (%#v)", resp.StatusCode, out)
	}
}
