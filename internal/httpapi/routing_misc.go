package httpapi

import (
	"net/http"

	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

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

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}
