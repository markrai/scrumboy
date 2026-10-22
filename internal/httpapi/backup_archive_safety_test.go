package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/assetrefs"
	"scrumboy/internal/backuparchive"
	"scrumboy/internal/httpapi/ratelimit"
)

// hexRef returns id with its first character written as a hexadecimal character reference.
func hexRef(id string) string { return fmt.Sprintf("&#x%x;%s", id[0], id[1:]) }

// encodedImageBody embeds three images using destination forms the extractor decodes:
// a character reference, a backslash escape, and a reference definition with named entities.
func encodedImageBody(a, b, c string) string {
	return "![a](/api/assets/" + hexRef(a) + ")\n\n" +
		"![b](/api/assets/" + b[:8] + `\` + b[8:] + ")\n\n" +
		"![c][ref]\n\n[ref]: &sol;api&sol;assets&sol;" + c + "\n\n" +
		"Code `/api/assets/" + a + "` and [a link](/api/assets/" + hexRef(b) + ") stay text."
}

type projectSnapshot struct {
	projects []string
	todos    int
	rows     int
	files    map[string]string
}

func (f *assetHTTPFixture) snapshot(t *testing.T) projectSnapshot {
	t.Helper()
	var s projectSnapshot
	rows, err := f.sqlDB.Query(`SELECT slug || ':' || name FROM projects ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		_ = rows.Scan(&v)
		s.projects = append(s.projects, v)
	}
	_ = rows.Close()
	if err := f.sqlDB.QueryRow(`SELECT COUNT(*) FROM todos`).Scan(&s.todos); err != nil {
		t.Fatal(err)
	}
	s.rows = f.assetRows(t)
	s.files = f.fileSHAs(t)
	return s
}

func (s projectSnapshot) equal(o projectSnapshot) bool {
	if !slices.Equal(s.projects, o.projects) || s.todos != o.todos || s.rows != o.rows || len(s.files) != len(o.files) {
		return false
	}
	for id, sum := range s.files {
		if o.files[id] != sum {
			return false
		}
	}
	return true
}

// Finding A: entity-encoded and escaped image destinations survive export -> import in every mode.
func TestArchiveRoundTripEntityEncodedReferences(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	a := src.upload(t, "maintainer", "", "a.png", testPNG(t, 11)).assetID()
	b := src.upload(t, "maintainer", "", "b.png", testPNG(t, 12)).assetID()
	c := src.upload(t, "maintainer", "", "c.png", testPNG(t, 13)).assetID()
	body := encodedImageBody(a, b, c)
	if got := assetrefs.Extract(body); !slices.Equal(got, []string{a, b, c}) {
		t.Fatalf("precondition: extractor sees %v", got)
	}
	src.createTodo(t, "Encoded", body, nil)
	resp, archive, err := src.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", resp.StatusCode, err)
	}
	srcSHAs := src.fileSHAs(t)

	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst := newAssetHTTPFixture(t, assetFixtureOptions{})
			dst.createTodo(t, "Existing", "keep me", nil)
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive)
			if res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			var got string
			if err := dst.sqlDB.QueryRow(`SELECT body FROM todos WHERE title = 'Encoded' ORDER BY id DESC LIMIT 1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			ids := assetrefs.Extract(got)
			if len(ids) != 3 {
				t.Fatalf("imported body embeds %d images, want 3:\n%s", len(ids), got)
			}
			dstSHAs := dst.fileSHAs(t)
			for i, id := range ids {
				if _, reused := srcSHAs[id]; reused {
					t.Fatalf("image %d kept its archive identity", i)
				}
				if dstSHAs[id] != srcSHAs[[]string{a, b, c}[i]] {
					t.Fatalf("image %d not restored with its bytes", i)
				}
				var linked int
				_ = dst.sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assets ta JOIN project_assets pa ON pa.id = ta.asset_id WHERE pa.uuid = ?`, id).Scan(&linked)
				if linked != 1 {
					t.Fatalf("image %d not linked", i)
				}
				if r := dst.get(t, "maintainer", id, nil); r.StatusCode != http.StatusOK {
					t.Fatalf("serve image %d: %d", i, r.StatusCode)
				}
			}
			// Non-image mentions keep their original bytes.
			if !strings.Contains(got, "Code `/api/assets/"+a+"`") || !strings.Contains(got, "[a link](/api/assets/"+hexRef(b)+")") {
				t.Fatalf("non-image references changed:\n%s", got)
			}
			if !dst.stagingEmpty(t) {
				t.Fatal("staging not cleaned")
			}
		})
	}
}

