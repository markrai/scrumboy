package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"scrumboy/internal/oauth"
	"scrumboy/internal/store"
)

// handleOAuth dispatches the /oauth/* surface (RFC 6749/7591/7636/7009). All
// of it is deliberately outside /api/*: OAuth clients authenticate via PKCE
// and client_id, not the X-Scrumboy CSRF header /api/* requires, and the
// consent form at /oauth/authorize relies on SameSite=Lax cookie semantics
// (a cross-site top-level POST navigation never carries the session cookie)
// rather than that header for its own CSRF protection.
func (s *Server) handleOAuth(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/oauth/register":
		s.handleOAuthRegister(w, r)
	case "/oauth/authorize":
		s.handleOAuthAuthorize(w, r)
	case "/oauth/token":
		s.handleOAuthToken(w, r)
	case "/oauth/revoke":
		s.handleOAuthRevoke(w, r)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	}
}

func (s *Server) oauthIssuer(r *http.Request) string {
	proto := "http"
	if isSecureRequest(r) {
		proto = "https"
	}
	return proto + "://" + r.Host
}

// handleOAuthProtectedResourceMetadata serves RFC 9728 discovery: the two
// fields Claude Code's MCP OAuth client actually reads.
func (s *Server) handleOAuthProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	issuer := s.oauthIssuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":              issuer + "/mcp",
		"authorization_servers": []string{issuer},
	})
}

// handleOAuthASMetadata serves RFC 8414 authorization server discovery.
func (s *Server) handleOAuthASMetadata(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	issuer := s.oauthIssuer(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                     issuer,
		"authorization_endpoint":                     issuer + "/oauth/authorize",
		"token_endpoint":                             issuer + "/oauth/token",
		"registration_endpoint":                      issuer + "/oauth/register",
		"revocation_endpoint":                        issuer + "/oauth/revoke",
		"response_types_supported":                   []string{"code"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none"},
		"revocation_endpoint_auth_methods_supported": []string{"none"},
	})
}

