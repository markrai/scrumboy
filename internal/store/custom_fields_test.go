package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func customFieldsTestSetup(t *testing.T) (*Store, func(), Project, Todo) {
	t.Helper()
	st, cleanup := newTestStore(t)
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "cf")
	if err != nil {
		cleanup()
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "t", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		cleanup()
		t.Fatalf("CreateTodo: %v", err)
	}
	return st, cleanup, p, todo
}

func customFieldsUpdate(st *Store, p Project, todo Todo, patch CustomFieldValues) (Todo, error) {
	return st.UpdateTodoByLocalID(context.Background(), p.ID, todo.LocalID, UpdateTodoInput{
		KeepTitle: true, KeepBody: true, KeepTags: true, KeepEstimationPoints: true, KeepAssigneeUserID: true,
		CustomFields: patch,
	}, ModeFull)
}

func TestCustomFields_DefinitionLifecycle(t *testing.T) {
	st, cleanup, p, _ := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()

	f, err := st.CreateCustomField(ctx, p.ID, "", "Merge Request", CustomFieldURL)
	if err != nil {
		t.Fatalf("CreateCustomField: %v", err)
	}
	if f.Key != "merge_request" || f.Type != CustomFieldURL || f.Position != 0 {
		t.Fatalf("field = %+v", f)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "merge_request", "Dup", CustomFieldText); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate key err = %v, want ErrConflict", err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "", "Bad", "boolean"); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad type err = %v, want ErrValidation", err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "Bad Key!", "x", CustomFieldText); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad key err = %v, want ErrValidation", err)
	}
	list, err := st.ListProjectCustomFields(ctx, p.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if err := st.DeleteCustomField(ctx, p.ID, "merge_request"); err != nil {
		t.Fatalf("DeleteCustomField: %v", err)
	}
	if err := st.DeleteCustomField(ctx, p.ID, "merge_request"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v, want ErrNotFound", err)
	}
}

func TestCustomFields_LimitPerProject(t *testing.T) {
	st, cleanup, p, _ := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	for i := 0; i < maxCustomFieldsPerProject; i++ {
		key := "f" + strings.Repeat("a", i+1)
		if _, err := st.CreateCustomField(ctx, p.ID, key, key, CustomFieldText); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "overflow", "overflow", CustomFieldText); !errors.Is(err, ErrValidation) {
		t.Fatalf("over-limit err = %v, want ErrValidation", err)
	}
}

func TestCustomFields_SetReadClear(t *testing.T) {
	st, cleanup, p, todo := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	for _, d := range []struct {
		key string
		typ CustomFieldType
	}{{"mr", CustomFieldURL}, {"repo", CustomFieldText}, {"points", CustomFieldNumber}} {
		if _, err := st.CreateCustomField(ctx, p.ID, d.key, d.key, d.typ); err != nil {
			t.Fatalf("create %s: %v", d.key, err)
		}
	}

	got, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"mr": "https://example.com/mr/1", "repo": "scrumboy", "points": 3.5})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if got.CustomFields["mr"] != "https://example.com/mr/1" || got.CustomFields["points"] != 3.5 {
		t.Fatalf("returned values = %v", got.CustomFields)
	}
	read, err := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(read.CustomFields) != 3 || read.CustomFields["repo"] != "scrumboy" || read.CustomFields["points"] != 3.5 {
		t.Fatalf("stored values = %v", read.CustomFields)
	}

	// Sparse patch: only "repo" is cleared; the others stay.
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"repo": nil}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	read, _ = st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if _, has := read.CustomFields["repo"]; has || len(read.CustomFields) != 2 {
		t.Fatalf("after clear = %v", read.CustomFields)
	}

	// Deleting a definition drops its values.
	if err := st.DeleteCustomField(ctx, p.ID, "mr"); err != nil {
		t.Fatalf("delete def: %v", err)
	}
	read, _ = st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if _, has := read.CustomFields["mr"]; has {
		t.Fatalf("value survived definition delete: %v", read.CustomFields)
	}
}

func TestCustomFields_Validation(t *testing.T) {
	st, cleanup, p, todo := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	for _, d := range []struct {
		key string
		typ CustomFieldType
	}{{"mr", CustomFieldURL}, {"repo", CustomFieldText}, {"points", CustomFieldNumber}} {
		if _, err := st.CreateCustomField(ctx, p.ID, d.key, d.key, d.typ); err != nil {
			t.Fatalf("create %s: %v", d.key, err)
		}
	}
	cases := map[string]CustomFieldValues{
		"unknown key":      {"nope": "x"},
		"javascript url":   {"mr": "javascript:alert(1)"},
		"relative url":     {"mr": "/mr/1"},
		"url wrong type":   {"mr": 5.0},
		"text wrong type":  {"repo": 5.0},
		"number as string": {"points": "3"},
		"text too long":    {"repo": strings.Repeat("x", maxCustomFieldValueLen+1)},
		"blank text":       {"repo": "   "},
		"huge number":      {"points": 1e16},
	}
	for name, patch := range cases {
		if _, err := customFieldsUpdate(st, p, todo, patch); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	// A rejected patch must not partially apply.
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"repo": "ok", "nope": "x"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("mixed patch err = %v", err)
	}
	read, _ := st.GetTodoByLocalID(ctx, p.ID, todo.LocalID, ModeFull)
	if len(read.CustomFields) != 0 {
		t.Fatalf("partial apply: %v", read.CustomFields)
	}
}

