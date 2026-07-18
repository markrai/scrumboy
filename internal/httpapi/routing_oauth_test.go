package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

// newTestOAuthServer builds a bare *Server (no httptest listener) for unit-level
// exercising of methods like oauthIssuer that only need Server fields and a
// synthetic *http.Request, not a live socket.
func newTestOAuthServer(t *testing.T, opts Options) *Server {
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
	var storeOpts *store.StoreOptions
	if len(opts.EncryptionKey) > 0 {
		storeOpts = &store.StoreOptions{EncryptionKey: opts.EncryptionKey}
	}
	st := store.New(sqlDB, storeOpts)
	if opts.MaxRequestBody == 0 {
		opts.MaxRequestBody = 1 << 20
	}
	if opts.ScrumboyMode == "" {
		opts.ScrumboyMode = "full"
	}
	return NewServer(st, opts)
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
	req.Header.Set("Origin", baseURL)
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

// TestOAuthDiscovery_PublicBaseURLOverridesSpoofedHostAndProto guards the top
// issuer rung: a configured canonical origin must win without consulting the
// request authority or forwarded headers. Separately, untrusted forwarded
// protocol cannot turn a cleartext non-loopback request into an HTTPS issuer.
func TestOAuthDiscovery_PublicBaseURLOverridesSpoofedHostAndProto(t *testing.T) {
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer sqlDB.Close()
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "normal",
		MCPHandler:     mcp.New(st, mcp.Options{Mode: "normal"}),
		PublicBaseURL:  "https://scrumboy.example.com",
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "attacker.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("discovery request: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode discovery response: %v", err)
	}
	issuer, _ := out["issuer"].(string)
	if issuer != "https://scrumboy.example.com" {
		t.Fatalf("expected issuer to use configured PublicBaseURL, got %q (body: %+v)", issuer, out)
	}
	if strings.Contains(fmt.Sprint(out), "attacker.example") {
		t.Fatalf("discovery response leaked spoofed Host header: %+v", out)
	}
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

	for _, bad := range []string{
		"not-a-url", "javascript:alert(1)", "ftp://example.com/cb", "://broken",
		"http://remote-host.example/callback",   // remote, non-loopback http
		"http://localhost.example.com/callback", // subdomain trick, not actually localhost
		"https://user@example.com/callback",     // userinfo
		"https://example.com/callback#fragment", // fragment
		"http://192.168.1.5/callback",           // RFC1918/LAN, not loopback
	} {
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

// TestOAuth_DCRRejectsInvalidPortsAndEmptyFragment covers the authority-parser residuals at the
// unauthenticated registration boundary. url.Parse alone accepts several of these, including an
// explicit empty port and a trailing fragment delimiter whose parsed Fragment value is empty.
func TestOAuth_DCRRejectsInvalidPortsAndEmptyFragment(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	for _, bad := range []string{
		"https://example.com:/callback",
		"https://example.com:0/callback",
		"https://example.com:65536/callback",
		"https://example.com:99999/callback",
		"https://example.com:bad/callback",
		"https://example.com/callback#",
	} {
		var out map[string]any
		resp, body := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/oauth/register", map[string]any{
			"client_name":   "Bad Redirect Authority",
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
// TestOAuth_ConsentSubmitRejectsCrossOriginPost guards against the gap
// SameSite=Lax structurally cannot close: "site" for SameSite purposes is
// the registrable domain, so a POST from any sibling subdomain covered by
// the same session cookie is same-site and still carries the cookie. A
// consent-form POST whose Origin doesn't match this server's own origin
// must be rejected regardless of a valid session cookie.
func TestOAuth_ConsentSubmitRejectsCrossOriginPost(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "cross-origin@example.com", "password123")

	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"s1"},
		"action":                {"approve"},
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := cookieClient.Do(req)
	if err != nil {
		t.Fatalf("cross-origin consent post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected cross-origin consent POST to be rejected with 400, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Fatalf("cross-origin consent POST must not redirect (would leak a code), got Location: %s", loc)
	}
}

// postConsentApprove builds a consent-form POST. Empty origin/referer omit those headers.
func postConsentApprove(t *testing.T, client *http.Client, baseURL, clientID, redirectURI, challenge, state, origin, referer string) *http.Response {
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
		t.Fatalf("new consent POST: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("consent POST: %v", err)
	}
	return resp
}

// TestOAuth_ConsentOriginRefererMatrix covers oauthConsentOriginAllowed with
// classic form-POST shapes (Origin may be absent; Referer is the fallback).
func TestOAuth_ConsentOriginRefererMatrix(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "origin-referer@example.com", "password123")

	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)

	assertReject := func(t *testing.T, resp *http.Response, label string) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", label, resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "" {
			t.Fatalf("%s: must not redirect (would leak a code), got Location: %s", label, loc)
		}
	}
	assertAccept := func(t *testing.T, resp *http.Response, state, label string) {
		t.Helper()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("%s: expected 302, got %d", label, resp.StatusCode)
		}
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatalf("%s: parse Location: %v", label, err)
		}
		if loc.Query().Get("code") == "" {
			t.Fatalf("%s: Location missing code: %s", label, loc)
		}
		if loc.Query().Get("state") != state {
			t.Fatalf("%s: state=%q, want %q", label, loc.Query().Get("state"), state)
		}
	}

	t.Run("matching Origin succeeds", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "origin-ok", ts.URL, "")
		assertAccept(t, resp, "origin-ok", "matching Origin")
	})

	t.Run("mismatching Origin fails", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "origin-bad", "https://evil.example.com", "")
		assertReject(t, resp, "mismatching Origin")
	})

	t.Run("matching Origin wins over evil Referer", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "origin-wins-accept", ts.URL, "https://evil.example.com/")
		assertAccept(t, resp, "origin-wins-accept", "Origin precedence accept")
	})

	t.Run("mismatching Origin wins over matching Referer", func(t *testing.T) {
		_, challenge := pkcePair(t)
		authURL := authorizeURL(ts.URL, clientID, redirectURI, challenge, "origin-wins-reject")
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "origin-wins-reject", "https://evil.example.com", authURL)
		assertReject(t, resp, "Origin precedence reject")
	})

	t.Run("Referer-only same-origin succeeds", func(t *testing.T) {
		_, challenge := pkcePair(t)
		authURL := authorizeURL(ts.URL, clientID, redirectURI, challenge, "referer-ok")
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "referer-ok", "", authURL)
		assertAccept(t, resp, "referer-ok", "same-origin Referer")
	})

	t.Run("Referer-only cross-origin rejected", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "referer-evil", "", "https://evil.example.com/oauth/authorize")
		assertReject(t, resp, "cross-origin Referer")
	})

	t.Run("Referer-only malformed rejected", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "referer-bad", "", "://not a url")
		assertReject(t, resp, "malformed Referer")
	})

	t.Run("missing Origin and Referer rejected", func(t *testing.T) {
		_, challenge := pkcePair(t)
		resp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "neither", "", "")
		assertReject(t, resp, "missing both headers")
	})
}

