package httpapi

import (
	"errors"
	"net/http"
	"time"

	"scrumboy/internal/store"
)

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request, rest []string) {
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

	// POST /api/webhooks — create subscription
	if len(rest) == 0 && r.Method == http.MethodPost {
		var in struct {
			ProjectID int64    `json:"projectId"`
			URL       string   `json:"url"`
			Events    []string `json:"events"`
			Secret    *string  `json:"secret"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if err := s.store.CheckProjectRole(ctx, in.ProjectID, userID, store.RoleMaintainer); err != nil {
			if errors.Is(err, store.ErrUnauthorized) {
				writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
				return
			}
			writeStoreErr(w, err, true)
			return
		}
		wh, err := s.store.CreateWebhook(ctx, userID, store.CreateWebhookInput{
			ProjectID: in.ProjectID,
			URL:       in.URL,
			Events:    in.Events,
			Secret:    in.Secret,
		})
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		writeJSON(w, http.StatusCreated, webhookToJSON(wh))
		return
	}

	// GET /api/webhooks — list user's webhooks
	if len(rest) == 0 && r.Method == http.MethodGet {
		hooks, err := s.store.ListWebhooks(ctx, userID)
		if err != nil {
			writeStoreErr(w, err, true)
			return
		}
		out := make([]webhookJSON, 0, len(hooks))
		for _, h := range hooks {
			out = append(out, webhookToJSON(h))
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// DELETE /api/webhooks/{id}
	if len(rest) == 1 && r.Method == http.MethodDelete {
		whID, ok := parseInt64(rest[0])
		if !ok {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid webhook id", nil)
			return
		}
		if err := s.store.DeleteWebhook(ctx, userID, whID); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}

type webhookJSON struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"projectId"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	CreatedAt time.Time `json:"createdAt"`
}

func webhookToJSON(wh store.Webhook) webhookJSON {
	return webhookJSON{
		ID:        wh.ID,
		ProjectID: wh.ProjectID,
		URL:       wh.URL,
		Events:    wh.Events,
		CreatedAt: wh.CreatedAt,
	}
}
