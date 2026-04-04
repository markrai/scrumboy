package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/projectcolor"
	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	// API responses are dynamic and often session-scoped; prevent browser/proxy
	// reuse so auth transitions (login/logout) are reflected immediately.
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		// Minimal CSRF protection for a no-auth, local app:
		// cross-origin "simple" requests can still POST JSON as text/plain; requiring a custom header forces a preflight.
		// Exception: /api/auth/logout form POST (Content-Type form) — form submit can't add custom headers;
		// same-origin form POST is the standard logout pattern behind tunnels/proxies.
		// Exception: /api/auth/reset-password — token is auth; user may arrive from email/link without session.
		isLogoutForm := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/auth/logout") &&
			(strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
				strings.Contains(r.Header.Get("Content-Type"), "multipart/form-data"))
		isResetPassword := r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/auth/reset-password")
		if !isLogoutForm && !isResetPassword && r.Header.Get("X-Scrumboy") != "1" {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "missing X-Scrumboy header", nil)
			return
		}
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	switch parts[1] {
	case "projects":
		s.handleProjects(w, r, parts[2:])
		return
	case "board":
		s.handleBoard(w, r, parts[2:])
		return
	case "todos":
		s.handleTodos(w, r, parts[2:])
		return
	case "auth":
		s.handleAuth(w, r, parts[2:])
		return
	case "me":
		s.handleMe(w, r, parts[2:])
		return
	case "backup":
		s.handleBackup(w, r, parts[2:])
		return
	case "user":
		s.handleUser(w, r, parts[2:])
		return
	case "admin":
		s.handleAdmin(w, r, parts[2:])
		return
	case "version":
		s.handleVersion(w, r)
		return
	case "tags":
		s.handleTags(w, r, parts[2:])
		return
	case "dashboard":
		s.handleDashboard(w, r, parts[2:])
		return
	case "webhooks":
		s.handleWebhooks(w, r, parts[2:])
		return
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}

// handleTags handles /api/tags endpoints
func (s *Server) handleTags(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	// GET /api/tags/mine - return user's entire tag library (cross-project)
	if len(rest) == 1 && rest[0] == "mine" && r.Method == http.MethodGet {
		tags, err := s.store.ListUserTags(ctx, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tags))
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	if len(rest) >= 1 && rest[0] == "tokens" {
		s.handleMeTokens(w, r, ctx, userID, rest[1:])
		return
	}
	if len(rest) > 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	switch r.Method {
	case http.MethodGet:
		u, err := s.store.GetUser(ctx, userID)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		writeJSON(w, http.StatusOK, userToJSON(u))
		return
	case http.MethodPatch:
		var in struct {
			Image *json.RawMessage `json:"image"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Image != nil {
			if bytes.Equal(*in.Image, []byte("null")) {
				if err := s.store.UpdateUserImage(ctx, userID, nil); err != nil {
					writeStoreErr(w, err, false)
					return
				}
			} else {
				var imgStr string
				if err := json.Unmarshal(*in.Image, &imgStr); err != nil {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid image", map[string]any{"field": "image"})
					return
				}
				if imgStr == "" {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "use null to clear avatar", map[string]any{"field": "image"})
					return
				}
				if len(imgStr) > 2_000_000 {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "image too large", map[string]any{"field": "image"})
					return
				}
				if err := s.store.UpdateUserImage(ctx, userID, &imgStr); err != nil {
					writeStoreErr(w, err, false)
					return
				}
			}
		}
		u, err := s.store.GetUser(ctx, userID)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		writeJSON(w, http.StatusOK, userToJSON(u))
		return
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
}

func (s *Server) handleMeTokens(w http.ResponseWriter, r *http.Request, ctx context.Context, userID int64, rest []string) {
	switch len(rest) {
	case 0:
		switch r.Method {
		case http.MethodGet:
			tokens, err := s.store.ListUserAPITokens(ctx, userID)
			if err != nil {
				writeStoreErr(w, err, false)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": apiTokensToJSON(tokens)})
			return
		case http.MethodPost:
			var in struct {
				Name *string `json:"name"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return
			}
			var namePtr *string
			if in.Name != nil {
				n := strings.TrimSpace(*in.Name)
				if n != "" {
					namePtr = &n
				}
			}
			id, plain, createdAt, err := s.store.CreateUserAPIToken(ctx, userID, namePtr)
			if err != nil {
				writeStoreErr(w, err, false)
				return
			}
			writeJSON(w, http.StatusCreated, apiTokenCreateJSON{
				ID:        id,
				Name:      namePtr,
				CreatedAt: createdAt,
				Token:     plain,
			})
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
	case 1:
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		tokenID, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil || tokenID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid token id", map[string]any{"field": "id"})
			return
		}
		if err := s.store.RevokeUserAPIToken(ctx, userID, tokenID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				return
			}
			writeStoreErr(w, err, false)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	}
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request, rest []string) {
	// User endpoints require authentication
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	if rest[0] == "preferences" {
		s.handleUserPreferences(w, r, userID)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleUserPreferences(w http.ResponseWriter, r *http.Request, userID int64) {
	ctx := s.requestContext(r)

	switch r.Method {
	case http.MethodGet:
		// GET /api/user/preferences?key=tagColors
		key := r.URL.Query().Get("key")
		if key == "" {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "missing key parameter", nil)
			return
		}
		value, err := s.store.GetUserPreference(ctx, userID, key)
		if err != nil {
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"value": value})
		return

	case http.MethodPut:
		// PUT /api/user/preferences - Body: { key: string, value: string }
		var in struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Key == "" {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "missing key", nil)
			return
		}
		if err := s.store.SetUserPreference(ctx, userID, in.Key, in.Value); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
}

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

	// POST /api/auth/reset-password — token-based; no session required
	if len(rest) == 1 && rest[0] == "reset-password" {
		s.handleAuthResetPassword(w, r)
		return
	}

	// GET /api/auth/oidc/login, GET /api/auth/oidc/callback
	if len(rest) == 2 && rest[0] == "oidc" {
		switch rest[1] {
		case "login":
			s.handleOIDCLogin(w, r)
			return
		case "callback":
			s.handleOIDCCallback(w, r)
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
				"user":               nil,
				"bootstrapAvailable": false,
				"mode":               "anonymous",
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
			"user":               user,
			"bootstrapAvailable": bootstrapAvailable,
			"mode":               "full",
		}
		resp["oidcEnabled"] = s.oidcService != nil
		resp["localAuthEnabled"] = localAuthEnabled
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
		ipKey := "ip:" + clientIP(r)
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

// requireProjectRole checks that the user in ctx has at least requiredRole for the project.
// Returns ErrUnauthorized if unauthenticated; ErrNotFound if no project access (caller maps to 404).
func (s *Server) requireProjectRole(ctx context.Context, projectID int64, requiredRole store.ProjectRole) error {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return store.ErrUnauthorized
	}
	return s.store.CheckProjectRole(ctx, projectID, userID, requiredRole)
}

