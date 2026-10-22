package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"scrumboy/internal/store"
)

func TestAssetUploadRoleMatrix(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	contributorID := f.users["contributor"].ID
	assigned := f.createTodo(t, "Assigned", "", &contributorID)
	unassigned := f.createTodo(t, "Unassigned", "", nil)
	archived := f.createTodo(t, "Archived", "", nil)
	if _, err := f.st.ArchiveTodosByLocalID(f.ownerCtx(), f.project.ID, []int64{archived.LocalID}, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, role, query string
		want              int
	}{
		{"maintainer without todo", "maintainer", "", http.StatusCreated},
		{"maintainer with todo", "maintainer", fmt.Sprintf("?todo=%d", unassigned.LocalID), http.StatusCreated},
		{"assigned contributor own todo", "contributor", fmt.Sprintf("?todo=%d", assigned.LocalID), http.StatusCreated},
		{"contributor unassigned todo", "contributor", fmt.Sprintf("?todo=%d", unassigned.LocalID), http.StatusForbidden},
		{"contributor without todo", "contributor", "", http.StatusForbidden},
		{"viewer", "viewer", fmt.Sprintf("?todo=%d", unassigned.LocalID), http.StatusForbidden},
		{"nonmember", "outsider", "", http.StatusNotFound},
		{"unauthenticated (board routes hide existence)", "", "", http.StatusNotFound},
		{"archived todo", "maintainer", fmt.Sprintf("?todo=%d", archived.LocalID), http.StatusConflict},
		{"missing todo", "maintainer", "?todo=9999", http.StatusNotFound},
		{"invalid todo", "maintainer", "?todo=abc", http.StatusBadRequest},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rowsBefore, filesBefore := f.assetRows(t), f.assetFiles(t)
			res := f.upload(t, c.role, c.query, "shot.png", testPNG(t, 100+i))
			if res.status != c.want {
				t.Fatalf("status = %d, want %d (%s)", res.status, c.want, res.raw)
			}
			if c.want != http.StatusCreated && (f.assetRows(t) != rowsBefore || f.assetFiles(t) != filesBefore) {
				t.Fatal("denied upload left a row or file")
			}
		})
	}
}

func TestAssetUploadResponseAndServe(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	data := testPNG(t, 1)
	res := f.upload(t, "maintainer", "", "  shot.png  ", data)
	if res.status != http.StatusCreated {
		t.Fatalf("upload: %d %s", res.status, res.raw)
	}
	id := res.assetID()
	if !store.IsCanonicalAssetUUID(id) || res.body["url"] != "/api/assets/"+id || res.body["mimeType"] != "image/png" || res.body["width"] != float64(8) {
		t.Fatalf("response %+v", res.body)
	}
	if res.body["filename"] != "shot.png" {
		t.Fatalf("filename not sanitized: %v", res.body["filename"])
	}

	resp := f.get(t, "viewer", id, nil)
	body, _ := io.ReadAll(resp.Body)
	sum := sha256.Sum256(data)
	want := map[string]string{
		"Content-Type":                 "image/png",
		"Cache-Control":                "private, no-cache",
		"ETag":                         `"` + hex.EncodeToString(sum[:]) + `"`,
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "default-src 'none'; sandbox",
		"Content-Disposition":          "inline",
		"Cross-Origin-Resource-Policy": "same-origin",
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, data) {
		t.Fatalf("serve: %d, %d bytes", resp.StatusCode, len(body))
	}
	for k, v := range want {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	notModified := f.get(t, "viewer", id, map[string]string{"If-None-Match": want["ETag"]})
	if notModified.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", notModified.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodHead, f.ts.URL+"/api/assets/"+id, nil)
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: f.tokens["viewer"]})
	head, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	headBody, _ := io.ReadAll(head.Body)
	head.Body.Close()
	if head.StatusCode != http.StatusOK || len(headBody) != 0 {
		t.Fatalf("HEAD: %d, %d bytes", head.StatusCode, len(headBody))
	}
	// Non-canonical identifiers are plain 404s.
	if r := f.get(t, "viewer", strings.ToUpper(id), nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("uppercase id: %d", r.StatusCode)
	}
}

