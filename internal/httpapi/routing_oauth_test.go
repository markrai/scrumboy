package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func newTestHTTPServerWithMCP(t *testing.T, mode string) (*httptest.Server, *sql.DB, func()) {
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
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   mode,
		MCPHandler:     mcp.New(st, mcp.Options{Mode: mode}),
	})
	ts := httptest.NewServer(srv)
	return ts, sqlDB, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

// pkcePair returns a random S256 code_verifier/code_challenge pair.
func pkcePair(t *testing.T) (verifier, challenge string) {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}

func registerOAuthClient(t *testing.T, baseURL, redirectURI string) string {
	t.Helper()
	client := &http.Client{}
	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/oauth/register", map[string]any{
		"client_name":   "Test Client",
		"redirect_uris": []string{redirectURI},
	}, &out)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", resp.StatusCode, string(body))
	}
	clientID, _ := out["client_id"].(string)
	if clientID == "" {
		t.Fatalf("expected client_id in register response, got %+v", out)
	}
	return clientID
}

func authorizeURL(baseURL, clientID, redirectURI, challenge, state string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return baseURL + "/oauth/authorize?" + q.Encode()
}

func approveConsent(t *testing.T, client *http.Client, baseURL, clientID, redirectURI, challenge, state string) *url.URL {
	t.Helper()
	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"action":                {"approve"},
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("approve consent: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from consent approval, got %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	return loc
}

func exchangeToken(t *testing.T, baseURL string, form url.Values) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	return resp.StatusCode, out
}

func TestOAuth_FullHappyPath(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)

	// Discovery metadata sanity checks.
	var asMeta map[string]any
	if resp, _ := doJSON(t, http.DefaultClient, http.MethodGet, ts.URL+"/.well-known/oauth-authorization-server", nil, &asMeta); resp.StatusCode != http.StatusOK {
		t.Fatalf("AS metadata status=%d", resp.StatusCode)
	}
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "registration_endpoint", "revocation_endpoint"} {
		if asMeta[field] == nil || asMeta[field] == "" {
			t.Fatalf("expected AS metadata field %q, got %+v", field, asMeta)
		}
	}
	var prMeta map[string]any
	if resp, _ := doJSON(t, http.DefaultClient, http.MethodGet, ts.URL+"/.well-known/oauth-protected-resource", nil, &prMeta); resp.StatusCode != http.StatusOK {
		t.Fatalf("protected resource metadata status=%d", resp.StatusCode)
	}
	if prMeta["resource"] == nil {
		t.Fatalf("expected protected resource metadata to include resource, got %+v", prMeta)
	}

	verifier, challenge := pkcePair(t)

	// Unauthenticated GET /oauth/authorize should render a login prompt, not a consent form.
	unauthResp, err := http.Get(authorizeURL(ts.URL, clientID, redirectURI, challenge, "s1"))
	if err != nil {
		t.Fatalf("unauthenticated authorize GET: %v", err)
	}
	unauthBody := make([]byte, 4096)
	n, _ := unauthResp.Body.Read(unauthBody)
	unauthResp.Body.Close()
	if !strings.Contains(string(unauthBody[:n]), "Log in") {
		t.Fatalf("expected login prompt for unauthenticated authorize GET, got: %s", unauthBody[:n])
	}

	// Authenticated GET should render the consent form.
	consentResp, err := cookieClient.Get(authorizeURL(ts.URL, clientID, redirectURI, challenge, "s1"))
	if err != nil {
		t.Fatalf("authenticated authorize GET: %v", err)
	}
	consentBody := make([]byte, 4096)
	cn, _ := consentResp.Body.Read(consentBody)
	consentResp.Body.Close()
	if !strings.Contains(string(consentBody[:cn]), "Approve access") {
		t.Fatalf("expected consent form for authenticated authorize GET, got: %s", consentBody[:cn])
	}

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("expected code in redirect, got %s", loc.String())
	}
	if loc.Query().Get("state") != "s1" {
		t.Fatalf("expected state echoed back, got %s", loc.String())
	}

	status, tokenResp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange status=%d body=%+v", status, tokenResp)
	}
	accessToken, _ := tokenResp["access_token"].(string)
	if accessToken == "" {
		t.Fatalf("expected access_token in response, got %+v", tokenResp)
	}
	if tokenResp["token_type"] != "Bearer" {
		t.Fatalf("expected token_type=Bearer, got %+v", tokenResp)
	}
	if tokenResp["refresh_token"] == nil || tokenResp["refresh_token"] == "" {
		t.Fatalf("expected refresh_token, got %+v", tokenResp)
	}

	// Use the OAuth access token as a Bearer credential on the MCP endpoint.
	mcpReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projects.list","arguments":{}}}`))
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Authorization", "Bearer "+accessToken)
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		t.Fatalf("mcp call: %v", err)
	}
	defer mcpResp.Body.Close()
	var mcpOut map[string]any
	if err := json.NewDecoder(mcpResp.Body).Decode(&mcpOut); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	result, ok := mcpOut["result"].(map[string]any)
	if !ok || result["isError"] == true {
		t.Fatalf("expected successful MCP tool call with OAuth bearer token, got %+v", mcpOut)
	}
}

