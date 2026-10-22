package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backupimportapp "scrumboy/internal/application/backupimport"
	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
)

type archiveResponse struct {
	status int
	body   map[string]any
	raw    string
}

func (f *assetHTTPFixture) postArchive(t *testing.T, role, action, importMode, confirmation string, archive []byte) archiveResponse {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("importMode", importMode)
	if confirmation != "" {
		_ = mw.WriteField("confirmation", confirmation)
	}
	fw, _ := mw.CreateFormFile("archive", "backup.sbz")
	_, _ = fw.Write(archive)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/backup/"+action, &b)
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := archiveResponse{status: resp.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// exportArchive seeds images in an active and an archived story and returns the .sbz bytes.
func exportArchive(t *testing.T, f *assetHTTPFixture, extraBody string) []byte {
	t.Helper()
	active, _, _ := f.seedExportImages(t)
	if extraBody != "" {
		f.createTodo(t, "Mixed", strings.ReplaceAll(extraBody, "ID", active), nil)
	}
	resp, body, err := f.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", resp.StatusCode, err)
	}
	return body
}

func (f *assetHTTPFixture) stagingEmpty(t *testing.T) bool {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dataDir, "import-staging"))
	return err != nil || len(entries) == 0
}

func (f *assetHTTPFixture) fileSHAs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	rows, err := f.sqlDB.Query(`SELECT uuid FROM project_assets`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		p, _ := f.blobs.Path(id, "png")
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("row %s has no file: %v", id, err)
		}
		sum := sha256.Sum256(b)
		out[id] = hex.EncodeToString(sum[:])
	}
	return out
}

func TestArchivePreviewAndImportRoundTrip(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	srcSHAs := src.fileSHAs(t)

	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst := newAssetHTTPFixture(t, assetFixtureOptions{})
			preview := dst.postArchive(t, "maintainer", "preview", mode, "", archive)
			if preview.status != http.StatusOK || preview.body["assets"] != float64(2) {
				t.Fatalf("preview: %d %s", preview.status, preview.raw)
			}
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive)
			if res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			dstSHAs := dst.fileSHAs(t)
			wantContent := map[string]bool{}
			for _, sum := range srcSHAs {
				wantContent[sum] = true
			}
			found := 0
			for id, sum := range dstSHAs {
				if _, sameID := srcSHAs[id]; sameID {
					t.Fatal("import reused an archive identity")
				}
				if wantContent[sum] {
					found++
				}
			}
			if found < 2 {
				t.Fatalf("imported %d of 2 referenced images", found)
			}
			// Each imported image serves to a member, and story bodies reference the new ids.
			var refs int
			if err := dst.sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assets`).Scan(&refs); err != nil || refs != 2 {
				t.Fatalf("todo_assets = %d, %v", refs, err)
			}
			for id := range dstSHAs {
				var linked int
				_ = dst.sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assets ta JOIN project_assets pa ON pa.id = ta.asset_id WHERE pa.uuid = ?`, id).Scan(&linked)
				if linked == 0 {
					continue
				}
				if r := dst.get(t, "maintainer", id, nil); r.StatusCode != http.StatusOK {
					t.Fatalf("serve %s: %d", id, r.StatusCode)
				}
			}
			if !dst.stagingEmpty(t) {
				t.Fatal("import staging not cleaned up")
			}
		})
	}
}

