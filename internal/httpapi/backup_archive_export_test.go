package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
)

func (f *assetHTTPFixture) export(t *testing.T, role, query string) (*http.Response, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/api/backup/export"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	return resp, body, readErr
}

// seedExportImages uploads three images: one in an active story, one in an archived story, and
// one unreferenced draft. It returns their ids.
func (f *assetHTTPFixture) seedExportImages(t *testing.T) (active, archived, draft string) {
	t.Helper()
	active = f.upload(t, "maintainer", "", "a.png", testPNG(t, 1)).assetID()
	archived = f.upload(t, "maintainer", "", "b.png", testPNG(t, 2)).assetID()
	draft = f.upload(t, "maintainer", "", "c.png", testPNG(t, 3)).assetID()
	f.createTodo(t, "Active", "![a](/api/assets/"+active+")", nil)
	arch := f.createTodo(t, "Archived", "![b](/api/assets/"+archived+")", nil)
	if _, err := f.st.ArchiveTodosByLocalID(f.ownerCtx(), f.project.ID, []int64{arch.LocalID}, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	return active, archived, draft
}

func TestExportWithoutImagesStaysJSON(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	f.createTodo(t, "Plain", "no images", nil)
	resp, body, _ := f.export(t, "maintainer", "")
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("export: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), ".json") || strings.Contains(string(body), `"assets"`) {
		t.Fatal("image-free export must stay JSON without an assets field")
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil || parsed["version"] != "1.2" {
		t.Fatalf("version: %v %v", parsed["version"], err)
	}
}

func TestExportWithImagesIsArchive(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	active, archived, draft := f.seedExportImages(t)
	resp, body, err := f.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %s %v", resp.StatusCode, resp.Header.Get("Content-Type"), err)
	}
	if !strings.Contains(resp.Header.Get("Content-Disposition"), ".sbz") {
		t.Fatalf("filename: %s", resp.Header.Get("Content-Disposition"))
	}
	a, err := backuparchive.Open(bytes.NewReader(body), int64(len(body)), backuparchive.DefaultLimits)
	if err != nil {
		t.Fatalf("archive invalid: %v", err)
	}
	if a.Data.Version != "1.3" {
		t.Fatalf("version %s", a.Data.Version)
	}
	if _, ok := a.Assets[active]; !ok {
		t.Fatal("active story image missing")
	}
	if _, ok := a.Assets[archived]; !ok {
		t.Fatal("archived story image missing")
	}
	if _, ok := a.Assets[draft]; ok {
		t.Fatal("unreferenced draft exported")
	}
	got, err := a.ReadAsset(active)
	if err != nil || !bytes.Equal(got, testPNG(t, 1)) {
		t.Fatalf("asset bytes: %v", err)
	}
}

func TestExportScopeExcludesNonMaintainerProjects(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	f.seedExportImages(t)
	resp, body, _ := f.export(t, "viewer", "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), `"assets"`) || resp.Header.Get("Content-Type") == "application/zip" {
		t.Fatalf("viewer export leaked assets: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestExportMissingFileFailsVisibly(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	active, _, _ := f.seedExportImages(t)
	if err := f.blobs.Remove(active, "png"); err != nil {
		t.Fatal(err)
	}
	resp, body, _ := f.export(t, "maintainer", "")
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "EXPORT_INCOMPLETE") || bytes.HasPrefix(body, []byte("PK")) {
		t.Fatalf("missing file: %d %s", resp.StatusCode, body)
	}
}

func TestExportMidStreamFailureAbortsConnection(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	active, _, _ := f.seedExportImages(t)
	path, _ := f.blobs.Path(active, "png")
	archiveExportAfterPreflight = func() {
		if err := os.WriteFile(path, []byte("truncated"), 0o600); err != nil {
			t.Error(err)
		}
	}
	defer func() { archiveExportAfterPreflight = nil }()
	// The abort happens before buffered bytes are flushed, so the client may transparently retry the
	// GET; the retry then fails preflight. Either way the client must never get a valid archive.
	resp, body, readErr := f.export(t, "maintainer", "")
	switch {
	case resp.StatusCode == http.StatusInternalServerError:
		if !strings.Contains(string(body), "EXPORT_INCOMPLETE") {
			t.Fatalf("retry body: %s", body)
		}
	case resp.StatusCode == http.StatusOK:
		if readErr == nil {
			if _, err := backuparchive.Open(bytes.NewReader(body), int64(len(body)), backuparchive.DefaultLimits); err == nil {
				t.Fatal("a failed export produced a valid archive")
			}
		}
	default:
		t.Fatalf("status %d %s", resp.StatusCode, body)
	}
}

func TestExportClientMaxBytesGuard(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	f.createTodo(t, "Plain", "no images", nil)
	unguarded, plainBody, _ := f.export(t, "maintainer", "")
	guarded, guardedBody, _ := f.export(t, "maintainer", "?clientMaxBytes=8388608")
	if unguarded.StatusCode != http.StatusOK || guarded.StatusCode != http.StatusOK {
		t.Fatal("small JSON export must pass the guard")
	}
	// exportedAt differs between calls; compare everything else.
	strip := func(b []byte) string {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		delete(m, "exportedAt")
		out, _ := json.Marshal(m)
		return string(out)
	}
	if strip(plainBody) != strip(guardedBody) {
		t.Fatal("guarded JSON export differs from unguarded")
	}
	if r, body, _ := f.export(t, "maintainer", "?clientMaxBytes=10"); r.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(body), "EXPORT_TOO_LARGE_FOR_CLIENT") {
		t.Fatalf("oversize JSON: %d %s", r.StatusCode, body)
	}
	for _, bad := range []string{"0", "-1", "67108865", "abc"} {
		if r, _, _ := f.export(t, "maintainer", "?clientMaxBytes="+bad); r.StatusCode != http.StatusBadRequest {
			t.Fatalf("clientMaxBytes=%s: %d", bad, r.StatusCode)
		}
	}
	f.seedExportImages(t)
	r, body, _ := f.export(t, "maintainer", "?clientMaxBytes=8388608")
	if r.StatusCode != http.StatusConflict || !strings.Contains(string(body), "ARCHIVE_EXPORT_UNSUPPORTED_CLIENT") || bytes.HasPrefix(body, []byte("PK")) {
		t.Fatalf("archive to native client: %d %s", r.StatusCode, body)
	}
}

func TestJSONImportRejectsArchiveVersions(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	post := func(payload string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/backup/preview", strings.NewReader(payload))
		req.Header.Set("X-Scrumboy", "1")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: f.tokens["maintainer"]})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	v13 := `{"importMode":"merge","data":{"version":"1.3","exportedAt":"2026-01-01T00:00:00Z","mode":"full","scope":"full","projects":[]}}`
	if code, body := post(v13); code != http.StatusBadRequest || !strings.Contains(body, ".sbz") {
		t.Fatalf("1.3 JSON: %d %s", code, body)
	}
	asset := `{"id":"11111111-1111-4111-8111-111111111111","mimeType":"image/png","sizeBytes":1,"width":1,"height":1,"sha256":"` + strings.Repeat("a", 64) + `"}`
	v12 := `{"importMode":"merge","data":{"version":"1.2","exportedAt":"2026-01-01T00:00:00Z","mode":"full","scope":"full","projects":[{"slug":"x","name":"X","createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z","expiresAt":null,"priorityTiers":[],"todos":[],"tags":[],"assets":[` + asset + `]}]}}`
	if code, body := post(v12); code != http.StatusBadRequest || !strings.Contains(body, "image assets") {
		t.Fatalf("1.2 JSON with assets: %d %s", code, body)
	}
}
