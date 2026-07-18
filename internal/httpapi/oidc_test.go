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
	"strconv"
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
	omitAuthTime    bool
	authTimeValue   any

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
	_ = r.ParseForm()
	state := r.Form.Get("state")
	nonce := r.Form.Get("nonce")
	redir := r.Form.Get("redirect_uri")

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
	if !f.omitAuthTime {
		if f.authTimeValue != nil {
			claims["auth_time"] = f.authTimeValue
		} else {
			claims["auth_time"] = now.Unix()
		}
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

	st := store.New(sqlDB, &store.StoreOptions{ConfiguredOIDCIssuer: idp.issuer, EncryptionKey: testEncryptionKey})

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
		EncryptionKey:  testEncryptionKey,
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

func TestOIDCLocalAuthDisabledMakesLocalLoginAndResetUnavailable(t *testing.T) {
	service := oidc.New(oidc.Config{
		IssuerCanonical:   "https://idp.example",
		ClientID:          "client",
		ClientSecret:      "secret",
		RedirectURL:       "https://scrumboy.example/api/auth/oidc/callback",
		LocalAuthDisabled: true,
	})
	ts, database, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		OIDCService:    service,
		SMTPHost:       "smtp.example.com",
		SMTPPort:       587,
		SMTPFrom:       "no-reply@example.com",
		PublicBaseURL:  "https://scrumboy.example",
	})
	defer cleanup()
	st := store.New(database, nil)
	owner, err := st.BootstrapUser(context.Background(), "owner@example.com", "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	target, err := st.CreateUser(context.Background(), "target@example.com", "Password123!", "Target")
	if err != nil {
		t.Fatal(err)
	}
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)

	var status map[string]any
	doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &status)
	if status["localAuthEnabled"] != false || status["selfServicePasswordResetEnabled"] != false {
		t.Fatalf("disabled-local status exposed local recovery: %#v", status)
	}
	for _, request := range []struct {
		path string
		body map[string]any
	}{
		{path: "/api/auth/login", body: map[string]any{"email": owner.Email, "password": "Password123!"}},
		{path: "/api/auth/request-password-reset", body: map[string]any{"email": owner.Email}},
		{path: "/api/auth/reset-password", body: map[string]any{"token": "unused", "new_password": "Replacement123!"}},
		{path: "/api/admin/users/" + strconv.FormatInt(target.ID, 10) + "/password-reset", body: map[string]any{}},
	} {
		resp, _ := doJSON(t, client, http.MethodPost, ts.URL+request.path, request.body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("disabled local endpoint %s status=%d", request.path, resp.StatusCode)
		}
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
		t.Fatalf("expected OIDC redirect, got %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse OIDC redirect: %v", err)
	}
	if redirectURL.Scheme+"://"+redirectURL.Host+redirectURL.Path != idp.issuer+"/authorize" {
		t.Fatalf("OIDC redirect = %q, want provider authorization endpoint", location)
	}
	if redirectURL.Query().Get("code_challenge_method") != "S256" || redirectURL.Query().Get("nonce") == "" {
		t.Errorf("OIDC redirect missing PKCE or nonce: %s", location)
	}
}

func followOIDCLogin(t *testing.T, client *http.Client, loginURL string) *http.Response {
	t.Helper()
	resp, err := client.Get(loginURL)
	if err != nil {
		t.Fatalf("follow OIDC login redirect: %v", err)
	}
	return resp
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

	resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/dashboard")
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

	resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
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

	st := store.New(sqlDB, &store.StoreOptions{ConfiguredOIDCIssuer: idp.issuer, EncryptionKey: testEncryptionKey})

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
		EncryptionKey:  testEncryptionKey,
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

func authenticatedOIDCTestClient(t *testing.T, ts *httptest.Server, st *store.Store, userID int64) *http.Client {
	t.Helper()
	token, expires, err := st.CreateSession(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expires}})
	return &http.Client{Jar: jar}
}

func TestOIDCMatchingEmailRequiresExplicitLink(t *testing.T) {
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

	resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
	resp.Body.Close()
	finalURL := resp.Request.URL.String()
	if !strings.Contains(finalURL, "oidc_error=link_required") {
		t.Fatalf("expected explicit-link guidance, got %s", finalURL)
	}
	var status map[string]any
	doJSON(t, client, "GET", ts.URL+"/api/auth/status", nil, &status)
	if status["user"] != nil {
		t.Fatalf("email collision must not authenticate or create a session: %v", status)
	}
	if _, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("matching email was implicitly linked: %v", err)
	}
}