func TestArchiveImportPreservesNonImageReferences(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	mixed := "Prose /api/assets/ID\n\n```\n/api/assets/ID\n```\n\nInline `/api/assets/ID` and [a link](/api/assets/ID)\n\n<img src=\"/api/assets/ID\">\n\n![real](/api/assets/ID)"
	archive := exportArchive(t, src, mixed)
	var activeID string
	for id := range src.fileSHAs(t) {
		var body string
		_ = src.sqlDB.QueryRow(`SELECT body FROM todos WHERE title = 'Mixed'`).Scan(&body)
		if strings.Contains(body, id) {
			activeID = id
		}
	}
	dst := newAssetHTTPFixture(t, assetFixtureOptions{})
	if res := dst.postArchive(t, "maintainer", "import", "copy", "", archive); res.status != http.StatusOK {
		t.Fatalf("import: %d %s", res.status, res.raw)
	}
	var body string
	if err := dst.sqlDB.QueryRow(`SELECT body FROM todos WHERE title = 'Mixed'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	original := strings.ReplaceAll(mixed, "ID", activeID)
	if len(body) != len(original) {
		t.Fatal("body length changed")
	}
	// Exactly one 36-byte span (the image destination) changed.
	diff := 0
	for i := range body {
		if body[i] != original[i] {
			diff++
		}
	}
	if diff == 0 || diff > 36 || strings.Count(body, activeID) != 5 {
		t.Fatalf("only the image destination may change: %d bytes differ\n%s", diff, body)
	}
	if !strings.HasSuffix(body, ")") || strings.HasSuffix(body, activeID+")") {
		t.Fatal("image destination was not remapped")
	}
}

func TestArchiveImportMergeDedupesSameProject(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, f, "")
	before := f.assetRows(t)
	if res := f.postArchive(t, "maintainer", "import", "merge", "", archive); res.status != http.StatusOK {
		t.Fatalf("merge: %d %s", res.status, res.raw)
	}
	if f.assetRows(t) != before || f.assetFiles(t) != before {
		t.Fatalf("merging a project's own backup duplicated images: rows %d -> %d, files %d", before, f.assetRows(t), f.assetFiles(t))
	}
}

func TestArchiveImportFailuresLeaveDataIntact(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")

	t.Run("replace without confirmation", func(t *testing.T) {
		dst := newAssetHTTPFixture(t, assetFixtureOptions{})
		if res := dst.postArchive(t, "maintainer", "import", "replace", "", archive); res.status != http.StatusBadRequest {
			t.Fatalf("%d %s", res.status, res.raw)
		}
		if dst.assetFiles(t) != 0 || !dst.stagingEmpty(t) {
			t.Fatal("files written without confirmation")
		}
	})
	t.Run("corrupt archive", func(t *testing.T) {
		dst := newAssetHTTPFixture(t, assetFixtureOptions{})
		res := dst.postArchive(t, "maintainer", "import", "copy", "", archive[:len(archive)/2])
		if res.status != http.StatusBadRequest || !strings.Contains(res.raw, "invalid_backup_archive") {
			t.Fatalf("%d %s", res.status, res.raw)
		}
		if dst.assetFiles(t) != 0 || !dst.stagingEmpty(t) {
			t.Fatal("corrupt archive left files")
		}
	})
	t.Run("store failure after files", func(t *testing.T) {
		dst := newAssetHTTPFixture(t, assetFixtureOptions{})
		existing := exportArchive(t, dst, "") // dst has its own images
		_ = existing
		beforeRows, beforeSHAs := dst.assetRows(t), dst.fileSHAs(t)
		if _, err := dst.sqlDB.Exec(`CREATE TRIGGER trg_test_fail_import BEFORE INSERT ON todos WHEN NEW.title = 'Archived' AND (SELECT COUNT(*) FROM projects) > 1 BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
			t.Fatal(err)
		}
		res := dst.postArchive(t, "maintainer", "import", "copy", "", archive)
		if res.status == http.StatusOK {
			t.Fatal("injected failure did not fail the import")
		}
		after := dst.fileSHAs(t)
		if dst.assetRows(t) != beforeRows || dst.assetFiles(t) != beforeRows || len(after) != len(beforeSHAs) {
			t.Fatalf("failed import changed data: rows %d->%d files %d", beforeRows, dst.assetRows(t), dst.assetFiles(t))
		}
		for id, sum := range beforeSHAs {
			if after[id] != sum {
				t.Fatal("existing image changed")
			}
		}
		if !dst.stagingEmpty(t) {
			t.Fatal("staging not cleaned")
		}
	})
	t.Run("budget exceeded before any write", func(t *testing.T) {
		dst := newAssetHTTPFixture(t, assetFixtureOptions{budgets: store.AssetBudgets{InstanceBytes: 10}})
		res := dst.postArchive(t, "maintainer", "import", "copy", "", archive)
		if res.status != http.StatusInsufficientStorage {
			t.Fatalf("%d %s", res.status, res.raw)
		}
		if dst.assetFiles(t) != 0 {
			t.Fatal("budget failure wrote files")
		}
	})
}

func TestArchiveImportOversizeAndAuth(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	dst := newAssetHTTPFixture(t, assetFixtureOptions{})
	if res := dst.postArchive(t, "", "import", "copy", "", archive); res.status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: %d", res.status)
	}
	limits := dst.srv.backupImports.Limits()
	_ = limits
	small := backuparchive.DefaultLimits
	small.MaxArchiveBytes = int64(len(archive) - 1)
	dst.srv.backupImports = newLimitedImports(dst, small)
	if res := dst.postArchive(t, "maintainer", "import", "copy", "", archive); res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: %d %s", res.status, res.raw)
	}
	if !dst.stagingEmpty(t) {
		t.Fatal("oversize upload left a temp file")
	}
}

func TestArchiveRequestsWhenFlagOffMatchBaseline(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	off := newAssetHTTPFixture(t, assetFixtureOptions{disableFlag: true})
	res := off.postArchive(t, "maintainer", "import", "copy", "", archive)
	if res.status == http.StatusOK || res.status == http.StatusInsufficientStorage {
		t.Fatalf("flag off accepted an archive: %d", res.status)
	}
	if off.assetFiles(t) != 0 {
		t.Fatal("flag off wrote files")
	}
}

func newLimitedImports(f *assetHTTPFixture, limits backuparchive.Limits) *backupimportapp.Service {
	return backupimportapp.New(backupimportapp.Options{
		Store:     f.st,
		Blobs:     f.blobs,
		Pending:   f.srv.assetPending,
		Validator: f.srv.assetService,
		Limits:    &limits,
	})
}
