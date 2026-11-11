package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/drawrefs"
	"scrumboy/internal/store"
)

const drawingTestAsset = "/api/assets/a8b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d"

func drawingDocJSON(color string, strokes int) map[string]any {
	list := make([]any, 0, strokes)
	for i := 0; i < strokes; i++ {
		list = append(list, map[string]any{"id": fmt.Sprintf("s%d", i), "tool": "pen", "color": color, "size": 4, "opacity": 1, "points": [][]float64{{10, 10, 0.2}, {30, 20, 0.9}}, "createdAt": 1})
	}
	return map[string]any{"version": 1, "width": 400, "height": 300, "strokes": list}
}

func newDrawingFixture(t *testing.T, drawing bool) *assetHTTPFixture {
	t.Helper()
	return newAssetHTTPFixture(t, assetFixtureOptions{configure: func(o *Options) {
		o.DrawingEnabled = drawing
		o.WallEnabled = true
		o.WallDrawingEnabled = drawing
	}})
}

func (f *assetHTTPFixture) drawReq(t *testing.T, role, method, path string, body any, accept string) (int, http.Header, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scrumboy", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, out
}

func createDrawnTodo(t *testing.T, f *assetHTTPFixture, body string, drawings map[string]any) int64 {
	t.Helper()
	status, _, raw := f.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+f.project.Slug+"/todos", map[string]any{"title": "Drawn", "body": body, "drawings": drawings}, "")
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	var out struct {
		LocalID int64 `json:"localId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out.LocalID
}

func TestDrawingHTTPSaveHydrateAndAuthorization(t *testing.T) {
	f := newDrawingFixture(t, true)
	id := drawrefs.NewID()
	body := "![shot|draw:" + id + "|480](" + drawingTestAsset + ")"
	localID := createDrawnTodo(t, f, body, map[string]any{id: drawingDocJSON("#e11d48", 1)})
	listPath := fmt.Sprintf("/api/board/%s/todos/%d/drawings", f.project.Slug, localID)

	status, _, raw := f.drawReq(t, "viewer", http.MethodGet, listPath, nil, "")
	if status != http.StatusOK || !strings.Contains(string(raw), id) {
		t.Fatalf("viewer hydrate status=%d body=%s", status, raw)
	}
	for _, role := range []string{"outsider", ""} {
		if status, _, _ := f.drawReq(t, role, http.MethodGet, listPath, nil, ""); status == http.StatusOK {
			t.Fatalf("%q read private drawings", role)
		}
		if status, _, _ := f.drawReq(t, role, http.MethodGet, "/api/board/"+f.project.Slug+"/drawings/"+id, nil, ""); status == http.StatusOK {
			t.Fatalf("%q read private drawing", role)
		}
	}
	if status, _, _ := f.drawReq(t, "maintainer", http.MethodPut, "/api/board/"+f.project.Slug+"/drawings/"+id, map[string]any{}, ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("mutating drawing route status=%d", status)
	}
}

func TestDrawingHTTPSVGFallbackIsInertAndNegotiated(t *testing.T) {
	f := newDrawingFixture(t, true)
	id := drawrefs.NewID()
	body := "![sketch|draw:" + id + "|640](/api/board/" + f.project.Slug + "/drawings/" + id + ")"
	createDrawnTodo(t, f, body, map[string]any{id: drawingDocJSON("#112233", 2)})
	path := "/api/board/" + f.project.Slug + "/drawings/" + id

	status, hdr, raw := f.drawReq(t, "viewer", http.MethodGet, path, nil, "application/json")
	if status != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") || !strings.Contains(hdr.Get("Cache-Control"), "private") {
		t.Fatalf("json status=%d hdr=%v", status, hdr)
	}
	var doc store.DrawingDocument
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Strokes) != 2 {
		t.Fatalf("json doc=%s", raw)
	}
	status, hdr, raw = f.drawReq(t, "viewer", http.MethodGet, path, nil, "image/avif,image/webp,image/svg+xml,image/*,*/*;q=0.8")
	if status != http.StatusOK || hdr.Get("Content-Type") != "image/svg+xml; charset=utf-8" {
		t.Fatalf("svg status=%d ct=%s", status, hdr.Get("Content-Type"))
	}
	svg := string(raw)
	if !strings.Contains(svg, `viewBox="0 0 400 300"`) || strings.Count(svg, "<path") != 2 || strings.Contains(strings.ToLower(svg), "script") {
		t.Fatalf("svg=%s", svg)
	}
	if !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'none'") || hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("svg headers=%v", hdr)
	}
}

func TestDrawingHTTPOwnershipAndFlagErrors(t *testing.T) {
	f := newDrawingFixture(t, true)
	id := drawrefs.NewID()
	body := "![shot|draw:" + id + "](" + drawingTestAsset + ")"
	createDrawnTodo(t, f, body, map[string]any{id: drawingDocJSON("#112233", 1)})
	status, _, raw := f.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+f.project.Slug+"/todos", map[string]any{"title": "Thief", "body": body, "drawings": map[string]any{id: drawingDocJSON("#ff0000", 1)}}, "")
	if status != http.StatusBadRequest || !strings.Contains(string(raw), store.ReasonDrawingOwnedElsewhere) {
		t.Fatalf("owned elsewhere status=%d body=%s", status, raw)
	}
	status, _, raw = f.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+f.project.Slug+"/todos", map[string]any{"title": "Bad", "body": body + "x", "drawings": "nope"}, "")
	if status != http.StatusBadRequest || !strings.Contains(string(raw), store.ReasonInvalidDrawing) {
		t.Fatalf("malformed status=%d body=%s", status, raw)
	}

	off := newDrawingFixture(t, false)
	other := drawrefs.NewID()
	status, _, raw = off.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+off.project.Slug+"/todos", map[string]any{"title": "T", "body": "![a|draw:" + other + "](" + drawingTestAsset + ")", "drawings": map[string]any{other: drawingDocJSON("#112233", 1)}}, "")
	if status != http.StatusBadRequest || !strings.Contains(string(raw), store.ReasonDrawingsDisabled) {
		t.Fatalf("disabled status=%d body=%s", status, raw)
	}
	if status, _, _ := off.drawReq(t, "maintainer", http.MethodGet, "/api/board/"+off.project.Slug+"/drawings/"+other, nil, ""); status != http.StatusNotFound {
		t.Fatalf("disabled read status=%d", status)
	}
}

func TestDrawingHTTPPublicBoardBoundary(t *testing.T) {
	f := newDrawingFixture(t, true)
	id := drawrefs.NewID()
	body := "![shot|draw:" + id + "](" + drawingTestAsset + ")"
	localID := createDrawnTodo(t, f, body, map[string]any{id: drawingDocJSON("#112233", 1)})
	listPath := fmt.Sprintf("/api/public/board/%s/todos/%d/drawings", f.project.Slug, localID)
	onePath := "/api/public/board/" + f.project.Slug + "/drawings/" + id
	if status, _, _ := f.drawReq(t, "", http.MethodGet, listPath, nil, ""); status != http.StatusNotFound {
		t.Fatalf("unpublished public list status=%d", status)
	}
	f.publish(t)
	status, _, raw := f.drawReq(t, "", http.MethodGet, listPath, nil, "")
	if status != http.StatusOK || !strings.Contains(string(raw), id) {
		t.Fatalf("public list status=%d body=%s", status, raw)
	}
	if status, hdr, _ := f.drawReq(t, "", http.MethodGet, onePath, nil, "image/svg+xml"); status != http.StatusOK || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("public svg status=%d hdr=%v", status, hdr)
	}
	if status, _, _ := f.drawReq(t, "maintainer", http.MethodPost, fmt.Sprintf("/api/board/%s/todos/archive", f.project.Slug), map[string]any{"localIds": []int64{localID}}, ""); status != http.StatusOK {
		t.Fatalf("archive status=%d", status)
	}
	if status, _, _ := f.drawReq(t, "", http.MethodGet, onePath, nil, ""); status != http.StatusNotFound {
		t.Fatalf("archived story drawing public status=%d", status)
	}
	if status, _, _ := f.drawReq(t, "", http.MethodGet, listPath, nil, ""); status != http.StatusNotFound {
		t.Fatalf("archived story public list status=%d", status)
	}
}

func TestWallDrawingHTTPCommitConflictAndRoles(t *testing.T) {
	f := newDrawingFixture(t, true)
	path := "/api/board/" + f.project.Slug + "/wall/drawings"
	stroke := map[string]any{"id": "w1", "tool": "pen", "color": "#112233", "size": 4, "opacity": 1, "points": [][]float64{{-500, 20, 0.5}}, "createdAt": 1}
	if status, _, _ := f.drawReq(t, "viewer", http.MethodPost, path, map[string]any{"baseVersion": 0, "add": []any{stroke}}, ""); status != http.StatusForbidden {
		t.Fatalf("viewer commit status=%d", status)
	}
	status, _, raw := f.drawReq(t, "contributor", http.MethodPost, path, map[string]any{"baseVersion": 0, "add": []any{stroke}}, "")
	if status != http.StatusOK || !strings.Contains(string(raw), `"applied":true`) {
		t.Fatalf("commit status=%d body=%s", status, raw)
	}
	status, _, raw = f.drawReq(t, "contributor", http.MethodPost, path, map[string]any{"baseVersion": 0, "add": []any{stroke}}, "")
	if status != http.StatusOK || !strings.Contains(string(raw), `"applied":false`) {
		t.Fatalf("retry status=%d body=%s", status, raw)
	}
	stroke2 := map[string]any{"id": "w2", "tool": "highlighter", "color": "#ffee00", "size": 20, "opacity": 0.35, "points": [][]float64{{1, 2, 0.5}}, "createdAt": 2}
	status, _, raw = f.drawReq(t, "contributor", http.MethodPost, path, map[string]any{"baseVersion": 0, "add": []any{stroke2}}, "")
	if status != http.StatusConflict || !strings.Contains(string(raw), store.ReasonWallDrawingVersionConflict) {
		t.Fatalf("stale status=%d body=%s", status, raw)
	}
	status, _, raw = f.drawReq(t, "viewer", http.MethodGet, "/api/board/"+f.project.Slug+"/wall", nil, "")
	if status != http.StatusOK || !strings.Contains(string(raw), `"w1"`) {
		t.Fatalf("wall read status=%d body=%s", status, raw)
	}
	off := newDrawingFixture(t, false)
	if status, _, _ := off.drawReq(t, "contributor", http.MethodPost, "/api/board/"+off.project.Slug+"/wall/drawings", map[string]any{"baseVersion": 0, "add": []any{stroke}}, ""); status != http.StatusNotFound {
		t.Fatalf("disabled wall drawing status=%d", status)
	}
}

func TestDrawingArchiveRoundTripThroughRealManifest(t *testing.T) {
	src := newDrawingFixture(t, true)
	a := drawrefs.NewID()
	b := drawrefs.NewID()
	body := "![sketch|draw:" + a + "|640](/api/board/" + src.project.Slug + "/drawings/" + a + ")\n\n![again|draw:" + b + "](/api/board/" + src.project.Slug + "/drawings/" + b + ")"
	createDrawnTodo(t, src, body, map[string]any{a: drawingDocJSON("#112233", 2), b: drawingDocJSON("#445566", 1)})
	stroke := map[string]any{"id": "w1", "tool": "pen", "color": "#112233", "size": 4, "opacity": 1, "points": [][]float64{{-500, 20, 0.5}}, "createdAt": 1}
	if status, _, raw := src.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+src.project.Slug+"/wall/drawings", map[string]any{"baseVersion": 0, "add": []any{stroke}}, ""); status != http.StatusOK {
		t.Fatalf("wall commit %d %s", status, raw)
	}
	resp, archive, err := src.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export status=%d ct=%s err=%v", resp.StatusCode, resp.Header.Get("Content-Type"), err)
	}
	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst := newDrawingFixture(t, true)
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive)
			if res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			var n int
			if err := dst.sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_drawings WHERE id IN (?, ?)`, a, b).Scan(&n); err != nil || n != 0 {
				t.Fatalf("archive ids persisted: %d %v", n, err)
			}
			var importedBody, slug string
			if err := dst.sqlDB.QueryRow(`SELECT t.body, p.slug FROM todos t JOIN projects p ON p.id = t.project_id WHERE t.title = 'Drawn'`).Scan(&importedBody, &slug); err != nil {
				t.Fatal(err)
			}
			refs := drawrefs.Extract(importedBody)
			if len(refs) != 2 {
				t.Fatalf("refs=%v body=%s", refs, importedBody)
			}
			for _, id := range refs {
				if !strings.Contains(importedBody, "/api/board/"+slug+"/drawings/"+id) {
					t.Fatalf("destination slug not rewritten: %s", importedBody)
				}
				if status, _, _ := dst.drawReq(t, "maintainer", http.MethodGet, "/api/board/"+slug+"/drawings/"+id, nil, "image/svg+xml"); status != http.StatusOK {
					t.Fatalf("imported drawing %s not served: %d", id, status)
				}
			}
			status, _, raw := dst.drawReq(t, "maintainer", http.MethodGet, "/api/board/"+slug+"/wall", nil, "")
			if status != http.StatusOK || !strings.Contains(string(raw), `"w1"`) {
				t.Fatalf("wall drawings not restored: %d %s", status, raw)
			}
		})
	}
}