// TestOAuth_ConsentFormRoundTripRefererOnly simulates a browser form Approve:
// GET consent (session retained), then POST without Origin using the GET URL as Referer.
func TestOAuth_ConsentFormRoundTripRefererOnly(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "form-roundtrip@example.com", "password123")

	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	state := "form-roundtrip"
	authURL := authorizeURL(ts.URL, clientID, redirectURI, challenge, state)

	getResp, err := cookieClient.Get(authURL)
	if err != nil {
		t.Fatalf("consent GET: %v", err)
	}
	body, err := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	if err != nil {
		t.Fatalf("read consent GET: %v", err)
	}
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("consent GET status=%d", getResp.StatusCode)
	}
	if got := getResp.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("consent Referrer-Policy=%q, want same-origin", got)
	}
	if !strings.Contains(string(body), "Approve access") || !strings.Contains(string(body), `method="POST"`) {
		t.Fatalf("expected consent form HTML: %s", body)
	}

	postResp := postConsentApprove(t, cookieClient, ts.URL, clientID, redirectURI, challenge, state, "", authURL)
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusFound {
		t.Fatalf("Referer-only consent POST status=%d, want 302", postResp.StatusCode)
	}
	loc, err := url.Parse(postResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://client.example.com/callback" {
		t.Fatalf("redirect target=%s, want client callback", loc)
	}
	if loc.Query().Get("code") == "" {
		t.Fatalf("redirect missing authorization code: %s", loc)
	}
	if loc.Query().Get("state") != state {
		t.Fatalf("redirect state=%q, want %q", loc.Query().Get("state"), state)
	}
}