func submitOIDCAuthorizationRequest(t *testing.T, client *http.Client, response map[string]any) *http.Response {
	t.Helper()
	endpoint, ok := response["authorizationEndpoint"].(string)
	if !ok || endpoint == "" {
		t.Fatalf("missing authorization endpoint: %#v", response)
	}
	rawParams, ok := response["authorizationParameters"].(map[string]any)
	if !ok {
		t.Fatalf("missing authorization parameters: %#v", response)
	}
	params := url.Values{}
	for key, value := range rawParams {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("non-string authorization parameter %s", key)
		}
		params.Set(key, text)
	}
	if params.Get("max_age") != "0" || params.Get("prompt") != "" {
		t.Fatalf("sensitive flow freshness parameters=%v", params)
	}
	if strings.Contains(endpoint, "nonce") || strings.Contains(endpoint, params.Get("nonce")) {
		t.Fatalf("nonce leaked into authorization endpoint URL")
	}
	resp, err := client.PostForm(endpoint, params)
	if err != nil {
		t.Fatalf("submit authorization request: %v", err)
	}
	return resp
}

func TestOIDCFirstPasswordStepUpProducesDualAuthentication(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, cleanup := newTestOIDCServer(t, idp)
	defer cleanup()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
	resp.Body.Close()
	var initial map[string]any
	doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &initial)
	initialUser := initial["user"].(map[string]any)
	if initialUser["hasLocalPassword"] != false || initialUser["oidcLinked"] != true {
		t.Fatalf("expected current-provider SSO-only account: %#v", initialUser)
	}

	// An ordinary authenticated session has no authority to set a first password.
	unauthorized, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{"newPassword": "NewPassword123!"}, nil)
	if unauthorized.StatusCode == http.StatusNoContent {
		t.Fatal("ordinary session set a first password")
	}

	var start map[string]any
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password/start", map[string]any{}, &start)
	stepUp := submitOIDCAuthorizationRequest(t, client, start)
	stepUp.Body.Close()
	if !strings.Contains(stepUp.Request.URL.String(), "auth_method=set_password") {
		t.Fatalf("step-up callback did not return to password flow: %s", stepUp.Request.URL)
	}
	var grantStatus map[string]any
	doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/oidc/set-password/status", nil, &grantStatus)
	if grantStatus["authorized"] != true {
		t.Fatalf("first-password grant not authorized: %#v", grantStatus)
	}
	weak, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{"newPassword": "weak"}, nil)
	if weak.StatusCode == http.StatusNoContent {
		t.Fatal("first-password flow bypassed the Scrumboy password policy")
	}
	complete, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{"newPassword": "NewPassword123!"}, nil)
	if complete.StatusCode != http.StatusNoContent {
		t.Fatalf("first-password completion status=%d", complete.StatusCode)
	}

	var final map[string]any
	doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &final)
	finalUser := final["user"].(map[string]any)
	if finalUser["hasLocalPassword"] != true || finalUser["oidcLinked"] != true {
		t.Fatalf("expected dual authentication: %#v", finalUser)
	}
	localJar, _ := cookiejar.New(nil)
	localClient := &http.Client{Jar: localJar}
	var localLogin map[string]any
	loginResp, _ := doJSON(t, localClient, http.MethodPost, ts.URL+"/api/auth/login", map[string]any{"email": idp.email, "password": "NewPassword123!"}, &localLogin)
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("local login after first password status=%d", loginResp.StatusCode)
	}
	replay, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{"newPassword": "AnotherPassword123!"}, nil)
	if replay.StatusCode == http.StatusNoContent {
		t.Fatal("first-password grant replay succeeded")
	}
}

func TestOIDCSensitiveFlowRequiresAuthTime(t *testing.T) {
	cases := []struct {
		name  string
		omit  bool
		value any
	}{
		{name: "missing", omit: true},
		{name: "malformed", value: "not-a-time"},
		{name: "stale", value: time.Now().Add(-10 * time.Minute).Unix()},
		{name: "future", value: time.Now().Add(10 * time.Minute).Unix()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			defer idp.close()
			ts, cleanup := newTestOIDCServer(t, idp)
			defer cleanup()
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar}
			resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
			resp.Body.Close()
			idp.omitAuthTime, idp.authTimeValue = tc.omit, tc.value
			var start map[string]any
			doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password/start", map[string]any{}, &start)
			stepUp := submitOIDCAuthorizationRequest(t, client, start)
			stepUp.Body.Close()
			if !strings.Contains(stepUp.Request.URL.String(), "oidc_error=auth_time") {
				t.Fatalf("invalid auth_time did not fail closed: %s", stepUp.Request.URL)
			}
		})
	}
}

