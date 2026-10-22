package backuparchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

const (
	idA = "11111111-1111-4111-8111-111111111111"
	idB = "22222222-2222-4222-9222-222222222222"
	idC = "33333333-3333-4333-a333-333333333333"
)

func assetFor(id, mime string, data []byte) store.AssetExport {
	sum := sha256.Sum256(data)
	return store.AssetExport{ID: id, MimeType: mime, SizeBytes: int64(len(data)), Width: 1, Height: 1, SHA256: hex.EncodeToString(sum[:])}
}

type fixture struct {
	data   *store.ExportData
	blobs  map[string][]byte
	assets []store.AssetExport
}

func newFixture() fixture {
	blobs := map[string][]byte{idA: []byte("png-bytes-a"), idB: []byte("jpeg-bytes-b"), idC: []byte("gif-bytes-c")}
	a := assetFor(idA, "image/png", blobs[idA])
	b := assetFor(idB, "image/jpeg", blobs[idB])
	c := assetFor(idC, "image/gif", blobs[idC])
	data := &store.ExportData{
		Version:    "1.3",
		ExportedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Mode:       "full",
		Scope:      "full",
		Projects: []store.ProjectExport{
			{Name: "One", Slug: "one", Todos: []store.TodoExport{}, Tags: []store.TagExport{}, Assets: []store.AssetExport{a, b}},
			{Name: "Two", Slug: "two", Todos: []store.TodoExport{}, Tags: []store.TagExport{}, Assets: []store.AssetExport{c}},
		},
	}
	return fixture{data: data, blobs: blobs, assets: []store.AssetExport{a, b, c}}
}

