package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"scrumboy/internal/store"
)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, rest []string) {
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}

	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}

	switch rest[0] {
	case "summary":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		tz := strings.TrimSpace(r.URL.Query().Get("tz"))
		summary, err := s.store.GetDashboardSummary(ctx, userID, tz)
		if err != nil {
			s.logger.Printf("dashboard/summary error: %v", err)
			writeInternal(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dashboardSummaryToJSON(summary))
		return

	case "todos":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		limit := 20
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid limit", nil)
				return
			}
			limit = n
		}

		var cursor *string
		if rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor")); rawCursor != "" {
			cursor = &rawCursor
		}

		sort := strings.TrimSpace(r.URL.Query().Get("sort"))

		items, nextCursor, err := s.store.ListDashboardTodos(ctx, userID, limit, cursor, sort)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}
		writeJSON(w, http.StatusOK, dashboardTodosToJSON(items, nextCursor))
		return
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}