func TestOAuth_ConsentPageDisclosesRedirectDestination(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	cookieClient := newCookieClient(t)
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "consent-disclosure@example.com", "password123")

	redirectURI := "https://attacker.example.com:9999/callback"
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

// TestOAuth_DCRRejectsMultipleRedirectURIs guards against silently registering only
// redirect_uris[0]: a client believing all of its listed URIs were accepted (when only the first
// was ever validated) is a footgun, so registration must reject anything but exactly one entry.
func TestOAuth_DCRRejectsMultipleRedirectURIs(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	var out map[string]any
	resp, body := doJSON(t, http.DefaultClient, http.MethodPost, ts.URL+"/oauth/register", map[string]any{
		"client_name":   "Multi Redirect",
		"redirect_uris": []string{"https://a.example.com/cb", "https://b.example.com/cb"},
	}, &out)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for multiple redirect_uris, got %d body=%s", resp.StatusCode, string(body))
	}
	if out["error"] != "invalid_redirect_uri" {
		t.Fatalf("expected invalid_redirect_uri, got %+v", out)
	}
}

// TestOAuth_DCRAcceptsCleanHTTPSAndLoopback covers the audit's documented pass cases so a future
// tightening of isValidOAuthRedirectURI can't silently regress legitimate clients.
func TestOAuth_DCRAcceptsCleanHTTPSAndLoopback(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	for _, good := range []string{
		"http://127.0.0.2/callback",
		"http://[::1]:49152/callback",
		"http://localhost/callback",
		"https://example.com/callback",
		"https://example.com:65535/callback",
	} {
		if clientID := registerOAuthClient(t, ts.URL, good); clientID == "" {
			t.Fatalf("redirect_uri=%q: expected registration to succeed", good)
		}
	}
}

// TestOAuthIssuer_PrefersPublicBaseURL guards the top of the ladder: when configured, the request
// is never consulted, so a forged Host/X-Forwarded-* header can't influence the issuer at all.
func TestOAuthIssuer_PrefersPublicBaseURL(t *testing.T) {
	srv := newTestOAuthServer(t, Options{PublicBaseURL: "https://scrumboy.example.com"})
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example.com/oauth/authorize", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	got, err := srv.oauthIssuer(req)
	if err != nil {
		t.Fatalf("oauthIssuer: %v", err)
	}
	if got != "https://scrumboy.example.com" {
		t.Fatalf("expected configured PublicBaseURL to win, got %q", got)
	}
}

// TestOAuthPublicBaseURLIssuer_HTTPSAndLoopbackHTTP enforces OAuth-only rules on
// SCRUMBOY_PUBLIC_BASE_URL: HTTPS always; HTTP only for explicit loopback (password
// reset may still allow non-loopback HTTP via NormalizeBaseURL).
func TestOAuthPublicBaseURLIssuer_HTTPSAndLoopbackHTTP(t *testing.T) {
	for _, tc := range []struct {
		base    string
		wantOK  bool
		wantOut string
	}{
		{base: "https://scrumboy.example.com", wantOK: true, wantOut: "https://scrumboy.example.com"},
		{base: "http://localhost:8080", wantOK: true, wantOut: "http://localhost:8080"},
		{base: "http://127.0.0.1:8080", wantOK: true, wantOut: "http://127.0.0.1:8080"},
		{base: "http://[::1]:8080", wantOK: true, wantOut: "http://[::1]:8080"},
		{base: "http://scrumboy.example.com", wantOK: false},
		{base: "http://192.168.1.20:8080", wantOK: false},
	} {
		t.Run(tc.base, func(t *testing.T) {
			got, err := oauthPublicBaseURLIssuer(tc.base)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("oauthPublicBaseURLIssuer(%q): %v", tc.base, err)
				}
				if got != tc.wantOut {
					t.Fatalf("oauthPublicBaseURLIssuer(%q) = %q, want %q", tc.base, got, tc.wantOut)
				}
				return
			}
			if err == nil {
				t.Fatalf("oauthPublicBaseURLIssuer(%q) = %q, want rejection", tc.base, got)
			}
		})
	}
}

