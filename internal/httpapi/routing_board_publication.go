package httpapi

import (
	"errors"
	"net/http"

	publicboardapp "scrumboy/internal/application/publicboard"
	"scrumboy/internal/store"
)

type publicationStatusJSON struct {
	Enabled     bool `json:"enabled"`
	Publishable bool `json:"publishable"`
}

type publicationUpdateJSON struct {
	Enabled bool `json:"enabled"`
	Changed bool `json:"changed"`
}

// handleBoardPublicationRoutes serves the Maintainer-only publication control:
//
//	GET   /api/board/{slug}/publication  -> {"enabled","publishable"}
//	PATCH /api/board/{slug}/publication  {"enabled": bool} -> {"enabled","changed"}
//
// The route has already resolved slug access through the member/temporary
// ProjectContext. Authorization (exact durable Maintainer), eligibility,
// the transaction, audit, and post-commit stream revocation are owned by the
// Phase 1 PublicationService and store; this adapter only maps transport.
// PATCH (not PUT) keeps the request inside the global X-Scrumboy CSRF gate.
func (s *Server) handleBoardPublicationRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if len(rest) != 2 || rest[1] != "publication" {
		return false
	}
	ctx := s.requestContext(r)
	projectID := pc.Project.ID
	switch r.Method {
	case http.MethodGet:
		status, err := s.publicBoardPublications.GetPublication(ctx, projectID)
		if err != nil {
			writePublicationError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, publicationStatusJSON{Enabled: status.Enabled, Publishable: status.Publishable})
	case http.MethodPatch:
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return true
		}
		if in.Enabled == nil {
			writeValidationError(w, "enabled is required", "publication_enabled_required", map[string]any{"field": "enabled"})
			return true
		}
		result, err := s.publicBoardPublications.SetPublication(ctx, publicboardapp.PublicationCommand{
			ProjectID: projectID,
			Enabled:   *in.Enabled,
		})
		if err != nil {
			writePublicationError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, publicationUpdateJSON{Enabled: result.Enabled, Changed: result.Changed})
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
	return true
}

func writePublicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, publicboardapp.ErrPublicationCapabilityDisabled):
		// The capability is invisible when the operator gate or mode is off.
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, publicboardapp.ErrActorRequired), errors.Is(err, store.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, store.ErrValidation):
		writeValidationError(w, "project slug is reserved and cannot be published", "publication_slug_reserved", nil)
	default:
		writeInternal(w, err)
	}
}
