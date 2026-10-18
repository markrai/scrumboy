package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCanonicalizeTagUnicodeContract(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ascii preserved", input: "  Make   Space--Now  ", want: "make-space-now"},
		{name: "chinese", input: "erp系统流程", want: "erp系统流程"},
		{name: "japanese", input: "つながるひろが", want: "つながるひろが"},
		{name: "korean whitespace", input: "개발 작업", want: "개발-작업"},
		{name: "arabic", input: "إصلاح-خطأ", want: "إصلاح-خطأ"},
		{name: "devanagari marks", input: "नमस्ते दुनिया", want: "नमस्ते-दुनिया"},
		{name: "accented latin", input: "Café", want: "café"},
		{name: "decomposed accent", input: "Cafe\u0301", want: "café"},
		{name: "greek", input: "ΕΛΛΗΝΙΚΆ", want: "ελληνικά"},
		{name: "cyrillic", input: "ТЕСТ", want: "тест"},
		{name: "mixed latin cjk", input: "API 系统", want: "api-系统"},
		{name: "unicode whitespace", input: "\u2003alpha\u00a0\t beta\u3000", want: "alpha-beta"},
		{name: "repeated separators", input: "---alpha--- ---beta---", want: "alpha-beta"},
		{name: "no compatibility folding", input: "Ａ", want: "ａ"},
		{name: "go simple lowercase", input: "İ", want: "i"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanonicalizeTag(tc.input)
			if got != tc.want {
				t.Fatalf("CanonicalizeTag(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if again := CanonicalizeTag(got); again != got {
				t.Fatalf("CanonicalizeTag is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestCanonicalizeTagUnicodeBoundariesAndRejections(t *testing.T) {
	exactly32 := strings.Repeat("界", 32)
	if got := CanonicalizeTag(exactly32); got != exactly32 || utf8.RuneCountInString(got) != 32 {
		t.Fatalf("32-code-point multibyte tag = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if got := CanonicalizeTag(strings.Repeat("界", 33)); got != "" {
		t.Fatalf("33-code-point tag accepted as %q", got)
	}
	marksAtLimit := strings.Repeat("क\u094d", 16)
	if got := CanonicalizeTag(marksAtLimit); got == "" || utf8.RuneCountInString(got) != 32 {
		t.Fatalf("base plus combining marks at limit = %q (%d runes)", got, utf8.RuneCountInString(got))
	}
	if got := CanonicalizeTag(marksAtLimit + "क"); got != "" {
		t.Fatalf("over-limit combining tag accepted as %q", got)
	}

	invalid := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: " \u2003 "},
		{name: "hyphens", input: "---"},
		{name: "invalid utf8", input: string([]byte{0xff, 'a'})},
		{name: "leading combining mark", input: "\u0301a"},
		{name: "mark after separator", input: "a-\u0301b"},
		{name: "control", input: "a\x00b"},
		{name: "bidi override", input: "ab\u202ecd"},
		{name: "zero width joiner", input: "क्\u200dष"},
		{name: "zero width non joiner", input: "a\u200cb"},
		{name: "soft hyphen", input: "soft\u00adhyphen"},
		{name: "punctuation", input: "r&d"},
		{name: "underscore", input: "under_score"},
		{name: "non ascii dash", input: "alpha–beta"},
		{name: "symbol", input: "c++"},
		{name: "emoji", input: "release🚀"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalizeTag(tc.input); got != "" {
				t.Fatalf("CanonicalizeTag(%q) = %q, want rejection", tc.input, got)
			}
		})
	}
}

func TestUnicodeTagCreateUpdateDedupAndRollback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "unicode-tags@example.com", "password123", "Unicode")
	if err != nil {
		t.Fatal(err)
	}
	userCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(userCtx, "Unicode Tags")
	if err != nil {
		t.Fatal(err)
	}

	todo, err := st.CreateTodo(userCtx, project.ID, CreateTodoInput{
		Title: "Unicode", Tags: []string{"erp系统流程", "つながるひろが", "개발 작업", "Café", "Cafe\u0301"},
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Unicode tags: %v", err)
	}
	want := []string{"café", "erp系统流程", "つながるひろが", "개발-작업"}
	if strings.Join(todo.Tags, "|") != strings.Join(want, "|") {
		t.Fatalf("created tags = %#v, want %#v", todo.Tags, want)
	}

	updated, err := st.UpdateTodo(userCtx, todo.ID, UpdateTodoInput{
		Title: todo.Title, Body: todo.Body, Tags: []string{"CAFÉ", "नमस्ते दुनिया", "إصلاح-خطأ"},
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo Unicode tags: %v", err)
	}
	if strings.Join(updated.Tags, "|") != "café|إصلاح-خطأ|नमस्ते-दुनिया" {
		t.Fatalf("updated tags = %#v", updated.Tags)
	}

	before := strings.Join(updated.Tags, "|")
	_, err = st.UpdateTodo(userCtx, todo.ID, UpdateTodoInput{
		Title: todo.Title, Body: todo.Body, Tags: []string{"café", "bad!"},
	}, ModeFull)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid update error = %v, want ErrValidation", err)
	}
	after, err := st.GetTodoByLocalID(userCtx, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(after.Tags, "|"); got != before {
		t.Fatalf("failed update partially persisted tags: got %q want %q", got, before)
	}
}

func TestUnicodeEquivalentHistoricalRowsAreReusedWithoutDestructiveDedup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "unicode-history@example.com", "password123", "Unicode")
	if err != nil {
		t.Fatal(err)
	}
	userCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(userCtx, "Unicode History")
	if err != nil {
		t.Fatal(err)
	}
	legacyName := "cafe\u0301"
	res, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, ?, 1)`, user.ID, legacyName)
	if err != nil {
		t.Fatal(err)
	}
	legacyID, _ := res.LastInsertId()
	todo, err := st.CreateTodo(userCtx, project.ID, CreateTodoInput{Title: "Reuse", Tags: []string{"Café"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(todo.Tags, []string{"café"}) {
		t.Fatalf("historical durable tag projection = %#v, want canonical café", todo.Tags)
	}
	var attachedID int64
	if err := st.db.QueryRowContext(ctx, `SELECT tag_id FROM todo_tags WHERE todo_id = ?`, todo.ID).Scan(&attachedID); err != nil {
		t.Fatal(err)
	}
	if attachedID != legacyID {
		t.Fatalf("attached tag id = %d, want reused historical id %d", attachedID, legacyID)
	}
	var rows int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE user_id = ?`, user.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("equivalent input created %d personal rows, want 1", rows)
	}

	board, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRes, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, project_id, name, created_at) VALUES (NULL, ?, ?, 1)`, board.ID, "café")
	if err != nil {
		t.Fatal(err)
	}
	canonicalID, _ := canonicalRes.LastInsertId()
	legacyBoardRes, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, project_id, name, created_at) VALUES (NULL, ?, ?, 2)`, board.ID, legacyName)
	if err != nil {
		t.Fatal(err)
	}
	legacyBoardID, _ := legacyBoardRes.LastInsertId()
	boardTodo, err := st.CreateTodo(ctx, board.ID, CreateTodoInput{Title: "Temporary", Tags: []string{"CAFÉ"}}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT tag_id FROM todo_tags WHERE todo_id = ?`, boardTodo.ID).Scan(&attachedID); err != nil {
		t.Fatal(err)
	}
	if attachedID != canonicalID {
		t.Fatalf("temporary attachment id = %d, want exact canonical row %d", attachedID, canonicalID)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, boardTodo.ID, legacyBoardID); err != nil {
		t.Fatal(err)
	}
	reloadedBoardTodo, err := st.GetTodoByLocalID(ctx, board.ID, boardTodo.LocalID, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloadedBoardTodo.Tags, []string{legacyName, "café"}) {
		t.Fatalf("temporary row-level Unicode tags = %#v", reloadedBoardTodo.Tags)
	}
	var equivalentRows int
	rowsResult, err := st.db.QueryContext(ctx, `SELECT name FROM tags WHERE project_id = ? AND name IN (?, ?)`, board.ID, "café", legacyName)
	if err != nil {
		t.Fatal(err)
	}
	for rowsResult.Next() {
		equivalentRows++
	}
	rowsResult.Close()
	if equivalentRows != 2 {
		t.Fatalf("temporary historical rows = %d, want both physical rows preserved", equivalentRows)
	}
}

func TestUnicodeDurableGroupingColorAndDeletionRemainOwnerScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "unicode-owner@example.com", "password123", "Owner")
	member, _ := st.CreateUser(ctx, "unicode-member@example.com", "password123", "Member")
	ownerCtx := WithUserID(ctx, owner.ID)
	memberCtx := WithUserID(ctx, member.ID)
	project, _ := st.CreateProject(ownerCtx, "Unicode grouping")
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, member.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	ownerTodo, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "Owner", Tags: []string{"Café"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTodo(memberCtx, project.ID, CreateTodoInput{Title: "Member", Tags: []string{"cafe\u0301"}}, ModeFull); err != nil {
		t.Fatal(err)
	}
	var memberTagID int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = 'café'`, member.ID).Scan(&memberTagID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, ownerTodo.ID, memberTagID); err != nil {
		t.Fatal(err)
	}
	reloadedOwnerTodo, err := st.GetTodoByLocalID(ownerCtx, project.ID, ownerTodo.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloadedOwnerTodo.Tags, []string{"café"}) {
		t.Fatalf("durable todo exposed duplicate equivalent tags: %#v", reloadedOwnerTodo.Tags)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM todo_tags WHERE todo_id = ? AND tag_id = ?`, ownerTodo.ID, memberTagID); err != nil {
		t.Fatal(err)
	}

	pc, err := st.GetProjectContextBySlug(ownerCtx, project.Slug, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := st.ListTagCounts(ownerCtx, &pc)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].Name != "café" || counts[0].Count != 2 {
		t.Fatalf("grouped Unicode tags = %+v", counts)
	}
	color := "#123456"
	if err := st.SetViewerTagColorByName(ownerCtx, project.ID, owner.ID, "Cafe\u0301", &color); err != nil {
		t.Fatal(err)
	}
	counts, err = st.ListTagCounts(ownerCtx, &pc)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].Color == nil || *counts[0].Color != color {
		t.Fatalf("Unicode grouped color = %+v", counts)
	}

	if _, err := st.DeleteMyTagByName(ownerCtx, project.ID, owner.ID, "CAFÉ"); err != nil {
		t.Fatal(err)
	}
	counts, err = st.ListTagCounts(ownerCtx, &pc)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].Name != "café" || counts[0].Count != 1 {
		t.Fatalf("owner-scoped Unicode deletion = %+v", counts)
	}
	if _, err := st.DeleteMyTagByName(ownerCtx, project.ID, owner.ID, "café"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner deleted another user's Unicode row: %v", err)
	}
}

func TestScopedTagExactLookupUsesUniqueIndexesAndWinsOverLegacyRows(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "tag-index@example.com", "password123", "Indexed")
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(WithUserID(ctx, user.ID), "Indexed tags")
	if err != nil {
		t.Fatal(err)
	}

	assertUsesIndex := func(query, indexName string, args ...any) {
		t.Helper()
		rows, err := st.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(details, "\n"), indexName) {
			t.Fatalf("query plan %q does not use %s", details, indexName)
		}
	}
	assertUsesIndex(`SELECT id FROM tags WHERE user_id = ? AND name = ? LIMIT 1`, "idx_tags_user_name", user.ID, "café")
	assertUsesIndex(`SELECT id FROM tags WHERE project_id = ? AND user_id IS NULL AND name = ? LIMIT 1`, "idx_tags_project_name", project.ID, "café")

	legacyUser, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, ?, 1)`, user.ID, "cafe\u0301")
	if err != nil {
		t.Fatal(err)
	}
	exactUser, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, ?, 2)`, user.ID, "café")
	if err != nil {
		t.Fatal(err)
	}
	legacyUserID, _ := legacyUser.LastInsertId()
	exactUserID, _ := exactUser.LastInsertId()
	if exactUserID <= legacyUserID {
		t.Fatal("test fixture must place the exact row after the legacy row")
	}
	got, found, err := findUserTagIDByGroupKey(ctx, st.db, user.ID, "café")
	if err != nil || !found || got != exactUserID {
		t.Fatalf("exact user lookup = (%d, %v, %v), want %d", got, found, err, exactUserID)
	}

	legacyBoard, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, project_id, name, created_at) VALUES (NULL, ?, ?, 1)`, project.ID, "cafe\u0301")
	if err != nil {
		t.Fatal(err)
	}
	exactBoard, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, project_id, name, created_at) VALUES (NULL, ?, ?, 2)`, project.ID, "café")
	if err != nil {
		t.Fatal(err)
	}
	legacyBoardID, _ := legacyBoard.LastInsertId()
	exactBoardID, _ := exactBoard.LastInsertId()
	if exactBoardID <= legacyBoardID {
		t.Fatal("test fixture must place the exact board row after the legacy row")
	}
	got, found, err = findBoardScopedTagIDByGroupKey(ctx, st.db, project.ID, "café")
	if err != nil || !found || got != exactBoardID {
		t.Fatalf("exact board lookup = (%d, %v, %v), want %d", got, found, err, exactBoardID)
	}

	iResult, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, 'i', 3)`, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	iID, _ := iResult.LastInsertId()
	if _, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at) VALUES (?, ?, 4)`, user.ID, "i\u0307"); err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(WithUserID(ctx, user.ID), project.ID, CreateTodoInput{Title: "Simple lowercase", Tags: []string{"İ"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(todo.Tags, []string{"i"}) {
		t.Fatalf("U+0130 todo tags = %#v, want server identity i", todo.Tags)
	}
	var attachedID int64
	if err := st.db.QueryRowContext(ctx, `SELECT tag_id FROM todo_tags WHERE todo_id = ?`, todo.ID).Scan(&attachedID); err != nil {
		t.Fatal(err)
	}
	if attachedID != iID {
		t.Fatalf("U+0130 attached tag = %d, want exact i row %d", attachedID, iID)
	}
}
