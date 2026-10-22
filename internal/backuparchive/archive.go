// Package backuparchive reads and writes .sbz project backups: a ZIP whose first entry is
// manifest.json (store.ExportData, version 1.3, with per-project assets) followed by
// assets/<id>.<png|jpg|gif> entries holding the image bytes.
//
// The reader trusts nothing in the archive: entry names are matched against one exact pattern,
// header sizes are advisory (every read is counted), and asset bytes are verified against the
// manifest's size and SHA-256. There is no compression-ratio rule; JSON manifests legitimately
// compress far beyond 10:1, so absolute ceilings bound the work instead.
package backuparchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

const (
	ManifestName = "manifest.json"
	assetsDir    = "assets/"
)

var (
	// ErrInvalidArchive reports a structurally invalid or over-limit archive.
	ErrInvalidArchive = errors.New("invalid backup archive")
	// ErrChecksum reports asset bytes that do not match the manifest.
	ErrChecksum = errors.New("backup archive asset checksum mismatch")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidArchive, fmt.Sprintf(format, args...))
}

// Limits are absolute resource ceilings for reading an archive.
type Limits struct {
	MaxArchiveBytes  int64 // upload ceiling (enforced by the HTTP layer)
	MaxTotalBytes    int64 // total uncompressed bytes read across all entries
	MaxEntries       int
	MaxAssetBytes    int64
	MaxManifestBytes int64
}

// DefaultLimits are the production ceilings.
var DefaultLimits = Limits{
	MaxArchiveBytes:  512 << 20,
	MaxTotalBytes:    1 << 30,
	MaxEntries:       5000,
	MaxAssetBytes:    10 << 20,
	MaxManifestBytes: 64 << 20,
}

// AssetEntryName returns the archive path for an asset.
func AssetEntryName(id, ext string) string { return assetsDir + id + "." + ext }

// fixedModified keeps the writer deterministic.
var fixedModified = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Writer writes an archive. WriteManifest must be called first, then WriteAsset per asset.
type Writer struct {
	zw       *zip.Writer
	manifest bool
}

// NewWriter returns a Writer on w.
func NewWriter(w io.Writer) *Writer { return &Writer{zw: zip.NewWriter(w)} }

// WriteManifest writes manifest.json (Deflate). data must encode as store.ExportData JSON
// (the HTTP layer passes the same projection it uses for JSON exports).
func (w *Writer) WriteManifest(data any) error {
	if w.manifest {
		return errors.New("backuparchive: manifest already written")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("backuparchive: encode manifest: %w", err)
	}
	f, err := w.zw.CreateHeader(&zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: fixedModified})
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		return err
	}
	w.manifest = true
	return nil
}

// WriteAsset copies one asset (Store method; images are already compressed), verifying that the
// bytes match the manifest's size and SHA-256 while copying.
func (w *Writer) WriteAsset(a store.AssetExport, r io.Reader) error {
	if !w.manifest {
		return errors.New("backuparchive: manifest must be written first")
	}
	ext, ok := store.AssetExtensionForMIME(a.MimeType)
	if !ok {
		return fmt.Errorf("backuparchive: unsupported asset type %q", a.MimeType)
	}
	f, err := w.zw.CreateHeader(&zip.FileHeader{Name: AssetEntryName(a.ID, ext), Method: zip.Store, Modified: fixedModified})
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, a.SizeBytes+1))
	if err != nil {
		return fmt.Errorf("backuparchive: copy asset %s: %w", a.ID, err)
	}
	if n != a.SizeBytes || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("%w: asset %s changed on disk", ErrChecksum, a.ID)
	}
	return nil
}

// Close finishes the archive.
func (w *Writer) Close() error { return w.zw.Close() }

// Archive is a validated archive.
type Archive struct {
	Data   *store.ExportData
	Assets map[string]store.AssetExport // by archive id
	// ProjectOf maps an asset id to the index of its project in Data.Projects.
	ProjectOf map[string]int

	files  map[string]*zip.File
	limits Limits
	total  *counter
}

type counter struct {
	mu    sync.Mutex
	n     int64
	limit int64
}

func (c *counter) add(n int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n += n
	if c.limit > 0 && c.n > c.limit {
		return invalid("archive expands beyond %d bytes", c.limit)
	}
	return nil
}

// Open validates the archive structure and manifest. Asset bytes are verified as they are read.
func Open(ra io.ReaderAt, size int64, lim Limits) (*Archive, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return nil, invalid("not a zip archive")
	}
	if lim.MaxEntries > 0 && len(zr.File) > lim.MaxEntries {
		return nil, invalid("too many entries")
	}
	a := &Archive{files: map[string]*zip.File{}, limits: lim, total: &counter{limit: lim.MaxTotalBytes}}
	var manifest *zip.File
	for _, f := range zr.File {
		name := f.Name
		if _, dup := a.files[name]; dup || (manifest != nil && name == ManifestName) {
			return nil, invalid("duplicate entry %q", name)
		}
		if f.Flags&0x1 != 0 {
			return nil, invalid("encrypted entry %q", name)
		}
		if f.Method != zip.Store && f.Method != zip.Deflate {
			return nil, invalid("unsupported compression in %q", name)
		}
		if name == assetsDir && f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, invalid("entry %q is not a regular file", name)
		}
		if name == ManifestName {
			manifest = f
			continue
		}
		if _, _, ok := parseAssetEntryName(name); !ok {
			return nil, invalid("unexpected entry %q", name)
		}
		if lim.MaxAssetBytes > 0 && f.UncompressedSize64 > uint64(lim.MaxAssetBytes) {
			return nil, invalid("entry %q too large", name)
		}
		a.files[name] = f
	}
	if manifest == nil {
		return nil, invalid("missing %s", ManifestName)
	}
	data, err := a.readManifest(manifest)
	if err != nil {
		return nil, err
	}
	a.Data = data
	if err := a.crossCheck(); err != nil {
		return nil, err
	}
	return a, nil
}

