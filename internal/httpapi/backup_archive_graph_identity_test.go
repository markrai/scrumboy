package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"scrumboy/internal/assetrefs"
	"scrumboy/internal/store"
)

// graphProject describes one project by story title: custom field definitions and values,
// embedded image bytes (by SHA-256 of the restored file), links, wall placements and wall edges.
// Two projects with equal descriptions have the same relationships, whatever their IDs.
func (f *assetHTTPFixture) graphProject(t *testing.T, projectID int64) []string {
	t.Helper()
	q := func(query string, args ...any) *sql.Rows {
		rows, err := f.sqlDB.Query(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	var out []string
	rows := q(`SELECT key, type FROM project_custom_fields WHERE project_id = ?`, projectID)
	for rows.Next() {
		var key, typ string
		_ = rows.Scan(&key, &typ)
		out = append(out, "field "+key+":"+typ)
	}
	rows.Close()
	titles := map[int64]string{}
	rows = q(`SELECT id, local_id, title, body, column_key, COALESCE(priority_key, '') FROM todos WHERE project_id = ?`, projectID)
	type story struct {
		id       int64
		localID  int64
		title    string
		body     string
		column   string
		priority string
	}
	var stories []story
	for rows.Next() {
		var s story
		_ = rows.Scan(&s.id, &s.localID, &s.title, &s.body, &s.column, &s.priority)
		titles[s.localID] = s.title
		stories = append(stories, s)
	}
	rows.Close()
	for _, s := range stories {
		// Bodies embed asset UUIDs that are remapped on import; compare text with content
		// hashes so identity is by image bytes, not by source UUID.
		bodyForGraph := s.body
		rows = q(`SELECT f.key, COALESCE(v.value_text, printf('%g', v.value_num)) FROM todo_custom_field_values v
JOIN project_custom_fields f ON f.id = v.field_id WHERE v.todo_id = ?`, s.id)
		for rows.Next() {
			var key, val string
			_ = rows.Scan(&key, &val)
			out = append(out, "story "+s.title+" cf "+key+"="+val)
		}
		rows.Close()
		for _, id := range assetrefs.Extract(s.body) {
			var owner int64
			var mime string
			if err := f.sqlDB.QueryRow(`SELECT project_id, mime_type FROM project_assets WHERE uuid = ?`, id).Scan(&owner, &mime); err != nil {
				t.Fatalf("story %q references %s without a row: %v", s.title, id, err)
			}
			if owner != projectID {
				t.Fatalf("story %q references image %s of project %d", s.title, id, owner)
			}
			var linked int
			if err := f.sqlDB.QueryRow(`SELECT COUNT(*) FROM todo_assets ta JOIN project_assets a ON a.id = ta.asset_id WHERE ta.todo_id = ? AND a.uuid = ?`, s.id, id).Scan(&linked); err != nil || linked != 1 {
				t.Fatalf("story %q image %s link rows = %d, %v", s.title, id, linked, err)
			}
			ext, _ := store.AssetExtensionForMIME(mime)
			p, _ := f.blobs.Path(id, ext)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("story %q image %s has no file: %v", s.title, id, err)
			}
			sum := sha256.Sum256(b)
			sha8 := hex.EncodeToString(sum[:8])
			bodyForGraph = strings.ReplaceAll(bodyForGraph, id, sha8)
			out = append(out, "story "+s.title+" img "+sha8)
		}
		out = append(out, fmt.Sprintf("story %s col=%s pri=%q body=%q", s.title, s.column, s.priority, bodyForGraph))
	}
	title := func(localID int64) string {
		if s, ok := titles[localID]; ok {
			return s
		}
		return fmt.Sprintf("missing#%d", localID)
	}
	rows = q(`SELECT from_local_id, to_local_id, link_type FROM todo_links WHERE project_id = ?`, projectID)
	for rows.Next() {
		var from, to int64
		var typ string
		_ = rows.Scan(&from, &to, &typ)
		out = append(out, "link "+title(from)+" -"+typ+"-> "+title(to))
	}
	rows.Close()
	rows = q(`SELECT t.title, p.x, p.y FROM wall_story_placements p JOIN todos t ON t.id = p.todo_id WHERE p.project_id = ?`, projectID)
	for rows.Next() {
		var s string
		var x, y float64
		_ = rows.Scan(&s, &x, &y)
		out = append(out, fmt.Sprintf("place %s@%g,%g", s, x, y))
	}
	rows.Close()
	wall, err := f.st.GetWall(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	noteText := map[string]string{}
	for _, n := range wall.Notes {
		noteText[n.ID] = n.Text
		out = append(out, fmt.Sprintf("note %q@%g,%g %gx%g %s", n.Text, n.X, n.Y, n.Width, n.Height, n.Color))
	}
	endpoint := func(raw string) string {
		ep, err := store.ParseWallEdgeEndpoint(raw)
		if err != nil {
			t.Fatalf("edge endpoint %q: %v", raw, err)
		}
		if ep.Kind() == store.WallEdgeEndpointStory {
			return title(ep.TodoLocalID())
		}
		return "note:" + noteText[raw]
	}
	for _, e := range wall.Edges {
		a, b := endpoint(e.From), endpoint(e.To)
		if a > b {
			a, b = b, a
		}
		out = append(out, "edge "+a+" ~ "+b)
	}
	sort.Strings(out)
	return out
}

func (f *assetHTTPFixture) projectIDWithStory(t *testing.T, title string) int64 {
	t.Helper()
	var id int64
	if err := f.sqlDB.QueryRow(`SELECT DISTINCT project_id FROM todos WHERE title = ?`, title).Scan(&id); err != nil {
		t.Fatalf("project with story %q: %v", title, err)
	}
	return id
}

func (f *assetHTTPFixture) uploadTo(t *testing.T, p store.Project, seed int) string {
	t.Helper()
	saved := f.project
	f.project = p
	defer func() { f.project = saved }()
	id := f.upload(t, "maintainer", "", "img.png", testPNG(t, seed)).assetID()
	if id == "" {
		t.Fatal("upload failed")
	}
	return id
}

func (f *assetHTTPFixture) graphTodo(t *testing.T, projectID int64, title, body string, values store.CustomFieldValues) store.Todo {
	t.Helper()
	todo, err := f.st.CreateTodo(f.ownerCtx(), projectID, store.CreateTodoInput{Title: title, Body: body, ColumnKey: store.DefaultColumnBacklog, CustomFields: values}, store.ModeFull)
	if err != nil {
		t.Fatalf("create %s: %v", title, err)
	}
	return todo
}

func (f *assetHTTPFixture) graphField(t *testing.T, projectID int64, key string, typ store.CustomFieldType) {
	t.Helper()
	if _, err := f.st.CreateCustomField(f.ownerCtx(), projectID, key, strings.ToUpper(key), typ); err != nil {
		t.Fatal(err)
	}
}

func (f *assetHTTPFixture) must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func img(id string) string { return "![x](/api/assets/" + id + ")" }

// seedGraphSource builds two projects whose stories carry custom fields, images, links and a
// wall. Images has local IDs 1, 2, 4, 5 (3 deleted); Other reuses local IDs 1 and 2 and embeds
// the same image bytes as Images' s1.
func seedGraphSource(t *testing.T) *assetHTTPFixture {
	src := newAssetHTTPFixture(t, assetFixtureOptions{})
	ctx := src.ownerCtx()
	p := src.project
	src.graphField(t, p.ID, "ref", store.CustomFieldText)
	src.graphField(t, p.ID, "site", store.CustomFieldURL)
	src.graphField(t, p.ID, "est", store.CustomFieldNumber)
	imgA, imgB, imgC := src.uploadTo(t, p, 41), src.uploadTo(t, p, 42), src.uploadTo(t, p, 43)
	s1 := src.graphTodo(t, p.ID, "s1", "first "+img(imgA), store.CustomFieldValues{"ref": "ref-s1"})
	s2 := src.graphTodo(t, p.ID, "s2", "no image", store.CustomFieldValues{"ref": "ref-s2", "site": "https://example.com/s2"})
	gap := src.graphTodo(t, p.ID, "gap", "", nil)
	s3 := src.graphTodo(t, p.ID, "s3", img(imgB), store.CustomFieldValues{"est": float64(3)})
	s4 := src.graphTodo(t, p.ID, "s4", img(imgC)+" and "+img(imgA), store.CustomFieldValues{"ref": "ref-s4", "est": float64(8)})
	src.must(t, src.st.DeleteTodoByLocalID(ctx, p.ID, gap.LocalID, store.ModeFull))
	if s3.LocalID != 4 || s4.LocalID != 5 {
		t.Fatalf("source local ids s3=%d s4=%d, want 4 and 5", s3.LocalID, s4.LocalID)
	}
	src.must(t, src.st.AddLink(ctx, p.ID, s1.LocalID, s3.LocalID, "blocks", store.ModeFull))
	src.must(t, src.st.AddLink(ctx, p.ID, s4.LocalID, s2.LocalID, "parent", store.ModeFull))
	for i, s := range []store.Todo{s1, s3, s4} {
		_, _, err := src.st.PinWallStory(ctx, p.ID, s.LocalID, float64(100*(i+1)), float64(10*(i+1)))
		src.must(t, err)
	}
	note, _, err := src.st.CreateNote(ctx, p.ID, store.CreateNoteInput{X: 1, Y: 2, Width: 200, Height: 120, Color: "#FFEE88", Text: "src note"})
	src.must(t, err)
	_, _, err = src.st.CreateEdge(ctx, p.ID, note.ID, store.FormatWallStoryEndpoint(s3.LocalID))
	src.must(t, err)
	_, _, err = src.st.CreateEdge(ctx, p.ID, store.FormatWallStoryEndpoint(s1.LocalID), store.FormatWallStoryEndpoint(s4.LocalID))
	src.must(t, err)

	other, err := src.st.CreateProject(ctx, "Other")
	src.must(t, err)
	src.graphField(t, other.ID, "ref", store.CustomFieldText)
	imgO := src.uploadTo(t, other, 41)
	o1 := src.graphTodo(t, other.ID, "o1", img(imgO), store.CustomFieldValues{"ref": "ref-o1"})
	o2 := src.graphTodo(t, other.ID, "o2", "", nil)
	src.must(t, src.st.AddLink(ctx, other.ID, o2.LocalID, o1.LocalID, "relates_to", store.ModeFull))
	return src
}

// seedGraphDestination gives the destination an Images project (same slug as the source) with
// local IDs 1, 3, 5, 6 (2 and 4 deleted), its own field "keep", links and wall, plus a bystander
// project with its own graph.
func seedGraphDestination(t *testing.T) (dst *assetHTTPFixture, bystander int64) {
	dst = newAssetHTTPFixture(t, assetFixtureOptions{})
	ctx := dst.ownerCtx()
	p := dst.project
	dst.graphField(t, p.ID, "ref", store.CustomFieldText)
	dst.graphField(t, p.ID, "keep", store.CustomFieldText)
	a := dst.graphTodo(t, p.ID, "dst-a", "", store.CustomFieldValues{"ref": "dst-ref-a", "keep": "ka"})
	b := dst.graphTodo(t, p.ID, "dst-b", "", nil)
	c := dst.graphTodo(t, p.ID, "dst-c", "", store.CustomFieldValues{"keep": "kc"})
	d := dst.graphTodo(t, p.ID, "dst-d", "", nil)
	dst.graphTodo(t, p.ID, "dst-e", "", store.CustomFieldValues{"keep": "ke"})
	f := dst.graphTodo(t, p.ID, "dst-f", "", store.CustomFieldValues{"ref": "dst-ref-f"})
	dst.must(t, dst.st.DeleteTodoByLocalID(ctx, p.ID, b.LocalID, store.ModeFull))
	dst.must(t, dst.st.DeleteTodoByLocalID(ctx, p.ID, d.LocalID, store.ModeFull))
	dst.must(t, dst.st.AddLink(ctx, p.ID, c.LocalID, f.LocalID, "duplicates", store.ModeFull))
	_, _, err := dst.st.PinWallStory(ctx, p.ID, a.LocalID, 7, 7)
	dst.must(t, err)
	_, _, err = dst.st.PinWallStory(ctx, p.ID, c.LocalID, 8, 8)
	dst.must(t, err)
	note, _, err := dst.st.CreateNote(ctx, p.ID, store.CreateNoteInput{X: 3, Y: 3, Width: 200, Height: 120, Color: "#FFEE88", Text: "dst note"})
	dst.must(t, err)
	_, _, err = dst.st.CreateEdge(ctx, p.ID, note.ID, store.FormatWallStoryEndpoint(c.LocalID))
	dst.must(t, err)

	by, err := dst.st.CreateProject(ctx, "Bystander")
	dst.must(t, err)
	dst.graphField(t, by.ID, "ref", store.CustomFieldNumber)
	imgY := dst.uploadTo(t, by, 41)
	y1 := dst.graphTodo(t, by.ID, "y1", img(imgY), store.CustomFieldValues{"ref": float64(1)})
	y2 := dst.graphTodo(t, by.ID, "y2", "", nil)
	dst.must(t, dst.st.AddLink(ctx, by.ID, y1.LocalID, y2.LocalID, "blocks", store.ModeFull))
	_, _, err = dst.st.PinWallStory(ctx, by.ID, y2.LocalID, 9, 9)
	dst.must(t, err)
	return dst, by.ID
}

// TestArchiveImportGraphIdentity imports a two-project .sbz through HTTP in every mode and
// compares each destination project, by story title, with its source: custom field values,
// restored image bytes, links, wall placements and edges must land on the intended stories.
func TestArchiveImportGraphIdentity(t *testing.T) {
	src := seedGraphSource(t)
	srcImages := src.graphProject(t, src.project.ID)
	srcOther := src.graphProject(t, src.projectIDWithStory(t, "o1"))
	resp, archive, err := src.export(t, "maintainer", "")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %v %v", resp, err)
	}

	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run(mode, func(t *testing.T) {
			dst, bystander := seedGraphDestination(t)
			bystanderBefore := dst.graphProject(t, bystander)
			existingBefore := dst.graphProject(t, dst.project.ID)
			if res := dst.postArchive(t, "maintainer", "preview", mode, "", archive); res.status != http.StatusOK {
				t.Fatalf("preview: %d %s", res.status, res.raw)
			}
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			if res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive); res.status != http.StatusOK {
				t.Fatalf("import: %d %s", res.status, res.raw)
			}
			if !dst.stagingEmpty(t) {
				t.Fatal("staging not cleaned")
			}

			wantImages := srcImages
			if mode == "merge" {
				// Merge updates todos by local ID: backup s1 (local 1) and s4 (local 5) land on
				// dst-a and dst-e, which keep their "keep" values; backup s2 (local 2) and s3
				// (local 4) hit destination gaps below its maximum and get regenerated IDs.
				// dst-c and dst-f are not in the backup and are preserved with their values,
				// but their link and wall placement go: Merge replaces links and the wall.
				wantImages = append(append([]string(nil), srcImages...),
					"field keep:text", "story s1 cf keep=ka", "story s4 cf keep=ke")
				for _, line := range existingBefore {
					if strings.HasPrefix(line, "story dst-c ") || strings.HasPrefix(line, "story dst-f ") {
						wantImages = append(wantImages, line)
					}
				}
				sort.Strings(wantImages)
				if got := dst.graphProject(t, dst.project.ID); strings.Join(got, "\n") != strings.Join(wantImages, "\n") {
					t.Fatalf("merged Images graph:\n got %q\nwant %q", got, wantImages)
				}
			} else {
				if id := dst.projectIDWithStory(t, "s1"); mode == "copy" && id == dst.project.ID {
					t.Fatal("copy wrote into the existing project")
				}
				if got := dst.graphProject(t, dst.projectIDWithStory(t, "s1")); strings.Join(got, "\n") != strings.Join(wantImages, "\n") {
					t.Fatalf("Images graph:\n got %q\nwant %q", got, wantImages)
				}
			}
			imagesID := dst.projectIDWithStory(t, "s1")
			otherID := dst.projectIDWithStory(t, "o1")
			if got := dst.graphProject(t, otherID); strings.Join(got, "\n") != strings.Join(srcOther, "\n") {
				t.Fatalf("Other graph:\n got %q\nwant %q", got, srcOther)
			}
			// Source Images and Other both used local_id 1; they must stay in separate projects.
			if imagesID == otherID {
				t.Fatal("Images and Other collapsed into one project")
			}
			var s1Local, o1Local int64
			if err := dst.sqlDB.QueryRow(`SELECT local_id FROM todos WHERE project_id = ? AND title = 's1'`, imagesID).Scan(&s1Local); err != nil {
				t.Fatal(err)
			}
			if err := dst.sqlDB.QueryRow(`SELECT local_id FROM todos WHERE project_id = ? AND title = 'o1'`, otherID).Scan(&o1Local); err != nil {
				t.Fatal(err)
			}
			if s1Local != 1 || o1Local != 1 {
				t.Fatalf("overlapping local IDs not preserved per project: s1=%d o1=%d", s1Local, o1Local)
			}

			switch mode {
			case "replace":
				var n int
				_ = dst.sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE id IN (?, ?)`, bystander, dst.project.ID).Scan(&n)
				if n != 0 {
					t.Fatal("replace kept a pre-existing project")
				}
			default:
				if got := dst.graphProject(t, bystander); strings.Join(got, "\n") != strings.Join(bystanderBefore, "\n") {
					t.Fatalf("bystander changed:\n got %q\nwant %q", got, bystanderBefore)
				}
			}
			if mode == "copy" {
				// The existing Images project is untouched by a copy.
				if got := dst.graphProject(t, dst.project.ID); strings.Join(got, "\n") != strings.Join(existingBefore, "\n") {
					t.Fatalf("existing Images changed by copy:\n got %q\nwant %q", got, existingBefore)
				}
			}
			// Every row has its file. Replace leaves the deleted bystander's file for the asset
			// janitor, which this fixture does not wire (the store reports it as RemovedAssets).
			wantFiles := dst.assetRows(t)
			if mode == "replace" {
				wantFiles++
			}
			if len(dst.fileSHAs(t)) != dst.assetRows(t) || dst.assetFiles(t) != wantFiles {
				t.Fatalf("asset files %d, rows %d", dst.assetFiles(t), dst.assetRows(t))
			}
		})
	}
}

// dbDump returns every row of the tables an import writes, for before/after comparison.
func (f *assetHTTPFixture) dbDump(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, table := range []string{"projects", "todos", "project_custom_fields", "todo_custom_field_values", "todo_links", "project_assets", "todo_assets", "project_walls", "wall_story_placements", "project_members", "project_tags"} {
		rows, err := f.sqlDB.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			_ = rows.Scan(ptrs...)
			lines = append(lines, fmt.Sprintln(table, vals))
		}
		rows.Close()
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, ""))
	}
	return b.String()
}

// TestArchiveImportGraphFailuresLeaveNoTrace covers a predictable failure (a custom field type
// conflict, rejected by preview and by the import before any file is staged) and an injected
// failure in the second project after the first project's images, todos and links were written.
func TestArchiveImportGraphFailuresLeaveNoTrace(t *testing.T) {
	src := seedGraphSource(t)
	_, archive, err := src.export(t, "maintainer", "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("custom field type conflict", func(t *testing.T) {
		dst, _ := seedGraphDestination(t)
		dst.graphField(t, dst.project.ID, "est", store.CustomFieldText) // backup has est:number
		before, beforeSHAs := dst.dbDump(t), dst.fileSHAs(t)
		for _, action := range []string{"preview", "import"} {
			res := dst.postArchive(t, "maintainer", action, "merge", "", archive)
			if res.status != http.StatusBadRequest || !strings.Contains(res.raw, `\"est\" already exists with type text`) {
				t.Fatalf("%s: %d %s", action, res.status, res.raw)
			}
		}
		if dst.dbDump(t) != before || len(dst.fileSHAs(t)) != len(beforeSHAs) || dst.assetFiles(t) != len(beforeSHAs) || !dst.stagingEmpty(t) {
			t.Fatal("rejected import left changes")
		}
		// Copy creates a new project, so the same archive is valid there.
		if res := dst.postArchive(t, "maintainer", "preview", "copy", "", archive); res.status != http.StatusOK {
			t.Fatalf("copy preview: %d %s", res.status, res.raw)
		}
	})

	for _, mode := range []string{"copy", "merge", "replace"} {
		t.Run("injected failure/"+mode, func(t *testing.T) {
			dst, _ := seedGraphDestination(t)
			if _, err := dst.sqlDB.Exec(`CREATE TRIGGER trg_graph_fail BEFORE INSERT ON todos WHEN NEW.title = 'o2' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
				t.Fatal(err)
			}
			before, beforeSHAs := dst.dbDump(t), dst.fileSHAs(t)
			confirm := ""
			if mode == "replace" {
				confirm = "REPLACE"
			}
			if res := dst.postArchive(t, "maintainer", "import", mode, confirm, archive); res.status == http.StatusOK {
				t.Fatal("injected failure did not fail the import")
			}
			if after := dst.dbDump(t); after != before {
				t.Fatalf("failed import changed data:\nbefore:\n%s\nafter:\n%s", before, after)
			}
			after := dst.fileSHAs(t)
			if dst.assetFiles(t) != len(beforeSHAs) || len(after) != len(beforeSHAs) {
				t.Fatalf("asset files %d, want %d", dst.assetFiles(t), len(beforeSHAs))
			}
			for id, sum := range beforeSHAs {
				if after[id] != sum {
					t.Fatalf("existing image %s changed", id)
				}
			}
			if !dst.stagingEmpty(t) {
				t.Fatal("staging not cleaned")
			}
		})
	}
}