func (f fixture) write(t testing.TB) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteManifest(f.data); err != nil {
		t.Fatal(err)
	}
	for _, a := range f.assets {
		if err := w.WriteAsset(a, bytes.NewReader(f.blobs[a.ID])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(b []byte, lim Limits) (*Archive, error) {
	return Open(bytes.NewReader(b), int64(len(b)), lim)
}

type rawEntry struct {
	name   string
	data   []byte
	method uint16
	mode   fs.FileMode
	flags  uint16
	size   uint64 // header override (0 = actual)
}

// rawZip builds archives with arbitrary (possibly hostile) entries.
func rawZip(t testing.TB, entries []rawEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		var w io.Writer
		var err error
		if e.size != 0 {
			h.CompressedSize64 = uint64(len(e.data))
			h.UncompressedSize64 = e.size
			h.CRC32 = 0
			w, err = zw.CreateRaw(h)
		} else {
			w, err = zw.CreateHeader(h)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manifestJSON(t testing.TB, data any) []byte {
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTripAndDeterminism(t *testing.T) {
	f := newFixture()
	b := f.write(t)
	if !bytes.Equal(b, f.write(t)) {
		t.Fatal("writer output is not deterministic")
	}
	a, err := open(b, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Assets) != 3 || a.ProjectOf[idC] != 1 || a.Data.Projects[0].Name != "One" {
		t.Fatalf("archive %+v", a.Assets)
	}
	for id, want := range f.blobs {
		got, err := a.ReadAsset(id)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("asset %s: %v", id, err)
		}
	}
}

func TestWriterRejectsChangedBytes(t *testing.T) {
	f := newFixture()
	w := NewWriter(io.Discard)
	if err := w.WriteManifest(f.data); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteAsset(f.assets[0], strings.NewReader("tampered")); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenRejectsHostileStructure(t *testing.T) {
	f := newFixture()
	good := manifestJSON(t, f.data)
	assetEntries := func() []rawEntry {
		var out []rawEntry
		for _, a := range f.assets {
			ext, _ := store.AssetExtensionForMIME(a.MimeType)
			out = append(out, rawEntry{name: AssetEntryName(a.ID, ext), data: f.blobs[a.ID]})
		}
		return out
	}
	with := func(extra ...rawEntry) []byte {
		return rawZip(t, append(append([]rawEntry{{name: ManifestName, data: good}}, assetEntries()...), extra...))
	}
	v12 := *f.data
	v12.Version = "1.2"
	unknown := strings.Replace(string(good), `"version":"1.3"`, `"version":"1.3","evil":true`, 1)
	reused := newFixture()
	reused.data.Projects[1].Assets = []store.AssetExport{reused.assets[0]}
	badMime := newFixture()
	badMime.data.Projects[1].Assets[0].MimeType = "image/png"

	cases := map[string][]byte{
		"not a zip":                  []byte("not a zip at all"),
		"path traversal":             with(rawEntry{name: "../evil.png", data: []byte("x")}),
		"absolute path":              with(rawEntry{name: "/abs/" + idA + ".png", data: []byte("x")}),
		"nested traversal":           with(rawEntry{name: "assets/../../x.png", data: []byte("x")}),
		"backslash":                  with(rawEntry{name: `assets\` + idA + ".png", data: []byte("x")}),
		"uppercase id":               with(rawEntry{name: "assets/" + strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa") + ".png", data: []byte("x")}),
		"svg entry":                  with(rawEntry{name: "assets/" + idA + ".svg", data: []byte("x")}),
		"duplicate entry":            with(rawEntry{name: "assets/" + idA + ".png", data: []byte("x")}),
		"duplicate manifest":         with(rawEntry{name: ManifestName, data: good}),
		"symlink entry":              with(rawEntry{name: "assets/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.png", data: []byte("x"), mode: fs.ModeSymlink | 0o777}),
		"encrypted entry":            with(rawEntry{name: "assets/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.png", data: []byte("x"), flags: 1}),
		"unknown method":             with(rawEntry{name: "assets/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.png", data: []byte("x"), method: 99, size: 1}),
		"unknown top-level entry":    with(rawEntry{name: "readme.txt", data: []byte("x")}),
		"missing manifest":           rawZip(t, assetEntries()),
		"manifest unknown field":     rawZip(t, append([]rawEntry{{name: ManifestName, data: []byte(unknown)}}, assetEntries()...)),
		"manifest version 1.2":       rawZip(t, append([]rawEntry{{name: ManifestName, data: manifestJSON(t, &v12)}}, assetEntries()...)),
		"entry without manifest row": with(rawEntry{name: "assets/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.png", data: []byte("x")}),
		"manifest row without entry": rawZip(t, append([]rawEntry{{name: ManifestName, data: good}}, assetEntries()[:2]...)),
		"mime/extension mismatch":    rawZip(t, append([]rawEntry{{name: ManifestName, data: manifestJSON(t, badMime.data)}}, assetEntries()...)),
		"id reused across projects":  rawZip(t, append([]rawEntry{{name: ManifestName, data: manifestJSON(t, reused.data)}}, assetEntries()[:2]...)),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := open(b, DefaultLimits); !errors.Is(err, ErrInvalidArchive) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	tooMany := make([]rawEntry, 0, 6)
	tooMany = append(tooMany, rawEntry{name: ManifestName, data: good})
	tooMany = append(tooMany, assetEntries()...)
	lim := DefaultLimits
	lim.MaxEntries = 3
	if _, err := open(rawZip(t, tooMany), lim); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("too many entries: %v", err)
	}
}

func TestLyingHeaderIsCaughtByCountingReader(t *testing.T) {
	f := newFixture()
	big := bytes.Repeat([]byte("x"), 2048)
	f.blobs[idA] = big
	f.assets[0] = assetFor(idA, "image/png", big)
	f.data.Projects[0].Assets[0] = f.assets[0]
	// Header claims 10 bytes; the stored data is 2048 bytes.
	entries := []rawEntry{{name: ManifestName, data: manifestJSON(t, f.data)}, {name: AssetEntryName(idA, "png"), data: big, method: zip.Store, size: 10}}
	for _, a := range f.assets[1:] {
		ext, _ := store.AssetExtensionForMIME(a.MimeType)
		entries = append(entries, rawEntry{name: AssetEntryName(a.ID, ext), data: f.blobs[a.ID]})
	}
	lim := DefaultLimits
	lim.MaxAssetBytes = 1024
	a, err := open(rawZip(t, entries), DefaultLimits)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	a.limits = lim
	if _, err := a.ReadAsset(idA); err == nil {
		t.Fatal("lying header not caught")
	}
}

func TestTotalExpansionLimit(t *testing.T) {
	f := newFixture()
	b := f.write(t)
	lim := DefaultLimits
	lim.MaxTotalBytes = int64(len(manifestJSON(t, f.data))) + 15
	a, err := open(b, lim)
	if err != nil {
		t.Fatal(err)
	}
	var lastErr error
	for _, as := range f.assets {
		if _, err := a.ReadAsset(as.ID); err != nil {
			lastErr = err
		}
	}
	if !errors.Is(lastErr, ErrInvalidArchive) {
		t.Fatalf("total limit not enforced: %v", lastErr)
	}
}

// C7 regression: highly compressible manifests are legitimate; there is no ratio rule.
func TestHighlyCompressibleManifestAccepted(t *testing.T) {
	f := newFixture()
	todos := make([]store.TodoExport, 0, 20000)
	for i := range 20000 {
		todos = append(todos, store.TodoExport{LocalID: int64(i + 1), Title: "Same title", Body: strings.Repeat("same note text ", 15), Tags: []string{}})
	}
	f.data.Projects[0].Todos = todos
	b := f.write(t)
	manifestSize := len(manifestJSON(t, f.data))
	if manifestSize/len(b) < 30 {
		t.Fatalf("fixture not compressible enough: %d -> %d", manifestSize, len(b))
	}
	if _, err := open(b, DefaultLimits); err != nil {
		t.Fatalf("compressible manifest rejected: %v", err)
	}
}

func TestOpenAssetChecksumMismatch(t *testing.T) {
	f := newFixture()
	f.data.Projects[0].Assets[0].SHA256 = strings.Repeat("0", 64)
	var entries []rawEntry
	entries = append(entries, rawEntry{name: ManifestName, data: manifestJSON(t, f.data)})
	for _, a := range f.assets {
		ext, _ := store.AssetExtensionForMIME(a.MimeType)
		entries = append(entries, rawEntry{name: AssetEntryName(a.ID, ext), data: f.blobs[a.ID]})
	}
	a, err := open(rawZip(t, entries), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadAsset(idA); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzOpen(f *testing.F) {
	f.Add(newFixture().write(f))
	f.Add([]byte("PK\x03\x04"))
	f.Fuzz(func(t *testing.T, b []byte) {
		a, err := open(b, DefaultLimits)
		if err != nil {
			if !errors.Is(err, ErrInvalidArchive) {
				t.Fatalf("unexpected error type: %v", err)
			}
			return
		}
		for id := range a.Assets {
			if _, err := a.ReadAsset(id); err != nil && !errors.Is(err, ErrInvalidArchive) && !errors.Is(err, ErrChecksum) {
				t.Fatalf("unexpected asset error: %v", err)
			}
		}
	})
}
