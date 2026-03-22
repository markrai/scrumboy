package httpapi

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	// FIRST: Service worker (version-injected, must not be served as raw static file)
	if path == "sw.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.swJS)
		return
	}

	// SECOND: Handle real static assets (files that exist in webFS)
	// Only check when path is not empty (don't treat / as a static asset)
	if path != "" {
		if fi, err := fs.Stat(s.webFS, path); err == nil && !fi.IsDir() {
			if strings.HasPrefix(path, "dist/") {
				// no-store: prevents Cloudflare (and any CDN/proxy) from caching dist modules.
				// no-cache alone is insufficient — Cloudflare can still serve a stale edge copy
				// while only telling the browser to revalidate. Each deployment embeds new JS
				// files and they must be served fresh from the origin every time.
				w.Header().Set("Cache-Control", "no-store")
			}
			s.fileSrv.ServeHTTP(w, r)
			return
		}
	}

	// SECOND: Explicit entrypoints for creating anonymous boards
	// Only GET is allowed; other methods should not have side effects.
	if path == "temp" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		http.Redirect(w, r, "/anon", http.StatusFound)
		return
	}
	if path == "anon" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
			return
		}
		project, err := s.store.CreateAnonymousBoard(s.requestContext(r))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to create board", nil)
			return
		}
		http.Redirect(w, r, "/"+project.Slug, http.StatusFound)
		return
	}

	// THIRD: Legacy /p/{id} route - redirect to canonical /{slug}
	if strings.HasPrefix(path, "p/") {
		idStr := strings.TrimPrefix(path, "p/")
		// Remove trailing slash if present
		idStr = strings.TrimSuffix(idStr, "/")
		projectID, ok := parseInt64(idStr)
		if ok {
			project, err := s.store.GetProject(s.requestContext(r), projectID)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusNotFound, "NOT_FOUND", "project not found", nil)
					return
				}
				writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to load project", nil)
				return
			}
			// Redirect to canonical slug URL
			http.Redirect(w, r, "/"+project.Slug, http.StatusFound)
			return
		}
		// If not a valid ID, fall through to SPA (might be a static file or other route)
	}

	// FOURTH: Root routing is idempotent in all modes
	if path == "" && s.mode == "anonymous" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(s.landingHTML)
		return
	}

	// FOURTH: Serve SPA index.html for all other routes
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.indexHTML)
}