// requireProjectMaintainerOrHigher checks that the user has Maintainer+ for the project.
func (s *Server) requireProjectMaintainerOrHigher(ctx context.Context, projectID int64) error {
	return s.requireProjectRole(ctx, projectID, store.RoleMaintainer)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request, rest []string) {
	// In anonymous deployment mode, block most numeric-ID project routes.
	// Only slug-based routes (/api/board/{slug}) are allowed for pastebin semantics.
	//
	// Exception: PATCH /api/projects/{id} is allowed for renaming anonymous temp boards.
	//
	// CRITICAL: This routing exception is permissive by design. Authorization MUST remain
	// enforced at the store boundary (UpdateProjectName). If store-layer authorization is
	// removed, modified, or bypassed, this could open a security hole.
	//
	// Store authorization enforces:
	// - Only anonymous temp boards (expires_at IS NOT NULL AND creator_user_id IS NULL) can be renamed without auth
	// - All other boards require proper authorization (contributor role or higher)
	//
	// See TestAnonymousMode_RenameProjectAuthorization for test coverage.
	if s.mode == "anonymous" {
		// Allow PATCH requests through - authorization will be checked in the store layer
		if len(rest) == 1 && r.Method == http.MethodPatch {
			// Continue to PATCH handler below
		} else {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
	}
	// /api/projects
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodGet:
			// If auth is enabled, require authentication.
			if _, ok := store.UserIDFromContext(s.requestContext(r)); !ok {
				if n, err := s.store.CountUsers(s.requestContext(r)); err == nil && n > 0 {
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
					return
				}
			}
			projects, err := s.store.ListProjects(s.requestContext(r))
			if err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
					return
				}
				writeInternal(w, err)
				return
			}
			writeJSON(w, http.StatusOK, projectsToJSON(projects))
			return

		case http.MethodPost:
			var in struct {
				Name     string `json:"name"`
				Workflow *[]struct {
					Key      string `json:"key"`
					Name     string `json:"name"`
					Color    string `json:"color"`
					Position int    `json:"position"`
					IsDone   bool   `json:"isDone"`
				} `json:"workflow"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return
			}
			var workflow []store.WorkflowColumn
			if in.Workflow != nil {
				workflow = make([]store.WorkflowColumn, 0, len(*in.Workflow))
				for _, col := range *in.Workflow {
					workflow = append(workflow, store.WorkflowColumn{
						Key:      col.Key,
						Name:     col.Name,
						Color:    col.Color,
						Position: col.Position,
						IsDone:   col.IsDone,
					})
				}
			}
			p, err := s.store.CreateProjectWithWorkflow(s.requestContext(r), in.Name, workflow)
			if err != nil {
				writeStoreErr(w, err, false)
				return
			}
			writeJSON(w, http.StatusCreated, projectToJSON(p))
			return

		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
	}

	// /api/projects/{id}/...
	projectID, ok := parseInt64(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid project id", map[string]any{"field": "projectId"})
		return
	}

	if len(rest) == 1 {
		switch r.Method {
		case http.MethodDelete:
			ctx := s.requestContext(r)
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return
			}
			if err := s.store.DeleteProject(ctx, projectID, userID); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), projectID, "project_deleted")
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPatch:
			ctx := s.requestContext(r)
			userID, hasUser := store.UserIDFromContext(ctx)
			var in struct {
				Name  *string `json:"name"`
				Image *string `json:"image"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return
			}
			if in.Image != nil {
				if !hasUser {
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
					return
				}
				color := projectcolor.ExtractFromDataURL(*in.Image)
				if err := s.store.UpdateProjectImage(ctx, projectID, userID, in.Image, color); err != nil {
					writeStoreErr(w, err, true)
					return
				}
			}
			if in.Name != nil {
				uid := int64(0)
				if hasUser {
					uid = userID
				}
				if err := s.store.UpdateProjectName(ctx, projectID, uid, *in.Name); err != nil {
					writeStoreErr(w, err, true)
					return
				}
			}
			project, err := s.store.GetProject(s.requestContext(r), projectID)
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			if in.Name != nil || in.Image != nil {
				s.emitRefreshNeeded(r.Context(), projectID, "project_updated")
			}
			writeJSON(w, http.StatusOK, projectToJSON(project))
			return
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
	}

	if len(rest) == 2 && rest[1] == "board" && r.Method == http.MethodGet {
		pc, err := s.store.GetProjectContextForRead(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		tag := r.URL.Query().Get("tag")
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if search == "" {
			search = ""
		}
		sprintFilter, err := s.parseSprintFilterFromQuery(r, projectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
			return
		}
		project, tags, workflow, cols, err := s.store.GetBoard(s.requestContext(r), &pc, tag, search, sprintFilter)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, boardToJSON(project, workflow, tags, cols))
		return
	}

	if len(rest) == 2 && rest[1] == "burndown" && r.Method == http.MethodGet {
		points, err := s.store.GetRealBurndown(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return
	}

	// GET /api/projects/{id}/backlog-size
	if len(rest) == 2 && rest[1] == "backlog-size" && r.Method == http.MethodGet {
		points, err := s.store.GetBacklogSize(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, burndownToJSON(points))
		return
	}

	// GET /api/projects/{id}/members
	if len(rest) == 2 && rest[1] == "members" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		members, err := s.store.ListProjectMembers(ctx, projectID, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return
	}

	// POST /api/projects/{id}/members
	if len(rest) == 2 && rest[1] == "members" && r.Method == http.MethodPost {
		var in struct {
			UserID int64  `json:"user_id"`
			Role   string `json:"role"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		// Parse and validate role (must be viewer, contributor, or maintainer)
		role, ok := store.ParseProjectRole(in.Role)
		if !ok || !store.IsValidProjectRole(role) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid role", map[string]any{"field": "role"})
			return
		}

		userID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}

		if err := s.store.AddProjectMember(s.requestContext(r), userID, projectID, in.UserID, role); err != nil {
			writeStoreErr(w, err, true)
			return
		}

		// Return updated membership list
		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return
	}

	// DELETE /api/projects/{id}/members/{user_id}
	if len(rest) == 3 && rest[1] == "members" && r.Method == http.MethodDelete {
		targetUserID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid user id", map[string]any{"field": "userId"})
			return
		}

		requesterID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}

		if err := s.store.RemoveProjectMember(s.requestContext(r), requesterID, projectID, targetUserID); err != nil {
			writeStoreErr(w, err, true)
			return
		}

		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, requesterID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return
	}

	// PATCH /api/projects/{id}/members/{user_id}
	if len(rest) == 3 && rest[1] == "members" && r.Method == http.MethodPatch {
		targetUserID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid user id", map[string]any{"field": "userId"})
			return
		}
		var in struct {
			Role string `json:"role"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		role, ok := store.ParseMemberRole(in.Role)
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid role", map[string]any{"field": "role"})
			return
		}
		requesterID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		if err := s.store.UpdateProjectMemberRole(s.requestContext(r), requesterID, projectID, targetUserID, role); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrConflict):
				writeError(w, http.StatusConflict, "CONFLICT", err.Error(), nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			case errors.Is(err, store.ErrValidation):
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "role"})
			default:
				writeStoreErr(w, err, true)
			}
			return
		}
		members, err := s.store.ListProjectMembers(s.requestContext(r), projectID, requesterID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitMembersUpdated(r.Context(), projectID)
		writeJSON(w, http.StatusOK, projectMembersToJSON(members))
		return
	}

	// GET /api/projects/{id}/available-users
	if len(rest) == 2 && rest[1] == "available-users" && r.Method == http.MethodGet {
		userID, ok := store.UserIDFromContext(s.requestContext(r))
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}

		users, err := s.store.ListAvailableUsersForProject(s.requestContext(r), userID, projectID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}

		// Convert to JSON
		usersJSON := make([]userJSON, 0, len(users))
		for _, u := range users {
			usersJSON = append(usersJSON, userToJSON(u))
		}
		writeJSON(w, http.StatusOK, usersJSON)
		return
	}

	// GET /api/projects/{id}/tags - return all tags used in project (grouped by name)
	if len(rest) == 2 && rest[1] == "tags" && r.Method == http.MethodGet {
		pc, err := s.store.GetProjectContextForRead(s.requestContext(r), projectID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		tags, err := s.store.ListTagCounts(s.requestContext(r), &pc)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		// Convert TagCount to TagWithColor (tag_id and canDelete from store)
		tagList := make([]store.TagWithColor, len(tags))
		for i, tc := range tags {
			tagList[i] = store.TagWithColor{
				TagID:     tc.TagID,
				Name:      tc.Name,
				Color:     tc.Color,
				CanDelete: tc.CanDelete,
			}
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tagList))
		return
	}

	// PATCH /api/projects/{id}/tags/id/{tagId}/color - update tag color by tag_id (required for durable projects).
	if len(rest) == 5 && rest[1] == "tags" && rest[2] == "id" && rest[4] == "color" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return
		}
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}
		if err := s.store.UpdateTagColor(ctx, viewerUserID, tagID, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), projectID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// PATCH /api/projects/{id}/tags/{tagName}/color - update tag color by name.
	// Name-based mutation routes are anonymous-only. Durable projects must use tag_id.
	if len(rest) == 4 && rest[1] == "tags" && rest[3] == "color" && r.Method == http.MethodPatch {
		// Projects handler is durable-only; reject name-based mutation
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based color update not allowed for durable projects; use /tags/id/{tagId}/color", nil)
		return
	}

	// DELETE /api/projects/{id}/tags/id/{tagId} - delete by tag_id (required for durable projects).
	if len(rest) == 4 && rest[1] == "tags" && rest[2] == "id" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return
		}
		if err := s.store.DeleteTag(ctx, userID, tagID, false); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), projectID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// DELETE /api/projects/{id}/tags/{tagName} - delete by name.
	// Name-based mutation routes are anonymous-only. Durable projects must use tag_id.
	if len(rest) == 3 && rest[1] == "tags" && r.Method == http.MethodDelete {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based delete not allowed for durable projects; use /tags/id/{tagId}", nil)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

// parseSprintFilterFromQuery parses sprintId from the request query and returns a SprintFilter.
// Absence of sprintId → Mode "none" (no sprint filter).
// sprintId=scheduled → Mode "scheduled" (sprint_id IS NOT NULL, i.e. "Scheduled" view). "assigned" is accepted for backward compatibility.
// sprintId=unscheduled → Mode "unscheduled" (sprint_id IS NULL).
// sprintId=<number> → project-local sprint number (resolved inline by board queries).
// Returns error for invalid values (caller should respond 400).
func (s *Server) parseSprintFilterFromQuery(r *http.Request, projectID int64) (store.SprintFilter, error) {
	v := r.URL.Query().Get("sprintId")
	if v == "" {
		return store.SprintFilter{Mode: "none"}, nil
	}
	if v == "scheduled" || v == "assigned" {
		return store.SprintFilter{Mode: "scheduled"}, nil
	}
	if v == "unscheduled" {
		return store.SprintFilter{Mode: "unscheduled"}, nil
	}
	n, ok := parseInt64(v)
	if !ok {
		return store.SprintFilter{}, fmt.Errorf("invalid sprintId: %q", v)
	}
	if n < 1 {
		return store.SprintFilter{}, fmt.Errorf("invalid sprintId: %q", v)
	}
	// Do not pre-query the sprint table here. Board SQL resolves sprint number inline,
	// avoiding an extra DB round-trip per board load when sprint filtering is active.
	return store.SprintFilter{Mode: "sprint_number", SprintNumber: n}, nil
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	slug, ok := parseSlug(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid slug", map[string]any{"field": "slug"})
		return
	}

	pc, err := s.store.GetProjectContextBySlug(s.requestContext(r), slug, s.storeMode())
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}
	project := pc.Project

	// GET /api/board/{slug}/events
	if len(rest) == 2 && rest[1] == "events" && r.Method == http.MethodGet {
		s.handleBoardEvents(w, r, project.ID)
		return
	}

	// GET /api/board/{slug}
	// Always use paged response (default limitPerLane=20) so mobile and cached clients get columnsMeta and limited items.
	if len(rest) == 1 && r.Method == http.MethodGet {
		tag := r.URL.Query().Get("tag")
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		if search == "" {
			search = ""
		}
		hasSprints, err := s.store.HasSprints(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		var sprintFilter store.SprintFilter
		if !hasSprints {
			sprintFilter = store.SprintFilter{Mode: "none"}
		} else {
			sprintFilter, err = s.parseSprintFilterFromQuery(r, project.ID)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
				return
			}
		}
		limitPerLane := 20
		if v := r.URL.Query().Get("limitPerLane"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limitPerLane = n
			}
		}
		project2, tags, workflow, cols, meta, err := s.store.GetBoardPaged(s.requestContext(r), &pc, tag, search, sprintFilter, limitPerLane)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, boardToJSONWithMeta(project2, workflow, tags, cols, meta))
		return
	}

	// PATCH /api/board/{slug}/settings - update board/project-level settings.
	if len(rest) == 2 && rest[1] == "settings" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}

		var in struct {
			DefaultSprintWeeks *int `json:"defaultSprintWeeks"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.DefaultSprintWeeks == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "defaultSprintWeeks required", map[string]any{"field": "defaultSprintWeeks"})
			return
		}
		if *in.DefaultSprintWeeks != 1 && *in.DefaultSprintWeeks != 2 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "defaultSprintWeeks must be 1 or 2", map[string]any{"field": "defaultSprintWeeks"})
			return
		}
		if project.DefaultSprintWeeks == *in.DefaultSprintWeeks {
			writeJSON(w, http.StatusOK, map[string]any{"defaultSprintWeeks": *in.DefaultSprintWeeks})
			return
		}

		if err := s.store.UpdateProjectDefaultSprintWeeks(ctx, project.ID, userID, *in.DefaultSprintWeeks); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "project_settings_updated")
		writeJSON(w, http.StatusOK, map[string]any{"defaultSprintWeeks": *in.DefaultSprintWeeks})
		return
	}

	// GET /api/board/{slug}/workflow/counts - unfiltered todo counts per lane (maintainer+).
	if len(rest) == 3 && rest[1] == "workflow" && rest[2] == "counts" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		counts, err := s.store.CountTodosByColumnKey(ctx, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if counts == nil {
			counts = map[string]int{}
		}
		writeJSON(w, http.StatusOK, workflowLaneCountsJSON{
			Slug:              project.Slug,
			CountsByColumnKey: counts,
		})
		return
	}

	// POST /api/board/{slug}/workflow - add a new non-done lane before done.
	if len(rest) == 2 && rest[1] == "workflow" && r.Method == http.MethodPost {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}

		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return
		}
		if len(in.Name) > 200 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column name", map[string]any{"field": "name"})
			return
		}

		col, err := s.store.AddWorkflowColumn(ctx, project.ID, in.Name)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_added")
		writeJSON(w, http.StatusCreated, workflowColumnJSON{
			Key:      col.Key,
			Name:     col.Name,
			Color:    col.Color,
			IsDone:   col.IsDone,
			Position: col.Position,
		})
		return
	}

	// PATCH /api/board/{slug}/workflow/{key} - update workflow lane label and color.
	if len(rest) == 3 && rest[1] == "workflow" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow key", map[string]any{"field": "key"})
			return
		}

		var in struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		in.Color = strings.TrimSpace(in.Color)
		if in.Name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return
		}
		if len(in.Name) > 200 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column name", map[string]any{"field": "name"})
			return
		}
		if in.Color == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "color required", map[string]any{"field": "color"})
			return
		}
		if !store.ValidWorkflowColumnColor(in.Color) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow column color", map[string]any{"field": "color"})
			return
		}
		if err := s.store.UpdateWorkflowColumn(ctx, project.ID, columnKey, in.Name, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_updated")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// DELETE /api/board/{slug}/workflow/{key} - delete an empty non-done lane.
	if len(rest) == 3 && rest[1] == "workflow" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		columnKey := strings.TrimSpace(rest[2])
		if columnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid workflow key", map[string]any{"field": "key"})
			return
		}
		if err := s.store.DeleteWorkflowColumn(ctx, project.ID, columnKey); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "workflow_column_deleted")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /api/board/{slug}/lanes/{status}
	if len(rest) == 3 && rest[1] == "lanes" && r.Method == http.MethodGet {
		columnKey := normalizeLaneKey(rest[2])
		tag := r.URL.Query().Get("tag")
		search := strings.TrimSpace(r.URL.Query().Get("search"))
		sprintFilter, err := s.parseSprintFilterFromQuery(r, project.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "sprintId"})
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
				limit = n
			}
		}
		afterCursor := r.URL.Query().Get("afterCursor")
		afterRank, afterID := store.ParseLaneCursor(afterCursor)

		items, nextCursor, hasMore, err := s.store.ListTodosForBoardLane(s.requestContext(r), project.ID, columnKey, limit, afterRank, afterID, tag, search, sprintFilter)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, lanePageToJSON(items, nextCursor, hasMore))
		return
	}

	// POST /api/board/{slug}/claim
	// Escape hatch: convert an unowned temporary board into an owned durable project.
	// No UI assumptions; server-side only.
	if len(rest) == 2 && rest[1] == "claim" && r.Method == http.MethodPost {
		// Not available in anonymous mode (no auth).
		if s.mode == "anonymous" {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		if err := s.store.ClaimTemporaryBoard(ctx, project.ID, userID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "board_claimed")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// POST /api/board/{slug}/todos
	if len(rest) == 2 && rest[1] == "todos" && r.Method == http.MethodPost {
		var in struct {
			Title            string   `json:"title"`
			Body             string   `json:"body"`
			Tags             []string `json:"tags"`
			ColumnKey        string   `json:"columnKey"`
			Status           string   `json:"status"`
			EstimationPoints *int64   `json:"estimationPoints"`
			SprintId         *int64   `json:"sprintId"`
			AssigneeUserId   *int64   `json:"assigneeUserId"`
			Position         *struct {
				AfterID  *int64 `json:"afterId"`
				BeforeID *int64 `json:"beforeId"`
			} `json:"position"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		columnKey := normalizeLaneKey(in.ColumnKey)
		if columnKey == "" && in.Status != "" {
			columnKey = normalizeLaneKey(in.Status)
		}
		if columnKey == "" {
			columnKey = store.DefaultColumnBacklog
		}

		var afterID, beforeID *int64
		if in.Position != nil {
			afterID = in.Position.AfterID
			beforeID = in.Position.BeforeID
		}

		todo, err := s.store.CreateTodo(s.requestContext(r), project.ID, store.CreateTodoInput{
			Title:            in.Title,
			Body:             in.Body,
			Tags:             in.Tags,
			ColumnKey:        columnKey,
			EstimationPoints: in.EstimationPoints,
			SprintID:         in.SprintId,
			AssigneeUserID:   in.AssigneeUserId,
			AfterID:          afterID,
			BeforeID:         beforeID,
		}, s.storeMode())
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			writeStoreErr(w, err, true)
			return
		}
		if !todo.AssignmentChanged {
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_created")
		}
		writeJSON(w, http.StatusCreated, todoToJSON(todo))
		return
	}

	// GET /api/board/{slug}/todos/search
	// NOTE: must be before /todos/{localId} parsing.
	if len(rest) == 3 && rest[1] == "todos" && rest[2] == "search" && r.Method == http.MethodGet {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
				limit = n
			}
		}

		var exclude []int64
		for _, raw := range strings.Split(r.URL.Query().Get("exclude"), ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
				exclude = append(exclude, n)
			}
		}

		items, err := s.store.SearchTodosForLinkPicker(s.requestContext(r), project.ID, q, limit, exclude, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, map[string]any{
				"localId": it.LocalID,
				"title":   it.Title,
			})
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// GET/POST/DELETE /api/board/{slug}/todos/{localId}/links[/targetLocalId]
	// NOTE: must be before /todos/{localId} parsing.
	if len(rest) >= 4 && rest[1] == "todos" && rest[3] == "links" {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
			return
		}

		// Ensure source todo exists and caller can read it.
		if _, err := s.store.GetTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode()); err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return
		}

		switch {
		case len(rest) == 4 && r.Method == http.MethodGet:
			outbound, err := s.store.ListLinksForTodo(s.requestContext(r), project.ID, localID, s.storeMode())
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			inbound, err := s.store.ListBacklinksForTodo(s.requestContext(r), project.ID, localID, s.storeMode())
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}

			outboundJSON := make([]map[string]any, 0, len(outbound))
			for _, t := range outbound {
				outboundJSON = append(outboundJSON, map[string]any{
					"localId":  t.LocalID,
					"title":    t.Title,
					"linkType": t.LinkType,
				})
			}
			inboundJSON := make([]map[string]any, 0, len(inbound))
			for _, t := range inbound {
				inboundJSON = append(inboundJSON, map[string]any{
					"localId":  t.LocalID,
					"title":    t.Title,
					"linkType": t.LinkType,
				})
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"outbound": outboundJSON,
				"inbound":  inboundJSON,
			})
			return

		case len(rest) == 4 && r.Method == http.MethodPost:
			var in struct {
				TargetLocalID int64  `json:"targetLocalId"`
				LinkType      string `json:"linkType"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return
			}
			if in.TargetLocalID <= 0 {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "targetLocalId required", map[string]any{"field": "targetLocalId"})
				return
			}
			if in.TargetLocalID == localID {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "cannot link todo to itself", map[string]any{"field": "targetLocalId"})
				return
			}
			if in.LinkType == "" {
				in.LinkType = "relates_to"
			}

			if err := s.store.AddLink(s.requestContext(r), project.ID, localID, in.TargetLocalID, in.LinkType, s.storeMode()); err != nil {
				switch {
				case errors.Is(err, store.ErrValidation):
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid link", nil)
				case errors.Is(err, store.ErrNotFound):
					writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				case errors.Is(err, store.ErrUnauthorized):
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				default:
					writeStoreErr(w, err, true)
				}
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_links_updated")
			w.WriteHeader(http.StatusNoContent)
			return

		case len(rest) == 5 && r.Method == http.MethodDelete:
			targetLocalID, ok := parseInt64(rest[4])
			if !ok {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid targetLocalId", map[string]any{"field": "targetLocalId"})
				return
			}
			// Idempotent delete: remove if present, return 204 either way.
			if err := s.store.RemoveLink(s.requestContext(r), project.ID, localID, targetLocalID, s.storeMode()); err != nil {
				switch {
				case errors.Is(err, store.ErrUnauthorized):
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				case errors.Is(err, store.ErrNotFound):
					writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				default:
					writeStoreErr(w, err, true)
				}
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_links_updated")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// GET /api/board/{slug}/todos/{localId}
	if len(rest) == 3 && rest[1] == "todos" && r.Method == http.MethodGet {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
			return
		}
		todo, err := s.store.GetTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode())
		if err != nil {
			switch {
			case errors.Is(err, store.ErrNotFound):
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			case errors.Is(err, store.ErrUnauthorized):
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
			default:
				writeStoreErr(w, err, true)
			}
			return
		}
		writeJSON(w, http.StatusOK, todoToJSON(todo))
		return
	}

	// PATCH/DELETE /api/board/{slug}/todos/{localId}
	if len(rest) == 3 && rest[1] == "todos" && (r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var raw map[string]json.RawMessage
			if err := readJSON(w, r, s.maxBody, &raw); err != nil {
				return
			}
			if _, ok := raw["assigneeUserId"]; !ok {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing assigneeUserId", map[string]any{"field": "assigneeUserId"})
				return
			}

			var in struct {
				Title            string   `json:"title"`
				Body             string   `json:"body"`
				Tags             []string `json:"tags"`
				EstimationPoints *int64   `json:"estimationPoints"`
				AssigneeUserId   *int64   `json:"assigneeUserId"`
				SprintId         *int64   `json:"sprintId"`
			}
			payload, err := json.Marshal(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return
			}
			if err := json.Unmarshal(payload, &in); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return
			}
			updateIn := store.UpdateTodoInput{
				Title:            in.Title,
				Body:             in.Body,
				Tags:             in.Tags,
				EstimationPoints: in.EstimationPoints,
				AssigneeUserID:   in.AssigneeUserId,
			}
			if _, hasSprintId := raw["sprintId"]; hasSprintId {
				if in.SprintId == nil {
					updateIn.ClearSprint = true
				} else {
					updateIn.SprintID = in.SprintId
				}
			}
			todo, err := s.store.UpdateTodoByLocalID(s.requestContext(r), project.ID, localID, updateIn, s.storeMode())
			if err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
					return
				}
				writeStoreErr(w, err, true)
				return
			}
			if !todo.AssignmentChanged {
				s.emitRefreshNeeded(r.Context(), project.ID, "todo_updated")
			}
			writeJSON(w, http.StatusOK, todoToJSON(todo))
			return
		case http.MethodDelete:
			if err := s.store.DeleteTodoByLocalID(s.requestContext(r), project.ID, localID, s.storeMode()); err != nil {
				if errors.Is(err, store.ErrUnauthorized) {
					writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
					return
				}
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "todo_deleted")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// POST /api/board/{slug}/todos/{localId}/move
	if len(rest) == 4 && rest[1] == "todos" && rest[3] == "move" && r.Method == http.MethodPost {
		localID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo localId", map[string]any{"field": "localId"})
			return
		}
		var in struct {
			ToColumnKey string `json:"toColumnKey"`
			ToStatus    string `json:"toStatus"`
			AfterID     *int64 `json:"afterId"`
			BeforeID    *int64 `json:"beforeId"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		toColumnKey := in.ToColumnKey
		if toColumnKey == "" && in.ToStatus != "" {
			toColumnKey = normalizeLaneKey(in.ToStatus)
		}
		if toColumnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing toColumnKey", map[string]any{"field": "toColumnKey"})
			return
		}
		// Interpret afterId/beforeId as localIds for this project.
		todo, err := s.store.MoveTodoByLocalID(s.requestContext(r), project.ID, localID, toColumnKey, in.AfterID, in.BeforeID, s.storeMode())
		if err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "todo_moved")
		writeJSON(w, http.StatusOK, todoToJSON(todo))
		return
	}

	// GET /api/board/{slug}/sprints - list sprints with todoCount and unscheduledCount
	if len(rest) == 2 && rest[1] == "sprints" && r.Method == http.MethodGet {
		sprints, err := s.store.ListSprintsWithTodoCount(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if len(sprints) == 0 {
			writeJSON(w, http.StatusNoContent, nil)
			return
		}
		unscheduledCount, err := s.store.CountUnscheduledTodos(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sprints": sprintsWithTodoCountToJSON(sprints), "unscheduledCount": unscheduledCount})
		return
	}

	// POST /api/board/{slug}/sprints - create sprint (Maintainer+)
	if len(rest) == 2 && rest[1] == "sprints" && r.Method == http.MethodPost {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		var in struct {
			Name           string `json:"name"`
			PlannedStartAt int64  `json:"plannedStartAt"`
			PlannedEndAt   int64  `json:"plannedEndAt"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Name == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name required", map[string]any{"field": "name"})
			return
		}
		sprint, err := s.store.CreateSprint(ctx, project.ID, in.Name, time.UnixMilli(in.PlannedStartAt), time.UnixMilli(in.PlannedEndAt))
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_created")
		writeJSON(w, http.StatusCreated, sprintToJSON(sprint))
		return
	}

	// GET /api/board/{slug}/sprints/active - get active sprint
	if len(rest) == 3 && rest[1] == "sprints" && rest[2] == "active" && r.Method == http.MethodGet {
		sp, err := s.store.GetActiveSprintByProjectID(s.requestContext(r), project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if sp == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, sprintToJSON(*sp))
		return
	}

	// GET/PATCH/DELETE /api/board/{slug}/sprints/{sprintId}
	if len(rest) == 3 && rest[1] == "sprints" {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return
		}
		sp, err := s.store.GetSprintByID(s.requestContext(r), sprintID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if sp.ProjectID != project.ID {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "sprint not found", nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, sprintToJSON(sp))
			return
		case http.MethodPatch:
			ctx := s.requestContext(r)
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return
			}
			role, err := s.store.GetProjectRole(ctx, project.ID, userID)
			if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
				return
			}
			var in struct {
				Name           *string `json:"name"`
				PlannedStartAt *int64  `json:"plannedStartAt"`
				PlannedEndAt   *int64  `json:"plannedEndAt"`
			}
			if err := readJSON(w, r, s.maxBody, &in); err != nil {
				return
			}
			opts := store.UpdateSprintInput{}
			if in.Name != nil {
				opts.Name = in.Name
			}
			if in.PlannedStartAt != nil {
				t := time.UnixMilli(*in.PlannedStartAt)
				opts.PlannedStartAt = &t
			}
			if in.PlannedEndAt != nil {
				t := time.UnixMilli(*in.PlannedEndAt)
				opts.PlannedEndAt = &t
			}
			if err := s.store.UpdateSprint(ctx, sprintID, opts); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "sprint_updated")
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodDelete:
			ctx := s.requestContext(r)
			userID, ok := store.UserIDFromContext(ctx)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
				return
			}
			role, err := s.store.GetProjectRole(ctx, project.ID, userID)
			if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
				return
			}
			if err := s.store.DeleteSprint(ctx, project.ID, sprintID); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "sprint_deleted")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// GET /api/board/{slug}/sprints/{sprintId}/burndown - sprint-scoped burndown
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "burndown" && r.Method == http.MethodGet {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return
		}
		points, err := s.store.GetRealBurndownForSprint(s.requestContext(r), project.ID, sprintID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return
	}

	// POST /api/board/{slug}/sprints/{sprintId}/activate - activate sprint (Maintainer+)
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "activate" && r.Method == http.MethodPost {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return
		}
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		if err := s.store.ActivateSprint(ctx, project.ID, sprintID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_activated")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// POST /api/board/{slug}/sprints/{sprintId}/close - close sprint (Maintainer+)
	if len(rest) == 4 && rest[1] == "sprints" && rest[3] == "close" && r.Method == http.MethodPost {
		sprintID, ok := parseInt64(rest[2])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid sprintId", map[string]any{"field": "sprintId"})
			return
		}
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		role, err := s.store.GetProjectRole(ctx, project.ID, userID)
		if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required", nil)
			return
		}
		if err := s.store.CloseSprint(ctx, sprintID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "sprint_closed")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /api/board/{slug}/tags - return all tags used in project (grouped by name)
	if len(rest) == 2 && rest[1] == "tags" && r.Method == http.MethodGet {
		tags, err := s.store.ListTagCounts(s.requestContext(r), &pc)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		// Convert TagCount to TagWithColor (tag_id and canDelete from store)
		tagList := make([]store.TagWithColor, len(tags))
		for i, tc := range tags {
			tagList[i] = store.TagWithColor{
				TagID:     tc.TagID,
				Name:      tc.Name,
				Color:     tc.Color,
				CanDelete: tc.CanDelete,
			}
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tagList))
		return
	}

	// GET /api/board/{slug}/tags/user - return user's tags for autocomplete
	if len(rest) == 3 && rest[1] == "tags" && rest[2] == "user" && r.Method == http.MethodGet {
		ctx := s.requestContext(r)
		userID, ok := store.UserIDFromContext(ctx)
		if !ok {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		tags, err := s.store.ListUserTagsForProject(ctx, userID, project.ID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, tagsToJSON(tags))
		return
	}

	// PATCH /api/board/{slug}/tags/id/{tagId}/color - update tag color by tag_id (works for both durable and anonymous).
	if len(rest) == 5 && rest[1] == "tags" && rest[2] == "id" && rest[4] == "color" && r.Method == http.MethodPatch {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return
		}
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}
		if err := s.store.UpdateTagColor(ctx, viewerUserID, tagID, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// PATCH /api/board/{slug}/tags/{tagName}/color - update tag color by name (anonymous-only).
	// Name-based mutation routes are anonymous-only. Durable projects must use tag_id.
	if len(rest) == 4 && rest[1] == "tags" && rest[3] == "color" && r.Method == http.MethodPatch {
		isAnonymousBoard := project.ExpiresAt != nil && project.CreatorUserID == nil
		if !isAnonymousBoard {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based color update not allowed for durable projects; use /tags/id/{tagId}/color", nil)
			return
		}
		ctx := s.requestContext(r)
		tagName := rest[2]
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		var viewerUserID *int64
		if userID, ok := store.UserIDFromContext(ctx); ok {
			viewerUserID = &userID
		}

		if err := s.store.UpdateTagColorForProject(ctx, project.ID, viewerUserID, tagName, in.Color, isAnonymousBoard); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_color_updated")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// DELETE /api/board/{slug}/tags/id/{tagId} - delete by tag_id (preferred; authority by tag_id).
	isAnonymousBoard := project.ExpiresAt != nil && project.CreatorUserID == nil
	if len(rest) == 4 && rest[1] == "tags" && rest[2] == "id" && r.Method == http.MethodDelete {
		ctx := s.requestContext(r)
		var tagID int64
		if _, err := fmt.Sscanf(rest[3], "%d", &tagID); err != nil || tagID <= 0 {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid tagId", map[string]any{"field": "tagId"})
			return
		}
		userID, _ := store.UserIDFromContext(ctx)
		if err := s.store.DeleteTag(ctx, userID, tagID, isAnonymousBoard); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// DELETE /api/board/{slug}/tags/{tagName} - delete by name (anonymous-only).
	// Name-based mutation routes are anonymous-only. Durable projects must use tag_id.
	if len(rest) == 3 && rest[1] == "tags" && r.Method == http.MethodDelete {
		if !isAnonymousBoard {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name-based delete not allowed for durable projects; use /tags/id/{tagId}", nil)
			return
		}
		ctx := s.requestContext(r)
		tagName := rest[2]
		userID, hasUserID := store.UserIDFromContext(ctx)

		boardTagID, err := s.store.GetBoardScopedTagIDByName(ctx, project.ID, tagName)
		if err == nil {
			if err := s.store.DeleteTag(ctx, 0, boardTagID, isAnonymousBoard); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			writeStoreErr(w, err, true)
			return
		}

		if !hasUserID {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
			return
		}
		tagID, err := s.store.GetTagIDByName(ctx, userID, tagName)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if err := s.store.DeleteTag(ctx, userID, tagID, isAnonymousBoard); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), project.ID, "tag_deleted")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// GET /api/board/{slug}/burndown
	if len(rest) == 2 && rest[1] == "burndown" && r.Method == http.MethodGet {
		points, err := s.store.GetRealBurndown(s.requestContext(r), project.ID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, realBurndownToJSON(points))
		return
	}

	// GET /api/board/{slug}/backlog-size
	if len(rest) == 2 && rest[1] == "backlog-size" && r.Method == http.MethodGet {
		points, err := s.store.GetBacklogSize(s.requestContext(r), project.ID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusOK, burndownToJSON(points))
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleTodos(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	todoID, ok := parseInt64(rest[0])
	if !ok {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid todo id", map[string]any{"field": "todoId"})
		return
	}

	// /api/todos/{id}
	if len(rest) == 1 {
		switch r.Method {
		case http.MethodPatch:
			var raw map[string]json.RawMessage
			if err := readJSON(w, r, s.maxBody, &raw); err != nil {
				return
			}
			if _, ok := raw["assigneeUserId"]; !ok {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing assigneeUserId", map[string]any{"field": "assigneeUserId"})
				return
			}

			var in struct {
				Title            string   `json:"title"`
				Body             string   `json:"body"`
				Tags             []string `json:"tags"`
				EstimationPoints *int64   `json:"estimationPoints"`
				AssigneeUserId   *int64   `json:"assigneeUserId"`
			}
			payload, err := json.Marshal(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return
			}
			if err := json.Unmarshal(payload, &in); err != nil {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", nil)
				return
			}
			todo, err := s.store.UpdateTodo(s.requestContext(r), todoID, store.UpdateTodoInput{
				Title:            in.Title,
				Body:             in.Body,
				Tags:             in.Tags,
				EstimationPoints: in.EstimationPoints,
				AssigneeUserID:   in.AssigneeUserId,
			}, s.storeMode())
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			if !todo.AssignmentChanged {
				s.emitRefreshNeeded(r.Context(), todo.ProjectID, "todo_updated")
			}
			writeJSON(w, http.StatusOK, todoToJSON(todo))
			return

		case http.MethodDelete:
			projectID, err := s.store.GetProjectIDForTodo(s.requestContext(r), todoID)
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			if err := s.store.DeleteTodo(s.requestContext(r), todoID, s.storeMode()); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			s.emitRefreshNeeded(r.Context(), projectID, "todo_deleted")
			w.WriteHeader(http.StatusNoContent)
			return

		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
	}

	// /api/todos/{id}/move
	if len(rest) == 2 && rest[1] == "move" && r.Method == http.MethodPost {
		var in struct {
			ToColumnKey string `json:"toColumnKey"`
			ToStatus    string `json:"toStatus"`
			AfterID     *int64 `json:"afterId"`
			BeforeID    *int64 `json:"beforeId"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		toColumnKey := in.ToColumnKey
		if toColumnKey == "" && in.ToStatus != "" {
			toColumnKey = normalizeLaneKey(in.ToStatus)
		}
		if toColumnKey == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing toColumnKey", map[string]any{"field": "toColumnKey"})
			return
		}
		todo, err := s.store.MoveTodo(s.requestContext(r), todoID, toColumnKey, in.AfterID, in.BeforeID, s.storeMode())
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		s.emitRefreshNeeded(r.Context(), todo.ProjectID, "todo_moved")
		writeJSON(w, http.StatusOK, todoToJSON(todo))
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request, rest []string) {
	// /api/backup/{action}
	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	action := rest[0]

	switch action {
	case "export":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()

		// In full mode, require authentication
		if mode == store.ModeFull {
			if _, ok := store.UserIDFromContext(ctx); !ok {
				if n, err := s.store.CountUsers(ctx); err == nil && n > 0 {
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
					return
				}
			}
		}

		data, err := s.store.ExportAllProjects(ctx, mode)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}

		// Convert to JSON
		jsonData := exportDataToJSON(data)

		// Set headers for download
		now := time.Now()
		// Format: scrumboy-backup-2026-01-24-03-45-PM.json
		filename := fmt.Sprintf("scrumboy-backup-%s-%s.json",
			now.Format("2006-01-02"),
			now.Format("03-04-PM"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		writeJSON(w, http.StatusOK, jsonData)
		return

	case "preview":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		var in struct {
			Data       *store.ExportData `json:"data"`
			ImportMode string            `json:"importMode"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		if in.Data == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing data", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()

		result, err := s.store.PreviewImport(ctx, in.Data, mode, in.ImportMode)
		if err != nil {
			s.logger.Printf("BACKUP PREVIEW ERROR: %v", err)
			writeStoreErr(w, err, false)
			return
		}

		writeJSON(w, http.StatusOK, previewResultToJSON(result))
		return

	case "import":
		s.logger.Printf("BACKUP IMPORT: method=%s, content-length=%s", r.Method, r.Header.Get("Content-Length"))
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		var in struct {
			Data         *store.ExportData `json:"data"`
			ImportMode   string            `json:"importMode"`
			Confirmation string            `json:"confirmation,omitempty"`
			TargetSlug   string            `json:"targetSlug,omitempty"`
		}
		s.logger.Printf("BACKUP IMPORT: about to read JSON, maxBody=%d", s.maxBody)
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			s.logger.Printf("BACKUP IMPORT: readJSON error: %v", err)
			return
		}
		s.logger.Printf("BACKUP IMPORT: JSON read successfully, importMode=%s, projects=%d", in.ImportMode, len(in.Data.Projects))

		if in.Data == nil {
			s.logger.Printf("BACKUP IMPORT: ERROR - missing data")
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing data", nil)
			return
		}

		// Validate confirmation for replace mode
		if in.ImportMode == "replace" && in.Confirmation != "REPLACE" {
			s.logger.Printf("BACKUP IMPORT: ERROR - replace mode requires confirmation")
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "replace mode requires confirmation: type REPLACE", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()
		s.logger.Printf("BACKUP IMPORT: Calling ImportProjects with mode=%s, importMode=%s, targetSlug=%s", mode, in.ImportMode, in.TargetSlug)

		result, err := s.store.ImportProjectsWithTarget(ctx, in.Data, mode, in.ImportMode, in.TargetSlug)
		if err != nil {
			s.logger.Printf("BACKUP IMPORT: ERROR from ImportProjects: %v", err)
			writeStoreErr(w, err, false)
			return
		}
		// Phase 2 contract: import does not emit SSE events.
		// Clients reconcile on next board load/focus.
		s.logger.Printf("BACKUP IMPORT: Success - imported=%d, created=%d, updated=%d", result.Imported, result.Created, result.Updated)

		writeJSON(w, http.StatusOK, importResultToJSON(result))
		return

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, rest []string) {
	// Admin endpoints require authentication and admin/owner role
	// Authorization matrix:
	// | Action                | Owner | Admin | User |
	// | --------------------- | ----- | ----- | ---- |
	// | List users            | ✅     | ✅     | ❌    |
	// | Promote user → admin  | ✅     | ❌     | ❌    |
	// | Promote admin → owner | ❌     | ❌     | ❌    |
	// | Delete user           | ✅     | ❌     | ❌    |
	// | Demote admin          | ✅     | ❌     | ❌    |
	// Note: All authorization checks are enforced in store layer, not routing.
	// Routing only wires requests to store methods.
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	// Check if user has admin or owner role
	u, err := s.store.GetUser(ctx, userID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}
	if u.SystemRole != store.SystemRoleOwner && u.SystemRole != store.SystemRoleAdmin {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "admin or owner role required", nil)
		return
	}

	if len(rest) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	if rest[0] == "users" {
		if len(rest) == 1 {
			// GET /api/admin/users or POST /api/admin/users
			s.handleAdminUsersListOrCreate(w, r, userID)
		} else if len(rest) == 3 && rest[2] == "role" {
			// PATCH /api/admin/users/{id}/role
			s.handleAdminUsersUpdateRole(w, r, userID, rest[1])
		} else if len(rest) == 3 && rest[2] == "password-reset" {
			// POST /api/admin/users/{id}/password-reset
			s.handleAdminUsersPasswordReset(w, r, userID, rest[1])
		} else if len(rest) == 2 {
			// DELETE /api/admin/users/{id}
			s.handleAdminUsersDelete(w, r, userID, rest[1])
		} else {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		}
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

func (s *Server) handleAdminUsersListOrCreate(w http.ResponseWriter, r *http.Request, requesterID int64) {
	ctx := s.requestContext(r)

	switch r.Method {
	case http.MethodGet:
		// GET /api/admin/users - list all users
		users, err := s.store.ListUsers(ctx, requesterID)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		usersJSON := make([]userJSON, len(users))
		for i, u := range users {
			usersJSON[i] = userToJSON(u)
		}
		writeJSON(w, http.StatusOK, usersJSON)

	case http.MethodPost:
		// POST /api/admin/users - create user
		var in struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		u, err := s.store.CreateUser(ctx, in.Email, in.Password, in.Name)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}

		writeJSON(w, http.StatusCreated, userToJSON(u))

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

func (s *Server) handleAdminUsersUpdateRole(w http.ResponseWriter, r *http.Request, requesterID int64, targetIDStr string) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	targetID, ok := parseInt64(targetIDStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}

	var in struct {
		Role string `json:"role"`
	}
	if err := readJSON(w, r, s.maxBody, &in); err != nil {
		return
	}

	// Only allow "admin" or "user" - do NOT allow "owner" promotion via API
	if in.Role != "admin" && in.Role != "user" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "role must be 'admin' or 'user'", nil)
		return
	}

	ctx := s.requestContext(r)
	newRole, ok := store.ParseSystemRole(in.Role)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid role", nil)
		return
	}

	if err := s.store.UpdateUserRole(ctx, requesterID, targetID, newRole); err != nil {
		writeStoreErr(w, err, false)
		return
	}

	// Return updated user
	u, err := s.store.GetUser(ctx, targetID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}

	writeJSON(w, http.StatusOK, userToJSON(u))
}

func (s *Server) handleAdminUsersDelete(w http.ResponseWriter, r *http.Request, requesterID int64, targetIDStr string) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	targetID, ok := parseInt64(targetIDStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}

	ctx := s.requestContext(r)
	if err := s.store.DeleteUser(ctx, requesterID, targetID); err != nil {
		writeStoreErr(w, err, false)
		return
	}

	writeJSON(w, http.StatusNoContent, nil)
}

func (s *Server) handleAdminUsersPasswordReset(w http.ResponseWriter, r *http.Request, requesterID int64, targetIDStr string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}

	if len(s.encryptionKey) == 0 {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Password reset is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.", nil)
		return
	}

	targetID, ok := parseInt64(targetIDStr)
	if !ok {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}

	ctx := s.requestContext(r)

	// Owner-only (same as Promote/Delete)
	requester, err := s.store.GetUser(ctx, requesterID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}
	if requester.SystemRole != store.SystemRoleOwner {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "owner role required", nil)
		return
	}

	// Admin cannot generate reset link for themselves (prevents self-lockout)
	if requesterID == targetID {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "cannot generate reset link for yourself", nil)
		return
	}

	// Deny if targetRole >= requesterRole (owner cannot reset another owner)
	target, err := s.store.GetUser(ctx, targetID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}
	if target.SystemRole == store.SystemRoleOwner {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "cannot reset password for another owner", nil)
		return
	}

	// Rate limit: max 10 resets per minute per admin
	if s.passwordResetAdminLimiter != nil && !s.passwordResetAdminLimiter.Allow("admin_reset:"+strconv.FormatInt(requesterID, 10), "") {
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many reset links; try again later", nil)
		return
	}

	passwordHash, err := s.store.GetUserPasswordHash(ctx, targetID)
	if err != nil {
		writeStoreErr(w, err, false)
		return
	}

	token, expiresAt, err := tokens.GeneratePasswordResetToken(s.encryptionKey, targetID, passwordHash)
	if err != nil {
		writeInternal(w, err)
		return
	}

	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	baseURL := proto + "://" + r.Host
	resetURL := baseURL + "/auth/reset-password?token=" + url.QueryEscape(token)

	writeJSON(w, http.StatusOK, map[string]any{
		"reset_url":  resetURL,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
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

	// Rate limit by IP (reuse auth ratelimit)
	if s.authRateLimit != nil && !s.authRateLimit.Allow("ip:"+clientIP(r), "") {
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

	// Store enforces auth.ValidatePassword internally
	if err := s.store.UpdateUserPassword(ctx, userID, in.NewPassword); err != nil {
		writeStoreErr(w, err, true)
		return
	}

	if err := s.store.DeleteSessionsByUserID(ctx, userID); err != nil {
		writeInternal(w, err)
		return
	}
	clearSessionCookie(w, r)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}

func normalizeLaneKey(v string) string {
	switch strings.TrimSpace(strings.ToUpper(v)) {
	case "BACKLOG":
		return store.DefaultColumnBacklog
	case "NOT_STARTED":
		return store.DefaultColumnNotStarted
	case "IN_PROGRESS":
		return store.DefaultColumnDoing
	case "TESTING":
		return store.DefaultColumnTesting
	case "DONE":
		return store.DefaultColumnDone
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}
