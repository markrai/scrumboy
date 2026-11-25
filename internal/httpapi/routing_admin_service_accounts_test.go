package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// REST transport for the settings UI. The store owns the authorization matrix; these tests pin that
// the routes surface it correctly (status codes, one-time plaintext, no-store) and add nothing new.

func serviceAccountRESTClient(t *testing.T, f *serviceAccountHTTPFixture, role string) *http.Client {
	t.Helper()
	email := role + "@example.com"
	resp, body := doJSON(t, f.owner, http.MethodPost, f.ts+"/api/admin/users", map[string]any{
		"email": email, "password": "password123", "name": strings.ToUpper(role[:1]) + role[1:],
	}, nil)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("create %s: %d %s", role, resp.StatusCode, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == 0 {
		t.Fatalf("create %s: bad body %s", role, body)
	}
	if role == "admin" {
		resp, body = doJSON(t, f.owner, http.MethodPatch, fmt.Sprintf("%s/api/admin/users/%d/role", f.ts, created.ID), map[string]any{"role": "admin"}, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("promote: %d %s", resp.StatusCode, body)
		}
	}
	c := newCookieClient(t)
	resp, body = doJSON(t, c, http.MethodPost, f.ts+"/api/auth/login", map[string]any{"email": email, "password": "password123"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: %d %s", role, resp.StatusCode, body)
	}
	return c
}

func TestServiceAccountREST_OwnerLifecycle(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)

	resp, body := doJSON(t, f.owner, http.MethodPost, f.ts+"/api/admin/service-accounts", map[string]any{"name": "  plans-bot  "}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var acct serviceAccountJSON
	if err := json.Unmarshal(body, &acct); err != nil || acct.UserID == 0 || acct.Name != "plans-bot" {
		t.Fatalf("create body = %s (%v)", body, err)
	}

	resp, body = doJSON(t, f.owner, http.MethodGet, f.ts+"/api/admin/service-accounts?limit=100", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "plans-bot") {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}

	tokensURL := fmt.Sprintf("%s/api/admin/service-accounts/%d/tokens", f.ts, acct.UserID)
	resp, body = doJSON(t, f.owner, http.MethodPost, tokensURL, map[string]any{"name": "prod"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint: %d %s", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("mint Cache-Control = %q, want no-store", cc)
	}
	var minted struct {
		TokenID int64  `json:"tokenId"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Token == "" || minted.TokenID == 0 {
		t.Fatalf("mint body = %s (%v)", body, err)
	}

	// The plaintext is never listed again.
	resp, body = doJSON(t, f.owner, http.MethodGet, tokensURL+"?limit=100", nil, nil)
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), minted.Token) || !strings.Contains(string(body), "prod") {
		t.Fatalf("list tokens: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, f.owner, http.MethodDelete, fmt.Sprintf("%s/api/admin/service-tokens/%d", f.ts, minted.TokenID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d %s", resp.StatusCode, body)
	}
	_, body = doJSON(t, f.owner, http.MethodGet, tokensURL+"?limit=100", nil, nil)
	if !strings.Contains(string(body), "revokedAt") {
		t.Fatalf("token not marked revoked: %s", body)
	}

	resp, body = doJSON(t, f.owner, http.MethodDelete, fmt.Sprintf("%s/api/admin/service-accounts/%d", f.ts, acct.UserID), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d %s", resp.StatusCode, body)
	}
	_, body = doJSON(t, f.owner, http.MethodGet, f.ts+"/api/admin/service-accounts?limit=100", nil, nil)
	if strings.Contains(string(body), "plans-bot") {
		t.Fatalf("deleted account still listed: %s", body)
	}
}

func TestServiceAccountREST_AdminCanListAndCreateButNotMintOrDelete(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	admin := serviceAccountRESTClient(t, f, "admin")

	resp, body := doJSON(t, admin, http.MethodGet, f.ts+"/api/admin/service-accounts", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list: %d %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, admin, http.MethodPost, f.ts+"/api/admin/service-accounts", map[string]any{"name": "by-admin"}, nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create: %d %s", resp.StatusCode, body)
	}
	tokensURL := fmt.Sprintf("%s/api/admin/service-accounts/%d/tokens", f.ts, f.svc.ID)
	resp, body = doJSON(t, admin, http.MethodGet, tokensURL, nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list tokens: %d %s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, admin, http.MethodPost, tokensURL, map[string]any{}, nil)
	if resp.StatusCode != http.StatusForbidden || strings.Contains(string(body), `"token"`) {
		t.Fatalf("admin mint: %d %s, want 403 and no token", resp.StatusCode, body)
	}
	resp, body = doJSON(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/admin/service-accounts/%d", f.ts, f.svc.ID), nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin delete: %d %s, want 403", resp.StatusCode, body)
	}
}

func TestServiceAccountREST_PlainUserAndAnonymousAreRejected(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	user := serviceAccountRESTClient(t, f, "user")
	anon := newCookieClient(t)
	tokensURL := fmt.Sprintf("%s/api/admin/service-accounts/%d/tokens", f.ts, f.svc.ID)

	for _, tc := range []struct{ method, url string }{
		{http.MethodGet, f.ts + "/api/admin/service-accounts"},
		{http.MethodPost, f.ts + "/api/admin/service-accounts"},
		{http.MethodGet, tokensURL},
		{http.MethodPost, tokensURL},
		{http.MethodDelete, fmt.Sprintf("%s/api/admin/service-accounts/%d", f.ts, f.svc.ID)},
		{http.MethodDelete, f.ts + "/api/admin/service-tokens/1"},
	} {
		resp, body := doJSON(t, user, tc.method, tc.url, map[string]any{"name": "x"}, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("user %s %s: %d %s, want 403", tc.method, tc.url, resp.StatusCode, body)
		}
		resp, body = doJSON(t, anon, tc.method, tc.url, map[string]any{"name": "x"}, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("anon %s %s: %d %s, want 401", tc.method, tc.url, resp.StatusCode, body)
		}
	}
}

func TestServiceAccountREST_Validation(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	base := f.ts + "/api/admin/service-accounts"

	cases := []struct {
		name, method, url string
		body              any
		want              int
	}{
		{"blank name", http.MethodPost, base, map[string]any{"name": "   "}, http.StatusBadRequest},
		{"bad limit", http.MethodGet, base + "?limit=0", nil, http.StatusBadRequest},
		{"huge limit", http.MethodGet, base + "?limit=100000", nil, http.StatusBadRequest},
		{"bad id", http.MethodDelete, base + "/abc", nil, http.StatusBadRequest},
		{"wrong method on id", http.MethodGet, fmt.Sprintf("%s/%d", base, f.svc.ID), nil, http.StatusMethodNotAllowed},
		{"unknown subpath", http.MethodGet, fmt.Sprintf("%s/%d/nope", base, f.svc.ID), nil, http.StatusNotFound},
		{"bad token cursor", http.MethodGet, fmt.Sprintf("%s/%d/tokens?cursor=x", base, f.svc.ID), nil, http.StatusBadRequest},
		{"bad token id", http.MethodDelete, f.ts + "/api/admin/service-tokens/0", nil, http.StatusBadRequest},
		{"wrong method on token", http.MethodGet, f.ts + "/api/admin/service-tokens/1", nil, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		resp, body := doJSON(t, f.owner, tc.method, tc.url, tc.body, nil)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: %d %s, want %d", tc.name, resp.StatusCode, body, tc.want)
		}
	}
}

// An Admin must not be able to reach human accounts or their personal API tokens through the
// service-account routes: the id must belong to a service account, not just pass the role gate.
func TestServiceAccountREST_AdminCannotTouchHumanAccountsOrTokens(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	admin := serviceAccountRESTClient(t, f, "admin")

	// Owner's personal API token.
	resp, body := doJSON(t, f.owner, http.MethodPost, f.ts+"/api/me/tokens", map[string]any{"name": "personal"}, nil)
	if resp.StatusCode >= 300 {
		t.Skipf("personal token route unavailable: %d %s", resp.StatusCode, body)
	}
	var personal struct {
		ID      int64  `json:"id"`
		TokenID int64  `json:"tokenId"`
		Token   string `json:"token"`
	}
	_ = json.Unmarshal(body, &personal)
	tokenID := personal.ID
	if tokenID == 0 {
		tokenID = personal.TokenID
	}
	if tokenID == 0 {
		t.Skipf("could not read personal token id: %s", body)
	}

	resp, body = doJSON(t, admin, http.MethodDelete, fmt.Sprintf("%s/api/admin/service-tokens/%d", f.ts, tokenID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("admin revoke owner's personal token: %d %s, want 404", resp.StatusCode, body)
	}
	resp, body = doJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/api/admin/service-accounts/%d/tokens", f.ts, f.ownerID), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("admin list human's tokens: %d %s, want 404", resp.StatusCode, body)
	}
	resp, body = doJSON(t, f.owner, http.MethodDelete, fmt.Sprintf("%s/api/admin/service-accounts/%d", f.ts, f.ownerID), nil, nil)
	if resp.StatusCode < 400 {
		t.Errorf("owner delete human via service route: %d %s, want error", resp.StatusCode, body)
	}
	// The owner is still there and can still list accounts.
	resp, body = doJSON(t, f.owner, http.MethodGet, f.ts+"/api/admin/service-accounts", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("owner list after attempts: %d %s", resp.StatusCode, body)
	}
}

// A human user id is not a service account: minting for one must fail and return no plaintext.
func TestServiceAccountREST_CannotMintForHumanUser(t *testing.T) {
	f, _ := newServiceAccountHTTPFixture(t)
	resp, body := doJSON(t, f.owner, http.MethodPost, fmt.Sprintf("%s/api/admin/service-accounts/%d/tokens", f.ts, f.ownerID), map[string]any{}, nil)
	if resp.StatusCode < 400 || strings.Contains(string(body), `"token"`) {
		t.Fatalf("mint for human: %d %s", resp.StatusCode, body)
	}
}
