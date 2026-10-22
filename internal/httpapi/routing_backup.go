package httpapi

import (
	"bytes"
	"encoding/json"
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

		// Native (Capacitor) clients buffer whole responses through a base64 bridge, so they
		// declare a byte ceiling; the server refuses before writing any body it cannot hold.
		clientMaxBytes, guarded, ok := parseClientMaxBytes(r)
		if !ok {
			writeValidationError(w, "clientMaxBytes must be between 1 and 67108864", "invalid_client_max_bytes", nil)
			return
		}

		data, err := s.store.ExportAllProjects(ctx, mode)
		if err != nil {
			writeStoreErr(w, err, false)
			return
		}

		now := time.Now()
		// Format: scrumboy-backup-2026-01-24-03-45-PM.json
		stem := fmt.Sprintf("scrumboy-backup-%s-%s", now.Format("2006-01-02"), now.Format("03-04-PM"))

		if store.ExportHasAssets(data) {
			if guarded {
				writeError(w, http.StatusConflict, "ARCHIVE_EXPORT_UNSUPPORTED_CLIENT", "this backup contains images; export it from a web browser", nil)
				return
			}
			s.writeArchiveExport(w, data, stem+".sbz")
			return
		}

		// Convert to JSON
		jsonData := exportDataToJSON(data)
		if guarded {
			var buf bytes.Buffer
			if err := json.NewEncoder(&buf).Encode(jsonData); err != nil {
				writeInternal(w, err)
				return
			}
			if int64(buf.Len()) > clientMaxBytes {
				writeError(w, http.StatusRequestEntityTooLarge, "EXPORT_TOO_LARGE_FOR_CLIENT", "backup is too large for this app; export it from a web browser", map[string]any{"bytes": buf.Len()})
				return
			}
		}

		// Set headers for download
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", stem+".json"))
		writeJSON(w, http.StatusOK, jsonData)
		return

	case "preview":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		if s.isArchiveBackupRequest(r) {
			s.handleArchiveBackup(w, r, "preview")
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
			writeValidationError(w, "missing data", "missing_data", nil)
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
		if s.isArchiveBackupRequest(r) {
			s.handleArchiveBackup(w, r, "import")
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
		if in.Data == nil {
			s.logger.Printf("BACKUP IMPORT: ERROR - missing data")
			writeValidationError(w, "missing data", "missing_data", nil)
			return
		}
		s.logger.Printf("BACKUP IMPORT: JSON read successfully, importMode=%s, projects=%d", in.ImportMode, len(in.Data.Projects))

		// Validate confirmation for replace mode
		if in.ImportMode == "replace" && in.Confirmation != "REPLACE" {
			s.logger.Printf("BACKUP IMPORT: ERROR - replace mode requires confirmation")
			writeValidationError(w, "replace mode requires confirmation: type REPLACE", "replace_confirmation_required", nil)
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
