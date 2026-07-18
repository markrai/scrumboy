package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"scrumboy/internal/oauth"
	"scrumboy/internal/store"
)

const (
	maxOAuthClientNameRunes  = 128
	maxOAuthRedirectURIBytes = 2048
)

// handleOAuth dispatches the /oauth/* surface (RFC 6749/7591/7636/7009). All
// of it is deliberately outside /api/*: OAuth clients authenticate via PKCE
// and client_id, not the X-Scrumboy CSRF header /api/* requires, and the
// consent form at /oauth/authorize instead combines SameSite=Lax cookie
// semantics with canonical Origin/Referer validation for CSRF protection.
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

// errOAuthIssuerUnavailable is returned by oauthIssuer when no origin can be
// derived that this server can vouch for. Callers must fail closed (a
// controlled error response) rather than fall back to a guessed value.
var errOAuthIssuerUnavailable = errors.New("no trustworthy issuer origin for this request")

// oauthIssuer returns the origin advertised in discovery metadata and used to
// build absolute endpoint URLs, in order:
//
//  1. SCRUMBOY_PUBLIC_BASE_URL, when set and OAuth-safe: HTTPS always, or
//     HTTP only for an explicit loopback host (local-dev exception). The
//     inbound request is never consulted. Non-loopback HTTP is rejected here
//     even though NormalizeBaseURL still allows it for password-reset links.
//  2. Direct TLS: TLS supplies the HTTPS scheme and r.Host supplies the HTTP
//     request authority after strict syntax and port validation.
//  3. SCRUMBOY_TRUST_PROXY: forwarded HTTPS plus an explicit, validated
//     X-Forwarded-Host (see forwardedOAuthOrigin), with no r.Host fallback.
//  4. A loopback request host (localhost, 127.0.0.0/8, ::1) — cleartext HTTP
//     is acceptable there since it never leaves the machine.
//  5. Otherwise: fail closed. Advertising a guessed issuer (e.g. reflecting
//     an attacker-controlled Host header, or an ungated X-Forwarded-Proto on
//     a cleartext connection) would let an attacker spoof the metadata
//     issuer or downgrade it to HTTP.
func (s *Server) oauthIssuer(r *http.Request) (string, error) {
	if s.publicBaseURL != "" {
		return oauthPublicBaseURLIssuer(s.publicBaseURL)
	}
	authority, hostname, authorityOK := parseHTTPAuthority(r.Host)
	if r.TLS != nil && authorityOK {
		return "https://" + authority, nil
	}
	if s.trustProxy {
		if origin, ok := forwardedOAuthOrigin(r); ok {
			return origin, nil
		}
	}
	if authorityOK && isLoopbackHostname(hostname) {
		return "http://" + authority, nil
	}
	return "", errOAuthIssuerUnavailable
}

// oauthPublicBaseURLIssuer reports whether configured PUBLIC_BASE_URL may be
// advertised as an OAuth authorization-server issuer (RFC 8414 / MCP auth
// expect HTTPS for non-loopback AS endpoints). Global NormalizeBaseURL still
// permits http for password-reset compatibility; this check is OAuth-only.
func oauthPublicBaseURLIssuer(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errOAuthIssuerUnavailable
	}
	scheme := strings.ToLower(u.Scheme)
	_, hostname, ok := parseHTTPAuthority(u.Host)
	if !ok {
		return "", errOAuthIssuerUnavailable
	}
	switch scheme {
	case "https":
		return base, nil
	case "http":
		if isLoopbackHostname(hostname) {
			return base, nil
		}
		return "", errOAuthIssuerUnavailable
	default:
		return "", errOAuthIssuerUnavailable
	}
}

// forwardedOAuthOrigin derives scheme+host as one validated decision from
// X-Forwarded-Proto/X-Forwarded-Host (falling back to CF-Visitor for scheme
// only when X-Forwarded-Proto is entirely absent). Only called when
// SCRUMBOY_TRUST_PROXY is enabled. X-Forwarded-Host is required explicitly;
// the backend-facing r.Host is never used as a fallback. Only https is
// accepted here. Each trusted forwarded header must be a single field with a
// single value (no comma-separated hop lists / duplicate fields).
func forwardedOAuthOrigin(r *http.Request) (string, bool) {
	proto, ok := forwardedOAuthScheme(r)
	if !ok || proto != "https" {
		return "", false
	}
	hostValues := r.Header.Values("X-Forwarded-Host")
	if len(hostValues) != 1 {
		return "", false
	}
	authority, _, ok := parseHTTPAuthority(hostValues[0])
	if !ok {
		return "", false
	}
	return "https://" + authority, true
}