// handleOAuthRegister implements RFC 7591 Dynamic Client Registration.
// Unauthenticated by design: DCR is inherently self-service.
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	// RFC 7591 clients always send application/json, so requiring it strictly rejects a
	// cross-origin "simple request" (e.g. Content-Type: text/plain, which needs no CORS preflight)
	// before it can spend a rate-limit slot: a hostile page could otherwise get many unwitting
	// visitors' browsers to each register clients from their own IP, defeating the per-IP limit
	// below by distributing it across real, distinct addresses.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidClientMetadata, "Content-Type must be application/json")
		return
	}
	// Unauthenticated by design (DCR is inherently self-service), so this is the only thing
	// standing between the endpoint and unbounded oauth_clients row growth / free client-identity
	// minting for a phishing-style consent-screen attack (see renderOAuthConsentPage).
	if s.authRateLimit != nil && !s.authRateLimit.Allow("ip:"+s.clientIP(r), "") {
		oauth.WriteJSON(w, http.StatusTooManyRequests, oauth.ErrInvalidRequest, "too many attempts; try again later")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, s.maxBody))
	if err != nil {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidClientMetadata, "could not read request body")
		return
	}
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidClientMetadata, "invalid JSON body")
		return
	}
	if len(in.RedirectURIs) == 0 || strings.TrimSpace(in.RedirectURIs[0]) == "" {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "redirect_uris is required")
		return
	}
	redirectURI := strings.TrimSpace(in.RedirectURIs[0])
	if !isValidOAuthRedirectURI(redirectURI) {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "redirect_uris[0] must be an absolute http(s) URL")
		return
	}
	clientName := strings.TrimSpace(in.ClientName)

	clientID, err := oauth.GenerateClientID()
	if err != nil {
		writeInternal(w, err)
		return
	}
	if _, err := s.store.CreateOAuthClient(s.requestContext(r), clientID, clientName, redirectURI); err != nil {
		writeInternal(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	State               string
}

func parseAuthorizeParams(r *http.Request) authorizeParams {
	return authorizeParams{
		ResponseType:        r.FormValue("response_type"),
		ClientID:            r.FormValue("client_id"),
		RedirectURI:         r.FormValue("redirect_uri"),
		CodeChallenge:       r.FormValue("code_challenge"),
		CodeChallengeMethod: r.FormValue("code_challenge_method"),
		State:               r.FormValue("state"),
	}
}

// handleOAuthAuthorize serves the RFC 6749 §3.1/§4.1.1 authorize endpoint.
// GET shows a login form (if the caller has no valid session) or a consent
// form (if logged in); POST is the consent form's approve/deny submission.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	params := parseAuthorizeParams(r)
	ctx := s.requestContext(r)

	client, err := s.store.GetOAuthClient(ctx, params.ClientID)
	if err != nil || params.ClientID == "" {
		s.renderOAuthErrorPage(w, http.StatusBadRequest, "Unknown client", "This authorization request does not reference a registered OAuth client.")
		return
	}
	if params.RedirectURI == "" || params.RedirectURI != client.RedirectURI {
		// redirect_uri is unverified or doesn't match the client's registered
		// URI: never redirect on it (open-redirect risk per RFC 6749 §4.1.2.1),
		// render a plain error page instead.
		s.renderOAuthErrorPage(w, http.StatusBadRequest, "Redirect URI mismatch", "The redirect_uri for this request does not match the one registered for this client.")
		return
	}

	// From here on redirect_uri is trusted (exact match to the registered
	// client), so remaining validation failures redirect with error params.
	if params.ResponseType != "code" {
		s.redirectOAuthError(w, r, params.RedirectURI, oauth.ErrUnsupportedResponse, "only response_type=code is supported", params.State)
		return
	}
	if params.CodeChallenge == "" || params.CodeChallengeMethod != "S256" {
		s.redirectOAuthError(w, r, params.RedirectURI, oauth.ErrInvalidRequest, "PKCE (code_challenge with S256) is required", params.State)
		return
	}

	if r.Method == http.MethodPost {
		s.handleOAuthAuthorizeSubmit(w, r, ctx, client, params)
		return
	}

	// GET: bootstrap-before-login and login-before-consent gates.
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if n == 0 {
		s.renderOAuthErrorPage(w, http.StatusServiceUnavailable, "Set up Scrumboy first", `This Scrumboy instance has no account yet. Complete first-time setup at <a href="/">the main app</a>, then reopen this link.`)
		return
	}
	if _, ok := store.UserIDFromContext(ctx); !ok {
		s.renderOAuthLoginPage(w, client)
		return
	}
	s.renderOAuthConsentPage(w, client, params)
}

func (s *Server) handleOAuthAuthorizeSubmit(w http.ResponseWriter, r *http.Request, ctx context.Context, client store.OAuthClient, params authorizeParams) {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		// Session expired between the GET and this POST; send back to the
		// GET flow, which will show the login form again.
		s.renderOAuthLoginPage(w, client)
		return
	}

	action := r.FormValue("action")
	if action == "deny" {
		s.redirectOAuthError(w, r, params.RedirectURI, oauth.ErrAccessDenied, "the user denied the request", params.State)
		return
	}
	if action != "approve" {
		s.renderOAuthErrorPage(w, http.StatusBadRequest, "Invalid request", "Missing or unrecognized consent action.")
		return
	}

	code, err := s.store.CreateOAuthAuthCode(ctx, client.ID, userID, params.RedirectURI, params.CodeChallenge, params.CodeChallengeMethod)
	if err != nil {
		writeInternal(w, err)
		return
	}

	redirectURL := params.RedirectURI + queryJoiner(params.RedirectURI) + "code=" + url.QueryEscape(code)
	if params.State != "" {
		redirectURL += "&state=" + url.QueryEscape(params.State)
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleOAuthToken implements the RFC 6749 §4.1.3/§6 token endpoint for the
// authorization_code and refresh_token grants.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if s.authRateLimit != nil && !s.authRateLimit.Allow("ip:"+s.clientIP(r), "") {
		oauth.WriteJSON(w, http.StatusTooManyRequests, oauth.ErrInvalidRequest, "too many attempts; try again later")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	ctx := s.requestContext(r)

	switch r.FormValue("grant_type") {
	case "authorization_code":
		s.handleOAuthTokenAuthCode(w, r, ctx)
	case "refresh_token":
		s.handleOAuthTokenRefresh(w, r, ctx)
	default:
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrUnsupportedGrantType, "unsupported grant_type")
	}
}