func TestOAuth_PKCEMismatch(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	status, resp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {"totally-wrong-verifier"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
	if resp["error"] != "invalid_grant" {
		t.Fatalf("expected invalid_grant, got %+v", resp)
	}
}

func TestOAuth_ExpiredAuthCode(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	if _, err := sqlDB.Exec(`UPDATE oauth_auth_codes SET expires_at = 0`); err != nil {
		t.Fatalf("backdate auth code: %v", err)
	}

	status, resp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || resp["error"] != "invalid_grant" {
		t.Fatalf("expected 400 invalid_grant for expired code, got status=%d body=%+v", status, resp)
	}
}

func TestOAuth_ReplayedAuthCodeRejected(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	status1, resp1 := exchangeToken(t, ts.URL, form)
	if status1 != http.StatusOK {
		t.Fatalf("first exchange should succeed, got status=%d body=%+v", status1, resp1)
	}

	status2, resp2 := exchangeToken(t, ts.URL, form)
	if status2 != http.StatusBadRequest || resp2["error"] != "invalid_grant" {
		t.Fatalf("expected replay to be rejected with invalid_grant, got status=%d body=%+v", status2, resp2)
	}
}

func TestOAuth_RedirectURIMismatchAtTokenExchange(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	status, resp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {"http://localhost:9999/different-callback"},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if status != http.StatusBadRequest || resp["error"] != "invalid_grant" {
		t.Fatalf("expected 400 invalid_grant for redirect_uri mismatch, got status=%d body=%+v", status, resp)
	}
}

func TestOAuth_AnonymousMode404s(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "anonymous")
	defer cleanup()

	paths := []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/oauth/register",
		"/oauth/authorize",
		"/oauth/token",
		"/oauth/revoke",
	}
	for _, p := range paths {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s in anonymous mode: expected 404, got %d", p, resp.StatusCode)
		}

		postResp, err := http.Post(ts.URL+p, "application/x-www-form-urlencoded", strings.NewReader(""))
		if err != nil {
			t.Fatalf("POST %s: %v", p, err)
		}
		postResp.Body.Close()
		if postResp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s in anonymous mode: expected 404, got %d", p, postResp.StatusCode)
		}
	}
}

