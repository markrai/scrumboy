package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/store"
)

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, rest []string) {
	// Admin endpoints require authentication and admin/owner role
	// Authorization matrix:
	// | Action                | Owner | Admin | User |
	// | --------------------- | ----- | ----- | ---- |
	// | List users            | ✅     | ✅     | ❌    |
	// | Promote user -> admin  | ✅     | ❌     | ❌    |
	// | Promote admin -> owner | ❌     | ❌     | ❌    |
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

	if rest[0] == "settings" && len(rest) == 2 && rest[1] == "email-notify-default" {
		// GET/PUT /api/admin/settings/email-notify-default
		s.handleAdminEmailNotifyDefault(w, r, userID)
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
	if s.oidcService != nil && s.oidcService.Config().LocalAuthDisabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
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

	resetURL := s.resetBaseURL(r) + "/auth/reset-password?token=" + url.QueryEscape(token)

	writeJSON(w, http.StatusOK, map[string]any{
		"reset_url":  resetURL,
		"expires_at": expiresAt.UTC().Format(time.RFC3339),
	})
}

// handleAdminEmailNotifyDefault gets/sets the org-wide default emailNotifications
// preference newly created users are seeded with. It never touches
// existing users' own preferences -- see seedEmailNotifyPrefTx in internal/store.
func (s *Server) handleAdminEmailNotifyDefault(w http.ResponseWriter, r *http.Request, requesterID int64) {
	ctx := s.requestContext(r)

	switch r.Method {
	case http.MethodGet:
		// GET /api/admin/settings/email-notify-default
		pref, customized, err := s.store.GetEmailNotifyOrgDefault(ctx)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		canonical, err := json.Marshal(pref)
		if err != nil {
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"value":      string(canonical),
			"customized": customized,
		})
		return

	case http.MethodPut:
		// PUT /api/admin/settings/email-notify-default - Body: { value: string }
		var in struct {
			Value string `json:"value"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if err := s.store.SetEmailNotifyOrgDefault(ctx, requesterID, in.Value); err != nil {
			writeStoreErr(w, err, false)
			return
		}
		pref, _, err := s.store.GetEmailNotifyOrgDefault(ctx)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		canonical, err := json.Marshal(pref)
		if err != nil {
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"value":      string(canonical),
			"customized": true,
		})
		return

	case http.MethodDelete:
		// DELETE /api/admin/settings/email-notify-default - reset to unconfigured.
		// Existing users' preferences are untouched; subsequent users get no seeded row.
		if err := s.store.ClearEmailNotifyOrgDefault(ctx, requesterID); err != nil {
			writeStoreErr(w, err, false)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}
