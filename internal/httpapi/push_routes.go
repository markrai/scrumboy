package httpapi

import (
	"net/http"

	"scrumboy/internal/store"
)

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request, rest []string) {
	if s.mode == "anonymous" {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	// GET /api/push/vapid-public-key — no session required for key discovery (client may prefetch).
	if len(rest) == 1 && rest[0] == "vapid-public-key" && r.Method == http.MethodGet {
		if !s.pushVapidConfigured {
			writeError(w, http.StatusServiceUnavailable, "PUSH_UNAVAILABLE", "Web Push is not configured", nil)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": s.vapidPublicKey})
		return
	}

	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	if len(rest) == 1 && rest[0] == "subscribe" && r.Method == http.MethodPost {
		if !s.pushVapidConfigured {
			writeError(w, http.StatusServiceUnavailable, "PUSH_UNAVAILABLE", "Web Push is not configured", nil)
			return
		}
		var in struct {
			Endpoint string `json:"endpoint"`
			Keys     struct {
				P256dh string `json:"p256dh"`
				Auth   string `json:"auth"`
			} `json:"keys"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Endpoint == "" || in.Keys.P256dh == "" || in.Keys.Auth == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "endpoint and keys.p256dh, keys.auth required", nil)
			return
		}
		ua := r.UserAgent()
		var uaPtr *string
		if ua != "" {
			uaPtr = &ua
		}
		if err := s.store.UpsertPushSubscription(ctx, userID, in.Endpoint, in.Keys.P256dh, in.Keys.Auth, uaPtr); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if s.pushDebug {
			s.logger.Printf("push: subscription upsert user=%d", userID)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if len(rest) == 1 && rest[0] == "unsubscribe" && r.Method == http.MethodDelete {
		var in struct {
			Endpoint string `json:"endpoint"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Endpoint == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "endpoint required", nil)
			return
		}
		if err := s.store.DeletePushSubscription(ctx, userID, in.Endpoint); err != nil {
			writeStoreErr(w, err, true)
			return
		}
		if s.pushDebug {
			s.logger.Printf("push: subscription removed user=%d", userID)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
}
