package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// resolveAndValidateAuth runs MCP auth resolution and writes JSON errors when auth cannot proceed.
// On success, ok is true and ctx is ready for tool handlers. On failure, ok is false (response already sent).
func (a *Adapter) resolveAndValidateAuth(w http.ResponseWriter, r *http.Request) (ctx context.Context, ok bool) {
	authRes := a.resolveRequestAuth(r)
	if authRes.Err != nil {
		writeError(w, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": authRes.Err.Error()}))
		return nil, false
	}
	if authRes.BearerAuthFailed {
		writeError(w, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Authentication required", nil))
		return nil, false
	}
	return authRes.Ctx, true
}

func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if r.URL.Path != "/mcp" && r.URL.Path != "/mcp/" {
		writeError(w, newAdapterError(http.StatusNotFound, CodeNotFound, "not found", nil))
		return
	}

	if r.Method == http.MethodGet {
		ctx, ok := a.resolveAndValidateAuth(w, r)
		if !ok {
			return
		}
		data, meta, err := a.handleSystemGetCapabilities(ctx, nil)
		if err != nil {
			writeError(w, err)
			return
		}
		writeSuccess(w, http.StatusOK, data, meta)
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, newAdapterError(http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed", nil))
		return
	}

	var req requestEnvelope
	if err := readJSON(r, &req); err != nil {
		writeError(w, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid json", map[string]any{"detail": err.Error()}))
		return
	}
	if req.Tool == "" {
		writeError(w, newAdapterError(http.StatusBadRequest, CodeValidationError, "missing tool", map[string]any{"field": "tool"}))
		return
	}

	handler, ok := a.tools[req.Tool]
	if !ok {
		writeError(w, newAdapterError(http.StatusNotFound, CodeNotFound, "tool not found", map[string]any{"tool": req.Tool}))
		return
	}

	ctx, ok := a.resolveAndValidateAuth(w, r)
	if !ok {
		return
	}
	data, meta, toolErr := handler(ctx, req.Input)
	if toolErr != nil {
		writeError(w, toolErr)
		return
	}
	writeSuccess(w, http.StatusOK, data, meta)
}

func readJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("extra json data")
		}
		return err
	}
	return nil
}

func writeSuccess(w http.ResponseWriter, status int, data any, meta map[string]any) {
	if meta == nil {
		meta = map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(successResponse{
		OK:   true,
		Data: data,
		Meta: meta,
	})
}

func writeError(w http.ResponseWriter, err *adapterError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(err.Status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		OK: false,
		Error: errorResponseBody{
			Code:    err.Code,
			Message: err.Message,
			Details: normalizeDetails(err.Details),
		},
	})
}

func normalizeDetails(v any) any {
	if v == nil {
		return map[string]any{}
	}
	return v
}