func TestAssetUploadBodyLimits(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	maxBytes := f.srv.assetService.Limits().MaxBytes
	if res := f.upload(t, "maintainer", "", "max.png", paddedPNG(t, int(maxBytes), 1)); res.status != http.StatusCreated {
		t.Fatalf("exactly MaxBytes: %d %s", res.status, res.raw)
	}
	if res := f.upload(t, "maintainer", "", "over.png", paddedPNG(t, int(maxBytes)+1, 2)); res.status != http.StatusRequestEntityTooLarge || res.reason() != "image_too_large" {
		t.Fatalf("MaxBytes+1: %d %s", res.status, res.raw)
	}
	tmpBefore := countMultipartTemp(t)
	huge := io.MultiReader(strings.NewReader("--b\r\nContent-Disposition: form-data; name=\"file\"; filename=\"x.png\"\r\n\r\n"), io.LimitReader(zeroReader{}, 100<<20))
	if res := f.doUpload(t, "maintainer", "", huge, "multipart/form-data; boundary=b"); res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("100 MiB stream: %d", res.status)
	}
	if countMultipartTemp(t) != tmpBefore {
		t.Fatal("multipart temp files were created")
	}
	if f.assetRows(t) != 1 || f.assetFiles(t) != 1 {
		t.Fatalf("rows=%d files=%d after oversize uploads", f.assetRows(t), f.assetFiles(t))
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func countMultipartTemp(t *testing.T) int {
	t.Helper()
	entries, _ := os.ReadDir(os.TempDir())
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "multipart-") {
			n++
		}
	}
	return n
}

func TestAssetUploadMultipartShape(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	png1 := testPNG(t, 1)
	shapes := map[string]func(*multipart.Writer){
		"no file part": func(mw *multipart.Writer) { _ = mw.WriteField("other", "x") },
		"two files": func(mw *multipart.Writer) {
			fileParts("a.png", png1)(mw)
			fileParts("b.png", png1)(mw)
		},
		"extra field": func(mw *multipart.Writer) {
			_ = mw.WriteField("todoLocalId", "1")
			fileParts("a.png", png1)(mw)
		},
		"file without filename": func(mw *multipart.Writer) { _ = mw.WriteField("file", "not a file") },
	}
	for name, parts := range shapes {
		t.Run(name, func(t *testing.T) {
			body, ct := multipartBody(t, parts)
			if res := f.doUpload(t, "maintainer", "", body, ct); res.status != http.StatusBadRequest || res.reason() != "invalid_multipart" {
				t.Fatalf("%d %s", res.status, res.raw)
			}
		})
	}
	if res := f.doUpload(t, "maintainer", "", bytes.NewReader(png1), "image/png"); res.status != http.StatusBadRequest {
		t.Fatalf("non-multipart: %d", res.status)
	}
	if f.assetRows(t) != 0 {
		t.Fatal("rejected shapes stored an asset")
	}
}

func TestAssetUploadRejectsFormats(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	cases := map[string]struct {
		data   []byte
		reason string
	}{
		"svg":           {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), "unsupported_image_type"},
		"html as png":   {[]byte("<html><script>alert(1)</script></html>"), "unsupported_image_type"},
		"png + trailer": {append(testPNG(t, 3), []byte("<html>")...), "image_trailing_data"},
		"truncated png": {testPNG(t, 4)[:40], "invalid_image"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if res := f.upload(t, "maintainer", "", "x.png", c.data); res.status != http.StatusBadRequest || res.reason() != c.reason {
				t.Fatalf("%d %s", res.status, res.raw)
			}
		})
	}
	if f.assetRows(t) != 0 || f.assetFiles(t) != 0 {
		t.Fatal("rejected formats stored something")
	}
}

