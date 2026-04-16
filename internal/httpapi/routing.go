package httpapi

import (
	"net/http"
	"strings"
)

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	// API responses are dynamic and often session-scoped; prevent browser/proxy
	// reuse so auth transitions (login/logout) are reflected immediately.
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		// Minimal CSRF protection for a no-auth, local app:
		// cross-origin "simple" requests can still POST JSON as text/plain; requiring a custom header forces a preflight.
		// Exception: /api/auth/logout form POST (Content-Type form) - form submit can't add custom headers;
		// same-origin form POST is the standard logout pattern behind tunnels/proxies.
		// Exception: /api/auth/reset-password - token is auth; user may arrive from email/link without session.
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
	case "push":
		s.handlePush(w, r, parts[2:])
		return
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}