// forwardedOAuthScheme returns a single validated forwarded scheme for OAuth
// issuer construction. X-Forwarded-Proto wins when present (exactly one field,
// no commas). CF-Visitor is consulted only when X-Forwarded-Proto is absent,
// and then only if exactly one CF-Visitor field is present.
func forwardedOAuthScheme(r *http.Request) (string, bool) {
	protoValues := r.Header.Values("X-Forwarded-Proto")
	if len(protoValues) > 0 {
		if len(protoValues) != 1 {
			return "", false
		}
		proto := strings.ToLower(strings.TrimSpace(protoValues[0]))
		if proto == "" || strings.Contains(proto, ",") {
			return "", false
		}
		return proto, true
	}
	cfValues := r.Header.Values("CF-Visitor")
	if len(cfValues) != 1 {
		return "", false
	}
	var cfVisitor struct {
		Scheme string `json:"scheme"`
	}
	if err := json.Unmarshal([]byte(cfValues[0]), &cfVisitor); err != nil {
		return "", false
	}
	proto := strings.ToLower(strings.TrimSpace(cfVisitor.Scheme))
	if proto == "" || strings.Contains(proto, ",") {
		return "", false
	}
	return proto, true
}

// isLoopbackHostname reports whether a validated, port-free hostname refers
// to localhost, 127.0.0.0/8, or ::1.
// Deliberately does not treat RFC1918/LAN addresses (192.168.x.x, etc.) as
// loopback — LAN cleartext HTTP is a separate product decision.
func isLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
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
	issuer, err := s.oauthIssuer(r)
	if err != nil {
		s.writeOAuthIssuerUnavailable(w)
		return
	}
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
	issuer, err := s.oauthIssuer(r)
	if err != nil {
		s.writeOAuthIssuerUnavailable(w)
		return
	}
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
	if s.oauthDCRRateLimit != nil && !s.oauthDCRRateLimit.Allow("ip:"+s.clientIP(r), "") {
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
	clientName := strings.TrimSpace(in.ClientName)
	if utf8.RuneCountInString(clientName) > maxOAuthClientNameRunes {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidClientMetadata, "client_name must not exceed 128 Unicode characters")
		return
	}
	// Exactly one redirect_uris entry is required and enforced — silently registering
	// only redirect_uris[0] while ignoring the rest would let a client list additional,
	// unvalidated URIs that this server never checked but the client believes were accepted.
	if len(in.RedirectURIs) != 1 {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "exactly one redirect_uris entry is required")
		return
	}
	redirectURI := strings.TrimSpace(in.RedirectURIs[0])
	if redirectURI == "" {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "exactly one redirect_uris entry is required")
		return
	}
	if len(redirectURI) > maxOAuthRedirectURIBytes {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "redirect_uris[0] must not exceed 2048 bytes")
		return
	}
	if !isValidOAuthRedirectURI(redirectURI) {
		oauth.WriteJSON(w, http.StatusBadRequest, oauth.ErrInvalidRedirectURI, "redirect_uris[0] must be an absolute http(s) URL")
		return
	}

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

func oauthAuthorizeRequestURI(params authorizeParams) string {
	query := url.Values{}
	query.Set("response_type", params.ResponseType)
	query.Set("client_id", params.ClientID)
	query.Set("redirect_uri", params.RedirectURI)
	query.Set("code_challenge", params.CodeChallenge)
	query.Set("code_challenge_method", params.CodeChallengeMethod)
	if params.State != "" {
		query.Set("state", params.State)
	}
	return "/oauth/authorize?" + query.Encode()
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
		s.renderOAuthErrorPage(w, http.StatusServiceUnavailable, "Set up Scrumboy first", `This Scrumboy instance has no account yet. Complete first-time setup at the main app (/), then reopen this link.`)
		return
	}
	if _, ok := store.UserIDFromContext(ctx); !ok {
		s.renderOAuthLoginPage(w, r, client)
		return
	}
	s.renderOAuthConsentPage(w, client, params)
}

