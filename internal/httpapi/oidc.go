package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
)

const (
	mobileOIDCCallbackURL = "com.markrai.scrumboy://oidc/callback"
	mobileOIDCFlowTTL     = 10 * time.Minute
	mobileOIDCGrantTTL    = 2 * time.Minute
)

func (s *Server) mobileOIDCAvailable(w http.ResponseWriter) bool {
	if s.mode == "anonymous" || s.oidcService == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return false
	}
	return true
}

func requireApplicationJSON(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeValidationError(w, "Content-Type must be application/json", "invalid_content_type", map[string]any{"field": "Content-Type"})
		return false
	}
	return true
}

func validRawURLProof(value string, decodedBytes int) bool {
	if value == "" || strings.Contains(value, "=") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == decodedBytes && base64.RawURLEncoding.EncodeToString(decoded) == value
}

func authorizationURL(request *oidc.AuthorizationRequest) (string, error) {
	u, err := url.Parse(request.AuthorizationEndpoint)
	if err != nil {
		return "", err
	}
	query := u.Query()
	for key, value := range request.Parameters {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func allowMobileOIDCAttempt(limiter *ratelimit.Limiter, r *http.Request, clientIP string) bool {
	return limiter == nil || limiter.Allow("mobile-oidc-ip:"+clientIP, "")
}

func (s *Server) handleMobileOIDCStart(w http.ResponseWriter, r *http.Request) {
	if !s.mobileOIDCAvailable(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if !requireApplicationJSON(w, r) {
		return
	}
	if !allowMobileOIDCAttempt(s.mobileOIDCStartRateLimit, r, s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	var input struct {
		CodeChallenge       string `json:"codeChallenge"`
		CodeChallengeMethod string `json:"codeChallengeMethod"`
		ReturnTo            string `json:"returnTo"`
	}
	if err := readJSON(w, r, s.maxBody, &input); err != nil {
		return
	}
	if input.CodeChallengeMethod != "S256" || !validRawURLProof(input.CodeChallenge, 32) {
		writeValidationError(w, "valid S256 code challenge required", "invalid_code_challenge", nil)
		return
	}
	returnTo := oidc.SanitizeReturnTo(input.ReturnTo)
	request, err := s.oidcService.LoginAuthorizationRequest(r.Context(), returnTo)
	if err != nil {
		s.logger.Printf("oidc: mobile discovery/start error: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	flowState := request.Parameters["state"]
	if !validRawURLProof(flowState, 32) {
		s.logger.Printf("oidc: mobile start produced invalid provider state")
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	if err := s.store.CreateMobileOIDCFlow(r.Context(), flowState, input.CodeChallenge, returnTo, mobileOIDCFlowTTL); err != nil {
		s.logger.Printf("oidc: create mobile flow: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	loginURL, err := authorizationURL(request)
	if err != nil {
		s.logger.Printf("oidc: build mobile authorization URL: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, http.StatusOK, map[string]any{
		"authorizationUrl": loginURL,
		"flowState":        flowState,
	})
}

func (s *Server) handleMobileOIDCExchange(w http.ResponseWriter, r *http.Request) {
	if !s.mobileOIDCAvailable(w) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if !requireApplicationJSON(w, r) {
		return
	}
	if !allowMobileOIDCAttempt(s.mobileOIDCExchangeRateLimit, r, s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	var input struct {
		Code     string `json:"code"`
		State    string `json:"state"`
		Verifier string `json:"verifier"`
	}
	if err := readJSON(w, r, s.maxBody, &input); err != nil {
		return
	}
	if !validRawURLProof(input.Code, 32) || !validRawURLProof(input.State, 32) || !validRawURLProof(input.Verifier, 32) {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "mobile OIDC handoff rejected", nil)
		return
	}
	exchange, err := s.store.ExchangeMobileOIDCHandoff(r.Context(), input.Code, input.State, input.Verifier, 30*24*time.Hour)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Printf("oidc: mobile exchange error: %v", err)
		}
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "mobile OIDC handoff rejected", nil)
		return
	}
	setSessionCookie(w, r, exchange.SessionToken, exchange.SessionExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"returnTo": exchange.ReturnTo})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" || s.oidcService == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	returnTo := oidc.SanitizeReturnTo(r.URL.Query().Get("return_to"))

	redirectURL, err := s.oidcService.LoginRedirectURL(r.Context(), returnTo)
	if err != nil {
		s.logger.Printf("oidc: discovery/login error: %v", err)
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.mode == "anonymous" || s.oidcService == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	// The persisted marker is the authoritative flow-kind discriminator. It is
	// consulted before the existing provider callback validator and retained
	// after use so replay never falls through to the browser-session path.
	rawState := r.URL.Query().Get("state")
	mobileFlow, mobileFlowErr := s.store.GetMobileOIDCFlow(r.Context(), rawState)
	isMobile := mobileFlowErr == nil
	if mobileFlowErr != nil && !errors.Is(mobileFlowErr, store.ErrNotFound) {
		s.logger.Printf("oidc: identify mobile callback: %v", mobileFlowErr)
		http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
		return
	}
	if isMobile && (mobileFlow.CallbackConsumed || !mobileFlow.ExpiresAt.After(time.Now())) {
		redirectMobileOIDC(w, r, rawState, "state_invalid", "")
		return
	}

	result, errCode := s.oidcService.HandleCallback(r.Context(), r)
	if errCode != "" {
		if errCode != "provider" {
			s.logger.Printf("oidc: callback error: %s", errCode)
		}
		if isMobile {
			redirectMobileOIDC(w, r, rawState, errCode, "")
			return
		}
		http.Redirect(w, r, "/?oidc_error="+errCode, http.StatusFound)
		return
	}

	ctx := r.Context()
	if result.Purpose != oidc.FlowLogin {
		s.handleSensitiveOIDCCallback(w, r, result)
		return
	}

	u, loginErrCode := s.resolveOIDCLoginIdentity(ctx, result)
	if loginErrCode != "" {
		if isMobile {
			redirectMobileOIDC(w, r, rawState, loginErrCode, "")
			return
		}
		http.Redirect(w, r, "/?oidc_error="+loginErrCode, http.StatusFound)
		return
	}

	if isMobile {
		code, _, _, err := s.store.CreateMobileOIDCHandoffGrant(ctx, rawState, u.ID, mobileOIDCGrantTTL)
		if err != nil {
			s.logger.Printf("oidc: create mobile handoff grant: %v", err)
			redirectMobileOIDC(w, r, rawState, "token", "")
			return
		}
		redirectMobileOIDC(w, r, rawState, "", code)
		return
	}

	token, expiresAt, err := s.store.CreateSession(ctx, u.ID, 30*24*time.Hour)
	if err != nil {
		s.logger.Printf("oidc: create session: %v", err)
		http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
		return
	}
	setSessionCookie(w, r, token, expiresAt)
	http.Redirect(w, r, result.ReturnTo, http.StatusFound)
}

func (s *Server) resolveOIDCLoginIdentity(ctx context.Context, result *oidc.CallbackResult) (store.User, string) {
	u, err := s.store.GetUserByOIDCIdentity(ctx, result.Issuer, result.Subject)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Printf("oidc: get user by identity: %v", err)
			return store.User{}, "token"
		}

		// New identity: create user. Pass configured issuer so the store
		// only grants owner when issuer matches (plan section I).
		configuredIssuer := s.oidcService.Config().IssuerCanonical
		u, err = s.store.CreateUserOIDC(ctx, configuredIssuer, result.Issuer, result.Subject, result.Email, result.Name)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				// Do not identify or attach identities by email. The response is
				// intentionally generic while still directing the legitimate user.
				return store.User{}, "link_required"
			} else {
				s.logger.Printf("oidc: create user: %v", err)
				return store.User{}, "token"
			}
		}
	} else if err := s.store.UpdateOIDCIdentityEmailAtLogin(ctx, u.ID, result.Issuer, result.Subject, result.Email); err != nil {
		s.logger.Printf("oidc: update linked identity metadata: %v", err)
		return store.User{}, "token"
	}

	if err := s.store.AssignUnownedDurableProjectsToUser(ctx, u.ID); err != nil {
		s.logger.Printf("oidc: assign unowned projects: %v", err)
	}
	return u, ""
}

func redirectMobileOIDC(w http.ResponseWriter, r *http.Request, state, errorCode, code string) {
	callback, _ := url.Parse(mobileOIDCCallbackURL)
	query := callback.Query()
	query.Set("state", state)
	if errorCode != "" {
		query.Set("error", errorCode)
	} else {
		query.Set("code", code)
	}
	callback.RawQuery = query.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, callback.String(), http.StatusFound)
}

func (s *Server) handleSensitiveOIDCCallback(w http.ResponseWriter, r *http.Request, result *oidc.CallbackResult) {
	sessionToken := sessionTokenFromRequest(r)
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok || userID != result.UserID || !result.SessionMatches(sessionToken) {
		http.Redirect(w, r, "/?oidc_error=session_changed", http.StatusFound)
		return
	}
	switch result.Purpose {
	case oidc.FlowSetPassword:
		linked, err := s.store.GetUserByOIDCIdentity(ctx, result.Issuer, result.Subject)
		if err != nil || linked.ID != userID {
			http.Redirect(w, r, "/?oidc_error=identity_mismatch", http.StatusFound)
			return
		}
		grant, expires, err := s.store.CreateFirstPasswordGrant(ctx, userID, sessionToken, 5*time.Minute)
		if err != nil {
			s.logger.Printf("oidc: create first-password authorization: %v", err)
			http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
			return
		}
		setFirstPasswordGrantCookie(w, r, grant, expires)
		http.Redirect(w, r, result.ReturnTo, http.StatusFound)
	case oidc.FlowLink:
		if err := s.store.LinkOIDCIdentityExplicit(ctx, userID, result.Issuer, result.Subject, result.Email); err != nil {
			if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrValidation) {
				http.Redirect(w, r, "/?oidc_error=link_rejected", http.StatusFound)
				return
			}
			s.logger.Printf("oidc: explicit identity link failed: %v", err)
			http.Redirect(w, r, "/?oidc_error=token", http.StatusFound)
			return
		}
		http.Redirect(w, r, result.ReturnTo, http.StatusFound)
	default:
		http.Redirect(w, r, "/?oidc_error=state_invalid", http.StatusFound)
	}
}
