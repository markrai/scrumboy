package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"
)

const testMCPResource = "https://scrumboy.example/mcp/rpc"

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func newOAuthGrantFixture(t *testing.T) (*Store, OAuthClient, User, func()) {
	t.Helper()
	st, cleanup := newTestStore(t)
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		cleanup()
		t.Fatalf("BootstrapUser: %v", err)
	}
	client, err := st.CreateOAuthClient(ctx, "client-1", "Client", "http://127.0.0.1/callback")
	if err != nil {
		cleanup()
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	return st, client, user, cleanup
}

func TestRedeemOAuthAuthCodeFailedValidationDoesNotBurnGrant(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	const verifier = "a-valid-pkce-verifier"
	code, err := st.CreateOAuthAuthCode(ctx, client.ID, user.ID, client.RedirectURI, pkceChallenge(verifier), "S256", testMCPResource)
	if err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}

	invalid := []struct {
		clientID, redirectURI, resource, verifier string
	}{
		{"other-client", client.RedirectURI, testMCPResource, verifier},
		{client.ID, "http://127.0.0.1/other", testMCPResource, verifier},
		{client.ID, client.RedirectURI, "https://other.example/mcp/rpc", verifier},
		{client.ID, client.RedirectURI, testMCPResource, "wrong-verifier"},
	}
	for _, attempt := range invalid {
		if _, err := st.RedeemOAuthAuthCode(ctx, code, attempt.clientID, attempt.redirectURI, attempt.resource, attempt.verifier); !errors.Is(err, ErrNotFound) {
			t.Fatalf("invalid redemption error = %v, want ErrNotFound", err)
		}
	}

	grant, err := st.RedeemOAuthAuthCode(ctx, code, client.ID, client.RedirectURI, testMCPResource, verifier)
	if err != nil {
		t.Fatalf("valid redemption after invalid attempts: %v", err)
	}
	if grant.Resource != testMCPResource || grant.ClientID != client.ID || grant.UserID != user.ID {
		t.Fatalf("redeemed grant = %+v", grant)
	}
	if _, err := st.RedeemOAuthAuthCode(ctx, code, client.ID, client.RedirectURI, testMCPResource, verifier); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay error = %v, want ErrNotFound", err)
	}
}

func TestConsumeOAuthRefreshTokenFailedValidationDoesNotBurnGrant(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	pair, err := st.IssueOAuthTokenPair(ctx, client.ID, user.ID, testMCPResource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair: %v", err)
	}

	if _, err := st.ConsumeOAuthRefreshToken(ctx, pair.RefreshToken, "other-client", testMCPResource); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-client error = %v, want ErrNotFound", err)
	}
	if _, err := st.ConsumeOAuthRefreshToken(ctx, pair.RefreshToken, client.ID, "https://other.example/mcp/rpc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-resource error = %v, want ErrNotFound", err)
	}
	grant, err := st.ConsumeOAuthRefreshToken(ctx, pair.RefreshToken, client.ID, testMCPResource)
	if err != nil {
		t.Fatalf("valid refresh after invalid attempts: %v", err)
	}
	if grant.Resource != testMCPResource || grant.ClientID != client.ID || grant.UserID != user.ID {
		t.Fatalf("refresh grant = %+v", grant)
	}
}

func TestOAuthAccessTokenRequiresExpectedResource(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	pair, err := st.IssueOAuthTokenPair(ctx, client.ID, user.ID, testMCPResource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair: %v", err)
	}
	if _, err := st.GetUserByOAuthAccessToken(ctx, pair.AccessToken, "https://other.example/mcp/rpc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-resource lookup error = %v, want ErrNotFound", err)
	}
	got, err := st.GetUserByOAuthAccessToken(ctx, pair.AccessToken, testMCPResource)
	if err != nil || got.ID != user.ID {
		t.Fatalf("canonical-resource lookup = user %+v, err %v", got, err)
	}
}

func TestConcurrentOAuthRedemptionAllowsExactlyOneSuccess(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	const verifier = "another-valid-verifier"
	code, err := st.CreateOAuthAuthCode(ctx, client.ID, user.ID, client.RedirectURI, pkceChallenge(verifier), "S256", testMCPResource)
	if err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}
	pair, err := st.IssueOAuthTokenPair(ctx, client.ID, user.ID, testMCPResource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair: %v", err)
	}

	for name, redeem := range map[string]func() error{
		"authorization code": func() error {
			_, err := st.RedeemOAuthAuthCodeAndIssue(ctx, code, client.ID, client.RedirectURI, testMCPResource, verifier)
			return err
		},
		"refresh token": func() error {
			_, err := st.ConsumeOAuthRefreshTokenAndIssue(ctx, pair.RefreshToken, client.ID, testMCPResource)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			const attempts = 8
			start := make(chan struct{})
			errs := make(chan error, attempts)
			var wg sync.WaitGroup
			for range attempts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs <- redeem()
				}()
			}
			close(start)
			wg.Wait()
			close(errs)
			successes := 0
			for err := range errs {
				if err == nil {
					successes++
					continue
				}
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("redemption error = %v, want ErrNotFound", err)
				}
			}
			if successes != 1 {
				t.Fatalf("successful redemptions = %d, want 1", successes)
			}
		})
	}
}

