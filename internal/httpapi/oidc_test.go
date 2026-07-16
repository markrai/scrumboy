package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIdP simulates an OIDC provider for integration tests.
type fakeIdP struct {
	server          *httptest.Server
	key             *rsa.PrivateKey
	keyID           string
	issuer          string
	discoveryIssuer string
	idTokenIssuer   string
	clientID        string
	subject         string
	email           string
	emailVer        bool
	name            string

	mu     sync.Mutex
	nonces map[string]string // auth code → nonce
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &fakeIdP{
		key:      key,
		keyID:    "test-kid-1",
		clientID: "test-client",
		subject:  "user-sub-123",
		email:    "alice@example.com",
		emailVer: true,
		name:     "Alice",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", idp.handleDiscovery)
	mux.HandleFunc("/jwks", idp.handleJWKS)
	mux.HandleFunc("/authorize", idp.handleAuthorize)
	mux.HandleFunc("/token", idp.handleToken)
	idp.server = httptest.NewServer(mux)
	idp.issuer = idp.server.URL
	return idp
}

func (f *fakeIdP) close() { f.server.Close() }

func (f *fakeIdP) discoveryIssuerValue() string {
	if f.discoveryIssuer != "" {
		return f.discoveryIssuer
	}
	return f.issuer
}

func (f *fakeIdP) idTokenIssuerValue() string {
	if f.idTokenIssuer != "" {
		return f.idTokenIssuer
	}
	return f.issuer
}

func (f *fakeIdP) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	discoveryIssuer := f.discoveryIssuerValue()
	disc := map[string]any{
		"issuer":                                discoveryIssuer,
		"authorization_endpoint":                f.issuer + "/authorize",
		"token_endpoint":                        f.issuer + "/token",
		"jwks_uri":                              f.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(disc)
}

func (f *fakeIdP) handleJWKS(w http.ResponseWriter, r *http.Request) {
	jwk := jose.JSONWebKey{
		Key:       &f.key.PublicKey,
		KeyID:     f.keyID,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(set)
}

func (f *fakeIdP) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	nonce := r.URL.Query().Get("nonce")
	redir := r.URL.Query().Get("redirect_uri")

	code := "code-" + state[:8]

	f.mu.Lock()
	if f.nonces == nil {
		f.nonces = make(map[string]string)
	}
	f.nonces[code] = nonce
	f.mu.Unlock()

	u, _ := url.Parse(redir)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (f *fakeIdP) handleToken(w http.ResponseWriter, r *http.Request) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", f.keyID),
	)
	if err != nil {
		http.Error(w, "signer error", 500)
		return
	}

	code := r.FormValue("code")
	f.mu.Lock()
	nonce := f.nonces[code]
	delete(f.nonces, code)
	f.mu.Unlock()

	now := time.Now()
	claims := map[string]any{
		"iss":            f.idTokenIssuerValue(),
		"sub":            f.subject,
		"aud":            f.clientID,
		"exp":            now.Add(10 * time.Minute).Unix(),
		"iat":            now.Unix(),
		"nonce":          nonce,
		"email":          f.email,
		"email_verified": f.emailVer,
		"name":           f.name,
	}

	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		http.Error(w, "jwt error", 500)
		return
	}

	resp := map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"id_token":     raw,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (f *fakeIdP) signIDToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", f.keyID),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return raw
}

func newTestOIDCServer(t *testing.T, idp *fakeIdP) (*httptest.Server, func()) {
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

	// Create OIDC service with the fake IdP as issuer.
	// RedirectURL is set to a placeholder; we override per test as needed.
	oidcSvc := oidc.New(oidc.Config{
		IssuerCanonical: idp.issuer,
		ClientID:        idp.clientID,
		ClientSecret:    "test-secret",
		RedirectURL:     "http://placeholder/api/auth/oidc/callback",
	})

	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		OIDCService:    oidcSvc,
	})
	ts := httptest.NewServer(srv)

	// Update redirect URL to point to the actual test server.
	oidcSvc2 := oidc.New(oidc.Config{
		IssuerCanonical: idp.issuer,
		ClientID:        idp.clientID,
		ClientSecret:    "test-secret",
		RedirectURL:     ts.URL + "/api/auth/oidc/callback",
	})
	srv.oidcService = oidcSvc2

	return ts, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func TestOIDCStatusFlags(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()

	ts, cleanup := newTestOIDCServer(t, idp)
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	var status map[string]any
	doJSON(t, client, "GET", ts.URL+"/api/auth/status", nil, &status)

	if status["oidcEnabled"] != true {
		t.Errorf("expected oidcEnabled=true, got %v", status["oidcEnabled"])
	}
	if status["localAuthEnabled"] != true {
		t.Errorf("expected localAuthEnabled=true, got %v", status["localAuthEnabled"])
	}
}

func TestOIDCLoginRedirect(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()

	ts, cleanup := newTestOIDCServer(t, idp)
	defer cleanup()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(ts.URL + "/api/auth/oidc/login?return_to=/dashboard")
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, idp.issuer+"/authorize") {
		t.Errorf("expected redirect to IdP, got %q", loc)
	}
	if !strings.Contains(loc, "code_challenge_method=S256") {
		t.Errorf("expected PKCE S256 in authorize URL, got %q", loc)
	}
}