func TestOAuthIssuer_RejectsNonLoopbackHTTPPublicBaseURL(t *testing.T) {
	srv := newTestOAuthServer(t, Options{PublicBaseURL: "http://scrumboy.example.com"})
	req := httptest.NewRequest(http.MethodGet, "https://scrumboy.example.com/oauth/authorize", nil)
	req.TLS = &tls.ConnectionState{}
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatal("expected oauthIssuer to reject non-loopback HTTP PUBLIC_BASE_URL even when request is TLS")
	}
}

func TestOAuthDiscovery_NonLoopbackHTTPPublicBaseURLReturns503(t *testing.T) {
	srv := newTestOAuthServer(t, Options{PublicBaseURL: "http://scrumboy.example.com"})
	req := httptest.NewRequest(http.MethodGet, "http://scrumboy.example.com/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	srv.handleOAuthASMetadata(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for non-loopback HTTP PUBLIC_BASE_URL, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestOAuthIssuer_DirectTLSUsesValidatedRequestHost guards the second rung: direct TLS supplies the
// HTTPS scheme, while the HTTP request authority still has to pass the shared syntax/port parser.
func TestOAuthIssuer_DirectTLSUsesValidatedRequestHost(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "https://scrumboy.internal/oauth/authorize", nil)
	req.TLS = &tls.ConnectionState{}
	got, err := srv.oauthIssuer(req)
	if err != nil {
		t.Fatalf("oauthIssuer: %v", err)
	}
	if got != "https://scrumboy.internal" {
		t.Fatalf("expected https://scrumboy.internal, got %q", got)
	}
}

func TestOAuthIssuer_DirectTLSRejectsMalformedAuthority(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	for _, host := range []string{
		"",
		"https://evil.example",
		"evil.example/path",
		"user@evil.example",
		"evil.example?query",
		"evil.example#fragment",
		"evil.example:",
		"evil.example:0",
		"evil.example:65536",
		"evil.example:99999",
		"evil.example:bad",
		"[::1",
		"host1.example,host2.example",
		"evil%2f.example",
	} {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://valid.example/oauth/authorize", nil)
			req.Host = host
			req.TLS = &tls.ConnectionState{}
			if _, err := srv.oauthIssuer(req); err == nil {
				t.Fatalf("host=%q: expected direct-TLS issuer derivation to reject malformed authority", host)
			}
		})
	}
}

// TestOAuthIssuer_ForwardedProtoIgnoredWithoutTrustProxy guards against the exact spoof the audit
// called out: an attacker-controlled X-Forwarded-Proto: https on a cleartext, non-loopback
// connection must never make the issuer look secure when SCRUMBOY_TRUST_PROXY is unset.
func TestOAuthIssuer_ForwardedProtoIgnoredWithoutTrustProxy(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "http://scrumboy.internal/oauth/authorize", nil)
	req.Host = "scrumboy.internal"
	req.Header.Set("X-Forwarded-Proto", "https")
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatalf("expected oauthIssuer to fail closed for a non-loopback host with unverified forwarded proto")
	}
}

// TestOAuthIssuer_TrustProxyHonorsForwardedOrigin covers the ladder's TrustProxy rung: scheme and
// host are trusted together as one decision, sourced from X-Forwarded-Proto/X-Forwarded-Host.
func TestOAuthIssuer_TrustProxyHonorsForwardedOrigin(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
	got, err := srv.oauthIssuer(req)
	if err != nil {
		t.Fatalf("oauthIssuer: %v", err)
	}
	if got != "https://scrumboy.example.com" {
		t.Fatalf("expected https://scrumboy.example.com, got %q", got)
	}
}

// TestOAuthIssuer_TrustProxyRequiresForwardedHost is the primary proxy hardening regression test:
// forwarded HTTPS alone must not fall back to the backend-facing request Host.
func TestOAuthIssuer_TrustProxyRequiresForwardedHost(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatal("expected oauthIssuer to fail closed when X-Forwarded-Host is missing")
	}
}

