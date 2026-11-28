package httpapi

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/store"
)

// Service-account administration over REST, for the settings UI. It is a thin transport over the
// same store methods the admin_*ServiceAccount / admin_*ServiceToken MCP tools use; the store
// remains authoritative for the authorization matrix (create/list: Owner or Admin; delete and
// mint: Owner; list/revoke tokens: Owner or Admin) and every invariant.
//
//	GET    /api/admin/service-accounts?limit=&cursor=
//	POST   /api/admin/service-accounts                      {"name"}
//	DELETE /api/admin/service-accounts/{id}
//	GET    /api/admin/service-accounts/{id}/tokens?limit=&cursor=
//	POST   /api/admin/service-accounts/{id}/tokens          {"name"?}  (plaintext returned once)
//	DELETE /api/admin/service-tokens/{tokenId}

type serviceAccountJSON struct {
	UserID       int64     `json:"userId"`
	StableUserID string    `json:"stableUserId"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"createdAt"`
}

type serviceTokenJSON struct {
	TokenID    int64      `json:"tokenId"`
	UserID     int64      `json:"userId"`
	Name       *string    `json:"name,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

func serviceAccountToJSON(u store.User) serviceAccountJSON {
	return serviceAccountJSON{UserID: u.ID, StableUserID: u.PublicID, Name: u.Name, CreatedAt: u.CreatedAt}
}

func serviceTokenMetaToJSON(t store.APITokenMeta) serviceTokenJSON {
	return serviceTokenJSON{
		TokenID: t.ID, UserID: t.UserID, Name: t.Name,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt,
	}
}

// writeServiceAdminErr maps typed store failures for the service-account admin
// surface. ErrUnauthorized becomes 403 (the requester is already authenticated,
// so a 401 would make the SPA treat "not the owner" as "signed out"). Unexpected
// errors are redacted as INTERNAL without matching message text; diagnostics
// stay in the server log via formatErrorWithCause.
func writeServiceAdminErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUnauthorized):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, store.ErrForbidden):
		writeError(w, http.StatusForbidden, "FORBIDDEN", "forbidden", nil)
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "CONFLICT", err.Error(), nil)
	case errors.Is(err, store.ErrValidation):
		writeValidationError(w, err.Error(), validationReasonFromStoreError(err), store.ErrorDetails(err))
	case errors.Is(err, store.ErrStorageBudget):
		var budget *store.StorageBudgetError
		details := map[string]any{}
		if errors.As(err, &budget) {
			details = map[string]any{"scope": budget.Scope, "limitBytes": budget.LimitBytes, "usedBytes": budget.UsedBytes}
		}
		writeError(w, http.StatusInsufficientStorage, "STORAGE_BUDGET_EXCEEDED", "storage budget exceeded", details)
	default:
		// Do not classify by matching arbitrary message text (e.g. "too large").
		log.Printf("service-account admin: %s", formatErrorWithCause(err))
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
	}
}

func parsePageLimit(w http.ResponseWriter, r *http.Request, max int) (int, bool) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > max {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid limit", nil)
			return 0, false
		}
		limit = n
	}
	return limit, true
}

// handleAdminServiceAccounts routes /api/admin/service-accounts[/...]; rest excludes the segment.
func (s *Server) handleAdminServiceAccounts(w http.ResponseWriter, r *http.Request, requesterID int64, rest []string) {
	switch {
	case len(rest) == 0:
		s.handleAdminServiceAccountsListOrCreate(w, r, requesterID)
	case len(rest) == 1:
		id, ok := parseInt64(rest[0])
		if !ok || id <= 0 {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid service account id", nil)
			return
		}
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		if err := s.store.DeleteServiceAccount(s.requestContext(r), requesterID, id); err != nil {
			writeServiceAdminErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "userId": id})
	case len(rest) == 2 && rest[1] == "tokens":
		id, ok := parseInt64(rest[0])
		if !ok || id <= 0 {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid service account id", nil)
			return
		}
		s.handleAdminServiceTokensListOrCreate(w, r, requesterID, id)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	}
}

func (s *Server) handleAdminServiceAccountsListOrCreate(w http.ResponseWriter, r *http.Request, requesterID int64) {
	ctx := s.requestContext(r)
	switch r.Method {
	case http.MethodGet:
		limit, ok := parsePageLimit(w, r, store.MaxServiceAccountPageSize)
		if !ok {
			return
		}
		var cursor *string
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			cursor = &raw
		}
		accounts, next, err := s.store.ListServiceAccounts(ctx, requesterID, limit, cursor)
		if err != nil {
			writeServiceAdminErr(w, err)
			return
		}
		items := make([]serviceAccountJSON, 0, len(accounts))
		for _, a := range accounts {
			items = append(items, serviceAccountToJSON(a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": next})
	case http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required", nil)
			return
		}
		account, err := s.store.CreateServiceAccount(ctx, requesterID, name)
		if err != nil {
			writeServiceAdminErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, serviceAccountToJSON(account))
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

func (s *Server) handleAdminServiceTokensListOrCreate(w http.ResponseWriter, r *http.Request, requesterID, serviceUserID int64) {
	ctx := s.requestContext(r)
	switch r.Method {
	case http.MethodGet:
		limit, ok := parsePageLimit(w, r, store.MaxServiceTokenPageSize)
		if !ok {
			return
		}
		var before int64
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			n, ok := parseInt64(raw)
			if !ok || n <= 0 {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid cursor", nil)
				return
			}
			before = n
		}
		tokens, next, err := s.store.ListServiceAccountTokens(ctx, requesterID, serviceUserID, limit, before)
		if err != nil {
			writeServiceAdminErr(w, err)
			return
		}
		items := make([]serviceTokenJSON, 0, len(tokens))
		for _, t := range tokens {
			items = append(items, serviceTokenMetaToJSON(t))
		}
		var nextCursor *string
		if next != nil {
			v := strconv.FormatInt(*next, 10)
			nextCursor = &v
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
	case http.MethodPost:
		var in struct {
			Name *string `json:"name"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}
		if in.Name != nil {
			name := strings.TrimSpace(*in.Name)
			if name == "" {
				in.Name = nil
			} else {
				in.Name = &name
			}
		}
		id, plaintext, createdAt, err := s.store.CreateServiceAccountToken(ctx, requesterID, serviceUserID, in.Name)
		if err != nil {
			writeServiceAdminErr(w, err)
			return
		}
		// The plaintext is shown exactly once; never let an intermediary cache it.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, map[string]any{
			"tokenId": id, "userId": serviceUserID, "name": in.Name, "createdAt": createdAt, "token": plaintext,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	}
}

// handleAdminServiceTokenRevoke handles DELETE /api/admin/service-tokens/{tokenId}.
func (s *Server) handleAdminServiceTokenRevoke(w http.ResponseWriter, r *http.Request, requesterID int64, rest []string) {
	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	id, ok := parseInt64(rest[0])
	if !ok || id <= 0 {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid token id", nil)
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if err := s.store.RevokeServiceAccountToken(s.requestContext(r), requesterID, id); err != nil {
		writeServiceAdminErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "revoked", "tokenId": id})
}
