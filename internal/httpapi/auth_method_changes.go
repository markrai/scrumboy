package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/auth"
	"scrumboy/internal/crypto"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
)

const firstPasswordGrantCookie = "scrumboy_first_password_grant"

var errSensitiveRateLimited = errors.New("sensitive authentication rate limited")

func sessionTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie("scrumboy_session")
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func (s *Server) allowSensitive(l *ratelimit.Limiter, r *http.Request, userID int64) bool {
	return l != nil && l.Allow("ip:"+s.clientIP(r), "user:"+strconv.FormatInt(userID, 10))
}

func classifySecondFactor(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(normalized) == 8 {
		return "recovery"
	}
	if len(normalized) == 6 {
		for _, c := range normalized {
			if c < '0' || c > '9' {
				return "invalid"
			}
		}
		return "totp"
	}
	return "invalid"
}

func (s *Server) verifySensitiveSecondFactor(r *http.Request, u store.User, code string) (bool, int64, error) {
	if !u.IsTwoFactorActive() {
		return true, 0, nil
	}
	if !s.allowSensitive(s.secondFactorLimiter, r, u.ID) {
		return false, 0, errSensitiveRateLimited
	}
	switch classifySecondFactor(code) {
	case "recovery":
		if !s.allowSensitive(s.recoveryCodeLimiter, r, u.ID) {
			return false, 0, errSensitiveRateLimited
		}
		recoveryCodeID, err := s.store.MatchRecoveryCode(r.Context(), u.ID, code)
		return recoveryCodeID != 0, recoveryCodeID, err
	case "totp":
		if !s.allowSensitive(s.totpLimiter, r, u.ID) {
			return false, 0, errSensitiveRateLimited
		}
		secret, err := s.store.GetUserTwoFactorSecret(r.Context(), u.ID)
		if err != nil {
			return false, 0, err
		}
		return crypto.ValidateTOTPCode(secret, strings.TrimSpace(code)), 0, nil
	default:
		return false, 0, nil
	}
}

func (s *Server) authenticatedMethodUser(w http.ResponseWriter, r *http.Request) (store.User, string, bool) {
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	sessionToken := sessionTokenFromRequest(r)
	if !ok || sessionToken == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return store.User{}, "", false
	}
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return store.User{}, "", false
	}
	return u, sessionToken, true
}

func (s *Server) handleOIDCSetPasswordStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.oidcService == nil || s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	u, sessionToken, ok := s.authenticatedMethodUser(w, r)
	if !ok {
		return
	}
	if !s.allowSensitive(s.firstPasswordStartLimiter, r, u.ID) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	if u.HasLocalPassword || !u.OIDCLinked {
		writeError(w, http.StatusConflict, "CONFLICT", "first password setup is not available", nil)
		return
	}
	req, err := s.oidcService.SensitiveAuthorizationRequest(r.Context(), oidc.FlowSetPassword, u.ID, sessionToken, "/?auth_method=set_password")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func setFirstPasswordGrantCookie(w http.ResponseWriter, r *http.Request, grant string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: firstPasswordGrantCookie, Value: grant, Path: "/api/auth/oidc/set-password", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isSecureRequest(r), Expires: expires})
}

func clearFirstPasswordGrantCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: firstPasswordGrantCookie, Path: "/api/auth/oidc/set-password", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isSecureRequest(r), MaxAge: -1, Expires: time.Unix(0, 0).UTC()})
}

func firstPasswordGrantFromRequest(r *http.Request) string {
	c, err := r.Cookie(firstPasswordGrantCookie)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

func (s *Server) handleOIDCSetPasswordStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	u, sessionToken, ok := s.authenticatedMethodUser(w, r)
	if !ok {
		return
	}
	valid, err := s.store.FirstPasswordGrantValid(r.Context(), firstPasswordGrantFromRequest(r), sessionToken, u.ID)
	if err != nil {
		writeInternal(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authorized": valid, "localAuthEnabled": s.oidcService == nil || !s.oidcService.Config().LocalAuthDisabled})
}

func (s *Server) handleOIDCSetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	u, sessionToken, ok := s.authenticatedMethodUser(w, r)
	if !ok {
		return
	}
	if !s.allowSensitive(s.firstPasswordFinishLimiter, r, u.ID) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	var in struct {
		NewPassword   string `json:"newPassword"`
		TwoFactorCode string `json:"twoFactorCode"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if err := auth.ValidatePassword(in.NewPassword); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	grant := firstPasswordGrantFromRequest(r)
	valid, err := s.store.FirstPasswordGrantValid(r.Context(), grant, sessionToken, u.ID)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication", nil)
		return
	}
	verified, recoveryCodeID, err := s.verifySensitiveSecondFactor(r, u, in.TwoFactorCode)
	if errors.Is(err, errSensitiveRateLimited) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if !verified {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication", nil)
		return
	}
	if recoveryCodeID > 0 {
		err = s.store.SetFirstPasswordWithRecoveryCode(r.Context(), u.ID, grant, sessionToken, in.NewPassword, recoveryCodeID)
	} else {
		err = s.store.SetFirstPassword(r.Context(), u.ID, grant, sessionToken, in.NewPassword)
	}
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	clearFirstPasswordGrantCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleOIDCLinkStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.oidcService == nil || s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	u, sessionToken, ok := s.authenticatedMethodUser(w, r)
	if !ok {
		return
	}
	if !s.allowSensitive(s.oidcLinkStartLimiter, r, u.ID) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	if !u.HasLocalPassword || u.OIDCLinked {
		writeError(w, http.StatusConflict, "CONFLICT", "SSO connection is not available", nil)
		return
	}
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		TwoFactorCode   string `json:"twoFactorCode"`
		ReturnTo        string `json:"returnTo"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	if !s.allowSensitive(s.currentPasswordLimiter, r, u.ID) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	if _, err := s.store.AuthenticateUser(r.Context(), u.Email, in.CurrentPassword); err != nil {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication", nil)
		return
	}
	returnTo := oidc.SanitizeReturnTo(in.ReturnTo)
	if returnTo == "/" {
		returnTo = "/?auth_method=linked"
	}
	req, err := s.oidcService.SensitiveAuthorizationRequest(r.Context(), oidc.FlowLink, u.ID, sessionToken, returnTo)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OIDC is currently unavailable", nil)
		return
	}
	verified, recoveryCodeID, err := s.verifySensitiveSecondFactor(r, u, in.TwoFactorCode)
	if errors.Is(err, errSensitiveRateLimited) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	if !verified {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication", nil)
		return
	}
	if recoveryCodeID > 0 {
		consumed, err := s.store.ConsumeRecoveryCodeID(r.Context(), u.ID, recoveryCodeID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if !consumed {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid authentication", nil)
			return
		}
	}
	writeJSON(w, http.StatusOK, req)
}
