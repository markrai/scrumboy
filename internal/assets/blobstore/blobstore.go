// Package blobstore stores immutable image asset files at DATA_DIR/assets/{uuid[0:2]}/{uuid}.{ext}.
//
// Files are created exactly once (O_EXCL) and fully fsynced before callers insert the metadata
// row that makes them reachable. Remove is the only function in Scrumboy that unlinks asset
// files; it honors the operator-managed DR removal hold (DATA_DIR/assets/.removal-hold).
package blobstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/assetrefs"
)

const (
	// RemovalHoldName is the operator-managed file that suspends every asset unlink while present.
	RemovalHoldName = ".removal-hold"
	// RemovalHoldSettle is the documented wait after creating the hold before taking a backup.
	RemovalHoldSettle = 5 * time.Second
)

var (
	ErrExists       = errors.New("blobstore: asset file already exists")
	ErrTooLarge     = errors.New("blobstore: asset exceeds size limit")
	ErrRemovalHeld  = errors.New("blobstore: removal hold is active")
	ErrInvalidAsset = errors.New("blobstore: invalid asset identity")
)

// Store is the asset file store rooted at DATA_DIR/assets.
type Store struct {
	root string
	// writeHook lets tests inject a write fault after the file is created.
	writeHook func(*os.File, []byte) (int, error)
}

// New returns a store rooted at dataDir/assets. The directory is created lazily on first write.
func New(dataDir string) (*Store, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("blobstore: data dir required")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("blobstore: resolve data dir: %w", err)
	}
	return &Store{root: filepath.Join(abs, "assets")}, nil
}

// Root returns DATA_DIR/assets.
func (s *Store) Root() string { return s.root }

// RootExists reports whether the assets directory has been created.
func (s *Store) RootExists() bool {
	fi, err := os.Lstat(s.root)
	return err == nil && fi.IsDir()
}

func validExt(ext string) bool { return ext == "png" || ext == "jpg" || ext == "gif" }

// Path returns the on-disk path for an asset. Non-canonical UUIDs and unknown extensions are
// rejected, so the result can never escape the root.
func (s *Store) Path(uuid, ext string) (string, error) {
	if !assetrefs.IsCanonicalUUID(uuid) || !validExt(ext) {
		return "", ErrInvalidAsset
	}
	return filepath.Join(s.root, uuid[:2], uuid+"."+ext), nil
}

// CreateExclusive writes data to a new file for uuid, failing with ErrExists if it already exists.
// The file is fsynced and closed before return.
func (s *Store) CreateExclusive(uuid, ext string, data []byte) error {
	f, path, err := s.openExclusive(uuid, ext)
	if err != nil {
		return err
	}
	write := func(f *os.File, b []byte) (int, error) { return f.Write(b) }
	if s.writeHook != nil {
		write = s.writeHook
	}
	if _, err := write(f, data); err != nil {
		return s.abandon(f, path, err)
	}
	return s.finish(f, path)
}

// CreateExclusiveFrom streams at most max bytes from r into a new file for uuid and returns the
// byte count and lowercase hex SHA-256. Exceeding max fails with ErrTooLarge.
func (s *Store) CreateExclusiveFrom(uuid, ext string, r io.Reader, max int64) (int64, string, error) {
	f, path, err := s.openExclusive(uuid, ext)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, max+1))
	if err != nil {
		return 0, "", s.abandon(f, path, err)
	}
	if n > max {
		return 0, "", s.abandon(f, path, ErrTooLarge)
	}
	if err := s.finish(f, path); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) openExclusive(uuid, ext string) (*os.File, string, error) {
	path, err := s.Path(uuid, ext)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", fmt.Errorf("blobstore: create shard dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, "", ErrExists
		}
		return nil, "", fmt.Errorf("blobstore: create asset file: %w", err)
	}
	return f, path, nil
}

// abandon closes and removes a partially written file this call created. Under a removal hold the
// partial file stays; it has no row, so the sweep reclaims it later.
func (s *Store) abandon(f *os.File, path string, cause error) error {
	_ = f.Close()
	if !s.RemovalHeld() {
		_ = os.Remove(path)
	}
	return cause
}

func (s *Store) finish(f *os.File, path string) error {
	if err := f.Sync(); err != nil {
		return s.abandon(f, path, fmt.Errorf("blobstore: fsync asset: %w", err))
	}
	if err := f.Close(); err != nil {
		if !s.RemovalHeld() {
			_ = os.Remove(path)
		}
		return fmt.Errorf("blobstore: close asset: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

// syncDir best-effort fsyncs a directory so the new entry is durable. Not supported on Windows.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}

// Open opens an asset file for reading.
func (s *Store) Open(uuid, ext string) (*os.File, os.FileInfo, error) {
	path, err := s.Path(uuid, ext)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fs.ErrNotExist
	}
	return f, fi, nil
}

// RemovalHeld reports whether the DR removal hold file exists. It is never cached.
func (s *Store) RemovalHeld() bool {
	_, err := os.Lstat(filepath.Join(s.root, RemovalHoldName))
	return err == nil
}

// Remove is the only unlink path for asset files. It returns ErrRemovalHeld (leaving the file)
// while the removal hold exists, and nil if the file is already absent.
func (s *Store) Remove(uuid, ext string) error {
	path, err := s.Path(uuid, ext)
	if err != nil {
		return err
	}
	if s.RemovalHeld() {
		return ErrRemovalHeld
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blobstore: remove asset: %w", err)
	}
	return nil
}

// Walk visits every well-formed asset file (root/<shard>/<uuid>.<ext> whose shard matches the
// UUID). Other names, directories and symlinks are skipped and never deleted.
func (s *Store) Walk(ctx context.Context, fn func(uuid, ext string, mod time.Time) error) error {
	shards, err := os.ReadDir(s.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("blobstore: read root: %w", err)
	}
	for _, shard := range shards {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !shard.IsDir() || shard.Type()&fs.ModeSymlink != 0 || len(shard.Name()) != 2 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.root, shard.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			name := e.Name()
			dot := strings.LastIndexByte(name, '.')
			if dot != 36 {
				continue
			}
			uuid, ext := name[:dot], name[dot+1:]
			if !assetrefs.IsCanonicalUUID(uuid) || !validExt(ext) || uuid[:2] != shard.Name() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if err := fn(uuid, ext, info.ModTime()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Pending tracks asset UUIDs whose files are being written but whose rows are not yet committed,
// so the orphan sweep never removes an in-flight file. It is process-local (SQLite is single-process).
type Pending struct {
	mu sync.Mutex
	m  map[string]int
}

// NewPending returns an empty registry.
func NewPending() *Pending { return &Pending{m: map[string]int{}} }

// Hold marks uuid as in flight until the returned release func is called (idempotent).
func (p *Pending) Hold(uuid string) (release func()) {
	p.mu.Lock()
	p.m[uuid]++
	p.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			if p.m[uuid] <= 1 {
				delete(p.m, uuid)
			} else {
				p.m[uuid]--
			}
			p.mu.Unlock()
		})
	}
}

// Held reports whether uuid is currently in flight.
func (p *Pending) Held(uuid string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[uuid] > 0
}
