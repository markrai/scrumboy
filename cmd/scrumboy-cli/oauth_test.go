package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOAuth is a minimal Scrumboy OAuth + /mcp/rpc server.
type fakeOAuth struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	redirectURI string
	challenge   string
	validAccess string // the only access token /mcp/rpc accepts
	validRefr   string
	badState    bool
	legacyCalls int
	rpcCalls    int
	refreshes   int
	revoked     []string

	tokenEndpointOverride string
	registered            bool
}

func (f *fakeOAuth) tokenEP() string {
	if f.tokenEndpointOverride != "" {
		return f.tokenEndpointOverride
	}
	return f.srv.URL + "/oauth/token"
}

func newFakeOAuth(t *testing.T) *fakeOAuth {
	f := &fakeOAuth{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/oauth/authorize",
			"token_endpoint":         f.tokenEP(),
			"registration_endpoint":  f.srv.URL + "/oauth/register",
			"revocation_endpoint":    f.srv.URL + "/oauth/revoke",
		})
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp/rpc", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"resource": f.srv.URL + "/mcp/rpc"})
	})
	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) != 1 || !strings.HasPrefix(body.RedirectURIs[0], "http://127.0.0.1:") || body.AuthMethod != "none" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client_metadata", "error_description": "bad metadata"})
			return
		}
		f.mu.Lock()
		f.redirectURI = body.RedirectURIs[0]
		f.registered = true
		f.mu.Unlock()
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]string{"client_id": "cid-1"})
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("client_id") != "cid-1" ||
			q.Get("redirect_uri") != f.redirectURI || q.Get("resource") != f.srv.URL+"/mcp/rpc" || q.Get("state") == "" {
			http.Error(w, "bad authorize request", 400)
			return
		}
		f.mu.Lock()
		f.challenge = q.Get("code_challenge")
		bad := f.badState
		f.mu.Unlock()
		state := q.Get("state")
		if bad {
			state = "forged"
		}
		http.Redirect(w, r, f.redirectURI+"?code=code-1&state="+state, http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		fail := func(code string) {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "nope"})
		}
		if r.Form.Get("resource") != f.srv.URL+"/mcp/rpc" || r.Form.Get("client_id") != "cid-1" {
			fail("invalid_target")
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "code-1" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge ||
				r.Form.Get("redirect_uri") != f.redirectURI {
				fail("invalid_grant")
				return
			}
			f.validAccess, f.validRefr = "at-1", "rt-1"
		case "refresh_token":
			if r.Form.Get("refresh_token") != f.validRefr {
				fail("invalid_grant")
				return
			}
			f.refreshes++
			f.validAccess = fmt.Sprintf("at-%d", f.refreshes+1)
			f.validRefr = fmt.Sprintf("rt-%d", f.refreshes+1)
		default:
			fail("unsupported_grant_type")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": f.validAccess, "token_type": "Bearer", "expires_in": 3600, "refresh_token": f.validRefr})
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.legacyCalls++
		f.mu.Unlock()
		w.WriteHeader(401) // legacy /mcp never accepts OAuth tokens
		w.Write([]byte(`{"ok":false,"error":{"code":"AUTH_REQUIRED","message":"unauthorized","details":{}}}`))
	})
	mux.HandleFunc("/mcp/rpc", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.rpcCalls++
		ok := r.Header.Get("Authorization") == "Bearer "+f.validAccess && f.validAccess != ""
		f.mu.Unlock()
		if !ok && r.Header.Get("Authorization") != "Bearer static-oauth" {
			w.WriteHeader(401)
			return
		}
		if r.Header.Get("MCP-Protocol-Version") == "" || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.WriteHeader(400)
			return
		}
		var req struct {
			ID     int `json:"id"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		result := map[string]any{}
		switch req.Params.Name {
		case "me_get":
			result = map[string]any{"structuredContent": map[string]any{"userId": 7, "name": "Ann", "email": "a@b.c"}}
		case "projects_list":
			result = map[string]any{"structuredContent": map[string]any{"items": []map[string]any{{"projectSlug": "demo", "name": "Demo", "role": "maintainer"}}}}
		default:
			result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "tool not found"}},
				"structuredContent": map[string]any{"code": "NOT_FOUND", "message": "tool not found", "details": map[string]any{}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// browser plays the user: it requests the authorization URL, follows the
// redirect to the CLI's loopback listener, and so completes the flow.
func browser(t *testing.T) func(string) error {
	return func(u string) error {
		hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := hc.Get(u)
		if err != nil {
			return err
		}
		resp.Body.Close()
		loc := resp.Header.Get("Location")
		if loc == "" {
			return fmt.Errorf("authorize returned HTTP %d", resp.StatusCode)
		}
		cb, err := http.Get(loc)
		if err != nil {
			return err
		}
		cb.Body.Close()
		return nil
	}
}

func oauthHarness(t *testing.T, f *fakeOAuth) *harness {
	h := newHarness(t, f.srv.URL, map[string]string{envToken: ""})
	h.app.openURL = browser(t)
	return h
}

func TestOAuthLoginFlowAndRPCTransport(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 0 {
		t.Fatalf("login exit %d: %s", code, h.err)
	}
	cfg, _ := loadConfig(h.cfgPath)
	if cfg.OAuth == nil || cfg.OAuth.AccessToken != "at-1" || cfg.OAuth.RefreshToken != "rt-1" || cfg.OAuth.ClientID != "cid-1" {
		t.Fatalf("saved oauth = %+v", cfg.OAuth)
	}
	if cfg.Token != "" {
		t.Errorf("static token must not be saved alongside OAuth")
	}
	h.out.Reset()
	if code := h.run("project", "list"); code != 0 || !strings.Contains(h.out.String(), "Demo") {
		t.Fatalf("project list code=%d out=%q err=%q", code, h.out, h.err)
	}
	if f.legacyCalls != 0 {
		t.Errorf("OAuth tokens must never be sent to legacy /mcp (%d calls)", f.legacyCalls)
	}
	h.out.Reset()
	if code := h.run("auth", "status"); code != 0 || !strings.Contains(h.out.String(), "OAuth session") {
		t.Errorf("status code=%d out=%q", code, h.out)
	}
}

// A stray or forged request to the loopback callback (no state, wrong state,
// or an escape-sequence "error") must neither abort nor complete the login.
func TestOAuthCallbackIgnoresStrayRequests(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	real := browser(t)
	h.app.openURL = func(u string) error {
		parsed, _ := url.Parse(u)
		redirect := parsed.Query().Get("redirect_uri")
		for _, stray := range []string{"", "?code=evil&state=forged", "?error=%1b%5b31mowned&state=forged"} {
			resp, err := http.Get(redirect + stray)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("stray callback %q: HTTP %d, want 400", stray, resp.StatusCode)
			}
		}
		return real(u)
	}
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 0 {
		t.Fatalf("login must survive stray callbacks, exit %d: %s", code, h.err)
	}
	if cfg, _ := loadConfig(h.cfgPath); cfg.OAuth == nil || cfg.OAuth.AccessToken != "at-1" {
		t.Errorf("saved oauth = %+v", cfg.OAuth)
	}
}

func TestOAuthDenialIsSanitized(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	h.app.openURL = func(u string) error {
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		resp, err := http.Get(q.Get("redirect_uri") + "?error=%1b%5b31maccess_denied&state=" + q.Get("state"))
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if strings.ContainsRune(h.err.String(), 0x1b) || !strings.Contains(h.err.String(), "denied") {
		t.Errorf("stderr must not carry raw control characters: %q", h.err)
	}
}

func TestOAuthRefusesCrossOriginEndpoints(t *testing.T) {
	f := newFakeOAuth(t)
	evil := httptest.NewServer(http.NotFoundHandler())
	defer evil.Close()
	// A discovery document that points the token endpoint at another host.
	f.tokenEndpointOverride = evil.URL + "/oauth/token"
	h := oauthHarness(t, f)
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 1 || !strings.Contains(h.err.String(), "different origin") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
	if f.registered {
		t.Error("nothing may be registered or sent once discovery fails the origin check")
	}
}

func TestOAuthRefusesPlainHTTPRemote(t *testing.T) {
	h := newHarness(t, "", map[string]string{envToken: ""})
	if code := h.run("auth", "login", "--oauth", "--server", "http://example.com"); code != 1 || !strings.Contains(h.err.String(), "plain http") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
}

func TestTokenEndpointRedirectIsNotFollowed(t *testing.T) {
	var leaked bool
	sink := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer sink.Close()
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer redir.Close()
	_, err := refreshOAuth(context.Background(), oauthConfig{TokenEndpoint: redir.URL, RefreshToken: "secret-rt", ClientID: "c", Resource: "r"})
	if err == nil || leaked {
		t.Errorf("err=%v leaked=%v: a redirecting token endpoint must fail without forwarding the refresh token", err, leaked)
	}
}

func TestSavedSessionIsNotSentToAnotherServer(t *testing.T) {
	f := newFakeOAuth(t)
	other := newFakeOAuth(t)
	h := oauthHarness(t, f)
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 0 {
		t.Fatalf("login: %s", h.err)
	}
	h2 := newHarness(t, other.srv.URL, map[string]string{envToken: "", envConfig: h.cfgPath})
	if code := h2.run("project", "list"); code != 1 || !strings.Contains(h2.err.String(), "not logged in") {
		t.Errorf("code=%d stderr=%q", code, h2.err)
	}
	if other.rpcCalls != 0 || other.legacyCalls != 0 {
		t.Errorf("the saved token reached a different server (%d rpc, %d legacy)", other.rpcCalls, other.legacyCalls)
	}
}

// Several CLI processes hitting an expired session must refresh exactly once:
// the refresh token rotates, so a second spend would fail.
func TestConcurrentRefreshSpendsRefreshTokenOnce(t *testing.T) {
	f := newFakeOAuth(t)
	f.validAccess, f.validRefr = "x", "rt-1"
	h := oauthHarness(t, f)
	saveConfig(h.cfgPath, fileConfig{Server: f.srv.URL, OAuth: &oauthConfig{
		Server: f.srv.URL, ClientID: "cid-1", TokenEndpoint: f.srv.URL + "/oauth/token", Resource: f.srv.URL + "/mcp/rpc",
		AccessToken: "old", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(-time.Minute)}})
	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hh := newHarness(t, f.srv.URL, map[string]string{envToken: "", envConfig: h.cfgPath})
			codes[i] = hh.run("project", "list")
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != 0 {
			t.Errorf("process %d exited %d", i, c)
		}
	}
	if f.refreshes != 1 {
		t.Errorf("refreshes = %d, want exactly 1", f.refreshes)
	}
}

// syncBuf is a goroutine-safe buffer: this test reads stderr while the CLI writes it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestPasteIgnoresWrongLines(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	errBuf := &syncBuf{}
	h.app.err = errBuf
	h.app.isTTY = true
	h.app.openURL = func(string) error { return nil }
	pr, pw := io.Pipe()
	h.app.in = pr
	go func() {
		io.WriteString(pw, "hello\n")
		io.WriteString(pw, "http://127.0.0.1:1/callback?code=c&state=forged\n")
		// the real redirect is produced by the fake server for the printed URL
		time.Sleep(200 * time.Millisecond)
		line := errBuf.String()
		start := strings.Index(line, f.srv.URL+"/oauth/authorize")
		end := strings.Index(line[start:], "\n")
		hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := hc.Get(line[start : start+end])
		if err == nil {
			resp.Body.Close()
			io.WriteString(pw, resp.Header.Get("Location")+"\n")
		}
	}()
	if code := h.run("auth", "login", "--oauth", "--no-browser", "--server", f.srv.URL); code != 0 {
		t.Fatalf("exit %d: %s", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "not the redirect URL") {
		t.Errorf("a wrong line should get a hint: %q", errBuf.String())
	}
}

func TestOAuthRefreshesAfter401AndPersistsRotation(t *testing.T) {
	f := newFakeOAuth(t)
	f.validAccess, f.validRefr = "fresh-but-unknown", "rt-1"
	h := oauthHarness(t, f)
	// Saved access token is not the one the server accepts, but not yet
	// expired locally: the first request 401s, then the CLI must refresh.
	saveConfig(h.cfgPath, fileConfig{Server: f.srv.URL, OAuth: &oauthConfig{
		Server: f.srv.URL, ClientID: "cid-1", TokenEndpoint: f.srv.URL + "/oauth/token", Resource: f.srv.URL + "/mcp/rpc",
		AccessToken: "stale", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(time.Hour)}})
	if code := h.run("project", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if f.refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", f.refreshes)
	}
	cfg, _ := loadConfig(h.cfgPath)
	if cfg.OAuth.AccessToken != "at-2" || cfg.OAuth.RefreshToken != "rt-2" {
		t.Errorf("rotated tokens were not persisted: %+v", cfg.OAuth)
	}
}

func TestOAuthRefreshesBeforeExpiry(t *testing.T) {
	f := newFakeOAuth(t)
	f.validAccess, f.validRefr = "x", "rt-1"
	h := oauthHarness(t, f)
	saveConfig(h.cfgPath, fileConfig{Server: f.srv.URL, OAuth: &oauthConfig{
		Server: f.srv.URL, ClientID: "cid-1", TokenEndpoint: f.srv.URL + "/oauth/token", Resource: f.srv.URL + "/mcp/rpc",
		AccessToken: "old", RefreshToken: "rt-1", ExpiresAt: time.Now().Add(-time.Minute)}})
	if code := h.run("project", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if f.rpcCalls != 1 {
		t.Errorf("rpc calls = %d; an expired token should be refreshed before the first request", f.rpcCalls)
	}
}

func TestOAuthRefreshFailureTellsUserToLogin(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	saveConfig(h.cfgPath, fileConfig{Server: f.srv.URL, OAuth: &oauthConfig{
		Server: f.srv.URL, ClientID: "cid-1", TokenEndpoint: f.srv.URL + "/oauth/token", Resource: f.srv.URL + "/mcp/rpc",
		AccessToken: "old", RefreshToken: "spent", ExpiresAt: time.Now().Add(-time.Minute)}})
	if code := h.run("project", "list"); code != 1 || !strings.Contains(h.err.String(), "auth login --oauth") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
}

func TestBareOAuthBearerFromEnvUsesRPC(t *testing.T) {
	f := newFakeOAuth(t)
	h := newHarness(t, f.srv.URL, map[string]string{envToken: "static-oauth"})
	if code := h.run("project", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if f.legacyCalls != 0 || f.rpcCalls != 1 {
		t.Errorf("legacy=%d rpc=%d; a non-sb_ token belongs on /mcp/rpc", f.legacyCalls, f.rpcCalls)
	}
}

func TestRPCToolErrorMapsToAPIError(t *testing.T) {
	f := newFakeOAuth(t)
	h := newHarness(t, f.srv.URL, map[string]string{envToken: "static-oauth"})
	if code := h.run("tool", "does_not_exist"); code != 1 || !strings.Contains(h.err.String(), "NOT_FOUND") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
}

func TestOAuthLogoutRevokesAndClears(t *testing.T) {
	f := newFakeOAuth(t)
	h := oauthHarness(t, f)
	if code := h.run("auth", "login", "--oauth", "--server", f.srv.URL); code != 0 {
		t.Fatalf("login: %s", h.err)
	}
	if code := h.run("auth", "logout"); code != 0 {
		t.Fatalf("logout: %s", h.err)
	}
	if len(f.revoked) != 2 {
		t.Errorf("revoked = %v, want refresh and access token", f.revoked)
	}
	if cfg, _ := loadConfig(h.cfgPath); cfg.OAuth != nil || cfg.Token != "" {
		t.Errorf("credentials not cleared: %+v", cfg)
	}
}

func TestParseRedirect(t *testing.T) {
	r, err := parseRedirect("http://127.0.0.1:5555/callback?code=abc&state=xyz\n")
	if err != nil || r.code != "abc" || r.state != "xyz" {
		t.Errorf("full URL: %+v %v", r, err)
	}
	r, err = parseRedirect("code=abc&state=xyz")
	if err != nil || r.code != "abc" || r.state != "xyz" {
		t.Errorf("bare query: %+v %v", r, err)
	}
	if _, err := parseRedirect("hello"); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestOAuthFlagsValidate(t *testing.T) {
	h := newHarness(t, "http://127.0.0.1:1", nil)
	if code := h.run("auth", "login", "--oauth", "--with-token"); code != 2 {
		t.Errorf("--oauth with --with-token: exit %d, want 2", code)
	}
	if code := h.run("auth", "login", "--no-browser"); code != 2 {
		t.Errorf("--no-browser alone: exit %d, want 2", code)
	}
}