// Finding A and follow-ups: an archive whose embedded image cannot be restored safely (an
// unrewritable destination, a reference definition shared with an ordinary link, or an
// oversized note) fails in preview and import before anything changes, in every mode (Replace
// keeps the existing projects).
func TestArchiveImportUnrestorableImageFailsSafely(t *testing.T) {
	cases := map[string]func(id string) string{
		// The extractor decodes this over-long character reference; the remapper cannot rewrite it.
		"unrewritable destination": func(id string) string {
			return "![x](/api/assets/&#x" + strings.Repeat("0", 80) + fmt.Sprintf("%x;", id[0]) + id[1:] + ")"
		},
		"shared definition, link after image": func(id string) string {
			return "![Screenshot][asset]\n\n[Open original][asset]\n\n[asset]: /api/assets/" + id
		},
		"shared definition, link before image": func(id string) string {
			return "[Open original][asset]\n\n![Screenshot][asset] ![Again][asset]\n\n[asset]: /api/assets/" + id
		},
		"oversized note": func(id string) string {
			return strings.Repeat("filler ", assetrefs.MaxRemapBodyBytes/7+1) + "\n\n![x](/api/assets/" + id + ")"
		},
	}
	seed := 21
	for name, makeBody := range cases {
		seed++
		t.Run(name, func(t *testing.T) {
			src := newAssetHTTPFixture(t, assetFixtureOptions{})
			a := src.upload(t, "maintainer", "", "a.png", testPNG(t, seed)).assetID()
			body := makeBody(a)
			if got := assetrefs.Extract(body); !slices.Equal(got, []string{a}) {
				t.Fatalf("precondition: extractor sees %v", got)
			}
			todo := src.createTodo(t, "Bad", "placeholder", nil)
			// Raw write: the oversized note exceeds the editor limit, as a hand-made archive could.
			if _, err := src.sqlDB.Exec(`UPDATE todos SET body = ? WHERE id = ?`, body, todo.ID); err != nil {
				t.Fatal(err)
			}
			resp, archive, err := src.export(t, "maintainer", "")
			if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
				t.Fatalf("export: %d %v", resp.StatusCode, err)
			}
			for _, mode := range []string{"copy", "merge", "replace"} {
				dst := newAssetHTTPFixture(t, assetFixtureOptions{})
				exportArchive(t, dst, "") // existing projects with their own images
				before := dst.snapshot(t)
				if res := dst.postArchive(t, "maintainer", "preview", mode, "", archive); res.status != http.StatusBadRequest || !strings.Contains(res.raw, "invalid_backup_archive") {
					t.Fatalf("%s preview: %d %.300s", mode, res.status, res.raw)
				}
				confirm := ""
				if mode == "replace" {
					confirm = "REPLACE"
				}
				res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive)
				if res.status != http.StatusBadRequest || !strings.Contains(res.raw, "invalid_backup_archive") {
					t.Fatalf("%s import: %d %.300s", mode, res.status, res.raw)
				}
				if !before.equal(dst.snapshot(t)) {
					t.Fatalf("%s: failed import changed existing data", mode)
				}
				if !dst.stagingEmpty(t) {
					t.Fatalf("%s: staging not cleaned", mode)
				}
			}
		})
	}
}

