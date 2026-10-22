package httpapi

import (
	"errors"
	"io/fs"
	"net/http"
	"time"

	publicboardapp "scrumboy/internal/application/publicboard"
	"scrumboy/internal/store"
)

// handleAssets serves GET/HEAD /api/assets/{uuid}. Members (Viewer or above) of the asset's
// durable project may read it. Everyone else, signed in or not, may read it only through the
// public-board boundary: the project must resolve as an eligible public board and a non-archived
// story must embed the asset. Every denial and unknown asset is a 404.
func (s *Server) handleAssets(w http.ResponseWriter, r *http.Request, rest []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
		return
	}
	if len(rest) != 1 || !store.IsCanonicalAssetUUID(rest[0]) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	ctx := s.requestContext(r)
	info, err := s.store.GetAssetForServe(ctx, rest[0])
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		writeInternal(w, err)
		return
	}
	if !info.MemberRole.HasMinimumRole(store.RoleViewer) {
		if !s.assetPublicLimit.Allow("ip:"+s.clientIP(r), "") {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
			return
		}
		prepared, err := s.publicBoardReads.Resolve(r.Context(), info.ProjectSlug)
		if err != nil {
			s.writeAssetPublicError(w, err)
			return
		}
		ok, err := prepared.AssetReferenced(r.Context(), info.Asset.ID)
		if err != nil {
			s.writeAssetPublicError(w, err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
			return
		}
		setPublicResponseHeaders(w)
	}
	s.serveAssetFile(w, r, info.Asset)
}

func (s *Server) writeAssetPublicError(w http.ResponseWriter, err error) {
	if errors.Is(err, publicboardapp.ErrPublicNotFound) {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	s.logger.Printf("public asset read failed: %v", err)
	writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}

func (s *Server) serveAssetFile(w http.ResponseWriter, r *http.Request, a store.ProjectAsset) {
	ext, ok := store.AssetExtensionForMIME(a.MimeType)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	f, fi, err := s.assetBlobs.Open(a.UUID, ext)
	if err != nil || fi.Size() != a.SizeBytes {
		if err == nil {
			_ = f.Close()
		}
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			s.logger.Printf("asset file missing or size mismatch uuid=%s", a.UUID)
		} else {
			s.logger.Printf("asset open failed uuid=%s: %v", a.UUID, err)
		}
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", a.MimeType)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", `"`+a.SHA256+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Referrer-Policy", "no-referrer")
	http.ServeContent(w, r, "", time.Time{}, f)
}
