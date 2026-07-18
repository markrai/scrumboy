package httpapi

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/store"
)

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request, rest []string) {
	// /api/auth/{action} or /api/auth/login/2fa or /api/auth/2fa/{setup|enable|disable} or /api/auth/2fa/recovery/regenerate
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	// POST /api/auth/login/2fa
	if len(rest) == 2 && rest[0] == "login" && rest[1] == "2fa" {
		s.handleLogin2FA(w, r)
		return
	}
	// POST /api/auth/2fa/setup, enable, disable
	if len(rest) == 2 && rest[0] == "2fa" {
		switch rest[1] {
		case "setup":
			s.handle2FASetup(w, r)
			return
		case "enable":
			s.handle2FAEnable(w, r)
			return
		case "disable":
			s.handle2FADisable(w, r)
			return
		}
	}
	// POST /api/auth/2fa/recovery/regenerate
	if len(rest) == 3 && rest[0] == "2fa" && rest[1] == "recovery" && rest[2] == "regenerate" {
		s.handle2FARecoveryRegenerate(w, r)
		return
	}

	// POST /api/auth/reset-password - token-based; no session required
	if len(rest) == 1 && rest[0] == "reset-password" {
		s.handleAuthResetPassword(w, r)
		return
	}

	// POST /api/auth/request-password-reset - no session required; enumeration-safe
	if len(rest) == 1 && rest[0] == "request-password-reset" {
		s.handleAuthRequestPasswordReset(w, r)
		return
	}

	if len(rest) == 3 && rest[0] == "oidc" && rest[1] == "set-password" {
		switch rest[2] {
		case "start":
			s.handleOIDCSetPasswordStart(w, r)
			return
		case "status":
			s.handleOIDCSetPasswordStatus(w, r)
			return
		}
	}
	if len(rest) == 3 && rest[0] == "oidc" && rest[1] == "link" && rest[2] == "start" {
		s.handleOIDCLinkStart(w, r)
		return
	}
	// OIDC login/callback and first-password completion.
	if len(rest) == 2 && rest[0] == "oidc" {
		switch rest[1] {
		case "login":
			s.handleOIDCLogin(w, r)
			return
		case "callback":
			s.handleOIDCCallback(w, r)
			return
		case "set-password":
			s.handleOIDCSetPassword(w, r)
			return
		}
	}

	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	action := rest[0]

	switch action {
	case "status":
		// Read-only auth status endpoint so the SPA can deterministically decide whether to show login vs bootstrap.
		// Returns user info including isBootstrap flag for UI decisions.
		// In anonymous mode, returns 200 with user: null, bootstrapAvailable: false (no auth endpoints available).
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		// Anonymous mode: return noop response (no console errors, clear contract)
		if s.mode == "anonymous" {
			writeJSON(w, http.StatusOK, map[string]any{
				"user":                            nil,
				"bootstrapAvailable":              false,
				"mode":                            "anonymous",
				"pushConfigured":                  false,
				"selfServicePasswordResetEnabled": false,
				"markdownNotesEnabled":            s.markdownNotesEnabled,
				"mermaidNotesEnabled":             s.mermaidNotesEnabled,
			})
			return
		}

		ctx := s.requestContext(r)

		// bootstrapAvailable is only meaningful in full mode and indicates that no users exist yet.
		// It does NOT imply authentication is required on this request; it only tells the UI whether to show bootstrap vs login.
		n, err := s.store.CountUsers(ctx)
		if err != nil {
			writeInternal(w, err)
			return
		}
		localAuthEnabled := s.oidcService == nil || !s.oidcService.Config().LocalAuthDisabled
		bootstrapAvailable := n == 0 && localAuthEnabled

		var user any = nil
		// Fetch full user record to include isBootstrap flag
		if userID, ok := store.UserIDFromContext(ctx); ok {
			u, err := s.store.GetUser(ctx, userID)
			if err != nil {
				// If user not found, treat as unauthenticated
				user = nil
			} else {
				user = userStatusJSON(u)
			}
		}

		resp := map[string]any{
			"user":                            user,
			"bootstrapAvailable":              bootstrapAvailable,
			"mode":                            "full",
			"pushConfigured":                  s.pushVapidConfigured,
			"selfServicePasswordResetEnabled": s.selfServicePasswordResetEnabled(),
			"markdownNotesEnabled":            s.markdownNotesEnabled,
			"mermaidNotesEnabled":             s.mermaidNotesEnabled,
		}
		resp["oidcEnabled"] = s.oidcService != nil
		resp["localAuthEnabled"] = localAuthEnabled
		resp["wallEnabled"] = s.wallEnabled
		writeJSON(w, http.StatusOK, resp)
		return

	case "bootstrap":
		// Auth endpoints (except status) are not available in anonymous mode.
		if s.mode == "anonymous" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		if s.oidcService != nil && s.oidcService.Config().LocalAuthDisabled {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Name     string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		// Hard-fail once a user exists.
		if n, err := s.store.CountUsers(s.requestContext(r)); err == nil && n > 0 {
			writeError(w, http.StatusConflict, "CONFLICT", "already bootstrapped", nil)
			return
		}
		u, err := s.store.BootstrapUser(s.requestContext(r), in.Email, in.Password, in.Name)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		// Idempotent: assign existing durable projects to this user.
		if err := s.store.AssignUnownedDurableProjectsToUser(s.requestContext(r), u.ID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		// Convenience: bootstrap also logs in by creating a session.
		token, expiresAt, err := s.store.CreateSession(s.requestContext(r), u.ID, 30*24*time.Hour)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		setSessionCookie(w, r, token, expiresAt)
		writeJSON(w, http.StatusCreated, userToJSON(u))
		return

	case "login":
		// Auth endpoints (except status) are not available in anonymous mode.
		if s.mode == "anonymous" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		if s.oidcService != nil && s.oidcService.Config().LocalAuthDisabled {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		ipKey := "ip:" + s.clientIP(r)
		emailKey := "email:" + ratelimit.NormalizeEmail(in.Email)
		if s.authRateLimit != nil && !s.authRateLimit.Allow(ipKey, emailKey) {
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
			return
		}
		u, err := s.store.AuthenticateUser(s.requestContext(r), in.Email, in.Password)
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return
			}
			writeStoreErr(w, err, true)
			return
		}
		if u.IsTwoFactorActive() {
			tempToken, _, err := s.store.CreateLogin2FAPending(s.requestContext(r), u.ID, 10*time.Minute)
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"requires2fa": true,
				"tempToken":   tempToken,
				"user": map[string]any{
					"id":    u.ID,
					"email": u.Email,
					"name":  u.Name,
				},
			})
			return
		}
		// Rotate session token every login (CreateSession deletes existing sessions for this user).
		// Also assign all existing durable projects to the first/only user (idempotent).
		if err := s.store.AssignUnownedDurableProjectsToUser(s.requestContext(r), u.ID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		token, expiresAt, err := s.store.CreateSession(s.requestContext(r), u.ID, 30*24*time.Hour)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		setSessionCookie(w, r, token, expiresAt)
		writeJSON(w, http.StatusOK, userToJSON(u))
		return

	case "logout":
		// Auth endpoints (except status) are not available in anonymous mode.
		if s.mode == "anonymous" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		// Best-effort delete session (if cookie is present), then clear cookie.
		if c, err := r.Cookie("scrumboy_session"); err == nil && c != nil && c.Value != "" {
			_ = s.store.DeleteSession(s.requestContext(r), c.Value)
		}
		clearSessionCookie(w, r)
		// Return 200 + HTML with meta refresh instead of 302. Some proxies (e.g. Cloudflare Tunnel)
		// handle Set-Cookie on 302 redirects unreliably; 200 + Set-Cookie works better.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><head><meta http-equiv="refresh" content="0;url=/"></head><body>Logging out...</body></html>`))
		return

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}

func (s *Server) handleAuthResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if s.oidcService != nil && s.oidcService.Config().LocalAuthDisabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	// Rate limit by IP (reuse auth ratelimit)
	if s.authRateLimit != nil && !s.authRateLimit.Allow("ip:"+s.clientIP(r), "") {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}

	if len(s.encryptionKey) == 0 {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Password reset is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.", nil)
		return
	}

	var in struct {
		Token       string `json:"token"`
		NewPassword string `json:"new_password"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}

	userID, timestamp, signature, err := tokens.ParsePasswordResetToken(in.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid or expired reset token", nil)
		return
	}

	ctx := s.requestContext(r)
	passwordHash, err := s.store.GetUserPasswordHash(ctx, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid or expired reset token", nil)
		return
	}

	if err := tokens.VerifyPasswordResetToken(s.encryptionKey, userID, timestamp, signature, passwordHash); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid or expired reset token", nil)
		return
	}

	if err := s.store.ResetLocalPassword(ctx, userID, passwordHash, in.NewPassword); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	clearSessionCookie(w, r)

	w.WriteHeader(http.StatusOK)
}

// handleAuthRequestPasswordReset lets a user self-serve a password-reset
// email, as an alternative to the admin-generated link
// (handleAdminUsersPasswordReset). It is deliberately enumeration-safe: the
// response is always identical whether or not the submitted email matches an
// account, whether SMTP is configured, and whether the encryption key is
// configured. Only the fully-successful path (user exists, SMTP configured,
// token generated) enqueues an email. minPasswordResetRequestDuration floors
// the response time so the account-exists path's extra DB calls and token
// generation can't be timed to distinguish it from the account-not-found path.
// minPasswordResetRequestDuration is a floor on this handler's total
// response time, so that the extra DB lookups and token generation on the
// account-exists path don't create a timing side-channel for enumerating
// accounts (the response body is already identical on every path).
const minPasswordResetRequestDuration = 200 * time.Millisecond

func (s *Server) handleAuthRequestPasswordReset(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if s.oidcService != nil && s.oidcService.Config().LocalAuthDisabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	// Require application/json as defense in depth so cross-origin simple
	// text/plain POSTs cannot reach the mail path if route-level protections change.
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeValidationError(w, "Content-Type must be application/json", "invalid_content_type", map[string]any{"field": "Content-Type"})
		return
	}

	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}
	email := ratelimit.NormalizeEmail(in.Email)

	// Rate limit before any DB lookup or config check, so neither an
	// unconfigured-SMTP response nor a per-email limiter bypass can become a
	// timing or enumeration oracle. 5/min per IP, 5/min per submitted email.
	if s.passwordResetRequestLimiter != nil && !s.passwordResetRequestLimiter.Allow("ip:"+s.clientIP(r), email) {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many attempts; try again later", nil)
		return
	}

	// Generic response, identical regardless of what happens below (user not
	// found, SMTP not configured, encryption key missing, or full success).
	// This is the enumeration-safety contract for this endpoint: no branch
	// below may change status code or body shape based on account existence.
	respond := func() {
		if elapsed := time.Since(start); elapsed < minPasswordResetRequestDuration {
			time.Sleep(minPasswordResetRequestDuration - elapsed)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "If that account exists, a password reset email has been sent.",
		})
	}

	// Self-service reset requires SCRUMBOY_PUBLIC_BASE_URL (see resetBaseURL).
	// This endpoint is unauthenticated, so we fail closed when the base URL is
	// unset rather than building a link from the attacker-controlled Host header.
	if email == "" || !s.selfServicePasswordResetEnabled() {
		respond()
		return
	}

	ctx := s.requestContext(r)
	u, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		respond()
		return
	}

	passwordHash, err := s.store.GetUserPasswordHash(ctx, u.ID)
	if err != nil {
		respond()
		return
	}
	token, _, err := tokens.GeneratePasswordResetToken(s.encryptionKey, u.ID, passwordHash)
	if err != nil {
		s.logger.Printf("password reset request: generate token user=%d: %v", u.ID, err)
		respond()
		return
	}

	resetURL := s.resetBaseURL(r) + "/auth/reset-password?token=" + url.QueryEscape(token)

	s.mailQueue.Enqueue(mailDelivery{
		To:      u.Email,
		Subject: "Reset your Scrumboy password",
		Body: "A password reset was requested for your Scrumboy account.\n\n" +
			"Reset your password using this link (expires in 30 minutes):\n" + resetURL + "\n\n" +
			"If you did not request this, you can safely ignore this email.\n",
		LogRef: fmt.Sprintf("password-reset user=%d", u.ID),
	})

	respond()
}

// resetBaseURL returns the origin (scheme + host, no trailing slash) to use
// when building password-reset links, for both the self-service email flow
// above and the admin-generated link in handleAdminUsersPasswordReset.
//
// If SCRUMBOY_PUBLIC_BASE_URL is configured, it is used verbatim and the
// inbound request is never consulted. Otherwise this falls back to deriving
// the origin from X-Forwarded-Proto/Host on the request itself. The
// self-service caller above never reaches this fallback: it refuses to send
// an email at all when SCRUMBOY_PUBLIC_BASE_URL is unset, since that endpoint
// is unauthenticated and the Host header is attacker-controlled there. The
// fallback only fires for the admin-generated link, which requires an
// authenticated owner and is returned to them directly rather than emailed
// to the target user.
func (s *Server) resetBaseURL(r *http.Request) string {
	if s.publicBaseURL != "" {
		return s.publicBaseURL
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + r.Host
}
