package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	assetapp "scrumboy/internal/application/asset"
	backupimportapp "scrumboy/internal/application/backupimport"
	"scrumboy/internal/assets/imagecheck"
	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
)

// isArchiveBackupRequest reports whether a backup preview/import request carries a .sbz archive.
// With inline images disabled this is always false, so multipart requests keep today's behavior.
func (s *Server) isArchiveBackupRequest(r *http.Request) bool {
	if !s.inlineImagesEnabled || s.backupImports == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "multipart/form-data"
}

// handleArchiveBackup serves multipart POST /api/backup/{preview,import} with parts
// importMode, confirmation (replace only) and archive (the .sbz file).
func (s *Server) handleArchiveBackup(w http.ResponseWriter, r *http.Request, action string) {
	ctx := s.requestContext(r)
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "unauthorized", nil)
		return
	}
	// Admission happens before any body byte is read or any temporary file exists: declared
	// size, per-user rate, then a staging slot (global and per user), which bounds temporary
	// disk use to the slot count times the archive limit however many requests arrive.
	limits := s.backupImports.Limits()
	maxBody := limits.MaxArchiveBytes + multipartOverheadBytes
	if r.ContentLength > maxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "backup archive too large", validationDetails("archive_too_large", nil))
		return
	}
	if !s.archiveImportLimit.Allow(fmt.Sprintf("user:%d", userID), "") {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many backup archive requests, please wait", nil)
		return
	}
	release, err := s.backupImports.AdmitStaging(userID)
	if err != nil {
		w.Header().Set("Retry-After", "10")
		if errors.Is(err, backupimportapp.ErrStagingUserBusy) {
			writeError(w, http.StatusTooManyRequests, "IMPORT_IN_PROGRESS", "another backup archive of yours is still being processed", nil)
			return
		}
		writeError(w, http.StatusServiceUnavailable, "BUSY", "the server is processing other backup archives, please retry", nil)
		return
	}
	defer release()
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	dir := filepath.Join(s.dataDir, assetapp.ImportStagingDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeInternal(w, err)
		return
	}
	tmp, err := os.CreateTemp(dir, "import-*.sbz")
	if err != nil {
		writeInternal(w, err)
		return
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	importMode, confirmation, size, err := readArchiveParts(r, tmp, limits.MaxArchiveBytes)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "backup archive too large", validationDetails("archive_too_large", nil))
			return
		}
		writeValidationError(w, "invalid multipart form: send importMode and one archive file", "invalid_multipart", nil)
		return
	}
	if importMode == "" {
		importMode = "merge"
	}
	mode := s.storeMode()

	switch action {
	case "preview":
		result, err := s.backupImports.PreviewArchive(ctx, tmp, size, mode, importMode)
		if err != nil {
			s.writeArchiveImportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, previewResultToJSON(result))
	case "import":
		if importMode == "replace" && confirmation != "REPLACE" {
			writeValidationError(w, "replace mode requires confirmation: type REPLACE", "replace_confirmation_required", nil)
			return
		}
		result, err := s.backupImports.ImportArchive(ctx, tmp, size, mode, importMode)
		if err != nil {
			s.logger.Printf("BACKUP ARCHIVE IMPORT: %v", err)
			s.writeArchiveImportError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, importResultToJSON(result))
	}
}

// readArchiveParts streams the multipart body: small text parts, and the archive written to dst.
func readArchiveParts(r *http.Request, dst *os.File, maxBytes int64) (importMode, confirmation string, size int64, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", 0, errInvalidMultipart
	}
	seenArchive := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return "", "", 0, errUploadTooLarge
			}
			return "", "", 0, errInvalidMultipart
		}
		switch part.FormName() {
		case "importMode", "confirmation":
			b, err := io.ReadAll(io.LimitReader(part, 257))
			_ = part.Close()
			if err != nil || len(b) > 256 {
				return "", "", 0, errInvalidMultipart
			}
			if part.FormName() == "importMode" {
				importMode = string(b)
			} else {
				confirmation = string(b)
			}
		case "archive":
			if seenArchive || part.FileName() == "" {
				_ = part.Close()
				return "", "", 0, errInvalidMultipart
			}
			seenArchive = true
			n, err := io.Copy(dst, io.LimitReader(part, maxBytes+1))
			_ = part.Close()
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					return "", "", 0, errUploadTooLarge
				}
				return "", "", 0, errInvalidMultipart
			}
			if n > maxBytes {
				return "", "", 0, errUploadTooLarge
			}
			size = n
		default:
			_ = part.Close()
			return "", "", 0, errInvalidMultipart
		}
	}
	if !seenArchive {
		return "", "", 0, errInvalidMultipart
	}
	return importMode, confirmation, size, nil
}

func (s *Server) writeArchiveImportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backuparchive.ErrInvalidArchive), errors.Is(err, backuparchive.ErrChecksum):
		writeValidationError(w, err.Error(), "invalid_backup_archive", nil)
	case errors.Is(err, imagecheck.ErrUnsupportedFormat), errors.Is(err, imagecheck.ErrCorrupt), errors.Is(err, imagecheck.ErrTrailingData):
		writeValidationError(w, err.Error(), "invalid_backup_archive", nil)
	default:
		writeStoreErr(w, err, false)
	}
}
