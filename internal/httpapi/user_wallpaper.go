package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scrumboy/internal/store"
)

const (
	wallpaperPrefKey     = "wallpaper"
	wallpaperMaxBytes    = 6 << 20 // 6 MiB raw multipart
	wallpaperJPEGQuality = 85
	wallpaperMaxDim      = 2560
)

// userWallpaperFilePath returns the on-disk path for the user's normalized JPEG wallpaper.
func (s *Server) userWallpaperFilePath(userID int64) string {
	return filepath.Join(s.dataDir, "user-wallpapers", fmt.Sprintf("%d.jpg", userID))
}

func (s *Server) ensureWallpaperDir() error {
	dir := filepath.Join(s.dataDir, "user-wallpapers")
	return os.MkdirAll(dir, 0o700)
}

func (s *Server) deleteUserWallpaperFile(userID int64) {
	path := s.userWallpaperFilePath(userID)
	_ = os.Remove(path)
}

// handleUserWallpaper routes DELETE /api/user/wallpaper, GET/POST /api/user/wallpaper/image
// rest is like ["wallpaper"] or ["wallpaper","image"] (path after /api/user/).
func (s *Server) handleUserWallpaper(w http.ResponseWriter, r *http.Request, userID int64, rest []string) {
	if s.dataDir == "" {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "wallpaper storage not configured", nil)
		return
	}

	ctx := s.requestContext(r)

	switch {
	case len(rest) == 1 && rest[0] == "wallpaper" && r.Method == http.MethodDelete:
		s.deleteUserWallpaper(w, r, ctx, userID)
		return

	case len(rest) == 2 && rest[0] == "wallpaper" && rest[1] == "image" && r.Method == http.MethodGet:
		s.serveUserWallpaperImage(w, r, ctx, userID)
		return

	case len(rest) == 2 && rest[0] == "wallpaper" && rest[1] == "image" && r.Method == http.MethodPost:
		s.uploadUserWallpaperImage(w, r, ctx, userID)
		return

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	}
}

func (s *Server) wallpaperPrefModeImage(ctx context.Context, userID int64) (store.WallpaperPref, bool) {
	raw, err := s.store.GetUserPreference(ctx, userID, wallpaperPrefKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return store.WallpaperPref{}, false
	}
	p, err := store.ParseWallpaperPref(raw)
	if err != nil || p.Mode != "image" {
		return store.WallpaperPref{}, false
	}
	return p, true
}

func (s *Server) serveUserWallpaperImage(w http.ResponseWriter, r *http.Request, ctx context.Context, userID int64) {
	p, ok := s.wallpaperPrefModeImage(ctx, userID)
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "no wallpaper image", nil)
		return
	}
	path := s.userWallpaperFilePath(userID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "wallpaper file missing", nil)
			return
		}
		writeInternal(w, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("ETag", strconv.FormatInt(p.Rev, 10))
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == strconv.FormatInt(p.Rev, 10) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

func decodeWallpaperUpload(data []byte, contentType string) (image.Image, error) {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	r := bytes.NewReader(data)
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return jpeg.Decode(r)
	case strings.Contains(ct, "png"):
		return png.Decode(r)
	case strings.Contains(ct, "gif"):
		return gif.Decode(r)
	default:
		return nil, fmt.Errorf("use JPEG, PNG, or GIF")
	}
}

func encodeWallpaperJPEG(img image.Image) (*bytes.Buffer, error) {
	bounds := img.Bounds()
	dx := bounds.Dx()
	dy := bounds.Dy()
	if dx <= 0 || dy <= 0 {
		return nil, fmt.Errorf("invalid dimensions")
	}
	if dx > wallpaperMaxDim || dy > wallpaperMaxDim {
		scale := float64(wallpaperMaxDim) / float64(max(dx, dy))
		newW := int(float64(dx) * scale)
		newH := int(float64(dy) * scale)
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}
		img = resizeNearest(img, newW, newH)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: wallpaperJPEGQuality}); err != nil {
		return nil, err
	}
	return &buf, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// resizeNearest simple resize for large photos (good enough for background wallpaper).
func resizeNearest(src image.Image, newW, newH int) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xratio := float64(b.Dx()) / float64(newW)
	yratio := float64(b.Dy()) / float64(newH)
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			sx := int(float64(x) * xratio)
			sy := int(float64(y) * yratio)
			if sx >= b.Dx() {
				sx = b.Dx() - 1
			}
			if sy >= b.Dy() {
				sy = b.Dy() - 1
			}
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func (s *Server) uploadUserWallpaperImage(w http.ResponseWriter, r *http.Request, ctx context.Context, userID int64) {
	if err := r.ParseMultipartForm(wallpaperMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid multipart form", nil)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "missing file field", map[string]any{"field": "file"})
		return
	}
	defer file.Close()

	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	if !strings.HasPrefix(strings.ToLower(ct), "image/") {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "file must be an image", map[string]any{"field": "file"})
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, wallpaperMaxBytes))
	if err != nil {
		writeInternal(w, err)
		return
	}
	img, err := decodeWallpaperUpload(data, ct)
	if err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), map[string]any{"field": "file"})
		return
	}

	buf, err := encodeWallpaperJPEG(img)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if buf.Len() > 3<<20 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "processed image too large", nil)
		return
	}

	if err := s.ensureWallpaperDir(); err != nil {
		writeInternal(w, err)
		return
	}
	path := s.userWallpaperFilePath(userID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		writeInternal(w, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		writeInternal(w, err)
		return
	}

	rev := time.Now().UTC().UnixMilli()
	pref := store.WallpaperPref{V: store.WallpaperPrefVersion, Mode: "image", Rev: rev}
	b, err := json.Marshal(pref)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if err := s.store.SetUserPreference(ctx, userID, wallpaperPrefKey, string(b)); err != nil {
		writeStoreErr(w, err, true)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"rev": rev, "mode": "image"})
}

func (s *Server) deleteUserWallpaper(w http.ResponseWriter, r *http.Request, ctx context.Context, userID int64) {
	s.deleteUserWallpaperFile(userID)
	off := store.WallpaperPref{V: store.WallpaperPrefVersion, Mode: "off"}
	b, err := json.Marshal(off)
	if err != nil {
		writeInternal(w, err)
		return
	}
	if err := s.store.SetUserPreference(ctx, userID, wallpaperPrefKey, string(b)); err != nil {
		writeStoreErr(w, err, true)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