func TestOAuth_RefreshTokenFlow(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	status, tokenResp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("initial token exchange status=%d body=%+v", status, tokenResp)
	}
	firstAccessToken := tokenResp["access_token"].(string)
	firstRefreshToken := tokenResp["refresh_token"].(string)

	// Rotate.
	status2, refreshResp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {firstRefreshToken},
	})
	if status2 != http.StatusOK {
		t.Fatalf("refresh exchange status=%d body=%+v", status2, refreshResp)
	}
	newAccessToken, _ := refreshResp["access_token"].(string)
	if newAccessToken == "" || newAccessToken == firstAccessToken {
		t.Fatalf("expected a new distinct access token, got %+v", refreshResp)
	}

	// Pre-rotation access token is unaffected by refresh rotation in v1 (only
	// the refresh token itself is rotated) and remains usable until its own
	// expiry.
	mcpReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projects.list","arguments":{}}}`))
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Authorization", "Bearer "+firstAccessToken)
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		t.Fatalf("mcp call with pre-rotation access token: %v", err)
	}
	defer mcpResp.Body.Close()
	var mcpOut map[string]any
	json.NewDecoder(mcpResp.Body).Decode(&mcpOut)
	if result, ok := mcpOut["result"].(map[string]any); !ok || result["isError"] == true {
		t.Fatalf("expected pre-rotation access token to still work, got %+v", mcpOut)
	}

	// Reusing the already-rotated-away-from refresh token must fail.
	status3, reuseResp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {firstRefreshToken},
	})
	if status3 != http.StatusBadRequest || reuseResp["error"] != "invalid_grant" {
		t.Fatalf("expected reused refresh token to be rejected, got status=%d body=%+v", status3, reuseResp)
	}
}

func TestOAuth_RevokedAccessTokenRejectedByMCP(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "owner@example.com", "password123")

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)

	loc := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "s1")
	code := loc.Query().Get("code")

	_, tokenResp := exchangeToken(t, ts.URL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	accessToken := tokenResp["access_token"].(string)

	// Revoking an active token succeeds (200), and a nonexistent/garbage
	// token also returns 200 (RFC 7009 §2.2: no token-existence oracle).
	revokeResp, err := http.PostForm(ts.URL+"/oauth/revoke", url.Values{"token": {accessToken}})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from revoke, got %d", revokeResp.StatusCode)
	}

	garbageRevokeResp, err := http.PostForm(ts.URL+"/oauth/revoke", url.Values{"token": {"not-a-real-token"}})
	if err != nil {
		t.Fatalf("revoke garbage token: %v", err)
	}
	garbageRevokeResp.Body.Close()
	if garbageRevokeResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from revoking a nonexistent token, got %d", garbageRevokeResp.StatusCode)
	}

	mcpReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(`{"tool":"projects.list","input":{}}`))
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Authorization", "Bearer "+accessToken)
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		t.Fatalf("mcp call with revoked token: %v", err)
	}
	defer mcpResp.Body.Close()
	if mcpResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked access token on legacy /mcp, got %d", mcpResp.StatusCode)
	}
}

func TestOAuth_DCRMissingRedirectURIs(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	var out map[string]any
	resp, body := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/oauth/register", map[string]any{
		"client_name": "No Redirect",
	}, &out)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, string(body))
	}
	if out["error"] != "invalid_redirect_uri" {
		t.Fatalf("expected invalid_redirect_uri, got %+v", out)
	}
}

// TestOAuth_DCRRejectsMalformedRedirectURI guards against registering a client with a redirect_uri
// that isn't even a well-formed absolute http(s) URL (e.g. a bare string, or a non-http(s) scheme
// like javascript:) — DCR is unauthenticated, so this is the only structural check available at
// registration time (exact-match comparison later in the flow is what actually prevents tampering).
func TestOAuth_DCRRejectsMalformedRedirectURI(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	for _, bad := range []string{"not-a-url", "javascript:alert(1)", "ftp://example.com/cb", "://broken"} {
		var out map[string]any
		resp, body := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/oauth/register", map[string]any{
			"client_name":   "Bad Redirect",
			"redirect_uris": []string{bad},
		}, &out)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("redirect_uri=%q: expected 400, got %d body=%s", bad, resp.StatusCode, string(body))
		}
		if out["error"] != "invalid_redirect_uri" {
			t.Fatalf("redirect_uri=%q: expected invalid_redirect_uri, got %+v", bad, out)
		}
	}
}

