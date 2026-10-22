package httpapi

import (
	"fmt"
	"net/http"
	"strconv"

	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

// archiveExportAfterPreflight lets tests change files between the preflight check and streaming.
var archiveExportAfterPreflight func()

// maxClientBackupBytes caps the clientMaxBytes a native client may declare.
const maxClientBackupBytes = 64 << 20

// parseClientMaxBytes reads the optional ?clientMaxBytes= guard. ok is false for an invalid value.
func parseClientMaxBytes(r *http.Request) (n int64, guarded bool, ok bool) {
	q := r.URL.Query()
	if !q.Has("clientMaxBytes") {
		return 0, false, true
	}
	v, err := strconv.ParseInt(q.Get("clientMaxBytes"), 10, 64)
	if err != nil || v < 1 || v > maxClientBackupBytes {
		return 0, true, false
	}
	return v, true, true
}

// writeArchiveExport streams a .sbz archive. Every referenced file is checked before the first
// body byte (missing files give a visible 500), and any failure mid-stream aborts the connection
// so the client never receives a seemingly complete archive. No DB transaction is held here.
func (s *Server) writeArchiveExport(w http.ResponseWriter, data *store.ExportData, filename string) {
	type entry struct {
		asset store.AssetExport
		ext   string
	}
	var entries []entry
	var missing []string
	for _, p := range data.Projects {
		for _, a := range p.Assets {
			ext, ok := store.AssetExtensionForMIME(a.MimeType)
			if !ok || s.assetBlobs == nil {
				missing = append(missing, a.ID)
				continue
			}
			f, fi, err := s.assetBlobs.Open(a.ID, ext)
			if err != nil {
				missing = append(missing, a.ID)
				continue
			}
			_ = f.Close()
			if fi.Size() != a.SizeBytes {
				missing = append(missing, a.ID)
				continue
			}
			entries = append(entries, entry{a, ext})
		}
	}
	if len(missing) > 0 {
		s.logger.Printf("backup export incomplete: %d image files missing or changed: %v", len(missing), missing)
		writeError(w, http.StatusInternalServerError, "EXPORT_INCOMPLETE", "some images in this backup are missing on the server; the export was not created", map[string]any{"missing": len(missing)})
		return
	}

	if archiveExportAfterPreflight != nil {
		archiveExportAfterPreflight()
	}
	archived := *data
	archived.Version = version.ArchiveExportFormatVersion
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)

	zw := backuparchive.NewWriter(w)
	abort := func(err error) {
		s.logger.Printf("backup export aborted mid-stream: %v", err)
		panic(http.ErrAbortHandler)
	}
	if err := zw.WriteManifest(exportDataToJSON(&archived)); err != nil {
		abort(err)
	}
	for _, e := range entries {
		f, _, err := s.assetBlobs.Open(e.asset.ID, e.ext)
		if err != nil {
			abort(err)
		}
		err = zw.WriteAsset(e.asset, f)
		_ = f.Close()
		if err != nil {
			abort(err)
		}
	}
	if err := zw.Close(); err != nil {
		abort(err)
	}
}
