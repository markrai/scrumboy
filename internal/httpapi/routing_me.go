package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"scrumboy/internal/store"
)

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
	if len(rest) == 1 && rest[0] == "realtime" && r.Method == http.MethodGet {
		s.handleMeRealtime(w, r, ctx, userID)
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

	if len(rest) >= 1 && rest[0] == "wallpaper" {
		s.handleUserWallpaper(w, r, userID, rest)
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
		if in.Key == wallpaperPrefKey {
			p, err := store.ParseWallpaperPref(in.Value)
			if err != nil {
				writeStoreErr(w, err, true)
				return
			}
			if p.Mode == "image" {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "image wallpaper must be uploaded via POST /api/user/wallpaper/image", map[string]any{"field": "value"})
				return
			}
			if err := s.store.SetUserPreference(ctx, userID, in.Key, in.Value); err != nil {
				writeStoreErr(w, err, true)
				return
			}
			if p.Mode == "off" || p.Mode == "color" {
				s.deleteUserWallpaperFile(userID)
			}
			w.WriteHeader(http.StatusNoContent)
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