func TestOIDCFirstPasswordRejectsDifferentIdentityAndMissingSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*fakeIdP, *http.Client, *store.Store, int64)
	}{
		{name: "different subject", mutate: func(idp *fakeIdP, _ *http.Client, _ *store.Store, _ int64) { idp.subject = "different-subject" }},
		{name: "missing session", mutate: func(_ *fakeIdP, _ *http.Client, st *store.Store, userID int64) {
			_ = st.DeleteSessionsByUserID(context.Background(), userID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			defer idp.close()
			ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
			defer cleanup()
			jar, _ := cookiejar.New(nil)
			client := &http.Client{Jar: jar}
			resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
			resp.Body.Close()
			u, err := st.GetUserByOIDCIdentity(context.Background(), idp.issuer, idp.subject)
			if err != nil {
				t.Fatal(err)
			}
			var start map[string]any
			doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password/start", map[string]any{}, &start)
			tc.mutate(idp, client, st, u.ID)
			stepUp := submitOIDCAuthorizationRequest(t, client, start)
			stepUp.Body.Close()
			if !strings.Contains(stepUp.Request.URL.String(), "oidc_error=") || strings.Contains(stepUp.Request.URL.String(), "auth_method=set_password") {
				t.Fatalf("sensitive callback accepted changed identity/session: %s", stepUp.Request.URL)
			}
		})
	}
}

func TestOIDCExplicitLinkAndCanonicalEmailOwnership(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Canonical Name")
	if err != nil {
		t.Fatal(err)
	}
	token, expires, err := st.CreateSession(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	base, _ := url.Parse(ts.URL)
	jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expires}})
	var start map[string]any
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/link/start", map[string]any{"currentPassword": "Password123!"}, &start)
	linked := submitOIDCAuthorizationRequest(t, client, start)
	linked.Body.Close()
	u, err := st.GetUser(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !u.HasLocalPassword || !u.OIDCLinked {
		t.Fatalf("explicit link did not produce dual auth: %+v", u)
	}

	// A later IdP email change, even one colliding with another canonical user,
	// must continue identifying by issuer/subject without transferring ownership.
	other, err := st.CreateUser(ctx, "other@example.com", "Password123!", "Other")
	if err != nil {
		t.Fatal(err)
	}
	idp.email = other.Email
	idp.name = "Changed IdP Name"
	newJar, _ := cookiejar.New(nil)
	newClient := &http.Client{Jar: newJar}
	login := followOIDCLogin(t, newClient, ts.URL+"/api/auth/oidc/login?return_to=/")
	login.Body.Close()
	original, _ := st.GetUser(ctx, owner.ID)
	collision, _ := st.GetUser(ctx, other.ID)
	if original.Email != "alice@example.com" || original.Name != "Canonical Name" || collision.Email != "other@example.com" {
		t.Fatalf("IdP profile change transferred canonical ownership: original=%+v collision=%+v", original, collision)
	}
	if _, err := st.AuthenticateUser(ctx, "alice@example.com", "Password123!"); err != nil {
		t.Fatalf("canonical local login was not retained: %v", err)
	}
}

func TestOIDCLinkDiscoveryFailureDoesNotConsumeRecoveryCode(t *testing.T) {
	idp := newFakeIdP(t)
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTwoFactor(ctx, owner.ID, "test-secret"); err != nil {
		t.Fatal(err)
	}
	const recoveryCode = "ABCD-EFGH"
	if err := st.AddRecoveryCodes(ctx, owner.ID, []string{recoveryCode}); err != nil {
		t.Fatal(err)
	}
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	idp.close()
	resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/link/start", map[string]any{
		"currentPassword": "Password123!",
		"twoFactorCode":   recoveryCode,
	}, nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("link start with unavailable provider status=%d, want 503", resp.StatusCode)
	}
	consumed, err := st.ConsumeRecoveryCode(ctx, owner.ID, recoveryCode)
	if err != nil {
		t.Fatal(err)
	}
	if !consumed {
		t.Fatal("provider discovery failure consumed the recovery code")
	}
}