func TestOAuthIssuer_TrustProxyRejectsMalformedForwardedHost(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	for _, host := range []string{
		"",
		"https://evil.example",
		"evil.example/path",
		"user@evil.example",
		"evil.example?query",
		"evil.example#fragment",
		"evil.example:",
		"evil.example:99999",
		"evil.example:bad",
		"[::1",
		"host1.example,host2.example",
		"evil%2f.example",
	} {
		t.Run(host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
			req.Header.Set("X-Forwarded-Proto", "https")
			req.Header.Set("X-Forwarded-Host", host)
			if _, err := srv.oauthIssuer(req); err == nil {
				t.Fatalf("X-Forwarded-Host=%q: expected oauthIssuer to fail closed", host)
			}
		})
	}

	t.Run("duplicate header fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Add("X-Forwarded-Host", "first.example")
		req.Header.Add("X-Forwarded-Host", "second.example")
		if _, err := srv.oauthIssuer(req); err == nil {
			t.Fatal("expected oauthIssuer to reject multiple X-Forwarded-Host fields")
		}
	})
}

// TestOAuthIssuer_TrustProxyRejectsInsecureForwardedProto guards against a proxy (or spoofed
// header) claiming plain http even when TrustProxy is on: this server never advertises an http
// issuer for a non-loopback host just because a proxy is in front of it.
func TestOAuthIssuer_TrustProxyRejectsInsecureForwardedProto(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatalf("expected oauthIssuer to fail closed for an insecure forwarded proto")
	}
}

// When X-Forwarded-Proto is present but not https, CF-Visitor must not be used as a
// fallback — only an entirely absent XFP may consult CF-Visitor.
func TestOAuthIssuer_TrustProxyDoesNotFallBackToCFVisitorWhenXFPPresent(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("CF-Visitor", `{"scheme":"https"}`)
	req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatal("expected oauthIssuer to reject when X-Forwarded-Proto is http even if CF-Visitor says https")
	}
}

func TestOAuthIssuer_TrustProxyRejectsMultiValueForwardedProto(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})

	t.Run("duplicate header fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
		req.Header.Add("X-Forwarded-Proto", "https")
		req.Header.Add("X-Forwarded-Proto", "http")
		req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
		if _, err := srv.oauthIssuer(req); err == nil {
			t.Fatal("expected oauthIssuer to reject multiple X-Forwarded-Proto fields")
		}
	})

	t.Run("comma-separated single field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
		req.Header.Set("X-Forwarded-Proto", "https, http")
		req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
		if _, err := srv.oauthIssuer(req); err == nil {
			t.Fatal("expected oauthIssuer to reject comma-separated X-Forwarded-Proto")
		}
	})
}

func TestOAuthIssuer_TrustProxyCFVisitorRequiresSingleField(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})

	t.Run("single CF-Visitor https", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
		req.Header.Set("CF-Visitor", `{"scheme":"https"}`)
		req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
		got, err := srv.oauthIssuer(req)
		if err != nil {
			t.Fatalf("oauthIssuer: %v", err)
		}
		if got != "https://scrumboy.example.com" {
			t.Fatalf("got %q, want https://scrumboy.example.com", got)
		}
	})

	t.Run("duplicate CF-Visitor fields", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/oauth/authorize", nil)
		req.Header.Add("CF-Visitor", `{"scheme":"https"}`)
		req.Header.Add("CF-Visitor", `{"scheme":"http"}`)
		req.Header.Set("X-Forwarded-Host", "scrumboy.example.com")
		if _, err := srv.oauthIssuer(req); err == nil {
			t.Fatal("expected oauthIssuer to reject multiple CF-Visitor fields")
		}
	})
}

// TestOAuthIssuer_LoopbackAllowsPlainHTTP and TestOAuthIssuer_FailsClosedForUnknownHost cover the
// bottom of the ladder: loopback is a safe, useful default for local/dev use; anything else with no
// PublicBaseURL, no TLS, and no (or untrusted) proxy must fail closed rather than guess.
func TestOAuthIssuer_LoopbackAllowsPlainHTTP(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	for _, host := range []string{"localhost:8080", "127.0.0.2:8080", "[::1]:8080"} {
		req := httptest.NewRequest(http.MethodGet, "http://"+host+"/oauth/authorize", nil)
		req.Host = host
		got, err := srv.oauthIssuer(req)
		if err != nil {
			t.Fatalf("host=%q: oauthIssuer: %v", host, err)
		}
		if got != "http://"+host {
			t.Fatalf("host=%q: expected http://%s, got %q", host, host, got)
		}
	}
}

