package httpapi

import (
	"fmt"
	"net/http"
	"time"

	"scrumboy/internal/store"
)

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request, rest []string) {
	// /api/backup/{action}
	if len(rest) != 1 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	action := rest[0]

	switch action {
	case "export":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()

		// In full mode, require authentication
		if mode == store.ModeFull {
			if _, ok := store.UserIDFromContext(ctx); !ok {
				if n, err := s.store.CountUsers(ctx); err == nil && n > 0 {
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
					return
				}
			}
		}

		data, err := s.store.ExportAllProjects(ctx, mode)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}

		// Convert to JSON
		jsonData := exportDataToJSON(data)

		// Set headers for download
		now := time.Now()
		// Format: scrumboy-backup-2026-01-24-03-45-PM.json
		filename := fmt.Sprintf("scrumboy-backup-%s-%s.json",
			now.Format("2006-01-02"),
			now.Format("03-04-PM"))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		writeJSON(w, http.StatusOK, jsonData)
		return

	case "preview":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		var in struct {
			Data       *store.ExportData `json:"data"`
			ImportMode string            `json:"importMode"`
		}
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			return
		}

		if in.Data == nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing data", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()

		result, err := s.store.PreviewImport(ctx, in.Data, mode, in.ImportMode)
		if err != nil {
			s.logger.Printf("BACKUP PREVIEW ERROR: %v", err)
			writeStoreErr(w, err, false)
			return
		}

		writeJSON(w, http.StatusOK, previewResultToJSON(result))
		return

	case "import":
		s.logger.Printf("BACKUP IMPORT: method=%s, content-length=%s", r.Method, r.Header.Get("Content-Length"))
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}

		var in struct {
			Data         *store.ExportData `json:"data"`
			ImportMode   string            `json:"importMode"`
			Confirmation string            `json:"confirmation,omitempty"`
			TargetSlug   string            `json:"targetSlug,omitempty"`
		}
		s.logger.Printf("BACKUP IMPORT: about to read JSON, maxBody=%d", s.maxBody)
		if err := readJSON(w, r, s.maxBody, &in); err != nil {
			s.logger.Printf("BACKUP IMPORT: readJSON error: %v", err)
			return
		}
		s.logger.Printf("BACKUP IMPORT: JSON read successfully, importMode=%s, projects=%d", in.ImportMode, len(in.Data.Projects))

		if in.Data == nil {
			s.logger.Printf("BACKUP IMPORT: ERROR - missing data")
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "missing data", nil)
			return
		}

		// Validate confirmation for replace mode
		if in.ImportMode == "replace" && in.Confirmation != "REPLACE" {
			s.logger.Printf("BACKUP IMPORT: ERROR - replace mode requires confirmation")
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "replace mode requires confirmation: type REPLACE", nil)
			return
		}

		ctx := s.requestContext(r)
		mode := s.storeMode()
		s.logger.Printf("BACKUP IMPORT: Calling ImportProjects with mode=%s, importMode=%s, targetSlug=%s", mode, in.ImportMode, in.TargetSlug)

		result, err := s.store.ImportProjectsWithTarget(ctx, in.Data, mode, in.ImportMode, in.TargetSlug)
		if err != nil {
			s.logger.Printf("BACKUP IMPORT: ERROR from ImportProjects: %v", err)
			writeStoreErr(w, err, false)
			return
		}
		// Phase 2 contract: import does not emit SSE events.
		// Clients reconcile on next board load/focus.
		s.logger.Printf("BACKUP IMPORT: Success - imported=%d, created=%d, updated=%d", result.Imported, result.Created, result.Updated)

		writeJSON(w, http.StatusOK, importResultToJSON(result))
		return

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
}
