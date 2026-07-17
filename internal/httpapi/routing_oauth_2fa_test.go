package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const oauthTestRecoveryCode = "ABCD-EFGH"

func newOAuthPasswordTestServer(t *testing.T) (*Server, *httptest.Server, int64) {
	t.Helper()
	srv := newTestOAuthServer(t, Options{EncryptionKey: testEncryptionKey})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	user := bootstrapUserClient(t, newCookieClient(t), ts.URL, "OAuth Owner", "oauth-2fa@example.com", "password123")
	userID, ok := user["id"].(float64)
	if !ok || userID <= 0 {
		t.Fatalf("bootstrap response has no numeric user id: %+v", user)
	}
	return srv, ts, int64(userID)
}

func enableOAuthTest2FA(t *testing.T, srv *Server, userID int64) {
	t.Helper()
	ctx := context.Background()
	encryptedSecret, err := srv.store.EncryptTOTPSecret([]byte("JBSWY3DPEHPK3PXP"))
	if err != nil {
		t.Fatalf("encrypt OAuth test TOTP secret: %v", err)
	}
	if err := srv.store.SetUserTwoFactor(ctx, userID, encryptedSecret); err != nil {
		t.Fatalf("enable OAuth test 2FA: %v", err)
	}
	if err := srv.store.AddRecoveryCodes(ctx, userID, []string{oauthTestRecoveryCode}); err != nil {
		t.Fatalf("add OAuth test recovery code: %v", err)
	}
}

func oauthPasswordAuthorizeURL(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	redirectURI := "https://client.example.com/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	return authorizeURL(ts.URL, clientID, redirectURI, challenge, "oauth-password")
}

func oauthPasswordLogin(t *testing.T, client *http.Client, baseURL string) map[string]any {
	t.Helper()
	var login map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]any{
		"email":    "oauth-2fa@example.com",
		"password": "password123",
	}, &login)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth password login status=%d body=%s", resp.StatusCode, body)
	}
	return login
}

func clientHasSessionCookie(t *testing.T, client *http.Client, baseURL string) bool {
	t.Helper()
	base, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	for _, cookie := range client.Jar.Cookies(base) {
		if cookie.Name == "scrumboy_session" && cookie.Value != "" {
			return true
		}
	}
	return false
}

func TestOAuthPassword2FARecoveryCodeReachesConsent(t *testing.T) {
	srv, ts, userID := newOAuthPasswordTestServer(t)
	enableOAuthTest2FA(t, srv, userID)
	authorize := oauthPasswordAuthorizeURL(t, ts)
	client := newCookieClient(t)

	status, _, loginPage := oauthPageResponse(t, client, authorize)
	if status != http.StatusOK || !strings.Contains(loginPage, `id="two-factor-login" hidden`) {
		t.Fatalf("OAuth authorize did not render inline 2FA-capable login: status=%d body=%s", status, loginPage)
	}

	login := oauthPasswordLogin(t, client, ts.URL)
	if login["requires2fa"] != true {
		t.Fatalf("2FA password login did not require verification: %+v", login)
	}
	tempToken, _ := login["tempToken"].(string)
	if tempToken == "" {
		t.Fatalf("2FA password login returned no tempToken: %+v", login)
	}
	if clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("password step established a session before 2FA verification")
	}

	var verified map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      oauthTestRecoveryCode,
	}, &verified)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth recovery-code verification status=%d body=%s", resp.StatusCode, body)
	}
	if !clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("successful OAuth 2FA verification did not establish a session")
	}

	status, location, consent := oauthPageResponse(t, client, authorize)
	if status != http.StatusOK || location != "" || !strings.Contains(consent, "Approve access for") {
		t.Fatalf("successful OAuth 2FA did not reach consent: status=%d Location=%q body=%s", status, location, consent)
	}
}

func TestOAuthPassword2FAFailuresRemainRecoverable(t *testing.T) {
	t.Run("invalid authentication code", func(t *testing.T) {
		srv, ts, userID := newOAuthPasswordTestServer(t)
		enableOAuthTest2FA(t, srv, userID)
		authorize := oauthPasswordAuthorizeURL(t, ts)
		client := newCookieClient(t)
		login := oauthPasswordLogin(t, client, ts.URL)
		tempToken, _ := login["tempToken"].(string)

		var out map[string]any
		resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
			"tempToken": tempToken,
			"code":      "not-a-valid-code",
		}, &out)
		if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
			t.Fatalf("invalid OAuth 2FA code status=%d session=%v body=%+v", resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), out)
		}
		status, _, page := oauthPageResponse(t, client, authorize)
		if status != http.StatusOK || strings.Contains(page, "Approve access for") || !strings.Contains(page, "Invalid authentication code.") {
			t.Fatalf("invalid OAuth 2FA code must remain on mapped login surface: status=%d body=%s", status, page)
		}
	})

	t.Run("invalid or expired temp token", func(t *testing.T) {
		_, ts, _ := newOAuthPasswordTestServer(t)
		authorize := oauthPasswordAuthorizeURL(t, ts)
		client := newCookieClient(t)

		var out map[string]any
		resp, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
			"tempToken": "expired-token",
			"code":      oauthTestRecoveryCode,
		}, &out)
		if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
			t.Fatalf("expired OAuth 2FA token status=%d session=%v body=%+v", resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), out)
		}
		status, _, page := oauthPageResponse(t, client, authorize)
		if status != http.StatusOK || strings.Contains(page, "Approve access for") || !strings.Contains(page, "Your sign-in attempt expired. Start over to try again.") || !strings.Contains(page, "function startOver()") {
			t.Fatalf("expired OAuth 2FA token must remain recoverable through Start over: status=%d body=%s", status, page)
		}
	})
}

func TestOAuthPasswordWithout2FAReachesConsent(t *testing.T) {
	_, ts, _ := newOAuthPasswordTestServer(t)
	authorize := oauthPasswordAuthorizeURL(t, ts)
	client := newCookieClient(t)

	login := oauthPasswordLogin(t, client, ts.URL)
	if login["requires2fa"] == true || !clientHasSessionCookie(t, client, ts.URL) {
		t.Fatalf("non-2FA OAuth password login did not establish a direct session: %+v", login)
	}
	status, location, consent := oauthPageResponse(t, client, authorize)
	if status != http.StatusOK || location != "" || !strings.Contains(consent, "Approve access for") {
		t.Fatalf("non-2FA OAuth password login did not reach consent: status=%d Location=%q body=%s", status, location, consent)
	}
}