func TestAssetUploadBusy(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: func(o *Options) { o.AssetReadSlots = -1 }})
	if res := f.upload(t, "maintainer", "", "x.png", testPNG(t, 1)); res.status != http.StatusServiceUnavailable || res.code() != "BUSY" {
		t.Fatalf("%d %s", res.status, res.raw)
	}
}

func TestAssetUploadDedupe(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	data := testPNG(t, 9)
	first := f.upload(t, "maintainer", "", "a.png", data)
	second := f.upload(t, "maintainer", "", "b.png", data)
	if first.status != http.StatusCreated || second.status != http.StatusOK || first.assetID() != second.assetID() {
		t.Fatalf("dedupe: %d/%d %s %s", first.status, second.status, first.assetID(), second.assetID())
	}
	if f.assetRows(t) != 1 || f.assetFiles(t) != 1 {
		t.Fatal("dedupe left a duplicate")
	}
}

func TestAssetServePublicMatrix(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	up := func(seed int) string {
		res := f.upload(t, "maintainer", "", "x.png", testPNG(t, seed))
		if res.status != http.StatusCreated {
			t.Fatalf("upload: %d", res.status)
		}
		return res.assetID()
	}
	active, archivedOnly, codeOnly, private := up(1), up(2), up(3), up(4)
	f.createTodo(t, "Active", "![a](/api/assets/"+active+")", nil)
	arch := f.createTodo(t, "Archived", "![b](/api/assets/"+archivedOnly+")", nil)
	if _, err := f.st.ArchiveTodosByLocalID(f.ownerCtx(), f.project.ID, []int64{arch.LocalID}, store.ModeFull); err != nil {
		t.Fatal(err)
	}
	f.createTodo(t, "Code", "```\n![c](/api/assets/"+codeOnly+")\n```", nil)

	// Not published: nonmembers and visitors get 404; members see everything.
	for _, role := range []string{"", "outsider"} {
		if r := f.get(t, role, active, nil); r.StatusCode != http.StatusNotFound {
			t.Fatalf("unpublished %q: %d", role, r.StatusCode)
		}
	}
	for _, id := range []string{active, archivedOnly, codeOnly, private} {
		if r := f.get(t, "viewer", id, nil); r.StatusCode != http.StatusOK {
			t.Fatalf("viewer %s: %d", id, r.StatusCode)
		}
	}

	f.publish(t)
	for _, role := range []string{"", "outsider"} {
		r := f.get(t, role, active, nil)
		if r.StatusCode != http.StatusOK || r.Header.Get("X-Robots-Tag") == "" {
			t.Fatalf("published active %q: %d", role, r.StatusCode)
		}
		for _, id := range []string{archivedOnly, codeOnly, private} {
			if r := f.get(t, role, id, nil); r.StatusCode != http.StatusNotFound {
				t.Fatalf("published %q must not read %s: %d", role, id, r.StatusCode)
			}
		}
	}
	// Correct ETag without access is still a 404, never a 304.
	var sha string
	if err := f.sqlDB.QueryRow(`SELECT sha256 FROM project_assets WHERE uuid = ?`, private).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	if r := f.get(t, "", private, map[string]string{"If-None-Match": `"` + sha + `"`}); r.StatusCode != http.StatusNotFound {
		t.Fatalf("ETag probe: %d", r.StatusCode)
	}
}

func TestAssetServePublicRequiresOperatorGate(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: func(o *Options) { o.PublicProjectsEnabled = false }})
	res := f.upload(t, "maintainer", "", "x.png", testPNG(t, 1))
	f.createTodo(t, "Active", "![a](/api/assets/"+res.assetID()+")", nil)
	f.publish(t)
	if r := f.get(t, "", res.assetID(), nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("operator gate off: %d", r.StatusCode)
	}
}

