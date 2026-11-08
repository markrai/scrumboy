package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

// A service account authenticates over MCP with a bearer API token, and that authentication grants
// exactly a plain user's capabilities. Slice 2 adds the provisioning path; the token row is inserted
// directly here so that the boundary is pinned before that path exists.
func TestMCP_ServiceAccountBearerIsAuthenticatedButUnprivileged(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t, ts)
	bootstrapUser(t, ownerClient, ts.URL)
	var ownerID int64
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE system_role = 'owner'`).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, nil)
	svc, err := st.CreateServiceAccount(context.Background(), ownerID, "Bot")
	if err != nil {
		t.Fatal(err)
	}
	const raw = store.APITokenPrefix + "service-account-mcp-token"
	sum := sha256.Sum256([]byte(raw))
	if _, err := sqlDB.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (?, ?, 1)`, svc.ID, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	bare := newStatelessClient(ts)

	resp, out := postMCPWithBearer(t, bare, ts.URL, raw, map[string]any{"tool": "projects_list", "input": map[string]any{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("projects_list: status=%d %#v", resp.StatusCode, out)
	}
	data, _ := out["data"].(map[string]any)
	if items, _ := data["items"].([]any); len(items) != 0 {
		t.Fatalf("service account sees projects without membership: %#v", out)
	}

	for name, input := range map[string]map[string]any{
		"admin_listUsers":      {},
		"admin_updateUserRole": {"userId": svc.ID, "role": "admin"},
		"admin_deleteUser":     {"userId": ownerID},
	} {
		resp, out := postMCPWithBearer(t, bare, ts.URL, raw, map[string]any{"tool": name, "input": input})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s with a service token: status=%d %#v, want 403", name, resp.StatusCode, out)
		}
	}

	// An owner cannot promote it through MCP either.
	resp, out = doMCP(t, ownerClient, ts.URL+"/mcp", map[string]any{
		"tool": "admin_updateUserRole", "input": map[string]any{"userId": svc.ID, "role": "admin"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("owner promoting a service account: status=%d %#v, want 403", resp.StatusCode, out)
	}
	got, err := st.GetUser(context.Background(), svc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemRole != store.SystemRoleUser {
		t.Fatalf("role = %q", got.SystemRole)
	}
}
