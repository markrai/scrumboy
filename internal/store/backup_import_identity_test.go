package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

func setupImportIdentityProject(t *testing.T) (*Store, *sql.DB, context.Context, Project) {
	t.Helper()
	st, sqlDB, base := setupAssetOwnerStore(t)
	owner, err := st.BootstrapUser(base, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(base, owner.ID)
	p, err := st.CreateProject(ctx, "Identity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "ref", "Ref", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "points", "Points", CustomFieldNumber); err != nil {
		t.Fatal(err)
	}
	return st, sqlDB, ctx, p
}

func createIdentityTodos(t *testing.T, st *Store, ctx context.Context, projectID int64, mode Mode, titles ...string) []Todo {
	t.Helper()
	out := make([]Todo, 0, len(titles))
	for _, title := range titles {
		todo, err := st.CreateTodo(ctx, projectID, CreateTodoInput{
			Title:        title,
			ColumnKey:    DefaultColumnBacklog,
			CustomFields: CustomFieldValues{"ref": "ref-" + title},
		}, mode)
		if err != nil {
			t.Fatalf("CreateTodo %s: %v", title, err)
		}
		out = append(out, todo)
	}
	return out
}

func exportIdentityProject(t *testing.T, st *Store, ctx context.Context, slug string) *ExportData {
	t.Helper()
	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range data.Projects {
		if p.Slug == slug {
			data.Projects = []ProjectExport{p}
			return data
		}
	}
	t.Fatalf("project %s not exported", slug)
	return nil
}

func customFieldRefsByTitle(t *testing.T, sqlDB *sql.DB, projectID int64) map[string]string {
	t.Helper()
	rows, err := sqlDB.Query(`
SELECT t.title, COALESCE(v.value_text, printf('%g', v.value_num))
FROM todo_custom_field_values v
JOIN todos t ON t.id = v.todo_id
JOIN project_custom_fields f ON f.id = v.field_id
WHERE t.project_id = ? AND f.key = 'ref'`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var title, v string
		if err := rows.Scan(&title, &v); err != nil {
			t.Fatal(err)
		}
		if prev, dup := out[title]; dup {
			t.Fatalf("todo title %q appears twice (%q, %q)", title, prev, v)
		}
		out[title] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func linksByTitle(t *testing.T, sqlDB *sql.DB, projectID int64) []string {
	t.Helper()
	rows, err := sqlDB.Query(`
SELECT COALESCE(f.title, '#' || l.from_local_id), COALESCE(o.title, '#' || l.to_local_id), l.link_type
FROM todo_links l
LEFT JOIN todos f ON f.project_id = l.project_id AND f.local_id = l.from_local_id
LEFT JOIN todos o ON o.project_id = l.project_id AND o.local_id = l.to_local_id
WHERE l.project_id = ?`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var from, to, typ string
		if err := rows.Scan(&from, &to, &typ); err != nil {
			t.Fatal(err)
		}
		out = append(out, from+" -"+typ+"-> "+to)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func localIDByTitle(t *testing.T, sqlDB *sql.DB, projectID int64, title string) int64 {
	t.Helper()
	var id int64
	if err := sqlDB.QueryRow(`SELECT local_id FROM todos WHERE project_id = ? AND title = ?`, projectID, title).Scan(&id); err != nil {
		t.Fatalf("local id of %q: %v", title, err)
	}
	return id
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestImportMergeRemapsCustomFieldsAndLinksForRegeneratedLocalID(t *testing.T) {
	st, sqlDB, ctx, p := setupImportIdentityProject(t)
	todos := createIdentityTodos(t, st, ctx, p.ID, ModeFull, "one", "two", "three")
	if err := st.AddLink(ctx, p.ID, todos[1].LocalID, todos[2].LocalID, "blocks", ModeFull); err != nil {
		t.Fatal(err)
	}
	if err := st.AddLink(ctx, p.ID, todos[0].LocalID, todos[1].LocalID, "relates_to", ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportIdentityProject(t, st, ctx, p.Slug)
	if err := st.DeleteTodoByLocalID(ctx, p.ID, todos[1].LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}

	res, err := st.ImportProjects(ctx, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("merge import: %v", err)
	}
	if got := localIDByTitle(t, sqlDB, p.ID, "two"); got == todos[1].LocalID {
		t.Fatalf("expected regenerated local id for %q, got original %d (warnings %v)", "two", got, res.Warnings)
	}
	refs := customFieldRefsByTitle(t, sqlDB, p.ID)
	for _, title := range []string{"one", "two", "three"} {
		if refs[title] != "ref-"+title {
			t.Fatalf("custom field refs = %v", refs)
		}
	}
	assertStrings(t, "links", linksByTitle(t, sqlDB, p.ID), []string{
		"one -relates_to-> two",
		"two -blocks-> three",
	})
}

func TestImportMergeRegeneratedLocalIDIsNotCapturedByLaterBackupTodo(t *testing.T) {
	st, sqlDB, ctx, p := setupImportIdentityProject(t)
	todos := createIdentityTodos(t, st, ctx, p.ID, ModeFull, "one", "two", "three", "four")
	if err := st.AddLink(ctx, p.ID, todos[1].LocalID, todos[3].LocalID, "parent", ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportIdentityProject(t, st, ctx, p.Slug)
	for _, todo := range []Todo{todos[1], todos[3]} {
		if err := st.DeleteTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.ImportProjects(ctx, data, ModeFull, "merge"); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM todos WHERE project_id = ?`, p.ID); n != 4 {
		t.Fatalf("todo count = %d, want 4", n)
	}
	refs := customFieldRefsByTitle(t, sqlDB, p.ID)
	for _, title := range []string{"one", "two", "three", "four"} {
		if refs[title] != "ref-"+title {
			t.Fatalf("custom field refs = %v", refs)
		}
	}
	assertStrings(t, "links", linksByTitle(t, sqlDB, p.ID), []string{"two -parent-> four"})
}

// TestImportMergeLinksMirrorBackup pins the Merge link contract: a matched project's links are
// replaced by the backup's, so a backup without links removes them all, including links between
// stories the backup does not contain. Those stories, their values and wall placements (the
// backup has no wall field) are kept, and other projects are untouched.
func TestImportMergeLinksMirrorBackup(t *testing.T) {
	st, sqlDB, ctx, p := setupImportIdentityProject(t)
	todos := createIdentityTodos(t, st, ctx, p.ID, ModeFull, "one", "two", "three")
	other, err := st.CreateProject(ctx, "Identity Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, other.ID, "ref", "Ref", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	otherTodos := createIdentityTodos(t, st, ctx, other.ID, ModeFull, "x", "y")
	for _, l := range []struct {
		projectID int64
		from, to  Todo
	}{{p.ID, todos[0], todos[1]}, {p.ID, todos[1], todos[2]}, {other.ID, otherTodos[0], otherTodos[1]}} {
		if err := st.AddLink(ctx, l.projectID, l.from.LocalID, l.to.LocalID, "blocks", ModeFull); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.PinWallStory(ctx, p.ID, todos[2].LocalID, 30, 40); err != nil {
		t.Fatal(err)
	}
	data := exportIdentityProject(t, st, ctx, p.Slug)
	data.Projects[0].Todos = data.Projects[0].Todos[:2] // "three" is not in the backup
	data.Projects[0].Links = nil
	data.Projects[0].Wall = nil
	otherBefore := linksByTitle(t, sqlDB, other.ID)

	if _, err := st.ImportProjects(ctx, data, ModeFull, "merge"); err != nil {
		t.Fatalf("merge import: %v", err)
	}
	assertStrings(t, "links", linksByTitle(t, sqlDB, p.ID), nil)
	assertStrings(t, "other links", linksByTitle(t, sqlDB, other.ID), otherBefore)
	if refs := customFieldRefsByTitle(t, sqlDB, p.ID); refs["three"] != "ref-three" || len(refs) != 3 {
		t.Fatalf("custom field refs = %v", refs)
	}
	if refs := customFieldRefsByTitle(t, sqlDB, other.ID); refs["x"] != "ref-x" || refs["y"] != "ref-y" {
		t.Fatalf("other refs = %v", refs)
	}
	assertStrings(t, "placements", wallPlacementsByTitle(t, sqlDB, p.ID), []string{"three@30,40"})
}

func TestImportIntoBoardRemapsOverlappingLocalIDsPerSourceProject(t *testing.T) {
	st, sqlDB, _ := setupAssetOwnerStore(t)
	base := context.Background()
	board, err := st.CreateAnonymousBoard(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(base, board.ID, "ref", "Ref", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	existing := createIdentityTodos(t, st, base, board.ID, ModeAnonymous, "existing one", "existing two")
	if err := st.AddLink(base, board.ID, existing[0].LocalID, existing[1].LocalID, "relates_to", ModeAnonymous); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	defs := []CustomFieldExport{{Key: "ref", Name: "Ref", Type: "text"}, {Key: "points", Name: "Points", Type: "number"}}
	todo := func(localID int64, title string) TodoExport {
		return TodoExport{LocalID: localID, Title: title, Status: "BACKLOG", Rank: localID * 1000, Tags: []string{}, CreatedAt: now, UpdatedAt: now,
			CustomFields: map[string]any{"ref": "ref-" + title, "points": float64(localID * 10)}}
	}
	data := &ExportData{Version: "1.2", ExportedAt: now, Mode: "anonymous", Scope: "board", Projects: []ProjectExport{
		{Slug: "src-a", Name: "A", CreatedAt: now, UpdatedAt: now, CustomFields: defs,
			Todos: []TodoExport{todo(1, "a1"), todo(2, "a2")},
			Links: []LinkExport{{FromLocalID: 1, ToLocalID: 2, LinkType: "blocks"}}},
		{Slug: "src-b", Name: "B", CreatedAt: now, UpdatedAt: now, CustomFields: defs,
			Todos: []TodoExport{todo(1, "b1"), todo(2, "b2")},
			Links: []LinkExport{{FromLocalID: 2, ToLocalID: 1, LinkType: "duplicates"}}},
	}}

	if _, err := st.ImportProjectsWithTarget(base, data, ModeAnonymous, "copy", board.Slug); err != nil {
		t.Fatalf("import into board: %v", err)
	}
	refs := customFieldRefsByTitle(t, sqlDB, board.ID)
	for _, title := range []string{"existing one", "existing two", "a1", "a2", "b1", "b2"} {
		if refs[title] != "ref-"+title {
			t.Fatalf("custom field refs = %v", refs)
		}
	}
	if n := countRows(t, sqlDB, `
SELECT COUNT(*) FROM todo_custom_field_values v JOIN todos t ON t.id = v.todo_id
JOIN project_custom_fields f ON f.id = v.field_id
WHERE t.project_id = ? AND f.key = 'points' AND t.title LIKE 'existing%'`, board.ID); n != 0 {
		t.Fatalf("existing todos received %d imported points values", n)
	}
	for _, title := range []string{"a1", "a2", "b1", "b2"} {
		want := float64(10)
		if strings.HasSuffix(title, "2") {
			want = 20
		}
		var got float64
		if err := sqlDB.QueryRow(`
SELECT v.value_num FROM todo_custom_field_values v JOIN todos t ON t.id = v.todo_id
JOIN project_custom_fields f ON f.id = v.field_id
WHERE t.project_id = ? AND t.title = ? AND f.key = 'points'`, board.ID, title).Scan(&got); err != nil || got != want {
			t.Fatalf("points of %s = %v, %v; want %v", title, got, err, want)
		}
	}
	assertStrings(t, "links", linksByTitle(t, sqlDB, board.ID), []string{
		"a1 -blocks-> a2",
		"b2 -duplicates-> b1",
		"existing one -relates_to-> existing two",
	})
}

func TestImportCopyMultipleProjectsWithOverlappingLocalIDs(t *testing.T) {
	st, sqlDB, ctx, p := setupImportIdentityProject(t)
	todos := createIdentityTodos(t, st, ctx, p.ID, ModeFull, "one", "two")
	if err := st.AddLink(ctx, p.ID, todos[0].LocalID, todos[1].LocalID, "blocks", ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportIdentityProject(t, st, ctx, p.Slug)
	second := data.Projects[0]
	second.Slug = "identity-second"
	second.Name = "Second"
	second.Todos = append([]TodoExport(nil), second.Todos...)
	for i := range second.Todos {
		second.Todos[i].Title = "second " + second.Todos[i].Title
		second.Todos[i].CustomFields = map[string]any{"ref": "ref-" + second.Todos[i].Title}
	}
	second.Links = []LinkExport{{FromLocalID: todos[1].LocalID, ToLocalID: todos[0].LocalID, LinkType: "parent"}}
	data.Projects = append(data.Projects, second)

	for _, mode := range []string{"copy", "replace"} {
		t.Run(mode, func(t *testing.T) {
			if _, err := st.ImportProjects(ctx, data, ModeFull, mode); err != nil {
				t.Fatalf("%s import: %v", mode, err)
			}
			var firstID, secondID int64
			if err := sqlDB.QueryRow(`SELECT MAX(project_id) FROM todos WHERE title = 'one'`).Scan(&firstID); err != nil {
				t.Fatal(err)
			}
			if err := sqlDB.QueryRow(`SELECT MAX(project_id) FROM todos WHERE title = 'second one'`).Scan(&secondID); err != nil {
				t.Fatal(err)
			}
			if firstID == secondID {
				t.Fatal("projects collapsed into one")
			}
			if refs := customFieldRefsByTitle(t, sqlDB, firstID); refs["one"] != "ref-one" || refs["two"] != "ref-two" || len(refs) != 2 {
				t.Fatalf("first project refs = %v", refs)
			}
			if refs := customFieldRefsByTitle(t, sqlDB, secondID); refs["second one"] != "ref-second one" || refs["second two"] != "ref-second two" || len(refs) != 2 {
				t.Fatalf("second project refs = %v", refs)
			}
			assertStrings(t, "first links", linksByTitle(t, sqlDB, firstID), []string{"one -blocks-> two"})
			assertStrings(t, "second links", linksByTitle(t, sqlDB, secondID), []string{"second two -parent-> second one"})
		})
	}
}

func importIdentitySnapshot(t *testing.T, sqlDB *sql.DB) string {
	t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id, project_id, local_id, title, body, column_key, rank FROM todos ORDER BY id`,
		`SELECT todo_id, field_id, COALESCE(value_text, ''), COALESCE(value_num, 0) FROM todo_custom_field_values ORDER BY todo_id, field_id`,
		`SELECT id, project_id, key, name, type, position FROM project_custom_fields ORDER BY id`,
		`SELECT project_id, from_local_id, to_local_id, link_type FROM todo_links ORDER BY project_id, from_local_id, to_local_id`,
		`SELECT id, uuid, project_id, COALESCE(unreferenced_at, -1) FROM project_assets ORDER BY id`,
		`SELECT todo_id, asset_id FROM todo_assets ORDER BY todo_id, asset_id`,
		`SELECT project_id, notes, edges, version, updated_at FROM project_walls ORDER BY project_id`,
		`SELECT project_id, todo_id, x, y, version FROM wall_story_placements ORDER BY project_id, todo_id`,
		`SELECT id, slug FROM projects ORDER BY id`,
	} {
		rows, err := sqlDB.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&b, vals...)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		b.WriteString("--\n")
	}
	return b.String()
}

func TestImportMergeFailureAfterRemapLeavesDataUnchanged(t *testing.T) {
	st, sqlDB, ctx, p := setupImportIdentityProject(t)
	asset := insertTestAsset(t, sqlDB, p.ID)
	todos := createIdentityTodos(t, st, ctx, p.ID, ModeFull, "one", "two", "three")
	if _, err := st.UpdateTodoByLocalID(ctx, p.ID, todos[0].LocalID, UpdateTodoInput{
		KeepTitle: true, KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true,
		Body: "![img](/api/assets/" + asset.UUID + ")",
	}, ModeFull); err != nil {
		t.Fatal(err)
	}
	if err := st.AddLink(ctx, p.ID, todos[0].LocalID, todos[2].LocalID, "blocks", ModeFull); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, "Identity Bystander")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, other.ID, "ref", "Ref", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	otherTodos := createIdentityTodos(t, st, ctx, other.ID, ModeFull, "keep-a", "keep-b")
	if err := st.AddLink(ctx, other.ID, otherTodos[0].LocalID, otherTodos[1].LocalID, "relates_to", ModeFull); err != nil {
		t.Fatal(err)
	}
	data := exportIdentityProject(t, st, ctx, p.Slug)
	if err := st.DeleteTodoByLocalID(ctx, p.ID, todos[1].LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	for i := range data.Projects[0].Todos {
		data.Projects[0].Todos[i].Body = ""
	}
	data.Projects[0].Links = []LinkExport{{FromLocalID: todos[1].LocalID, ToLocalID: todos[0].LocalID, LinkType: "parent"}}
	data.Projects[0].Assets = nil
	// Valid second project: preflight must pass. A trigger aborts the INSERT of its todo after
	// the first project has already remapped and written links/custom fields inside the tx.
	now := time.Now().UTC().Truncate(time.Millisecond)
	data.Projects = append(data.Projects, ProjectExport{
		Slug: "identity-broken", Name: "Broken", CreatedAt: now, UpdatedAt: now,
		Todos: []TodoExport{{LocalID: 1, Title: "broken", Status: "BACKLOG", Rank: 1000, Tags: []string{"ok"}, CreatedAt: now, UpdatedAt: now}},
	})
	if err := st.PreflightImport(ctx, data, ModeFull, "merge", false); err != nil {
		t.Fatalf("preflight err = %v, want the failure to happen inside the import transaction", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER trg_identity_fail_after_remap BEFORE INSERT ON todos WHEN NEW.title = 'broken' BEGIN SELECT RAISE(ABORT, 'injected failure after remap'); END`); err != nil {
		t.Fatal(err)
	}

	beforeLocalOne := localIDByTitle(t, sqlDB, p.ID, "one")
	beforeLocalThree := localIDByTitle(t, sqlDB, p.ID, "three")
	beforeRefs := customFieldRefsByTitle(t, sqlDB, p.ID)
	beforeLinks := linksByTitle(t, sqlDB, p.ID)
	beforeOtherRefs := customFieldRefsByTitle(t, sqlDB, other.ID)
	beforeOtherLinks := linksByTitle(t, sqlDB, other.ID)
	before := importIdentitySnapshot(t, sqlDB)
	_, err = st.ImportProjects(ctx, data, ModeFull, "merge")
	if err == nil || !strings.Contains(err.Error(), "injected failure after remap") {
		t.Fatalf("merge import err = %v, want injected failure after first-project remap writes", err)
	}
	if after := importIdentitySnapshot(t, sqlDB); after != before {
		t.Fatalf("failed import changed data:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if localIDByTitle(t, sqlDB, p.ID, "one") != beforeLocalOne || localIDByTitle(t, sqlDB, p.ID, "three") != beforeLocalThree {
		t.Fatal("story local IDs changed after rolled-back import")
	}
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM todos WHERE project_id = ?`, p.ID); n != 2 {
		t.Fatalf("primary todo count = %d, want 2 (two still deleted)", n)
	}
	if n := countRows(t, sqlDB, `SELECT COUNT(*) FROM projects WHERE slug = 'identity-broken'`); n != 0 {
		t.Fatal("broken project leaked after rollback")
	}
	afterRefs := customFieldRefsByTitle(t, sqlDB, p.ID)
	for title, want := range beforeRefs {
		if afterRefs[title] != want {
			t.Fatalf("primary custom fields changed: before %v after %v", beforeRefs, afterRefs)
		}
	}
	if len(afterRefs) != len(beforeRefs) {
		t.Fatalf("primary custom fields changed: before %v after %v", beforeRefs, afterRefs)
	}
	assertStrings(t, "primary links", linksByTitle(t, sqlDB, p.ID), beforeLinks)
	afterOtherRefs := customFieldRefsByTitle(t, sqlDB, other.ID)
	for title, want := range beforeOtherRefs {
		if afterOtherRefs[title] != want {
			t.Fatalf("bystander custom fields changed: before %v after %v", beforeOtherRefs, afterOtherRefs)
		}
	}
	if len(afterOtherRefs) != len(beforeOtherRefs) {
		t.Fatalf("bystander custom fields changed: before %v after %v", beforeOtherRefs, afterOtherRefs)
	}
	assertStrings(t, "bystander links", linksByTitle(t, sqlDB, other.ID), beforeOtherLinks)
}