func TestDrawingFreeExportStaysJSON(t *testing.T) {
	f := newDrawingFixture(t, true)
	f.createTodo(t, "Plain", "no drawings here", nil)
	resp, raw, err := f.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("export status=%d ct=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if strings.Contains(string(raw), `"drawings"`) {
		t.Fatal("JSON export carries a drawings key")
	}
}

func notesInkJSON(strokes int) map[string]any {
	list := make([]any, 0, strokes)
	for i := 0; i < strokes; i++ {
		list = append(list, map[string]any{"id": fmt.Sprintf("n%d", i), "tool": "pen", "color": "#2563eb", "size": 3, "opacity": 1, "points": [][]float64{{10, 20, 0.5}, {200, 400, 0.5}}, "createdAt": 1})
	}
	return map[string]any{"version": 1, "width": 600, "height": 900, "strokes": list}
}

func createInkedTodo(t *testing.T, f *assetHTTPFixture, title, body string, strokes int) int64 {
	t.Helper()
	status, _, raw := f.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+f.project.Slug+"/todos", map[string]any{"title": title, "body": body, "notesInk": notesInkJSON(strokes)}, "")
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, raw)
	}
	var created struct {
		LocalID int64 `json:"localId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	return created.LocalID
}

func TestNotesInkHTTPSaveHydrateClearAndAuthorization(t *testing.T) {
	f := newDrawingFixture(t, true)
	localID := createInkedTodo(t, f, "Ink", "", 2)
	listPath := fmt.Sprintf("/api/board/%s/todos/%d/drawings", f.project.Slug, localID)
	status, _, raw := f.drawReq(t, "viewer", http.MethodGet, listPath, nil, "")
	if status != http.StatusOK || !strings.Contains(string(raw), "\"notesInk\":{") || !strings.Contains(string(raw), "\"n1\"") {
		t.Fatalf("hydrate status=%d body=%s", status, raw)
	}
	if status, _, _ := f.drawReq(t, "outsider", http.MethodGet, listPath, nil, ""); status == http.StatusOK {
		t.Fatal("outsider read notes ink")
	}
	patchPath := fmt.Sprintf("/api/board/%s/todos/%d", f.project.Slug, localID)
	base := func(extra map[string]any) map[string]any {
		out := map[string]any{"title": "Ink renamed", "body": "text", "tags": []string{}, "assigneeUserId": nil}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	if status, _, raw := f.drawReq(t, "maintainer", http.MethodPatch, patchPath, base(nil), ""); status != http.StatusOK {
		t.Fatalf("unrelated patch status=%d body=%s", status, raw)
	}
	if _, _, raw := f.drawReq(t, "viewer", http.MethodGet, listPath, nil, ""); !strings.Contains(string(raw), "\"n1\"") {
		t.Fatalf("unrelated edit erased ink: %s", raw)
	}
	if status, _, _ := f.drawReq(t, "viewer", http.MethodPatch, patchPath, base(map[string]any{"notesInk": nil}), ""); status == http.StatusOK {
		t.Fatal("viewer cleared notes ink")
	}
	if _, _, raw := f.drawReq(t, "viewer", http.MethodGet, listPath, nil, ""); !strings.Contains(string(raw), "\"n1\"") {
		t.Fatalf("viewer attempt changed ink: %s", raw)
	}
	if status, _, raw := f.drawReq(t, "maintainer", http.MethodPatch, patchPath, base(map[string]any{"notesInk": "bad"}), ""); status != http.StatusBadRequest || !strings.Contains(string(raw), store.ReasonInvalidDrawing) {
		t.Fatalf("malformed ink status=%d body=%s", status, raw)
	}
	if status, _, raw := f.drawReq(t, "maintainer", http.MethodPatch, patchPath, base(map[string]any{"notesInk": nil}), ""); status != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", status, raw)
	}
	if _, _, raw := f.drawReq(t, "viewer", http.MethodGet, listPath, nil, ""); !strings.Contains(string(raw), "\"notesInk\":null") {
		t.Fatalf("clear kept ink: %s", raw)
	}
	off := newDrawingFixture(t, false)
	if status, _, raw := off.drawReq(t, "maintainer", http.MethodPost, "/api/board/"+off.project.Slug+"/todos", map[string]any{"title": "T", "notesInk": notesInkJSON(1)}, ""); status != http.StatusBadRequest || !strings.Contains(string(raw), store.ReasonDrawingsDisabled) {
		t.Fatalf("disabled status=%d body=%s", status, raw)
	}
}

func TestNotesInkHTTPPublicBoundary(t *testing.T) {
	f := newDrawingFixture(t, true)
	localID := createInkedTodo(t, f, "Ink", "hello", 1)
	path := fmt.Sprintf("/api/public/board/%s/todos/%d/drawings", f.project.Slug, localID)
	if status, _, _ := f.drawReq(t, "", http.MethodGet, path, nil, ""); status != http.StatusNotFound {
		t.Fatalf("unpublished status=%d", status)
	}
	f.publish(t)
	if status, _, raw := f.drawReq(t, "", http.MethodGet, path, nil, ""); status != http.StatusOK || !strings.Contains(string(raw), "\"n0\"") {
		t.Fatalf("public status=%d body=%s", status, raw)
	}
	if status, _, _ := f.drawReq(t, "", http.MethodPost, "/api/board/"+f.project.Slug+"/todos", map[string]any{"title": "x", "notesInk": notesInkJSON(1)}, ""); status == http.StatusCreated {
		t.Fatal("anonymous public visitor wrote notes ink")
	}
}

func TestNotesInkArchiveRoundTripThroughRealManifest(t *testing.T) {
	src := newDrawingFixture(t, true)
	createInkedTodo(t, src, "Inked", "notes", 3)
	resp, archive, err := src.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export status=%d ct=%s err=%v", resp.StatusCode, resp.Header.Get("Content-Type"), err)
	}
	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst := newDrawingFixture(t, true)
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			if res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive); res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			var data string
			if err := dst.sqlDB.QueryRow("SELECT i.data FROM todo_notes_ink i JOIN todos t ON t.id = i.todo_id WHERE t.title = 'Inked'").Scan(&data); err != nil {
				t.Fatalf("ink not restored: %v", err)
			}
			if !strings.Contains(data, "\"n2\"") {
				t.Fatalf("restored ink=%s", data)
			}
		})
	}
}