func TestOIDCTrailingSlashIssuerLoginStoresCanonicalIdentity(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.discoveryIssuer = idp.issuer + "/"
	idp.idTokenIssuer = idp.issuer + "/"

	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Get(ts.URL + "/api/auth/oidc/login?return_to=/dashboard")
	if err != nil {
		t.Fatalf("oidc login: %v", err)
	}
	resp.Body.Close()

	if finalURL := resp.Request.URL.String(); strings.Contains(finalURL, "oidc_error") {
		t.Fatalf("expected successful login, got redirect to %s", finalURL)
	}

	ctx := context.Background()
	u, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject)
	if err != nil {
		t.Fatalf("expected canonical OIDC identity lookup to succeed: %v", err)
	}
	if u.Email != idp.email {
		t.Fatalf("canonical identity email = %q, want %q", u.Email, idp.email)
	}

	_, err = st.GetUserByOIDCIdentity(ctx, idp.issuer+"/", idp.subject)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected no slash-form identity row, got err=%v", err)
	}
}

func TestOIDCTrailingSlashDiscoveryRejectsNonSlashIDTokenIssuer(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	idp.discoveryIssuer = idp.issuer + "/"
	idp.idTokenIssuer = idp.issuer

	ts, cleanup := newTestOIDCServer(t, idp)
	defer cleanup()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Get(ts.URL + "/api/auth/oidc/login?return_to=/")
	if err != nil {
		t.Fatalf("oidc login: %v", err)
	}
	resp.Body.Close()

	finalURL := resp.Request.URL.String()
	if !strings.Contains(finalURL, "oidc_error=token") {
		t.Fatalf("expected token error for mismatched ID token issuer, got %s", finalURL)
	}
}

func TestOIDCAnonymousMode404(t *testing.T) {
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
	oidcSvc := oidc.New(oidc.Config{
		IssuerCanonical: "https://example.com",
		ClientID:        "c",
		ClientSecret:    "s",
		RedirectURL:     "http://example.com/cb",
	})
	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		OIDCService:    oidcSvc,
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/auth/oidc/login")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 in anonymous mode, got %d", resp.StatusCode)
	}
}

func TestOIDCCallbackInvalidState(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()

	ts, cleanup := newTestOIDCServer(t, idp)
	defer cleanup()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	clientRedirect := "https://client.example.com/callback"
	resp, err := client.Get(ts.URL + "/api/auth/oidc/callback?code=abc&state=bogus&redirect_uri=" + url.QueryEscape(clientRedirect))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	location, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse callback Location: %v", err)
	}
	if location.IsAbs() || location.Host != "" || location.Path != "/" || location.Query().Get("oidc_error") != "state_invalid" {
		t.Errorf("expected existing internal state-error destination, got Location=%q", loc)
	}
	if strings.HasPrefix(loc, clientRedirect) || location.Query().Get("code") != "" || strings.Contains(loc, "code=") {
		t.Errorf("invalid callback must not redirect to the OAuth client or expose a code, got Location=%q", loc)
	}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "scrumboy_session" {
			t.Errorf("invalid callback must not establish an authenticated session: %+v", cookie)
		}
	}
}

// newTestOIDCServerWithStore is like newTestOIDCServer but also returns the
// store so tests can pre-create local users before exercising the OIDC flow.
func newTestOIDCServerWithStore(t *testing.T, idp *fakeIdP) (*httptest.Server, *store.Store, func()) {
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

	oidcSvc := oidc.New(oidc.Config{
		IssuerCanonical: idp.issuer,
		ClientID:        idp.clientID,
		ClientSecret:    "test-secret",
		RedirectURL:     "http://placeholder/api/auth/oidc/callback",
	})

	srv := NewServer(st, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		OIDCService:    oidcSvc,
	})
	ts := httptest.NewServer(srv)

	oidcSvc2 := oidc.New(oidc.Config{
		IssuerCanonical: idp.issuer,
		ClientID:        idp.clientID,
		ClientSecret:    "test-secret",
		RedirectURL:     ts.URL + "/api/auth/oidc/callback",
	})
	srv.oidcService = oidcSvc2

	return ts, st, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

// TestOIDCAutoLinkExistingUser verifies that when a pre-existing local-password
// user tries OIDC login with a matching verified email, the identity is
// auto-linked and login succeeds (instead of failing with a conflict error).
func TestOIDCAutoLinkExistingUser(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()

	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()

	ctx := context.Background()

	// Bootstrap the owner with the same email the IdP will provide.
	_, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Alice Local")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Follow the full OIDC flow: login redirect → IdP authorize → callback.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Get(ts.URL + "/api/auth/oidc/login?return_to=/")
	if err != nil {
		t.Fatalf("oidc login: %v", err)
	}
	resp.Body.Close()

	// After the full redirect chain the user should land on "/" with a session
	// cookie, NOT on "/?oidc_error=token".
	finalURL := resp.Request.URL.String()
	if strings.Contains(finalURL, "oidc_error") {
		t.Fatalf("expected successful login, got redirect to %s", finalURL)
	}

	// Verify the session is live: GET /api/auth/status should return the user.
	var status map[string]any
	doJSON(t, client, "GET", ts.URL+"/api/auth/status", nil, &status)
	user, ok := status["user"].(map[string]any)
	if !ok || user == nil {
		t.Fatalf("expected authenticated user in status, got %v", status)
	}
	if user["email"] != idp.email {
		t.Errorf("expected email=%s, got %v", idp.email, user["email"])
	}

	// A second OIDC login with the same identity should succeed (identity
	// already linked, no conflict).
	jar2, _ := cookiejar.New(nil)
	client2 := &http.Client{Jar: jar2}
	resp2, err := client2.Get(ts.URL + "/api/auth/oidc/login?return_to=/")
	if err != nil {
		t.Fatalf("second oidc login: %v", err)
	}
	resp2.Body.Close()
	if strings.Contains(resp2.Request.URL.String(), "oidc_error") {
		t.Fatalf("second login failed: %s", resp2.Request.URL.String())
	}
}
