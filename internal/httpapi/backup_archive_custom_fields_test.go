package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"scrumboy/internal/backuparchive"
	"scrumboy/internal/store"
)

func (f *assetHTTPFixture) seedCustomFieldImageTodo(t *testing.T) string {
	t.Helper()
	if _, err := f.st.CreateCustomField(f.ownerCtx(), f.project.ID, "ticket", "Ticket", store.CustomFieldURL); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateCustomField(f.ownerCtx(), f.project.ID, "points", "Points", store.CustomFieldNumber); err != nil {
		t.Fatal(err)
	}
	asset := f.upload(t, "maintainer", "", "both.png", testPNG(t, 7)).assetID()
	todo, err := f.st.CreateTodo(f.ownerCtx(), f.project.ID, store.CreateTodoInput{
		Title:        "Both",
		Body:         "Spec\n\n![shot](/api/assets/" + asset + ")",
		ColumnKey:    store.DefaultColumnBacklog,
		CustomFields: store.CustomFieldValues{"ticket": "https://example.com/T-1", "points": float64(5)},
	}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(todo.CustomFields) != 2 {
		t.Fatalf("created custom fields = %v", todo.CustomFields)
	}
	return asset
}

type customFieldImageState struct {
	assetUUID string
	values    map[string]string
	defs      map[string]string
}

func (f *assetHTTPFixture) customFieldImageState(t *testing.T) customFieldImageState {
	t.Helper()
	var todoID, projectID int64
	if err := f.sqlDB.QueryRow(`SELECT id, project_id FROM todos WHERE title = 'Both' ORDER BY id DESC LIMIT 1`).Scan(&todoID, &projectID); err != nil {
		t.Fatalf("find imported todo: %v", err)
	}
	out := customFieldImageState{values: map[string]string{}, defs: map[string]string{}}
	var refs int
	if err := f.sqlDB.QueryRow(`
SELECT COUNT(*), COALESCE(MAX(pa.uuid), '') FROM todo_assets ta JOIN project_assets pa ON pa.id = ta.asset_id
WHERE ta.todo_id = ? AND pa.project_id = ?`, todoID, projectID).Scan(&refs, &out.assetUUID); err != nil {
		t.Fatal(err)
	}
	if refs != 1 {
		t.Fatalf("todo asset refs = %d, want 1", refs)
	}
	rows, err := f.sqlDB.Query(`
SELECT f.key, COALESCE(v.value_text, printf('%g', v.value_num))
FROM todo_custom_field_values v JOIN project_custom_fields f ON f.id = v.field_id
WHERE v.todo_id = ? AND f.project_id = ?`, todoID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out.values[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	defRows, err := f.sqlDB.Query(`SELECT key, type FROM project_custom_fields WHERE project_id = ?`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer defRows.Close()
	for defRows.Next() {
		var k, typ string
		if err := defRows.Scan(&k, &typ); err != nil {
			t.Fatal(err)
		}
		out.defs[k] = typ
	}
	if err := defRows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func archiveManifestProjects(t *testing.T, archive []byte) []map[string]json.RawMessage {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		if zf.Name != backuparchive.ManifestName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Projects []map[string]json.RawMessage `json:"projects"`
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		return manifest.Projects
	}
	t.Fatal("archive has no manifest.json")
	return nil
}

func TestArchiveRoundTripKeepsCustomFieldsAndImagesOnSameTodo(t *testing.T) {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	srcAsset := src.seedCustomFieldImageTodo(t)
	resp, archive, err := src.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", resp.StatusCode, err)
	}
	projects := archiveManifestProjects(t, archive)
	if len(projects) != 1 || projects[0]["customFields"] == nil || projects[0]["assets"] == nil {
		t.Fatalf("manifest project lacks customFields or assets: %v", projects)
	}

	want := map[string]string{"ticket": "https://example.com/T-1", "points": "5"}
	wantDefs := map[string]string{"ticket": "url", "points": "number"}
	check := func(t *testing.T, f *assetHTTPFixture, sameInstance bool) {
		t.Helper()
		got := f.customFieldImageState(t)
		for k, v := range want {
			if got.values[k] != v {
				t.Fatalf("custom field %s = %q, want %q (all %v)", k, got.values[k], v, got.values)
			}
		}
		for k, typ := range wantDefs {
			if got.defs[k] != typ {
				t.Fatalf("custom field def %s = %q, want %q", k, got.defs[k], typ)
			}
		}
		if sameInstance != (got.assetUUID == srcAsset) {
			t.Fatalf("image asset %s, source %s, same instance %v", got.assetUUID, srcAsset, sameInstance)
		}
		if r := f.get(t, "maintainer", got.assetUUID, nil); r.StatusCode != http.StatusOK {
			t.Fatalf("serve restored image: %d", r.StatusCode)
		}
	}

	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst := newAssetHTTPFixture(t, assetFixtureOptions{})
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive)
			if res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			check(t, dst, false)
		})
	}

	t.Run("merge into source", func(t *testing.T) {
		if _, err := src.st.UpdateTodoByLocalID(src.ownerCtx(), src.project.ID, 1, store.UpdateTodoInput{
			Title:        "Both",
			Body:         "Spec\n\n![shot](/api/assets/" + srcAsset + ")",
			Tags:         []string{},
			CustomFields: store.CustomFieldValues{"ticket": nil, "points": float64(8)},
		}, store.ModeFull); err != nil {
			t.Fatal(err)
		}
		res := src.postArchive(t, "maintainer", "import", "merge", "", archive)
		if res.status != http.StatusOK {
			t.Fatalf("import: %d %s", res.status, res.raw)
		}
		check(t, src, true)
	})
}

func TestArchiveMergeRemapsImageAndCustomFieldsForRegeneratedLocalID(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	f.createTodo(t, "Before", "", nil)
	srcAsset := f.seedCustomFieldImageTodo(t)
	f.createTodo(t, "After", "", nil)
	var original int64
	if err := f.sqlDB.QueryRow(`SELECT local_id FROM todos WHERE project_id = ? AND title = 'Both'`, f.project.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	resp, archive, err := f.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", resp.StatusCode, err)
	}
	if err := f.st.DeleteTodoByLocalID(f.ownerCtx(), f.project.ID, original, store.ModeFull); err != nil {
		t.Fatal(err)
	}

	res := f.postArchive(t, "maintainer", "import", "merge", "", archive)
	if res.status != http.StatusOK {
		t.Fatalf("import: %d %s", res.status, res.raw)
	}
	var restored int64
	if err := f.sqlDB.QueryRow(`SELECT local_id FROM todos WHERE project_id = ? AND title = 'Both'`, f.project.ID).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored == original {
		t.Fatalf("expected a regenerated local id, got original %d", original)
	}
	got := f.customFieldImageState(t)
	if got.values["ticket"] != "https://example.com/T-1" || got.values["points"] != "5" {
		t.Fatalf("custom fields = %v", got.values)
	}
	if got.assetUUID != srcAsset {
		t.Fatalf("image asset %s, want deduped %s", got.assetUUID, srcAsset)
	}
	if r := f.get(t, "maintainer", got.assetUUID, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("serve restored image: %d", r.StatusCode)
	}
	if !f.stagingEmpty(t) {
		t.Fatal("import staging not cleaned up")
	}
}