func TestCustomFields_OmittedPatchLeavesValues(t *testing.T) {
	st, cleanup, p, todo := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.CreateCustomField(ctx, p.ID, "repo", "Repo", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"repo": "a"}); err != nil {
		t.Fatal(err)
	}
	got, err := customFieldsUpdate(st, p, todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomFields["repo"] != "a" {
		t.Fatalf("values after unrelated update = %v", got.CustomFields)
	}
}

func TestCustomFields_BackupRoundTrip(t *testing.T) {
	st, cleanup, p, todo := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.CreateCustomField(ctx, p.ID, "mr", "MR", CustomFieldURL); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "points", "Points", CustomFieldNumber); err != nil {
		t.Fatal(err)
	}
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"mr": "https://example.com/1", "points": 7.0}); err != nil {
		t.Fatal(err)
	}

	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	var exported *ProjectExport
	for i := range data.Projects {
		if data.Projects[i].Slug == p.Slug {
			exported = &data.Projects[i]
		}
	}
	if exported == nil || len(exported.CustomFields) != 2 || exported.Todos[0].CustomFields["points"] != 7.0 {
		t.Fatalf("export = %+v", exported)
	}

	// Create-copy import into a fresh store state: definitions and values return.
	if _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); err != nil {
		t.Fatalf("import copy: %v", err)
	}
	rows, err := st.db.QueryContext(ctx, `SELECT id FROM projects WHERE id != ?`, p.ID)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	var copyIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		copyIDs = append(copyIDs, id)
	}
	rows.Close()
	copies := 0
	for _, id := range copyIDs {
		defs, err := st.ListProjectCustomFields(ctx, id)
		if err != nil || len(defs) != 2 {
			t.Fatalf("copy defs = %v, %v", defs, err)
		}
		todos, err := st.exportAllTodosForProject(ctx, id, ModeFull)
		if err != nil || len(todos) != 1 || todos[0].CustomFields["mr"] != "https://example.com/1" || todos[0].CustomFields["points"] != 7.0 {
			t.Fatalf("copy todos = %+v, %v", todos, err)
		}
		copies++
	}
	if copies != 1 {
		t.Fatalf("copies = %d", copies)
	}

	// A value for an undefined field is rejected.
	bad := *data
	bad.Projects = []ProjectExport{*exported}
	bad.Projects[0].CustomFields = nil
	bad.Projects[0].Todos = []TodoExport{{LocalID: 1, Title: "x", Status: "backlog", CustomFields: map[string]any{"ghost": "v"}}}
	if _, err := st.ImportProjects(ctx, &bad, ModeFull, "copy"); !errors.Is(err, ErrValidation) {
		t.Fatalf("undefined field import err = %v, want ErrValidation", err)
	}
}

func TestCustomFields_CreateAndBoardProjection(t *testing.T) {
	st, cleanup, p, _ := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.CreateCustomField(ctx, p.ID, "repo", "Repo", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "with fields", ColumnKey: DefaultColumnBacklog, CustomFields: CustomFieldValues{"repo": "scrumboy"}}, ModeFull)
	if err != nil {
		t.Fatalf("create with fields: %v", err)
	}
	if created.CustomFields["repo"] != "scrumboy" {
		t.Fatalf("created = %v", created.CustomFields)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "bad", ColumnKey: DefaultColumnBacklog, CustomFields: CustomFieldValues{"nope": "x"}}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown field err = %v, want ErrValidation", err)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE project_id = ? AND title = 'bad'`, p.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected create left a todo behind: %d %v", count, err)
	}

	ids := []int64{created.ID}
	got, err := listCustomFieldsForTodosQ(ctx, st.db, ids)
	if err != nil || got[created.ID]["repo"] != "scrumboy" {
		t.Fatalf("batch load = %v, %v", got, err)
	}
	todos, err := st.exportAllTodosForProject(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, td := range todos {
		if td.ID == created.ID && td.CustomFields["repo"] == "scrumboy" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("export todos missing values: %+v", todos)
	}
}

func TestCustomFields_LengthCountsRunesAndImportKeysNormalize(t *testing.T) {
	st, cleanup, p, todo := customFieldsTestSetup(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.CreateCustomField(ctx, p.ID, "note", "備考", CustomFieldText); err != nil {
		t.Fatalf("multibyte name: %v", err)
	}
	cjk := strings.Repeat("字", 1000) // 3000 bytes, 1000 runes
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"note": cjk}); err != nil {
		t.Fatalf("1000-rune value rejected: %v", err)
	}
	if _, err := customFieldsUpdate(st, p, todo, CustomFieldValues{"note": strings.Repeat("字", maxCustomFieldValueLen+1)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("over-limit err = %v", err)
	}

	data, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	pe := data.Projects[0]
	pe.CustomFields = []CustomFieldExport{{Key: "NOTE", Name: "備考", Type: "text"}}
	pe.Todos = []TodoExport{{LocalID: 1, Title: "x", Status: "backlog", CustomFields: map[string]any{"NOTE": "ok"}}}
	data.Projects = []ProjectExport{pe}
	if _, err := st.ImportProjects(ctx, data, ModeFull, "copy"); err != nil {
		t.Fatalf("non-canonical key import: %v", err)
	}
}