func TestRedeemOAuthAuthCodeAndIssueRollsBackWhenTokenInsertFails(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	const verifier = "rollback-verifier"
	code, err := st.CreateOAuthAuthCode(ctx, client.ID, user.ID, client.RedirectURI, pkceChallenge(verifier), "S256", testMCPResource)
	if err != nil {
		t.Fatalf("CreateOAuthAuthCode: %v", err)
	}

	const collidingAccess = "deterministic-access-secret-for-collision"
	const collidingRefresh = "deterministic-refresh-secret-for-collision"
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, resource, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)
`, hashToken(collidingAccess), client.ID, user.ID, testMCPResource, nowMs, nowMs+3600_000); err != nil {
		t.Fatalf("pre-insert colliding access token: %v", err)
	}

	prev := newOAuthTokenSecrets
	newOAuthTokenSecrets = func() (string, string, error) {
		return collidingAccess, collidingRefresh, nil
	}
	t.Cleanup(func() { newOAuthTokenSecrets = prev })

	if _, err := st.RedeemOAuthAuthCodeAndIssue(ctx, code, client.ID, client.RedirectURI, testMCPResource, verifier); err == nil {
		t.Fatal("expected token insert collision to fail redemption")
	}

	var consumedAt any
	if err := st.db.QueryRowContext(ctx, `
SELECT consumed_at FROM oauth_auth_codes WHERE code_hash = ?
`, hashToken(code)).Scan(&consumedAt); err != nil {
		t.Fatalf("lookup auth code: %v", err)
	}
	if consumedAt != nil {
		t.Fatalf("authorization code was consumed after rolled-back issue; consumed_at=%v", consumedAt)
	}

	newOAuthTokenSecrets = defaultNewOAuthTokenSecrets
	pair, err := st.RedeemOAuthAuthCodeAndIssue(ctx, code, client.ID, client.RedirectURI, testMCPResource, verifier)
	if err != nil {
		t.Fatalf("redemption after restoring secret generator: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("issued pair incomplete: %+v", pair)
	}
}

func TestConsumeOAuthRefreshTokenAndIssueRollsBackWhenTokenInsertFails(t *testing.T) {
	st, client, user, cleanup := newOAuthGrantFixture(t)
	defer cleanup()
	ctx := context.Background()
	pair, err := st.IssueOAuthTokenPair(ctx, client.ID, user.ID, testMCPResource)
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair: %v", err)
	}

	const collidingAccess = "deterministic-access-secret-for-refresh-collision"
	const collidingRefresh = "deterministic-refresh-secret-for-refresh-collision"
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, resource, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)
`, hashToken(collidingAccess), client.ID, user.ID, testMCPResource, nowMs, nowMs+3600_000); err != nil {
		t.Fatalf("pre-insert colliding access token: %v", err)
	}

	prev := newOAuthTokenSecrets
	newOAuthTokenSecrets = func() (string, string, error) {
		return collidingAccess, collidingRefresh, nil
	}
	t.Cleanup(func() { newOAuthTokenSecrets = prev })

	if _, err := st.ConsumeOAuthRefreshTokenAndIssue(ctx, pair.RefreshToken, client.ID, testMCPResource); err == nil {
		t.Fatal("expected token insert collision to fail refresh rotation")
	}

	var revokedAt any
	if err := st.db.QueryRowContext(ctx, `
SELECT revoked_at FROM oauth_refresh_tokens WHERE token_hash = ?
`, hashToken(pair.RefreshToken)).Scan(&revokedAt); err != nil {
		t.Fatalf("lookup refresh token: %v", err)
	}
	if revokedAt != nil {
		t.Fatalf("refresh token was revoked after rolled-back issue; revoked_at=%v", revokedAt)
	}

	newOAuthTokenSecrets = defaultNewOAuthTokenSecrets
	rotated, err := st.ConsumeOAuthRefreshTokenAndIssue(ctx, pair.RefreshToken, client.ID, testMCPResource)
	if err != nil {
		t.Fatalf("refresh after restoring secret generator: %v", err)
	}
	if rotated.AccessToken == "" || rotated.RefreshToken == "" {
		t.Fatalf("rotated pair incomplete: %+v", rotated)
	}
}