// TestOAuth_DCRAcceptsLoopbackHTTP guards against over-tightening the redirect_uri check: native/CLI
// clients (RFC 8252) commonly redirect to a plain-http loopback address, which must keep working.
func TestOAuth_DCRAcceptsLoopbackHTTP(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	clientID := registerOAuthClient(t, ts.URL, "http://127.0.0.1:54321/callback")
	if clientID == "" {
		t.Fatal("expected a client_id for a valid loopback redirect_uri")
	}
}

// TestOAuth_DCRRejectsNonJSONContentType guards against a cross-origin "simple request" (e.g.
// Content-Type: text/plain, which browsers send with no CORS preflight) reaching DCR: a hostile
// page could otherwise get visitors' browsers to each register a client from their own IP,
// defeating the per-IP rate limit by distributing registration load across many real addresses.
func TestOAuth_DCRRejectsNonJSONContentType(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	payload := `{"client_name":"Simple Request Probe","redirect_uris":["http://localhost:9999/callback"]}`
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/register", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-JSON Content-Type, got %d body=%+v", resp.StatusCode, out)
	}
	if out["error"] != "invalid_client_metadata" {
		t.Fatalf("expected invalid_client_metadata, got %+v", out)
	}
}

// TestOAuth_DCRRateLimitIgnoresSpoofedXFFByDefault guards against the rate limit added to
// /oauth/register being trivially defeated: without TrustProxy, a client can't get a fresh
// rate-limit bucket per request just by sending a different X-Forwarded-For value each time.
func TestOAuth_DCRRateLimitIgnoresSpoofedXFFByDefault(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	var lastStatus int
	for i := 0; i < 11; i++ {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/register", strings.NewReader(
			`{"client_name":"XFF Spoof Probe","redirect_uris":["http://localhost:9999/callback"]}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// A different spoofed source IP on every request -- without TrustProxy this must be ignored,
		// so all requests still count against the same (RemoteAddr-keyed) rate-limit bucket.
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		lastStatus = resp.StatusCode
		resp.Body.Close()
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected rate limiting to ignore spoofed X-Forwarded-For and still trigger by the 11th request, got %d", lastStatus)
	}
}

// TestOAuth_DCRRateLimited guards against unauthenticated, unbounded client registration: an
// attacker minting unlimited oauth_clients rows for free is both a DB-growth DoS vector and the
// zero-cost first step of a consent-screen phishing attack (register a trusted-sounding client_name
// pointing at an attacker redirect_uri).
func TestOAuth_DCRRateLimited(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	var lastStatus int
	for i := 0; i < 11; i++ {
		var out map[string]any
		resp, _ := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/oauth/register", map[string]any{
			"client_name":   "Rate Limit Probe",
			"redirect_uris": []string{"http://localhost:9999/callback"},
		}, &out)
		lastStatus = resp.StatusCode
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected the 11th registration within a minute to be rate limited (429), got %d", lastStatus)
	}
}

// TestOAuth_ConsentPageDisclosesRedirectDestination guards the phishing-mitigation fix: since any
// client can self-register via unauthenticated DCR with an arbitrary client_name, the consent
// screen must show the actual redirect_uri destination, not just the (spoofable) name, so a user
// has a chance to notice an untrusted destination before approving.
func TestOAuth_ConsentPageDisclosesRedirectDestination(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "consent-disclosure@example.com", "password123")

	redirectURI := "http://attacker.example.com:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)

	resp, err := cookieClient.Get(authorizeURL(ts.URL, clientID, redirectURI, challenge, "s1"))
	if err != nil {
		t.Fatalf("authorize GET: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 8192)
	n, _ := resp.Body.Read(body)
	if !strings.Contains(string(body[:n]), redirectURI) {
		t.Fatalf("expected consent page to disclose the redirect_uri destination %q, got: %s", redirectURI, body[:n])
	}
}
