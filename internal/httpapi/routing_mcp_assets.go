package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

const mcpAssetsPrefix = "/mcp/assets/"

// bearerAuthenticator is implemented by the MCP adapter. It authenticates Authorization: Bearer
// credentials (API tokens and OAuth access tokens issued for the MCP resource) and never the
// session cookie.
type bearerAuthenticator interface {
	AuthenticateBearerRequest(r *http.Request) (context.Context, bool, error)
}

// handleMCPAssets serves the bearer-authenticated image routes used by scrumboy-cli, whose OAuth
// login is only accepted under the /mcp resource:
//
//	GET  /mcp/assets/{uuid}          download (same access rules as GET /api/assets/{uuid})
//	POST /mcp/assets/{projectSlug}   multipart upload, optional ?todo={localId} (same rules and
//	                                 limits as POST /api/board/{slug}/assets)
//
// It reuses the cookie routes' handlers; only the way the actor is established differs.
func (s *Server) handleMCPAssets(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.mcpHandler.(bearerAuthenticator)
	if !ok || !s.inlineImagesEnabled {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, mcpAssetsPrefix), "/")
	if rest == "" || strings.Contains(rest, "/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	ctx, authed, err := auth.AuthenticateBearerRequest(r)
	if err != nil {
		s.logger.Printf("mcp assets: bearer authentication failed: %v", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
		return
	}
	if !authed {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication required", nil)
		return
	}
	// The shared handlers call requestContext, which would let a session cookie replace the bearer
	// identity. Drop the cookie so the bearer token is the only credential on these routes.
	r = r.WithContext(ctx)
	r.Header = r.Header.Clone()
	r.Header.Del("Cookie")
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodPost {
		project, err := s.store.GetProjectBySlug(ctx, rest)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrValidation) {
				writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
				return
			}
			s.logger.Printf("mcp assets: project lookup failed: %v", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
			return
		}
		s.handleAssetUpload(w, r, project.ID)
		return
	}
	s.handleAssets(w, r, []string{rest})
}