func TestOAuthIssuer_LoopbackRejectsMalformedAuthority(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	for _, host := range []string{"localhost:", "127.0.0.1:0", "[::1", "[::1]:65536"} {
		req := httptest.NewRequest(http.MethodGet, "http://localhost/oauth/authorize", nil)
		req.Host = host
		if _, err := srv.oauthIssuer(req); err == nil {
			t.Fatalf("host=%q: expected loopback issuer derivation to reject malformed authority", host)
		}
	}
}

func TestOAuthIssuer_FailsClosedForUnknownHost(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	req := httptest.NewRequest(http.MethodGet, "http://random-internet-host.example/oauth/authorize", nil)
	req.Host = "random-internet-host.example"
	if _, err := srv.oauthIssuer(req); err == nil {
		t.Fatalf("expected oauthIssuer to fail closed for a non-loopback plaintext request with no PublicBaseURL/TLS/TrustProxy")
	}
}

// TestOAuthDiscovery_FailsClosedReturns503 checks the fail-closed behavior actually reaches the
// wire as a controlled error, not a panic or a guessed issuer value.
func TestOAuthDiscovery_FailsClosedReturns503(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/.well-known/oauth-authorization-server", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "random-internet-host.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when no trustworthy issuer can be derived, got %d", resp.StatusCode)
	}
}

func TestOAuthDiscovery_TrustProxyWithoutForwardedHostReturns503(t *testing.T) {
	srv := newTestOAuthServer(t, Options{TrustProxy: true})
	req := httptest.NewRequest(http.MethodGet, "http://internal-backend:8080/.well-known/oauth-authorization-server", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()

	srv.handleOAuthASMetadata(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when a trusted proxy sends only X-Forwarded-Proto, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestOAuthHTMLPages_SecurityHeaders guards MEDIUM-02: login/consent/error pages must all carry
// no-store plus anti-framing headers so the consent Approve button can't be UI-redressed and
// cached copies never persist an auth-code-bearing or session-bound page.
func TestOAuthHTMLPages_SecurityHeaders(t *testing.T) {
	assertHeaders := func(t *testing.T, resp *http.Response) {
		t.Helper()
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("X-Frame-Options = %q, want DENY", got)
		}
		if got := resp.Header.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
			t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("Referrer-Policy = %q, want same-origin", got)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
	}

	t.Run("error page", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
		defer cleanup()
		resp, err := http.Get(ts.URL + "/oauth/authorize")
		if err != nil {
			t.Fatalf("GET /oauth/authorize: %v", err)
		}
		defer resp.Body.Close()
		assertHeaders(t, resp)
	})

	t.Run("login page", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
		defer cleanup()
		cookieClient := newCookieClient(t)
		bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "headers-login@example.com", "password123")
		redirectURI := "https://client.example.com/callback"
		clientID := registerOAuthClient(t, ts.URL, redirectURI)
		_, challenge := pkcePair(t)
		// Unauthenticated client hits the login page.
		resp, err := http.Get(authorizeURL(ts.URL, clientID, redirectURI, challenge, "s1"))
		if err != nil {
			t.Fatalf("authorize GET: %v", err)
		}
		defer resp.Body.Close()
		assertHeaders(t, resp)
	})

	t.Run("consent page", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
		defer cleanup()
		cookieClient := newCookieClient(t)
		bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "headers-consent@example.com", "password123")
		redirectURI := "https://client.example.com/callback"
		clientID := registerOAuthClient(t, ts.URL, redirectURI)
		_, challenge := pkcePair(t)
		resp, err := cookieClient.Get(authorizeURL(ts.URL, clientID, redirectURI, challenge, "s1"))
		if err != nil {
			t.Fatalf("authorize GET: %v", err)
		}
		defer resp.Body.Close()
		assertHeaders(t, resp)
	})
}
