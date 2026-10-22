package blobstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testUUID = "e2a1b940-7bf3-4f28-86d1-cf3c1538f902"

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPathRejectsNonCanonical(t *testing.T) {
	s := newTestStore(t)
	p, err := s.Path(testUUID, "png")
	if err != nil || p != filepath.Join(s.Root(), "e2", testUUID+".png") {
		t.Fatalf("Path = %q, %v", p, err)
	}
	for _, c := range []struct{ uuid, ext string }{
		{"../../etc/passwd", "png"},
		{"E2A1B940-7BF3-4F28-86D1-CF3C1538F902", "png"},
		{testUUID, "svg"},
		{testUUID, "png/../x"},
		{testUUID + "\x00", "png"},
		{`..\..\x`, "png"},
		{"/abs/" + testUUID, "png"},
	} {
		if _, err := s.Path(c.uuid, c.ext); !errors.Is(err, ErrInvalidAsset) {
			t.Errorf("Path(%q, %q) err = %v", c.uuid, c.ext, err)
		}
	}
}

func TestCreateExclusiveNeverOverwrites(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateExclusive(testUUID, "png", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateExclusive(testUUID, "png", []byte("second")); !errors.Is(err, ErrExists) {
		t.Fatalf("second create err = %v", err)
	}
	f, fi, err := s.Open(testUUID, "png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := make([]byte, fi.Size())
	_, _ = f.Read(b)
	if string(b) != "first" {
		t.Fatalf("content = %q", b)
	}
}

func TestCreateExclusiveRemovesPartialOnWriteError(t *testing.T) {
	s := newTestStore(t)
	s.writeHook = func(f *os.File, b []byte) (int, error) {
		_, _ = f.Write(b[:1])
		return 1, errors.New("disk full")
	}
	if err := s.CreateExclusive(testUUID, "png", []byte("data")); err == nil {
		t.Fatal("want error")
	}
	p, _ := s.Path(testUUID, "png")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
}

func TestCreateExclusiveFromLimitAndHash(t *testing.T) {
	s := newTestStore(t)
	data := bytes.Repeat([]byte("x"), 100)
	n, sum, err := s.CreateExclusiveFrom(testUUID, "gif", bytes.NewReader(data), 100)
	want := sha256.Sum256(data)
	if err != nil || n != 100 || sum != hex.EncodeToString(want[:]) {
		t.Fatalf("CreateExclusiveFrom = %d, %s, %v", n, sum, err)
	}
	other := "0f1e2d3c-4b5a-4968-8778-695a4b3c2d1e"
	if _, _, err := s.CreateExclusiveFrom(other, "gif", bytes.NewReader(data), 99); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over limit err = %v", err)
	}
	p, _ := s.Path(other, "gif")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("oversize file left behind")
	}
}

func TestWalkSkipsForeignAndSymlinks(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateExclusive(testUUID, "png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// Foreign names, a wrong shard, and a nested dir are skipped.
	_ = os.WriteFile(filepath.Join(s.Root(), "e2", "notes.txt"), []byte("x"), 0o600)
	_ = os.MkdirAll(filepath.Join(s.Root(), "ab"), 0o700)
	_ = os.WriteFile(filepath.Join(s.Root(), "ab", testUUID+".png"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(s.Root(), RemovalHoldName+"x"), []byte("x"), 0o600)
	if err := os.Symlink(filepath.Join(s.Root(), "e2", testUUID+".png"), filepath.Join(s.Root(), "e2", "0e2a1b94-7bf3-4f28-86d1-cf3c1538f902.png")); err != nil {
		t.Logf("symlink unsupported here: %v", err)
	}
	var seen []string
	if err := s.Walk(context.Background(), func(uuid, ext string, _ time.Time) error {
		seen = append(seen, uuid+"."+ext)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != testUUID+".png" {
		t.Fatalf("Walk saw %v", seen)
	}
}

func TestWalkMissingRoot(t *testing.T) {
	s := newTestStore(t)
	if err := s.Walk(context.Background(), func(string, string, time.Time) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if s.RootExists() {
		t.Fatal("root must not be created by Walk")
	}
}

func TestRemovalHoldBlocksRemove(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateExclusive(testUUID, "png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	hold := filepath.Join(s.Root(), RemovalHoldName)
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(testUUID, "png"); !errors.Is(err, ErrRemovalHeld) {
		t.Fatalf("Remove under hold err = %v", err)
	}
	// Failed writes keep their partial file under a hold too.
	s.writeHook = func(*os.File, []byte) (int, error) { return 0, errors.New("boom") }
	other := "0f1e2d3c-4b5a-4968-8778-695a4b3c2d1e"
	_ = s.CreateExclusive(other, "png", []byte("x"))
	if p, _ := s.Path(other, "png"); !fileExists(p) {
		t.Fatal("partial file removed under hold")
	}
	if p, _ := s.Path(testUUID, "png"); !fileExists(p) {
		t.Fatal("file removed under hold")
	}
	_ = os.Remove(hold)
	if err := s.Remove(testUUID, "png"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(testUUID, "png"); err != nil {
		t.Fatalf("second Remove must be a no-op: %v", err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestPendingHoldRelease(t *testing.T) {
	p := NewPending()
	r1 := p.Hold(testUUID)
	r2 := p.Hold(testUUID)
	r1()
	r1()
	if !p.Held(testUUID) {
		t.Fatal("second hold released early")
	}
	r2()
	if p.Held(testUUID) {
		t.Fatal("still held after release")
	}
}