func parseAssetEntryName(name string) (string, string, bool) {
	if !strings.HasPrefix(name, assetsDir) {
		return "", "", false
	}
	rest := name[len(assetsDir):]
	dot := strings.LastIndexByte(rest, '.')
	if dot != 36 {
		return "", "", false
	}
	id, ext := rest[:dot], rest[dot+1:]
	if !store.IsCanonicalAssetUUID(id) || (ext != "png" && ext != "jpg" && ext != "gif") {
		return "", "", false
	}
	return id, ext, true
}

func (a *Archive) readManifest(f *zip.File) (*store.ExportData, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("open manifest")
	}
	defer rc.Close()
	b, err := readCounted(rc, a.limits.MaxManifestBytes, a.total)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var data store.ExportData
	if err := dec.Decode(&data); err != nil {
		return nil, invalid("manifest is not valid backup JSON: %v", err)
	}
	if dec.More() {
		return nil, invalid("trailing data after manifest JSON")
	}
	if data.Version != version.ArchiveExportFormatVersion {
		return nil, invalid("manifest version %q (expected %s)", data.Version, version.ArchiveExportFormatVersion)
	}
	return &data, nil
}

func (a *Archive) crossCheck() error {
	a.Assets = map[string]store.AssetExport{}
	a.ProjectOf = map[string]int{}
	for pi, p := range a.Data.Projects {
		for _, as := range p.Assets {
			if !store.IsCanonicalAssetUUID(as.ID) {
				return invalid("asset id %q is not canonical", as.ID)
			}
			if _, dup := a.Assets[as.ID]; dup {
				return invalid("asset %s listed twice", as.ID)
			}
			ext, ok := store.AssetExtensionForMIME(as.MimeType)
			if !ok {
				return invalid("asset %s has unsupported type", as.ID)
			}
			if as.SizeBytes <= 0 || (a.limits.MaxAssetBytes > 0 && as.SizeBytes > a.limits.MaxAssetBytes) || as.Width <= 0 || as.Height <= 0 {
				return invalid("asset %s has invalid metadata", as.ID)
			}
			if len(as.SHA256) != 64 || strings.Trim(as.SHA256, "0123456789abcdef") != "" {
				return invalid("asset %s has invalid sha256", as.ID)
			}
			if _, ok := a.files[AssetEntryName(as.ID, ext)]; !ok {
				return invalid("asset %s has no archive entry", as.ID)
			}
			a.Assets[as.ID] = as
			a.ProjectOf[as.ID] = pi
		}
	}
	for name := range a.files {
		id, ext, _ := parseAssetEntryName(name)
		as, ok := a.Assets[id]
		if !ok {
			return invalid("entry %q is not in the manifest", name)
		}
		if want, _ := store.AssetExtensionForMIME(as.MimeType); want != ext {
			return invalid("entry %q does not match its manifest type", name)
		}
	}
	return nil
}

// OpenAsset returns a reader for the asset's bytes. It fails with ErrInvalidArchive past the
// size limits and with ErrChecksum at EOF if the bytes do not match the manifest.
func (a *Archive) OpenAsset(id string) (io.ReadCloser, error) {
	as, ok := a.Assets[id]
	if !ok {
		return nil, invalid("unknown asset %s", id)
	}
	ext, _ := store.AssetExtensionForMIME(as.MimeType)
	f := a.files[AssetEntryName(id, ext)]
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("open asset %s", id)
	}
	return &verifyingReader{rc: rc, want: as, limit: a.limits.MaxAssetBytes, total: a.total, h: sha256.New()}, nil
}

// ReadAsset reads and verifies one asset fully.
func (a *Archive) ReadAsset(id string) ([]byte, error) {
	rc, err := a.OpenAsset(id)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

type verifyingReader struct {
	rc    io.ReadCloser
	want  store.AssetExport
	limit int64
	total *counter
	h     hash.Hash
	n     int64
}

func (v *verifyingReader) Read(p []byte) (int, error) {
	n, err := v.rc.Read(p)
	if n > 0 {
		v.n += int64(n)
		v.h.Write(p[:n])
		if v.limit > 0 && v.n > v.limit {
			return 0, invalid("asset %s exceeds %d bytes", v.want.ID, v.limit)
		}
		if terr := v.total.add(int64(n)); terr != nil {
			return 0, terr
		}
	}
	if errors.Is(err, io.EOF) {
		if v.n != v.want.SizeBytes || hex.EncodeToString(v.h.Sum(nil)) != v.want.SHA256 {
			return n, fmt.Errorf("%w: asset %s", ErrChecksum, v.want.ID)
		}
	} else if err != nil {
		return n, invalid("read asset %s: %v", v.want.ID, err)
	}
	return n, err
}

func (v *verifyingReader) Close() error { return v.rc.Close() }

func readCounted(r io.Reader, limit int64, total *counter) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, invalid("read entry: %v", err)
	}
	if limit > 0 && int64(len(b)) > limit {
		return nil, invalid("entry exceeds %d bytes", limit)
	}
	if err := total.add(int64(len(b))); err != nil {
		return nil, err
	}
	return b, nil
}