func TestOIDCFirstPasswordValidationFailureDoesNotConsumeRecoveryCode(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp := followOIDCLogin(t, client, ts.URL+"/api/auth/oidc/login?return_to=/")
	resp.Body.Close()
	u, err := st.GetUserByOIDCIdentity(context.Background(), idp.issuer, idp.subject)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserTwoFactor(context.Background(), u.ID, "test-secret"); err != nil {
		t.Fatal(err)
	}
	const recoveryCode = "ABCD-EFGH"
	if err := st.AddRecoveryCodes(context.Background(), u.ID, []string{recoveryCode}); err != nil {
		t.Fatal(err)
	}
	var start map[string]any
	doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password/start", map[string]any{}, &start)
	stepUp := submitOIDCAuthorizationRequest(t, client, start)
	stepUp.Body.Close()
	weak, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{
		"newPassword":   "weak",
		"twoFactorCode": recoveryCode,
	}, nil)
	if weak.StatusCode == http.StatusNoContent {
		t.Fatal("weak first password unexpectedly succeeded")
	}
	complete, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/set-password", map[string]any{
		"newPassword":   "NewPassword123!",
		"twoFactorCode": recoveryCode,
	}, nil)
	if complete.StatusCode != http.StatusNoContent {
		t.Fatalf("recovery code was not reusable after validation failure: status=%d", complete.StatusCode)
	}
}

func TestOIDCExplicitLinkRejectsMissingLocalProofAndIdentityInvariants(t *testing.T) {
	t.Run("current password required", func(t *testing.T) {
		idp := newFakeIdP(t)
		defer idp.close()
		ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
		defer cleanup()
		owner, err := st.BootstrapUser(context.Background(), idp.email, "Password123!", "Owner")
		if err != nil {
			t.Fatal(err)
		}
		client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
		resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/link/start", map[string]any{"currentPassword": "wrong-password"}, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("link start without current-password proof status=%d", resp.StatusCode)
		}
	})

	for _, tc := range []struct {
		name      string
		ownerMail string
		verified  bool
	}{
		{name: "unverified email", ownerMail: "alice@example.com", verified: false},
		{name: "mismatched email", ownerMail: "owner@example.com", verified: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIdP(t)
			defer idp.close()
			idp.emailVer = tc.verified
			ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
			defer cleanup()
			owner, err := st.BootstrapUser(context.Background(), tc.ownerMail, "Password123!", "Owner")
			if err != nil {
				t.Fatal(err)
			}
			client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
			var start map[string]any
			resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/link/start", map[string]any{"currentPassword": "Password123!"}, &start)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("link start status=%d", resp.StatusCode)
			}
			callback := submitOIDCAuthorizationRequest(t, client, start)
			callback.Body.Close()
			if !strings.Contains(callback.Request.URL.String(), "oidc_error=") {
				t.Fatalf("invalid identity invariant linked successfully: %s", callback.Request.URL)
			}
			if _, err := st.GetUserByOIDCIdentity(context.Background(), idp.issuer, idp.subject); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("rejected identity became linked: %v", err)
			}
		})
	}

	t.Run("identity already belongs to another user", func(t *testing.T) {
		idp := newFakeIdP(t)
		defer idp.close()
		idp.email = "other@example.com"
		ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
		defer cleanup()
		ctx := context.Background()
		owner, err := st.BootstrapUser(ctx, "owner@example.com", "Password123!", "Owner")
		if err != nil {
			t.Fatal(err)
		}
		other, err := st.CreateUser(ctx, idp.email, "Password123!", "Other")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.LinkOIDCIdentityExplicit(ctx, other.ID, idp.issuer, idp.subject, other.Email); err != nil {
			t.Fatal(err)
		}
		idp.email = owner.Email
		client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
		var start map[string]any
		doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/oidc/link/start", map[string]any{"currentPassword": "Password123!"}, &start)
		callback := submitOIDCAuthorizationRequest(t, client, start)
		callback.Body.Close()
		if !strings.Contains(callback.Request.URL.String(), "oidc_error=link_rejected") {
			t.Fatalf("identity ownership conflict was not rejected: %s", callback.Request.URL)
		}
		linked, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject)
		if err != nil || linked.ID != other.ID {
			t.Fatalf("identity ownership changed: linked=%+v err=%v", linked, err)
		}
	})
}