func (s *Server) handleOAuthAuthorizeSubmit(w http.ResponseWriter, r *http.Request, ctx context.Context, client store.OAuthClient, params authorizeParams) {
	// SameSite=Lax on the session cookie (see the package doc comment above)
	// blocks cross-site form submissions, but "site" per SameSite is the
	// registrable domain, not this exact origin: a page on any sibling
	// subdomain the cookie's Domain also covers is same-site and would still
	// carry the session cookie into an auto-submitting POST here. Requiring
	// Origin (falling back to Referer) to match this server's own origin
	// closes that gap. Classic HTML form navigations may omit Origin; the
	// OAuth HTML pages therefore send Referrer-Policy: same-origin so a
	// same-origin consent POST still carries Referer for this fallback,
	// while cross-origin navigations withhold it. Missing both headers
	// fails closed.
	if !s.oauthConsentOriginAllowed(r) {
		s.renderOAuthErrorPage(w, http.StatusBadRequest, "Invalid request origin", "This consent submission did not originate from this Scrumboy instance.")
		return
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		// Session expired between consent rendering and submission. Rebuild
		// the validated authorize request from the consent form fields and
		// return to the GET flow so password reloads and OIDC continuation
		// preserve the pending request without resubmitting this POST.
		http.Redirect(w, r, oauthAuthorizeRequestURI(params), http.StatusSeeOther)
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
	if s.oauthTokenRateLimit != nil && !s.oauthTokenRateLimit.Allow("ip:"+s.clientIP(r), "") {
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

// oauthConsentOriginAllowed reports whether r's Origin (or, absent that,
// Referer) header matches this server's own origin. See the call site in
// handleOAuthAuthorizeSubmit for why this check exists.
func (s *Server) oauthConsentOriginAllowed(r *http.Request) bool {
	self, err := s.oauthIssuer(r)
	if err != nil {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == self
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		return u.Scheme+"://"+u.Host == self
	}
	return false
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

// writeOAuthIssuerUnavailable is the fail-closed response for discovery
// endpoints when oauthIssuer can't determine a trustworthy origin (see
// errOAuthIssuerUnavailable) — a controlled 503, never a guessed issuer.
func (s *Server) writeOAuthIssuerUnavailable(w http.ResponseWriter) {
	oauth.WriteJSON(w, http.StatusServiceUnavailable, oauth.ErrServerError,
		"unable to determine a trustworthy issuer for this request; configure SCRUMBOY_PUBLIC_BASE_URL")
}

// isValidOAuthRedirectURI reports whether raw is a well-formed absolute http(s) URL with a
// validated host[:port].
// This doesn't make DCR trustworthy on its own (registration stays unauthenticated, and exact-match
// comparison against the registered value is what actually prevents redirect-target tampering later
// in the flow) — it only rejects garbage/malformed input at registration time: non-URL strings,
// non-http(s) schemes, a missing host, invalid ports, embedded userinfo/fragment delimiters (phishing
// display tricks), or a plain-http URI targeting anything other than loopback. https is allowed for
// any host; http is only allowed for loopback (localhost, 127.0.0.0/8, ::1 — not RFC1918/LAN) since
// native/CLI clients commonly redirect there (RFC 8252) and it has no TLS.
func isValidOAuthRedirectURI(raw string) bool {
	// url.Parse represents both no fragment and a trailing empty fragment as
	// Fragment == "". Reject the delimiter itself before parsing so the two
	// cases cannot collapse into the same accepted value.
	if strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.User != nil || u.Fragment != "" {
		return false
	}
	_, hostname, ok := parseHTTPAuthority(u.Host)
	if !ok {
		return false
	}
	if u.Scheme == "http" && !isLoopbackHostname(hostname) {
		return false
	}
	return true
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
.sso-button{display:block;padding:10px 18px;border-radius:6px;text-align:center;text-decoration:none;margin-top:8px}
.auth-divider{display:flex;align-items:center;gap:12px;color:#7f8796;font-size:12px;margin:20px 0 12px}
.auth-divider:before,.auth-divider:after{content:"";height:1px;background:#2a2e37;flex:1}
.err{color:#ff6b6b;font-size:13px;margin-top:8px}
a{color:#5b8cff}
</style>`

// setOAuthHTMLHeaders applies the headers every /oauth/* HTML page (login,
// consent, error) needs: no-store so a shared/cached browser never persists
// a copy carrying an auth code or session-bound form, and anti-framing so the
// consent page's Approve button can't be UI-redressed into an invisible
// frame on an attacker's page. Referrer-Policy is same-origin (not
// no-referrer) so classic form POSTs that omit Origin still send a
// same-origin Referer for oauthConsentOriginAllowed, while cross-origin
// navigations omit Referer entirely.
func setOAuthHTMLHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Content-Type-Options", "nosniff")
}

// renderOAuthErrorPage renders body as plain text (HTML-escaped): every call
// site passes a fixed, developer-authored string today, but escaping keeps
// it that way if a future caller ever interpolates a client- or
// request-derived value here.
func (s *Server) renderOAuthErrorPage(w http.ResponseWriter, status int, title, body string) {
	setOAuthHTMLHeaders(w)
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s</title>%s</head><body><div class="card"><h1>%s</h1><p>%s</p></div></body></html>`,
		html.EscapeString(title), oauthPageStyle, html.EscapeString(title), html.EscapeString(body))
}

func (s *Server) renderOAuthLoginPage(w http.ResponseWriter, r *http.Request, client store.OAuthClient) {
	setOAuthHTMLHeaders(w)
	w.WriteHeader(http.StatusOK)
	name := client.ClientName
	if name == "" {
		name = "This application"
	}

	oidcAvailable := s.oidcService != nil
	localAuthEnabled := !oidcAvailable || !s.oidcService.Config().LocalAuthDisabled
	title := "Log in to Scrumboy"
	heading := "Log in to connect " + name
	intro := "Sign in with your Scrumboy account to continue."

	var actions strings.Builder
	if localAuthEnabled {
		actions.WriteString(`<div id="err" class="err"></div>
<div id="password-login">
<input id="email" type="email" placeholder="Email" autocomplete="username">
<input id="password" type="password" placeholder="Password" autocomplete="current-password">
<button class="btn-primary" type="button" onclick="doLogin()">Log in</button>
</div>
<div id="two-factor-login" hidden>
<p><label for="two-factor-code">Enter your authentication code</label></p>
<input id="two-factor-code" type="text" placeholder="Code" autocomplete="one-time-code">
<p>Authenticator or recovery code</p>
<button id="verify-two-factor" class="btn-primary" type="button" onclick="doVerify2FA()">Verify</button>
<button class="btn-secondary" type="button" onclick="startOver()">Start over</button>
</div>
<script>
var tempToken = '';

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
    if (r.status === 200 && r.body && r.body.requires2fa) {
      if (typeof r.body.tempToken !== 'string' || !r.body.tempToken) {
        err.textContent = 'Verification failed. Please try again.';
        return;
      }
      tempToken = r.body.tempToken;
      document.getElementById('password').value = '';
      document.getElementById('password-login').hidden = true;
      document.getElementById('two-factor-login').hidden = false;
      document.getElementById('two-factor-code').focus();
      return;
    }
    if (r.status === 200) {
      window.location.reload();
      return;
    }
    err.textContent = 'Login failed. Check your email and password.';
  }).catch(function() {
    err.textContent = 'Login failed. Please try again.';
  });
}

function twoFactorErrorMessage(status, body) {
  if (status === 429) {
    return 'Too many attempts. Try again shortly.';
  }
  var message = body && body.error && body.error.message;
  if (status === 401 && message === 'invalid code') {
    return 'Invalid authentication code.';
  }
  if (status === 401 && (message === 'invalid or expired code' || message === 'too many attempts; please sign in again')) {
    return 'Your sign-in attempt expired. Start over to try again.';
  }
  return 'Verification failed. Please try again.';
}

function doVerify2FA() {
  var code = document.getElementById('two-factor-code').value;
  var err = document.getElementById('err');
  var verifyButton = document.getElementById('verify-two-factor');
  if (!tempToken) {
    err.textContent = 'Verification failed. Please try again.';
    return;
  }
  err.textContent = '';
  verifyButton.disabled = true;
  fetch('/api/auth/login/2fa', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-Scrumboy': '1'},
    body: JSON.stringify({tempToken: tempToken, code: code})
  }).then(function(res) {
    return res.json().then(function(body) { return {status: res.status, body: body}; });
  }).then(function(r) {
    if (r.status === 200) {
      window.location.reload();
      return;
    }
    err.textContent = twoFactorErrorMessage(r.status, r.body);
  }).catch(function() {
    err.textContent = 'Verification failed. Please try again.';
  }).then(function() {
    if (tempToken) {
      verifyButton.disabled = false;
    }
  });
}

function startOver() {
  tempToken = '';
  document.getElementById('two-factor-code').value = '';
  document.getElementById('err').textContent = '';
  document.getElementById('verify-two-factor').disabled = false;
  document.getElementById('two-factor-login').hidden = true;
  document.getElementById('password-login').hidden = false;
  document.getElementById('password').value = '';
  document.getElementById('password').focus();
}
</script>`)
	}

	if oidcAvailable {
		title = "Sign in to continue"
		heading = "Sign in to continue"
		if localAuthEnabled {
			intro = "Choose a sign-in method to connect " + name + "."
			actions.WriteString(`<div class="auth-divider"><span>or</span></div>`)
		} else {
			intro = "Sign in through your configured identity provider to review this access request."
		}
		query := url.Values{}
		query.Set("return_to", r.URL.RequestURI())
		oidcLoginURL := "/api/auth/oidc/login?" + query.Encode()
		fmt.Fprintf(&actions, `<a id="oauth-oidc-login" href="%s" class="sso-button btn-primary">Continue with SSO</a>`, html.EscapeString(oidcLoginURL))
	}

	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>%s</title>%s</head><body>
<div class="card">
<h1>%s</h1>
<p>%s</p>
%s
</div></body></html>`, html.EscapeString(title), oauthPageStyle, html.EscapeString(heading), html.EscapeString(intro), actions.String())
}

func (s *Server) renderOAuthConsentPage(w http.ResponseWriter, client store.OAuthClient, params authorizeParams) {
	setOAuthHTMLHeaders(w)
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
