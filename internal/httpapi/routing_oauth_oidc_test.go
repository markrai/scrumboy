package httpapi

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"scrumboy/internal/oidc"
)

func newTestOAuthServerWithOIDC(t *testing.T, localAuthDisabled, withExistingUser bool) (*Server, *httptest.Server, *fakeIdP, *http.Client) {
	t.Helper()

	idp := newFakeIdP(t)
	t.Cleanup(idp.close)
	srv := newTestOAuthServer(t, Options{})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	var sessionClient *http.Client
	if withExistingUser {
		sessionClient = newCookieClient(t)
		bootstrapUserClient(t, sessionClient, ts.URL, idp.name, idp.email, "password123")
	}

	srv.oidcService = oidc.New(oidc.Config{
		IssuerCanonical:   idp.issuer,
		ClientID:          idp.clientID,
		ClientSecret:      "test-secret",
		RedirectURL:       ts.URL + "/api/auth/oidc/callback",
		LocalAuthDisabled: localAuthDisabled,
	})
	return srv, ts, idp, sessionClient
}

func oauthPageResponse(t *testing.T, client *http.Client, target string) (int, string, string) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

func oauthOIDCLoginHref(t *testing.T, body string) string {
	t.Helper()
	const marker = `id="oauth-oidc-login" href="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("OAuth login page has no OIDC continuation link: %s", body)
	}
	start += len(marker)
	end := strings.IndexByte(body[start:], '"')
	if end < 0 {
		t.Fatalf("OAuth OIDC continuation href is unterminated: %s", body)
	}
	return html.UnescapeString(body[start : start+end])
}

func TestOAuthLoginPageAuthModes(t *testing.T) {
	noRedirectClient := func() *http.Client {
		return &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}

	t.Run("local auth only", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
		defer cleanup()
		bootstrapUserClient(t, newCookieClient(t), ts.URL, "Owner", "local-only@example.com", "password123")

		redirectURI := "https://client.example.com/callback"
		clientID := registerOAuthClient(t, ts.URL, redirectURI)
		_, challenge := pkcePair(t)
		status, location, body := oauthPageResponse(t, noRedirectClient(), authorizeURL(ts.URL, clientID, redirectURI, challenge, "local"))

		if status != http.StatusOK || location != "" {
			t.Fatalf("local-only login page status=%d Location=%q", status, location)
		}
		if !strings.Contains(body, `id="email"`) || !strings.Contains(body, `id="password"`) || !strings.Contains(body, "/api/auth/login") {
			t.Fatalf("local-only login page must retain the password form: %s", body)
		}
		if strings.Contains(body, "Continue with SSO") || strings.Contains(body, `id="oauth-oidc-login"`) {
			t.Fatalf("local-only login page must not show an SSO action: %s", body)
		}
	})

	t.Run("hybrid", func(t *testing.T) {
		_, ts, _, _ := newTestOAuthServerWithOIDC(t, false, true)
		redirectURI := "https://client.example.com/callback"
		clientID := registerOAuthClient(t, ts.URL, redirectURI)
		_, challenge := pkcePair(t)
		authorize := authorizeURL(ts.URL, clientID, redirectURI, challenge, "hybrid")
		status, location, body := oauthPageResponse(t, noRedirectClient(), authorize)

		if status != http.StatusOK || location != "" {
			t.Fatalf("hybrid login page must render without an automatic redirect: status=%d Location=%q", status, location)
		}
		if !strings.Contains(body, "<h1>Sign in to continue</h1>") || !strings.Contains(body, `id="email"`) || !strings.Contains(body, `id="password"`) {
			t.Fatalf("hybrid login page must retain password login under the resolved heading: %s", body)
		}
		if !strings.Contains(body, "<span>or</span>") || !strings.Contains(body, "Continue with SSO") {
			t.Fatalf("hybrid login page must show SSO as a clear alternative: %s", body)
		}

		href := oauthOIDCLoginHref(t, body)
		loginURL, err := url.Parse(href)
		if err != nil {
			t.Fatalf("parse hybrid OIDC href: %v", err)
		}
		wantAuthorize, _ := url.Parse(authorize)
		if loginURL.Path != "/api/auth/oidc/login" || loginURL.Query().Get("return_to") != wantAuthorize.RequestURI() {
			t.Fatalf("hybrid OIDC href=%q, want complete authorize RequestURI %q", href, wantAuthorize.RequestURI())
		}
	})

	t.Run("OIDC only", func(t *testing.T) {
		_, ts, _, _ := newTestOAuthServerWithOIDC(t, true, true)
		redirectURI := "https://client.example.com/callback"
		clientID := registerOAuthClient(t, ts.URL, redirectURI)
		_, challenge := pkcePair(t)
		status, location, body := oauthPageResponse(t, noRedirectClient(), authorizeURL(ts.URL, clientID, redirectURI, challenge, "oidc-only"))

		if status != http.StatusOK || location != "" {
			t.Fatalf("OIDC-only login page must render without an automatic redirect: status=%d Location=%q", status, location)
		}
		if !strings.Contains(body, "<h1>Sign in to continue</h1>") || !strings.Contains(body, "Continue with SSO") {
			t.Fatalf("OIDC-only login page must make SSO the primary action: %s", body)
		}
		if !strings.Contains(body, "Sign in through your configured identity provider to review this access request.") {
			t.Fatalf("OIDC-only login page is missing the resolved supporting copy: %s", body)
		}
		if !strings.Contains(body, `class="sso-button btn-primary"`) {
			t.Fatalf("OIDC-only SSO continuation must be styled as the primary action: %s", body)
		}
		if strings.Contains(body, `id="email"`) || strings.Contains(body, `id="password"`) || strings.Contains(body, "/api/auth/login") || strings.Contains(body, "main app first") {
			t.Fatalf("OIDC-only login page must not render the dead password form or main-app-first copy: %s", body)
		}
	})
}

func TestOAuthOIDCOnlyExistingSessionReachesConsent(t *testing.T) {
	_, ts, _, sessionClient := newTestOAuthServerWithOIDC(t, true, true)
	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)

	status, location, body := oauthPageResponse(t, sessionClient, authorizeURL(ts.URL, clientID, redirectURI, challenge, "sessioned"))
	if status != http.StatusOK || location != "" {
		t.Fatalf("sessioned OIDC-only authorize status=%d Location=%q", status, location)
	}
	if !strings.Contains(body, "Approve access for") || strings.Contains(body, `id="oauth-oidc-login"`) {
		t.Fatalf("existing session must skip login and reach consent: %s", body)
	}
}

func TestOAuthOIDCConfiguredZeroUsersRequiresSetup(t *testing.T) {
	srv, ts, _, _ := newTestOAuthServerWithOIDC(t, true, false)
	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)

	status, location, body := oauthPageResponse(t, http.DefaultClient, authorizeURL(ts.URL, clientID, redirectURI, challenge, "bootstrap"))
	if status != http.StatusServiceUnavailable || location != "" {
		t.Fatalf("zero-user OIDC authorize status=%d Location=%q", status, location)
	}
	if !strings.Contains(body, "Set up Scrumboy first") || !strings.Contains(body, "Complete first-time setup at the main app") {
		t.Fatalf("zero-user OIDC authorize must retain the setup-required page: %s", body)
	}
	if strings.Contains(body, "Continue with SSO") || strings.Contains(body, `id="oauth-oidc-login"`) {
		t.Fatalf("zero-user OIDC authorize must not offer an SSO continuation: %s", body)
	}

	users, err := srv.store.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users after blocked OAuth bootstrap: %v", err)
	}
	if users != 0 {
		t.Fatalf("OAuth authorization created %d users before instance setup", users)
	}
}

func TestOAuthOIDCContinuationPreservesAuthorizeRequest(t *testing.T) {
	_, ts, _, _ := newTestOAuthServerWithOIDC(t, true, true)
	redirectURI := "https://client.example.com/callback?existing=a%2Bb"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	state := "state + / ? & = %"
	authorize := authorizeURL(ts.URL, clientID, redirectURI, challenge, state) +
		"&return_to=https%3A%2F%2Fattacker.example" +
		"&future=a%2Bb&future=second&long=" + strings.Repeat("x", 512)
	authorizeURLParsed, err := url.Parse(authorize)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	wantReturnTo := authorizeURLParsed.RequestURI()

	client := newCookieClient(t)
	status, location, loginBody := oauthPageResponse(t, client, authorize)
	if status != http.StatusOK || location != "" {
		t.Fatalf("initial OIDC-only authorize status=%d Location=%q", status, location)
	}
	if strings.Contains(loginBody, `id="password"`) {
		t.Fatalf("OIDC-only continuation unexpectedly rendered a password form: %s", loginBody)
	}

	href := oauthOIDCLoginHref(t, loginBody)
	loginURL, err := url.Parse(href)
	if err != nil {
		t.Fatalf("parse OIDC login href: %v", err)
	}
	returnTo := loginURL.Query().Get("return_to")
	if returnTo != wantReturnTo {
		t.Fatalf("OIDC outer return_to changed authorize RequestURI\n got: %q\nwant: %q", returnTo, wantReturnTo)
	}
	returnURL, err := url.Parse(returnTo)
	if err != nil {
		t.Fatalf("parse nested authorize return_to: %v", err)
	}
	if got := returnURL.Query().Get("return_to"); got != "https://attacker.example" {
		t.Fatalf("inner return_to was filtered or replaced: got %q", got)
	}

	resp, err := client.Get(ts.URL + href)
	if err != nil {
		t.Fatalf("follow OAuth OIDC continuation: %v", err)
	}
	defer resp.Body.Close()
	consentBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read consent page: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OIDC continuation final status=%d URL=%s body=%s", resp.StatusCode, resp.Request.URL, consentBody)
	}
	serverURL, _ := url.Parse(ts.URL)
	if resp.Request.URL.Scheme != serverURL.Scheme || resp.Request.URL.Host != serverURL.Host || resp.Request.URL.Path != "/oauth/authorize" {
		t.Fatalf("OIDC continuation escaped Scrumboy authorize: %s", resp.Request.URL)
	}
	if got := resp.Request.URL.RequestURI(); got != wantReturnTo {
		t.Fatalf("OIDC callback did not preserve complete authorize RequestURI\n got: %q\nwant: %q", got, wantReturnTo)
	}
	if !strings.Contains(string(consentBody), "Approve access for") {
		t.Fatalf("successful OIDC continuation did not reach consent: %s", consentBody)
	}

	var authStatus map[string]any
	doJSON(t, client, http.MethodGet, ts.URL+"/api/auth/status", nil, &authStatus)
	if authStatus["user"] == nil {
		t.Fatalf("successful OIDC continuation did not establish a session: %+v", authStatus)
	}
}

func TestOAuthExpiredConsentPOSTReturnsToCompleteAuthorizeGET(t *testing.T) {
	_, ts, _, _ := newTestOAuthServerWithOIDC(t, true, true)
	redirectURI := "https://client.example.com/callback?existing=a%2Bb"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	state := "expired + / ? & = %"

	form := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"action":                {"approve"},
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new expired-session consent request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", ts.URL)

	client := newCookieClient(t)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expired-session consent POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expired-session consent POST status=%d, want 303", resp.StatusCode)
	}

	wantAuthorize, err := url.Parse(authorizeURL(ts.URL, clientID, redirectURI, challenge, state))
	if err != nil {
		t.Fatalf("parse expected authorize URL: %v", err)
	}
	wantRequestURI := wantAuthorize.RequestURI()
	location := resp.Header.Get("Location")
	if location != wantRequestURI {
		t.Fatalf("expired-session consent redirect changed validated OAuth parameters\n got: %q\nwant: %q", location, wantRequestURI)
	}

	client.CheckRedirect = nil
	status, nextLocation, loginBody := oauthPageResponse(t, client, ts.URL+location)
	if status != http.StatusOK || nextLocation != "" {
		t.Fatalf("canonical authorize GET status=%d Location=%q", status, nextLocation)
	}
	if !strings.Contains(loginBody, "Continue with SSO") || strings.Contains(loginBody, `id="password"`) {
		t.Fatalf("canonical authorize GET must show the configured OIDC-only login option: %s", loginBody)
	}

	href := oauthOIDCLoginHref(t, loginBody)
	loginURL, err := url.Parse(href)
	if err != nil {
		t.Fatalf("parse expired-session OIDC href: %v", err)
	}
	if got := loginURL.Query().Get("return_to"); got != wantRequestURI {
		t.Fatalf("expired-session OIDC continuation=%q, want %q", got, wantRequestURI)
	}

	resp, err = client.Get(ts.URL + href)
	if err != nil {
		t.Fatalf("follow expired-session OIDC continuation: %v", err)
	}
	defer resp.Body.Close()
	consentBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read expired-session consent page: %v", err)
	}
	if resp.StatusCode != http.StatusOK || resp.Request.URL.RequestURI() != wantRequestURI || !strings.Contains(string(consentBody), "Approve access for") {
		t.Fatalf("expired-session OIDC continuation did not restore consent: status=%d URL=%s body=%s", resp.StatusCode, resp.Request.URL, consentBody)
	}
}

func TestOAuthLoginPageDoesNotProbeOIDCProvider(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	bootstrapUserClient(t, newCookieClient(t), ts.URL, "Owner", "no-probe@example.com", "password123")
	srv.oidcService = oidc.New(oidc.Config{
		IssuerCanonical:   "http://127.0.0.1:1",
		ClientID:          "configured-client",
		ClientSecret:      "configured-secret",
		RedirectURL:       ts.URL + "/api/auth/oidc/callback",
		LocalAuthDisabled: true,
	})

	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	status, _, body := oauthPageResponse(t, http.DefaultClient, authorizeURL(ts.URL, clientID, redirectURI, challenge, "no-probe"))
	if status != http.StatusOK || !strings.Contains(body, "Continue with SSO") {
		t.Fatalf("static OIDC configuration should render SSO without probing the provider: status=%d body=%s", status, body)
	}
}