func TestAssetServeStagingAndMissingFile(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	staged := f.upload(t, "maintainer", "", "x.png", testPNG(t, 1)).assetID()
	missing := f.upload(t, "maintainer", "", "y.png", testPNG(t, 2)).assetID()
	if err := f.blobs.Remove(missing, "png"); err != nil {
		t.Fatal(err)
	}
	if r := f.get(t, "maintainer", missing, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file: %d", r.StatusCode)
	}
	if _, err := f.sqlDB.Exec(`UPDATE projects SET import_batch_id = 'batch' WHERE id = ?`, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if r := f.get(t, "maintainer", staged, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("staging asset served to maintainer: %d", r.StatusCode)
	}
}

func TestAssetRoutesFlagOffMatchBaseline(t *testing.T) {
	on := newAssetHTTPFixture(t, assetFixtureOptions{})
	id := on.upload(t, "maintainer", "", "x.png", testPNG(t, 1)).assetID()
	off := newAssetHTTPFixture(t, assetFixtureOptions{disableFlag: true})
	// The same requests against a flag-off server behave exactly like an unknown route.
	up := off.upload(t, "maintainer", "", "x.png", testPNG(t, 1))
	baseline := off.doUpload(t, "maintainer", "", strings.NewReader(""), "application/json")
	if up.status != http.StatusNotFound || string(up.raw) != string(baseline.raw) {
		t.Fatalf("flag-off upload: %d %s vs %s", up.status, up.raw, baseline.raw)
	}
	get := off.get(t, "maintainer", id, nil)
	body, _ := io.ReadAll(get.Body)
	unknown := off.get(t, "maintainer", "../nothing", nil)
	unknownBody, _ := io.ReadAll(unknown.Body)
	if get.StatusCode != http.StatusNotFound || string(body) != string(unknownBody) {
		t.Fatalf("flag-off serve: %d %s", get.StatusCode, body)
	}
	if off.srv.inlineImagesEnabled || off.srv.assetService != nil {
		t.Fatal("flag off must not construct the asset service")
	}
}

func TestAssetEffectiveFlag(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Options)
		want bool
	}{
		{"all on", func(*Options) {}, true},
		{"markdown off", func(o *Options) { o.MarkdownNotesEnabled = false }, false},
		{"anonymous", func(o *Options) { o.ScrumboyMode = "anonymous" }, false},
		{"no data dir", func(o *Options) { o.DataDir = "" }, false},
		{"no blobs", func(o *Options) { o.AssetBlobs = nil }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAssetHTTPFixture(t, assetFixtureOptions{configure: c.mod})
			if f.srv.inlineImagesEnabled != c.want {
				t.Fatalf("inlineImagesEnabled = %v", f.srv.inlineImagesEnabled)
			}
			resp, err := http.Get(f.ts.URL + "/api/auth/status")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), fmt.Sprintf(`"inlineImagesEnabled":%v`, c.want)) {
				t.Fatalf("status body: %s", body)
			}
		})
	}
}

func TestAssetPublicRateLimitSeparate(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: func(o *Options) {
		o.AssetPublicRateLimit = nil
	}})
	res := f.upload(t, "maintainer", "", "x.png", testPNG(t, 1))
	f.createTodo(t, "Active", "![a](/api/assets/"+res.assetID()+")", nil)
	f.publish(t)
	status := 0
	for range 601 {
		r := f.get(t, "", res.assetID(), nil)
		_, _ = io.Copy(io.Discard, r.Body)
		status = r.StatusCode
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("601st public read: %d", status)
	}
	if r := f.get(t, "viewer", res.assetID(), nil); r.StatusCode != http.StatusOK {
		t.Fatalf("member reads are not public-rate-limited: %d", r.StatusCode)
	}
}

