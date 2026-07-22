package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"scrumboy/internal/oauth"
)

type oauthGrantFixture struct {
	baseURL     string
	redirectURI string
	clientID    string
	verifier    string
	challenge   string
	code        string
}

func newOAuthGrantFixture(t *testing.T) oauthGrantFixture {
	t.Helper()
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	t.Cleanup(cleanup)
	cookieClient := newCookieClient(t)
	cookieClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	bootstrapUserClient(t, cookieClient, ts.URL, "Owner", "oauth-form@example.com", "password123")
	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	verifier, challenge := pkcePair(t)
	location := approveConsent(t, cookieClient, ts.URL, clientID, redirectURI, challenge, "form-test")
	code := location.Query().Get("code")
	if code == "" {
		t.Fatal("authorization did not issue a code")
	}
	return oauthGrantFixture{
		baseURL:     ts.URL,
		redirectURI: redirectURI,
		clientID:    clientID,
		verifier:    verifier,
		challenge:   challenge,
		code:        code,
	}
}

func (f oauthGrantFixture) codeForm() url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {f.code},
		"redirect_uri":  {f.redirectURI},
		"client_id":     {f.clientID},
		"code_verifier": {f.verifier},
		"resource":      {f.baseURL + "/mcp/rpc"},
	}
}

func postOAuthRequest(t *testing.T, method, target, contentType, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		return resp, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("OAuth response is not JSON (status=%d)", resp.StatusCode)
	}
	return resp, out
}

func assertTokenNoCache(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	if got := resp.Header.Get("Pragma"); got != "no-cache" {
		t.Fatalf("Pragma=%q, want no-cache", got)
	}
}

func oauthBearerStatus(t *testing.T, baseURL, accessToken string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestOAuthTokenFormBodyOnlyAndAntiCaching(t *testing.T) {
	t.Run("authorization code and refresh success", func(t *testing.T) {
		fixture := newOAuthGrantFixture(t)
		resp, out := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded; charset=UTF-8", fixture.codeForm().Encode())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("authorization-code status=%d, want 200", resp.StatusCode)
		}
		assertTokenNoCache(t, resp)
		refreshToken, _ := out["refresh_token"].(string)
		if refreshToken == "" {
			t.Fatal("authorization-code exchange did not issue a refresh token")
		}
		refreshForm := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
			"client_id":     {fixture.clientID},
			"resource":      {fixture.baseURL + "/mcp/rpc"},
		}
		refreshResp, refreshOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", refreshForm.Encode())
		if refreshResp.StatusCode != http.StatusOK || refreshOut["access_token"] == nil {
			t.Fatalf("refresh status=%d, want token response", refreshResp.StatusCode)
		}
		assertTokenNoCache(t, refreshResp)
	})

	t.Run("query-only exchange does not consume code", func(t *testing.T) {
		fixture := newOAuthGrantFixture(t)
		queryResp, queryOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token?"+fixture.codeForm().Encode(), "application/x-www-form-urlencoded", "")
		if queryResp.StatusCode != http.StatusBadRequest || queryOut["error"] != oauth.ErrInvalidRequest || queryOut["access_token"] != nil {
			t.Fatalf("query-only exchange status=%d error=%v", queryResp.StatusCode, queryOut["error"])
		}
		validResp, validOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", fixture.codeForm().Encode())
		if validResp.StatusCode != http.StatusOK || validOut["access_token"] == nil {
			t.Fatalf("valid retry status=%d, want success", validResp.StatusCode)
		}
	})

	t.Run("JSON plus query credentials", func(t *testing.T) {
		ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
		defer cleanup()
		resp, out := postOAuthRequest(t, http.MethodPost, ts.URL+"/oauth/token?grant_type=authorization_code&code=query-code", "application/json", `{"client_id":"client"}`)
		if resp.StatusCode != http.StatusBadRequest || out["error"] != oauth.ErrInvalidRequest || out["access_token"] != nil {
			t.Fatalf("JSON/query exchange status=%d error=%v", resp.StatusCode, out["error"])
		}
	})
}

