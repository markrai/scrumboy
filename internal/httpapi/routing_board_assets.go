package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	assetapp "scrumboy/internal/application/asset"
	"scrumboy/internal/assets/imagecheck"
	"scrumboy/internal/store"
)

// multipartOverheadBytes is headroom for multipart framing on top of the maximum file size, so a
// file of exactly the maximum size is accepted.
const multipartOverheadBytes = 64 << 10

// handleBoardAssetRoutes serves POST /api/board/{slug}/assets[?todo={localId}]. When inline images
// are disabled it matches nothing, so the request falls through to the existing 404.
func (s *Server) handleBoardAssetRoutes(w http.ResponseWriter, r *http.Request, rest []string, pc *store.ProjectContext) bool {
	if !s.inlineImagesEnabled || len(rest) != 2 || rest[1] != "assets" || r.Method != http.MethodPost {
		return false
	}
	s.handleAssetUpload(w, r, pc.Project.ID)
	return true
}

func (s *Server) handleAssetUpload(w http.ResponseWriter, r *http.Request, projectID int64) {
	ctx := s.requestContext(r)
	var todoLocalID *int64
	if r.URL.Query().Has("todo") {
		id, err := strconv.ParseInt(r.URL.Query().Get("todo"), 10, 64)
		if err != nil || id < 1 {
			writeValidationError(w, "invalid todo", "invalid_todo", map[string]any{"field": "todo"})
			return
		}
		todoLocalID = &id
	}
	// Authorize before reading any body bytes.
	if err := s.store.AuthorizeAssetUpload(ctx, projectID, todoLocalID); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	userID, _ := store.UserIDFromContext(ctx)
	if !s.assetUploadLimit.Allow(fmt.Sprintf("user:%d", userID), "") {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many uploads", nil)
		return
	}
	release, err := s.assetService.AcquireRead()
	if err != nil {
		w.Header().Set("Retry-After", "2")
		writeError(w, http.StatusServiceUnavailable, "BUSY", "server is processing too many uploads, please retry", nil)
		return
	}
	defer release()

	limits := s.assetService.Limits()
	r.Body = http.MaxBytesReader(w, r.Body, limits.MaxBytes+multipartOverheadBytes)
	name, data, err := readSingleFilePart(r, "file", limits.MaxBytes)
	if err != nil {
		writeUploadReadError(w, err)
		return
	}
	asset, deduped, err := s.assetService.Upload(ctx, projectID, todoLocalID, name, data)
	if err != nil {
		s.writeAssetUploadError(w, err)
		return
	}
	status := http.StatusCreated
	if deduped {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"assetId":   asset.UUID,
		"url":       "/api/assets/" + asset.UUID,
		"mimeType":  asset.MimeType,
		"sizeBytes": asset.SizeBytes,
		"width":     asset.Width,
		"height":    asset.Height,
		"filename":  store.SanitizeAssetOriginalName(name),
	})
}

var (
	errInvalidMultipart = errors.New("invalid multipart form")
	errUploadTooLarge   = errors.New("upload too large")
)

// readSingleFilePart streams the multipart body and returns the one file part named field,
// read into memory with a hard limit. Any other part, or a second file, is rejected. No temp
// files are created (no ParseMultipartForm).
func readSingleFilePart(r *http.Request, field string, maxBytes int64) (string, []byte, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return "", nil, errInvalidMultipart
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, errInvalidMultipart
	}
	var (
		name string
		data []byte
		seen bool
	)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return "", nil, errUploadTooLarge
			}
			return "", nil, errInvalidMultipart
		}
		if part.FormName() != field || part.FileName() == "" || seen {
			_ = part.Close()
			return "", nil, errInvalidMultipart
		}
		seen = true
		name = part.FileName()
		data, err = io.ReadAll(io.LimitReader(part, maxBytes+1))
		_ = part.Close()
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return "", nil, errUploadTooLarge
			}
			return "", nil, errInvalidMultipart
		}
		if int64(len(data)) > maxBytes {
			return "", nil, errUploadTooLarge
		}
	}
	if !seen {
		return "", nil, errInvalidMultipart
	}
	return name, data, nil
}

func writeUploadReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, errUploadTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image too large", validationDetails("image_too_large", nil))
		return
	}
	writeValidationError(w, "invalid multipart form: send exactly one file field", "invalid_multipart", nil)
}

func (s *Server) writeAssetUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imagecheck.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "image too large", validationDetails("image_too_large", nil))
	case errors.Is(err, imagecheck.ErrUnsupportedFormat):
		writeValidationError(w, "unsupported image type: use PNG, JPEG or GIF", "unsupported_image_type", nil)
	case errors.Is(err, imagecheck.ErrDimensions):
		writeValidationError(w, "image dimensions exceed the limit", "image_dimensions", nil)
	case errors.Is(err, imagecheck.ErrTrailingData):
		writeValidationError(w, "image contains unexpected trailing data", "image_trailing_data", nil)
	case errors.Is(err, imagecheck.ErrTooManyFrames):
		writeValidationError(w, "animation has too many frames", "image_too_many_frames", nil)
	case errors.Is(err, imagecheck.ErrCorrupt), errors.Is(err, imagecheck.ErrInvalidAnimation):
		writeValidationError(w, "invalid image data", "invalid_image", nil)
	case errors.Is(err, assetapp.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "BUSY", "server is processing too many uploads, please retry", nil)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "upload cancelled", nil)
	default:
		if isStoreError(err) {
			writeStoreErr(w, err, true)
			return
		}
		s.logger.Printf("asset upload failed: %v", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
	}
}

func isStoreError(err error) bool {
	for _, target := range []error{store.ErrNotFound, store.ErrUnauthorized, store.ErrForbidden, store.ErrValidation, store.ErrConflict, store.ErrStorageBudget} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
