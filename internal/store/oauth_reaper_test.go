package store

import (
	"context"
	"testing"
	"time"
)

// TestDeleteExpiredOAuthArtifacts guards against unbounded growth of the
// oauth_auth_codes/oauth_access_tokens/oauth_refresh_tokens tables: nothing
// else ever deletes rows from them (consuming/rotating/revoking only flips a
// consumed_at/revoked_at column), so a periodic sweep is the only thing
// keeping them bounded.
func TestDeleteExpiredOAuthArtifacts(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	const resource = "https://scrumboy.example/mcp/rpc"

	u, err := st.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	client, err := st.CreateOAuthClient(ctx, "client-1", "Test Client", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}

	// A live, unconsumed, unexpired auth code: must survive.
	if _, err := st.CreateOAuthAuthCode(ctx, client.ID, u.ID, client.RedirectURI, "challenge", "S256", resource); err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}
	// A live, unrevoked, unexpired token pair: must survive.
	livePair, err := st.IssueOAuthTokenPair(ctx, client.ID, u.ID, resource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair (live): %v", err)
	}

	// A consumed auth code and a rotated-away (revoked) token pair: must be
	// swept even though neither is expired yet.
	consumedCode, err := st.CreateOAuthAuthCode(ctx, client.ID, u.ID, client.RedirectURI, "challenge2", "S256", resource)
	if err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}
	if _, err := st.RedeemOAuthAuthCode(ctx, consumedCode, client.ID, client.RedirectURI, resource, "verifier"); err == nil {
		t.Fatal("RedeemOAuthAuthCode unexpectedly accepted mismatched PKCE")
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE oauth_auth_codes SET consumed_at = ? WHERE code_hash = ?`, time.Now().UTC().UnixMilli(), hashToken(consumedCode)); err != nil {
		t.Fatalf("mark auth code consumed: %v", err)
	}
	rotatedPair, err := st.IssueOAuthTokenPair(ctx, client.ID, u.ID, resource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair (to rotate): %v", err)
	}
	if err := st.RevokeOAuthToken(ctx, rotatedPair.AccessToken, "access_token"); err != nil {
		t.Fatalf("RevokeOAuthToken: %v", err)
	}
	if _, err := st.ConsumeOAuthRefreshToken(ctx, rotatedPair.RefreshToken, client.ID, resource); err != nil {
		t.Fatalf("ConsumeOAuthRefreshToken: %v", err)
	}

	// An expired-but-never-consumed code and an expired-but-never-revoked
	// token pair: must be swept for being past expires_at.
	expiredCode, err := st.CreateOAuthAuthCode(ctx, client.ID, u.ID, client.RedirectURI, "challenge3", "S256", resource)
	if err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}
	pastMs := time.Now().UTC().Add(-time.Hour).UnixMilli()
	if _, err := st.db.ExecContext(ctx, `UPDATE oauth_auth_codes SET expires_at = ? WHERE code_hash = ?`, pastMs, hashToken(expiredCode)); err != nil {
		t.Fatalf("backdate auth code: %v", err)
	}
	expiredPair, err := st.IssueOAuthTokenPair(ctx, client.ID, u.ID, resource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair (to expire): %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE oauth_access_tokens SET expires_at = ? WHERE token_hash = ?`, pastMs, hashToken(expiredPair.AccessToken)); err != nil {
		t.Fatalf("backdate access token: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE oauth_refresh_tokens SET expires_at = ? WHERE token_hash = ?`, pastMs, hashToken(expiredPair.RefreshToken)); err != nil {
		t.Fatalf("backdate refresh token: %v", err)
	}

	deleted, err := st.DeleteExpiredOAuthArtifacts(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredOAuthArtifacts: %v", err)
	}
	// consumedCode + expiredCode + (rotatedPair access+refresh) + (expiredPair access+refresh) = 6
	if deleted != 6 {
		t.Fatalf("expected 6 rows deleted, got %d", deleted)
	}

	// The live token pair must still resolve, and re-sweeping finds nothing
	// further left to delete (checked before consuming the live refresh
	// token below, since that would itself create a freshly-revoked row).
	if _, err := st.GetUserByOAuthAccessToken(ctx, livePair.AccessToken, resource); err != nil {
		t.Fatalf("expected live access token to still resolve, got: %v", err)
	}
	deletedAgain, err := st.DeleteExpiredOAuthArtifacts(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredOAuthArtifacts (second run): %v", err)
	}
	if deletedAgain != 0 {
		t.Fatalf("expected second sweep to find nothing left to delete, got %d", deletedAgain)
	}

	if _, err := st.ConsumeOAuthRefreshToken(ctx, livePair.RefreshToken, client.ID, resource); err != nil {
		t.Fatalf("expected live refresh token to still be consumable, got: %v", err)
	}
}
