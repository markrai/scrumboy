package httpapi

import (
	"fmt"
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

	// PATCH /api/tags/mine/{tagId}/color - set/clear the owner's personal color preference.
	if len(rest) == 3 && rest[0] == "mine" && rest[2] == "color" && r.Method == http.MethodPatch {
		var tagID int64
		if _, err := fmt.Sscanf(rest[1], "%d", &tagID); err != nil || tagID <= 0 {
			writeValidationError(w, "invalid tagId", "invalid_tag_id", map[string]any{"field": "tagId"})
			return
		}
		var in struct {
			Color *string `json:"color"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if err := s.store.UpdateMyTagColor(ctx, userID, tagID, in.Color); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// DELETE /api/tags/mine/{tagId} - owner-only personal-tag delete; refreshes every
	// project that referenced the (cross-project) row.
	if len(rest) == 2 && rest[0] == "mine" && r.Method == http.MethodDelete {
		var tagID int64
		if _, err := fmt.Sscanf(rest[1], "%d", &tagID); err != nil || tagID <= 0 {
			writeValidationError(w, "invalid tagId", "invalid_tag_id", map[string]any{"field": "tagId"})
			return
		}
		affected, err := s.store.DeleteMyTagByID(ctx, userID, tagID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		for _, pid := range affected {
			s.emitRefreshNeeded(ctx, pid, "tag_deleted")
		}
		w.WriteHeader(http.StatusNoContent)
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