func TestOAuthTokenRejectsWrongMediaAndSetsNoCacheOnErrors(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()

	for name, contentType := range map[string]string{
		"missing":   "",
		"JSON":      "application/json",
		"text":      "text/plain",
		"multipart": "multipart/form-data; boundary=test",
		"malformed": "application/x-www-form-urlencoded; charset=\"",
	} {
		t.Run(name, func(t *testing.T) {
			resp, out := postOAuthRequest(t, http.MethodPost, ts.URL+"/oauth/token", contentType, "grant_type=authorization_code")
			if resp.StatusCode != http.StatusBadRequest || out["error"] != oauth.ErrInvalidRequest {
				t.Fatalf("status=%d error=%v, want invalid_request", resp.StatusCode, out["error"])
			}
			assertTokenNoCache(t, resp)
		})
	}

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{name: "invalid request", form: url.Values{"grant_type": {"authorization_code"}, "resource": {ts.URL + "/mcp/rpc"}}, want: oauth.ErrInvalidRequest},
		{name: "invalid grant", form: url.Values{"grant_type": {"authorization_code"}, "code": {"invalid"}, "redirect_uri": {"http://localhost/callback"}, "client_id": {"invalid"}, "code_verifier": {"invalid"}, "resource": {ts.URL + "/mcp/rpc"}}, want: oauth.ErrInvalidGrant},
		{name: "invalid target", form: url.Values{"grant_type": {"authorization_code"}, "code": {"invalid"}, "redirect_uri": {"http://localhost/callback"}, "client_id": {"invalid"}, "code_verifier": {"invalid"}, "resource": {ts.URL + "/mcp"}}, want: oauth.ErrInvalidTarget},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, out := postOAuthRequest(t, http.MethodPost, ts.URL+"/oauth/token", "application/x-www-form-urlencoded", tc.form.Encode())
			if resp.StatusCode != http.StatusBadRequest || out["error"] != tc.want {
				t.Fatalf("status=%d error=%v, want %s", resp.StatusCode, out["error"], tc.want)
			}
			assertTokenNoCache(t, resp)
		})
	}

	methodResp, _ := postOAuthRequest(t, http.MethodGet, ts.URL+"/oauth/token", "", "")
	if methodResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET token status=%d, want 405", methodResp.StatusCode)
	}
	assertTokenNoCache(t, methodResp)
}

