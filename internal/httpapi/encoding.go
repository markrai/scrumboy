package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"scrumboy/internal/store"
)

func readJSON(w http.ResponseWriter, r *http.Request, maxBody int64, dst any) error {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing body", nil)
		return errors.New("missing body")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json", map[string]any{"detail": err.Error()})
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json", map[string]any{"detail": "extra data"})
		return errors.New("extra json data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeInternal(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", map[string]any{"detail": err.Error()})
}

func writeStoreErr(w http.ResponseWriter, err error, hideUnauthorized bool) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, store.ErrUnauthorized):
		if hideUnauthorized {
			// Map to 404 to prevent existence probing on resource endpoints
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		} else {
			// Return 401 for entry points (so SPA can show login)
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		}
	case errors.Is(err, store.ErrValidation):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", err.Error(), nil)
	case errors.Is(err, store.ErrTooManyAttempts):
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "too many attempts; please sign in again", nil)
	case errors.Is(err, store.Err2FAEncryptionNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Two-factor authentication is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.", nil)
	default:
		writeInternal(w, err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"details": details,
		},
	})
}