func (s *Server) handleOAuthTokenAuthCode(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	code := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	clientID := r.FormValue("client_id")
	codeVerifier := r.FormValue("code_verifier")

	if code == "" || redirectURI == "" || clientID == "" || codeVerifier == "" {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRequest, "code, redirect_uri, client_id, and code_verifier are required")
		return
	}

	ac, err := s.store.ConsumeOAuthAuthCode(ctx, code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidGrant, "the authorization code is invalid, expired, or already used")
			return
		}
		writeInternal(w, err)
		return
	}
	if ac.ClientID != clientID || ac.RedirectURI != redirectURI {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidGrant, "client_id or redirect_uri does not match the authorization request")
		return
	}
	if !oauth.VerifyPKCE(ac.CodeChallengeMethod, codeVerifier, ac.CodeChallenge) {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidGrant, "code_verifier does not match the original code_challenge")
		return
	}

	pair, err := s.store.IssueOAuthTokenPair(ctx, ac.ClientID, ac.UserID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    pair.ExpiresIn,
		"refresh_token": pair.RefreshToken,
	})
}

func (s *Server) handleOAuthTokenRefresh(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	refreshToken := r.FormValue("refresh_token")
	if refreshToken == "" {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRequest, "refresh_token is required")
		return
	}
	clientID, userID, err := s.store.ConsumeOAuthRefreshToken(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidGrant, "the refresh token is invalid, expired, or already used")
			return
		}
		writeInternal(w, err)
		return
	}
	if reqClientID := r.FormValue("client_id"); reqClientID != "" && reqClientID != clientID {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidGrant, "client_id does not match this refresh token")
		return
	}

	pair, err := s.store.IssueOAuthTokenPair(ctx, clientID, userID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  pair.AccessToken,
		"token_type":    "Bearer",
		"expires_in":    pair.ExpiresIn,
		"refresh_token": pair.RefreshToken,
	})
}