func TestOAuthAuthorizationRejectsDuplicateParameters(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithMCP(t, "full")
	defer cleanup()
	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, ts.URL, redirectURI)
	_, challenge := pkcePair(t)
	base := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {"state"},
		"resource":              {ts.URL + "/mcp/rpc"},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	for name, mutate := range map[string]func(url.Values){
		"identical response_type": func(q url.Values) { q["response_type"] = []string{"code", "code"} },
		"conflicting challenge":   func(q url.Values) { q["code_challenge"] = []string{challenge, "different"} },
	} {
		t.Run(name, func(t *testing.T) {
			q := cloneValues(base)
			mutate(q)
			resp, err := client.Get(ts.URL + "/oauth/authorize?" + q.Encode())
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			location, _ := url.Parse(resp.Header.Get("Location"))
			if resp.StatusCode != http.StatusFound || location.Query().Get("error") != oauth.ErrInvalidRequest {
				t.Fatalf("status=%d error=%q, want redirected invalid_request", resp.StatusCode, location.Query().Get("error"))
			}
		})
	}

	t.Run("ambiguous client is not redirected", func(t *testing.T) {
		q := cloneValues(base)
		q["client_id"] = []string{clientID, clientID}
		resp, out := postOAuthRequest(t, http.MethodGet, ts.URL+"/oauth/authorize?"+q.Encode(), "", "")
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" || out["error"] != oauth.ErrInvalidRequest {
			t.Fatalf("status=%d location=%q error=%v", resp.StatusCode, resp.Header.Get("Location"), out["error"])
		}
	})

	t.Run("ambiguous redirect is not redirected", func(t *testing.T) {
		q := cloneValues(base)
		q["redirect_uri"] = []string{redirectURI, "https://attacker.example/callback"}
		resp, out := postOAuthRequest(t, http.MethodGet, ts.URL+"/oauth/authorize?"+q.Encode(), "", "")
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" || out["error"] != oauth.ErrInvalidRequest {
			t.Fatalf("status=%d location=%q error=%v", resp.StatusCode, resp.Header.Get("Location"), out["error"])
		}
	})

	t.Run("query and body split", func(t *testing.T) {
		form := cloneValues(base)
		form.Set("state", "body-state")
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/authorize?state=query-state", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		location, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound || location.Query().Get("error") != oauth.ErrInvalidRequest {
			t.Fatalf("status=%d error=%q, want redirected invalid_request", resp.StatusCode, location.Query().Get("error"))
		}
	})

	t.Run("duplicate resource remains invalid_target", func(t *testing.T) {
		q := cloneValues(base)
		q["resource"] = []string{ts.URL + "/mcp/rpc", ts.URL + "/mcp/rpc"}
		resp, err := client.Get(ts.URL + "/oauth/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		location, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound || location.Query().Get("error") != oauth.ErrInvalidTarget {
			t.Fatalf("status=%d error=%q, want invalid_target", resp.StatusCode, location.Query().Get("error"))
		}
	})

	var codeCount int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM oauth_auth_codes`).Scan(&codeCount); err != nil {
		t.Fatal(err)
	}
	if codeCount != 0 {
		t.Fatalf("duplicate authorization attempts issued %d codes, want 0", codeCount)
	}
}

func cloneValues(source url.Values) url.Values {
	clone := make(url.Values, len(source))
	for key, values := range source {
		clone[key] = append([]string(nil), values...)
	}
	return clone
}

func TestOAuthTokenDuplicateParametersDoNotConsumeGrants(t *testing.T) {
	for _, tc := range []struct {
		name      string
		duplicate func(url.Values)
	}{
		{name: "identical code", duplicate: func(form url.Values) { form["code"] = []string{form.Get("code"), form.Get("code")} }},
		{name: "conflicting verifier", duplicate: func(form url.Values) { form["code_verifier"] = []string{form.Get("code_verifier"), "different"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newOAuthGrantFixture(t)
			form := fixture.codeForm()
			tc.duplicate(form)
			resp, out := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", form.Encode())
			if resp.StatusCode != http.StatusBadRequest || out["error"] != oauth.ErrInvalidRequest {
				t.Fatalf("duplicate status=%d error=%v", resp.StatusCode, out["error"])
			}
			validResp, validOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", fixture.codeForm().Encode())
			if validResp.StatusCode != http.StatusOK || validOut["access_token"] == nil {
				t.Fatalf("valid retry status=%d, want success", validResp.StatusCode)
			}
		})
	}

	t.Run("duplicate refresh token", func(t *testing.T) {
		fixture := newOAuthGrantFixture(t)
		_, issued := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", fixture.codeForm().Encode())
		refreshToken := issued["refresh_token"].(string)
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken, refreshToken},
			"client_id":     {fixture.clientID},
			"resource":      {fixture.baseURL + "/mcp/rpc"},
		}
		resp, out := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", form.Encode())
		if resp.StatusCode != http.StatusBadRequest || out["error"] != oauth.ErrInvalidRequest {
			t.Fatalf("duplicate refresh status=%d error=%v", resp.StatusCode, out["error"])
		}
		form.Set("refresh_token", refreshToken)
		validResp, validOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", form.Encode())
		if validResp.StatusCode != http.StatusOK || validOut["access_token"] == nil {
			t.Fatalf("valid refresh retry status=%d, want success", validResp.StatusCode)
		}
	})
}

func TestOAuthRevocationIsFormBodyOnlyAndDuplicateSafe(t *testing.T) {
	fixture := newOAuthGrantFixture(t)
	_, issued := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/token", "application/x-www-form-urlencoded", fixture.codeForm().Encode())
	accessToken := issued["access_token"].(string)

	queryResp, queryOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/revoke?token="+url.QueryEscape(accessToken), "application/x-www-form-urlencoded", "")
	if queryResp.StatusCode != http.StatusBadRequest || queryOut["error"] != oauth.ErrInvalidRequest {
		t.Fatalf("query revocation status=%d error=%v", queryResp.StatusCode, queryOut["error"])
	}
	duplicate := url.Values{"token": {accessToken, accessToken}}
	duplicateResp, duplicateOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/revoke", "application/x-www-form-urlencoded", duplicate.Encode())
	if duplicateResp.StatusCode != http.StatusBadRequest || duplicateOut["error"] != oauth.ErrInvalidRequest {
		t.Fatalf("duplicate revocation status=%d error=%v", duplicateResp.StatusCode, duplicateOut["error"])
	}
	if status := oauthBearerStatus(t, fixture.baseURL, accessToken); status != http.StatusOK {
		t.Fatalf("rejected revocation changed token status to %d, want 200", status)
	}

	for name, contentType := range map[string]string{
		"missing":   "",
		"JSON":      "application/json",
		"text":      "text/plain",
		"multipart": "multipart/form-data; boundary=test",
		"malformed": "application/x-www-form-urlencoded; charset=\"",
	} {
		t.Run(name, func(t *testing.T) {
			wrongMediaResp, wrongMediaOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/revoke", contentType, `{}`)
			if wrongMediaResp.StatusCode != http.StatusBadRequest || wrongMediaOut["error"] != oauth.ErrInvalidRequest {
				t.Fatalf("wrong-media revocation status=%d error=%v", wrongMediaResp.StatusCode, wrongMediaOut["error"])
			}
		})
	}
	if status := oauthBearerStatus(t, fixture.baseURL, accessToken); status != http.StatusOK {
		t.Fatalf("wrong-media revocation changed token status to %d, want 200", status)
	}

	validResp, validOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/revoke", "application/x-www-form-urlencoded", url.Values{"token": {accessToken}}.Encode())
	if validResp.StatusCode != http.StatusOK || validOut != nil {
		t.Fatalf("valid revocation status=%d, want empty 200", validResp.StatusCode)
	}
	unknownResp, unknownOut := postOAuthRequest(t, http.MethodPost, fixture.baseURL+"/oauth/revoke", "application/x-www-form-urlencoded", url.Values{"token": {"unknown-token"}}.Encode())
	if unknownResp.StatusCode != http.StatusOK || unknownOut != nil {
		t.Fatalf("unknown-token revocation status=%d, want empty 200", unknownResp.StatusCode)
	}
	if status := oauthBearerStatus(t, fixture.baseURL, accessToken); status != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d, want 401", status)
	}
}