func TestAssetStorageBudgets(t *testing.T) {
	const mib = 1 << 20
	t.Run("project scope", func(t *testing.T) {
		f := newAssetHTTPFixture(t, assetFixtureOptions{budgets: store.AssetBudgets{ProjectBytes: 3 * mib}})
		for i := range 2 {
			if res := f.upload(t, "maintainer", "", "a.png", paddedPNG(t, mib, i)); res.status != http.StatusCreated {
				t.Fatalf("upload %d: %d", i, res.status)
			}
		}
		res := f.upload(t, "maintainer", "", "c.png", paddedPNG(t, mib+mib/2, 9))
		if res.status != http.StatusInsufficientStorage || res.code() != "STORAGE_BUDGET_EXCEEDED" || !strings.Contains(string(res.raw), `"scope":"project"`) {
			t.Fatalf("over budget: %d %s", res.status, res.raw)
		}
		if f.assetRows(t) != 2 || f.assetFiles(t) != 2 {
			t.Fatal("rejected upload left a row or file")
		}
		// Identical bytes dedupe and consume nothing.
		if res := f.upload(t, "maintainer", "", "dup.png", paddedPNG(t, mib, 0)); res.status != http.StatusOK {
			t.Fatalf("dedupe at full budget: %d", res.status)
		}
	})
	t.Run("instance scope", func(t *testing.T) {
		f := newAssetHTTPFixture(t, assetFixtureOptions{budgets: store.AssetBudgets{InstanceBytes: 2 * mib}})
		other, err := f.st.CreateProject(f.ownerCtx(), "Other")
		if err != nil {
			t.Fatal(err)
		}
		if res := f.upload(t, "maintainer", "", "a.png", paddedPNG(t, mib+mib/2, 1)); res.status != http.StatusCreated {
			t.Fatalf("first: %d", res.status)
		}
		f.project = other
		res := f.upload(t, "maintainer", "", "b.png", paddedPNG(t, mib, 2))
		if res.status != http.StatusInsufficientStorage || !strings.Contains(string(res.raw), `"scope":"instance"`) {
			t.Fatalf("instance budget: %d %s", res.status, res.raw)
		}
	})
	t.Run("concurrent uploads never overshoot", func(t *testing.T) {
		for iter := range 5 {
			f := newAssetHTTPFixture(t, assetFixtureOptions{budgets: store.AssetBudgets{ProjectBytes: 3*mib + mib/2}})
			var wg sync.WaitGroup
			statuses := make([]int, 8)
			for i := range 8 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					statuses[i] = f.upload(t, "maintainer", "", "c.png", paddedPNG(t, mib, iter*100+i)).status
				}(i)
			}
			wg.Wait()
			ok, rejected := 0, 0
			for _, s := range statuses {
				switch s {
				case http.StatusCreated:
					ok++
				case http.StatusInsufficientStorage:
					rejected++
				default:
					t.Fatalf("unexpected status %d", s)
				}
			}
			var used int64
			if err := f.sqlDB.QueryRow(`SELECT COALESCE(SUM(size_bytes),0) FROM project_assets`).Scan(&used); err != nil {
				t.Fatal(err)
			}
			if ok != 3 || rejected != 5 || used > 3*mib+mib/2 || f.assetFiles(t) != f.assetRows(t) {
				t.Fatalf("iter %d: ok=%d rejected=%d used=%d files=%d rows=%d", iter, ok, rejected, used, f.assetFiles(t), f.assetRows(t))
			}
		}
	})
	t.Run("gc frees budget", func(t *testing.T) {
		f := newAssetHTTPFixture(t, assetFixtureOptions{budgets: store.AssetBudgets{ProjectBytes: 2 * mib}})
		if res := f.upload(t, "maintainer", "", "a.png", paddedPNG(t, mib+mib/2, 1)); res.status != http.StatusCreated {
			t.Fatal(res.status)
		}
		if res := f.upload(t, "maintainer", "", "b.png", paddedPNG(t, mib, 2)); res.status != http.StatusInsufficientStorage {
			t.Fatal(res.status)
		}
		if _, err := f.sqlDB.Exec(`DELETE FROM project_assets`); err != nil {
			t.Fatal(err)
		}
		if res := f.upload(t, "maintainer", "", "b.png", paddedPNG(t, mib, 2)); res.status != http.StatusCreated {
			t.Fatalf("after freeing: %d", res.status)
		}
	})
}