// handleOAuthRevoke implements RFC 7009 token revocation. Per §2.2, it always
// returns 200 regardless of whether the token existed, so a caller can never
// use this endpoint to probe for a token's existence.
func (s *Server) handleOAuthRevoke(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	token := r.FormValue("token")
	hint := r.FormValue("token_type_hint")
	if token != "" {
		if err := s.store.RevokeOAuthToken(s.requestContext(r), token, hint); err != nil {
			s.logger.Printf("oauth: revoke token: %v", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// redirectOAuthError redirects to the (already-verified) client redirect_uri
// with RFC 6749 §4.1.2.1 error query params.
func (s *Server) redirectOAuthError(w http.ResponseWriter, r *http.Request, redirectURI, code, description, state string) {
	dest := redirectURI + queryJoiner(redirectURI) + "error=" + url.QueryEscape(code) + "&error_description=" + url.QueryEscape(description)
	if state != "" {
		dest += "&state=" + url.QueryEscape(state)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// isValidOAuthRedirectURI reports whether raw is a well-formed absolute http(s) URL with a host.
// This doesn't make DCR trustworthy on its own (registration stays unauthenticated, and exact-match
// comparison against the registered value is what actually prevents redirect-target tampering later
// in the flow) — it only rejects garbage/malformed input at registration time, e.g. non-URL strings,
// non-http(s) schemes, or a missing host. http is allowed (not just https) since native/CLI clients
// commonly redirect to a loopback address (RFC 8252), which has no TLS.
func isValidOAuthRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

func queryJoiner(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

const oauthPageStyle = `<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#0f1115;color:#e6e6e6;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#1a1d24;border:1px solid #2a2e37;border-radius:12px;padding:32px;max-width:420px;width:90%}
h1{font-size:18px;margin:0 0 16px}
p{font-size:14px;line-height:1.5;color:#b8bcc4}
input{width:100%;box-sizing:border-box;padding:10px;margin:6px 0;border-radius:6px;border:1px solid #2a2e37;background:#0f1115;color:#e6e6e6}
button{padding:10px 18px;border-radius:6px;border:none;font-size:14px;cursor:pointer;margin-top:8px}
.btn-primary{background:#5b8cff;color:#fff}
.btn-secondary{background:#2a2e37;color:#e6e6e6;margin-left:8px}
.err{color:#ff6b6b;font-size:13px;margin-top:8px}
a{color:#5b8cff}
</style>`

func (s *Server) renderOAuthErrorPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s</title>%s</head><body><div class="card"><h1>%s</h1><p>%s</p></div></body></html>`,
		html.EscapeString(title), oauthPageStyle, html.EscapeString(title), body)
}

func (s *Server) renderOAuthLoginPage(w http.ResponseWriter, client store.OAuthClient) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	name := client.ClientName
	if name == "" {
		name = "This application"
	}
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Log in to Scrumboy</title>%s</head><body>
<div class="card">
<h1>Log in to connect %s</h1>
<p>Sign in with your Scrumboy account to continue.</p>
<div id="err" class="err"></div>
<input id="email" type="email" placeholder="Email" autocomplete="username">
<input id="password" type="password" placeholder="Password" autocomplete="current-password">
<button class="btn-primary" onclick="doLogin()">Log in</button>
<script>
function doLogin() {
  var email = document.getElementById('email').value;
  var password = document.getElementById('password').value;
  var err = document.getElementById('err');
  err.textContent = '';
  fetch('/api/auth/login', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-Scrumboy': '1'},
    body: JSON.stringify({email: email, password: password})
  }).then(function(res) {
    return res.json().then(function(body) { return {status: res.status, body: body}; });
  }).then(function(r) {
    if (r.status === 200 && !r.body.requires2fa) {
      window.location.reload();
      return;
    }
    if (r.body && r.body.requires2fa) {
      err.textContent = 'This account has 2FA enabled. Please log in at the main app first, then reopen this link.';
      return;
    }
    err.textContent = 'Login failed. Check your email and password.';
  }).catch(function() {
    err.textContent = 'Login failed. Please try again.';
  });
}
</script>
</div></body></html>`, oauthPageStyle, html.EscapeString(name))
}

func (s *Server) renderOAuthConsentPage(w http.ResponseWriter, client store.OAuthClient, params authorizeParams) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	name := client.ClientName
	if name == "" {
		name = "This application"
	}
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Authorize %s</title>%s</head><body>
<div class="card">
<h1>Approve access for %s?</h1>
<p>%s will be able to read and manage projects, todos, sprints, and tags in this Scrumboy instance on your behalf.</p>
<p>After you approve, you'll be redirected to:<br><strong>%s</strong></p>
<p>Only approve this if you recognize the application above and intended to connect it — anyone can register a client with any name, so a name alone doesn't confirm who you're granting access to. Check that this destination is one you trust.</p>
<form method="POST" action="/oauth/authorize">
<input type="hidden" name="response_type" value="%s">
<input type="hidden" name="client_id" value="%s">
<input type="hidden" name="redirect_uri" value="%s">
<input type="hidden" name="code_challenge" value="%s">
<input type="hidden" name="code_challenge_method" value="%s">
<input type="hidden" name="state" value="%s">
<button class="btn-primary" type="submit" name="action" value="approve">Approve</button>
<button class="btn-secondary" type="submit" name="action" value="deny">Deny</button>
</form>
</div></body></html>`,
		html.EscapeString(name), oauthPageStyle, html.EscapeString(name), html.EscapeString(name),
		html.EscapeString(params.RedirectURI),
		html.EscapeString(params.ResponseType), html.EscapeString(params.ClientID), html.EscapeString(params.RedirectURI),
		html.EscapeString(params.CodeChallenge), html.EscapeString(params.CodeChallengeMethod), html.EscapeString(params.State))
}