// Finding B: right after a downgrade (bodies written without link rows, reconciliation not yet
// run) the .sbz still carries every embedded image's bytes.
func TestExportAfterDowngradeCarriesUnlinkedImages(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	a := f.upload(t, "maintainer", "", "a.png", testPNG(t, 31)).assetID()
	b := f.upload(t, "maintainer", "", "b.png", testPNG(t, 32)).assetID()
	todo := f.createTodo(t, "T", "![a](/api/assets/"+a+")", nil)
	if _, err := f.sqlDB.Exec(`DELETE FROM todo_assets`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sqlDB.Exec(`UPDATE project_assets SET unreferenced_at = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sqlDB.Exec(`UPDATE todos SET body = ? WHERE id = ?`, "![a](/api/assets/"+a+") ![b](/api/assets/"+hexRef(b)+")", todo.ID); err != nil {
		t.Fatal(err)
	}
	resp, body, err := f.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", resp.StatusCode, err)
	}
	arch, err := backuparchive.Open(bytes.NewReader(body), int64(len(body)), backuparchive.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		data, err := arch.ReadAsset(id)
		if err != nil || len(data) == 0 {
			t.Fatalf("archive lacks %s: %v", id, err)
		}
	}
}

// startBlockedArchive sends an archive request whose body stalls after the archive part begins,
// so the request holds its staging slot until finish is called.
func (f *assetHTTPFixture) startBlockedArchive(t *testing.T, role string) (finish func() int) {
	t.Helper()
	pr, pw := io.Pipe()
	// Registered after the fixture's server cleanup, so it runs first even when the test fails.
	t.Cleanup(func() { _ = pw.CloseWithError(errors.New("test ended")) })
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/backup/import", pr)
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=XB")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: f.tokens[role]})
	status := make(chan int, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()
	go func() {
		_, _ = io.WriteString(pw, "--XB\r\nContent-Disposition: form-data; name=\"importMode\"\r\n\r\ncopy\r\n--XB\r\nContent-Disposition: form-data; name=\"archive\"; filename=\"b.sbz\"\r\nContent-Type: application/octet-stream\r\n\r\nPK")
	}()
	return func() int {
		_ = pw.CloseWithError(errors.New("client gave up"))
		select {
		case s := <-status:
			return s
		case <-time.After(10 * time.Second):
			t.Fatal("blocked request did not finish")
			return 0
		}
	}
}

func (f *assetHTTPFixture) stagingCount() int {
	entries, err := os.ReadDir(filepath.Join(f.dataDir, "import-staging"))
	if err != nil {
		return 0
	}
	return len(entries)
}

func (f *assetHTTPFixture) waitStaging(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.stagingCount() != n {
		if time.Now().After(deadline) {
			t.Fatalf("staging files = %d, want %d", f.stagingCount(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func errorCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// Finding C: a Viewer cannot stage more than one archive at a time, and all users together are
// bounded by the global slot count, so parallel requests cannot exhaust temporary disk space.
func TestArchiveStagingAdmissionBoundsParallelRequests(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	f := newAssetHTTPFixture(t, assetFixtureOptions{})

	finishViewer := f.startBlockedArchive(t, "viewer")
	f.waitStaging(t, 1)

	var wg sync.WaitGroup
	results := make([]archiveResponse, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = f.postArchive(t, "viewer", "import", "copy", "", archive)
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.status != http.StatusTooManyRequests || errorCode(r.body) != "IMPORT_IN_PROGRESS" {
			t.Fatalf("parallel viewer request %d: %d %s", i, r.status, r.raw)
		}
	}
	if n := f.stagingCount(); n != 1 {
		t.Fatalf("viewer staged %d archives at once", n)
	}

	finishContributor := f.startBlockedArchive(t, "contributor")
	f.waitStaging(t, 2)
	if r := f.postArchive(t, "outsider", "preview", "copy", "", archive); r.status != http.StatusServiceUnavailable || errorCode(r.body) != "BUSY" {
		t.Fatalf("third user over the global bound: %d %s", r.status, r.raw)
	}
	if n := f.stagingCount(); n != 2 {
		t.Fatalf("staging files = %d, want 2", n)
	}

	// JSON backups are not archive requests and are not throttled by staging.
	_, jsonExport, err := newAssetHTTPFixture(t, assetFixtureOptions{}).export(t, "maintainer", "")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"data": json.RawMessage(jsonExport), "importMode": "copy"})
	req, _ := http.NewRequest(http.MethodPost, f.ts.URL+"/api/backup/preview", bytes.NewReader(payload))
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: f.tokens["viewer"]})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("JSON preview while archives staged: %d", resp.StatusCode)
	}

	if s := finishViewer(); s == http.StatusOK {
		t.Fatal("truncated archive accepted")
	}
	_ = finishContributor()
	f.waitStaging(t, 0)
	if r := f.postArchive(t, "viewer", "preview", "copy", "", archive); r.status != http.StatusOK {
		t.Fatalf("slot not released: %d %s", r.status, r.raw)
	}
}

// Finding C: per-user rate limit on archive preview/import.
func TestArchiveRequestsRateLimited(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: func(o *Options) {
		o.ArchiveImportRateLimit = ratelimit.New(2, time.Hour)
	}})
	for i := range 2 {
		if r := f.postArchive(t, "viewer", "preview", "copy", "", archive); r.status != http.StatusOK {
			t.Fatalf("request %d: %d %s", i, r.status, r.raw)
		}
	}
	r := f.postArchive(t, "viewer", "preview", "copy", "", archive)
	if r.status != http.StatusTooManyRequests || errorCode(r.body) != "RATE_LIMITED" {
		t.Fatalf("over the limit: %d %s", r.status, r.raw)
	}
	if r := f.postArchive(t, "contributor", "preview", "copy", "", archive); r.status != http.StatusOK {
		t.Fatalf("limit is per user: %d %s", r.status, r.raw)
	}
}

// Finding C: an oversized declared body is refused before any staging file or directory exists.
func TestArchiveOversizeRefusedBeforeStaging(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	archive := exportArchive(t, src, "")
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	small := backuparchive.DefaultLimits
	small.MaxArchiveBytes = 64
	f.srv.backupImports = newLimitedImports(f, small)
	r := f.postArchive(t, "viewer", "import", "copy", "", bytes.Repeat(archive, 1+int(multipartOverheadBytes)/len(archive)))
	if r.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "import-staging")); !os.IsNotExist(err) {
		t.Fatal("oversized request reached staging")
	}
}
