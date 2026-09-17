package mcp_test

import (
	"context"
	"database/sql"
	"net/http"
	"reflect"
	"sort"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func boardGetBacklogTitles(t *testing.T, data map[string]any) []string {
	t.Helper()
	backlog := boardColumnByKey(t, data["columns"].([]any), store.DefaultColumnBacklog)
	items := backlog["items"].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(map[string]any)["title"].(string))
	}
	return out
}

func callBoardGetLegacy(t *testing.T, client *http.Client, baseURL, toolName string, input map[string]any) (int, map[string]any, map[string]any) {
	t.Helper()
	resp, out := doMCP(t, client, baseURL+"/mcp", map[string]any{"tool": toolName, "input": input})
	var data map[string]any
	if raw, ok := out["data"].(map[string]any); ok {
		data = raw
	}
	return resp.StatusCode, out, data
}

func callBoardGetJSONRPC(t *testing.T, client *http.Client, baseURL, toolName string, input map[string]any) (map[string]any, map[string]any, bool) {
	t.Helper()
	_, out := doJSONRPC(t, client, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": input,
		},
	})
	result, _ := out["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	isError, _ := result["isError"].(bool)
	return result, structured, isError
}

func TestMCPBoardGetMultiTagANDAndCompatibility(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "MCP Multi Tag Board",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}

	slug := projectSlugByName(t, sqlDB, "MCP Multi Tag Board")
	projectID := projectIDBySlug(t, sqlDB, slug)
	ownerID := firstUserID(t, sqlDB)
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	start := time.Unix(1_000, 0).UTC()
	sprint, err := st.CreateSprint(ctx, projectID, "Sprint 1", start, start.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	high := "high"
	mustCreate := func(in store.CreateTodoInput) store.Todo {
		t.Helper()
		todo, err := st.CreateTodo(ctx, projectID, in, store.ModeFull)
		if err != nil {
			t.Fatalf("create todo %q: %v", in.Title, err)
		}
		return todo
	}

	mustCreate(store.CreateTodoInput{Title: "feature only", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"feature"}})
	mustCreate(store.CreateTodoInput{Title: "ux only", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"ux"}})
	mustCreate(store.CreateTodoInput{Title: "feature ux", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"feature", "ux"}})
	mustCreate(store.CreateTodoInput{Title: "feature ux mobile", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"feature", "ux", "mobile"}})
	mustCreate(store.CreateTodoInput{
		Title:          "composed match",
		ColumnKey:      store.DefaultColumnBacklog,
		Tags:           []string{"feature", "ux"},
		Body:           "mobile search needle",
		AssigneeUserID: &ownerID,
		PriorityKey:    &high,
		SprintID:       &sprint.ID,
	})
	mustCreate(store.CreateTodoInput{Title: "doing feature ux", ColumnKey: store.DefaultColumnDoing, Tags: []string{"feature", "ux"}})
	mustCreate(store.CreateTodoInput{Title: "page a", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"pager", "keep"}})
	mustCreate(store.CreateTodoInput{Title: "page b", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"pager", "keep"}})
	mustCreate(store.CreateTodoInput{Title: "page c", ColumnKey: store.DefaultColumnBacklog, Tags: []string{"pager", "keep"}})

	t.Run("legacy scalar still works", func(t *testing.T) {
		status, out, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature",
		})
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		got := boardGetBacklogTitles(t, data)
		if !containsAll(got, "feature only", "feature ux", "feature ux mobile", "composed match") || containsAny(got, "ux only") {
			t.Fatalf("scalar feature titles = %#v", got)
		}
	})

	t.Run("single-element tags matches scalar", func(t *testing.T) {
		_, _, scalarData := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature",
		})
		_, _, arrayData := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature"},
		})
		if !reflect.DeepEqual(sortedStrings(boardGetBacklogTitles(t, scalarData)), sortedStrings(boardGetBacklogTitles(t, arrayData))) {
			t.Fatalf("scalar=%#v array=%#v", boardGetBacklogTitles(t, scalarData), boardGetBacklogTitles(t, arrayData))
		}
		if !reflect.DeepEqual(sortedMapKeys(scalarData), sortedMapKeys(arrayData)) {
			t.Fatalf("response keys scalar=%v array=%v", sortedMapKeys(scalarData), sortedMapKeys(arrayData))
		}
	})

	t.Run("two tags use AND", func(t *testing.T) {
		status, out, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "ux"},
		})
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		got := boardGetBacklogTitles(t, data)
		if containsAny(got, "feature only", "ux only") || !containsAll(got, "feature ux", "feature ux mobile", "composed match") {
			t.Fatalf("AND titles = %#v", got)
		}
	})

	t.Run("three tags use AND", func(t *testing.T) {
		_, _, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "ux", "mobile"},
		})
		got := boardGetBacklogTitles(t, data)
		if !reflect.DeepEqual(got, []string{"feature ux mobile"}) && !containsAll(got, "feature ux mobile") {
			t.Fatalf("three-tag titles = %#v", got)
		}
		if containsAny(got, "feature ux", "feature only") {
			t.Fatalf("three-tag leaked non-matching titles = %#v", got)
		}
	})

	t.Run("search assignee priority sprint compose with tags", func(t *testing.T) {
		status, out, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "ux"},
			"search":      "mobile",
			"assignee":    "me",
			"priority":    "high",
			"sprintId":    sprint.ID,
		})
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		got := boardGetBacklogTitles(t, data)
		if !reflect.DeepEqual(got, []string{"composed match"}) {
			t.Fatalf("composed titles = %#v", got)
		}
	})

	t.Run("column-scoped board_get keeps AND", func(t *testing.T) {
		status, out, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "ux"},
			"columnKey":   store.DefaultColumnDoing,
		})
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		if _, ok := boardColumnByKey(t, data["columns"].([]any), store.DefaultColumnDoing)["items"]; !ok {
			t.Fatalf("missing doing items: %#v", data)
		}
		doing := boardColumnByKey(t, data["columns"].([]any), store.DefaultColumnDoing)
		items := doing["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["title"] != "doing feature ux" {
			t.Fatalf("doing items = %#v", items)
		}
		if len(data["columns"].([]any)) != 1 {
			t.Fatalf("column-scoped columns = %#v", data["columns"])
		}
	})

	t.Run("pagination preserves every tag", func(t *testing.T) {
		status, out, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"pager", "keep"},
			"limit":       2,
		})
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		first := boardGetBacklogTitles(t, data)
		if len(first) != 2 {
			t.Fatalf("first page = %#v", first)
		}
		meta := out["meta"].(map[string]any)
		if meta["hasMoreByColumn"].(map[string]any)[store.DefaultColumnBacklog] != true {
			t.Fatalf("expected another page: %#v", meta)
		}
		if int(meta["totalCountByColumn"].(map[string]any)[store.DefaultColumnBacklog].(float64)) != 3 {
			t.Fatalf("totalCount = %#v", meta["totalCountByColumn"])
		}
		cursor := meta["nextCursorByColumn"].(map[string]any)[store.DefaultColumnBacklog].(string)
		status, out2, data2 := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"pager", "keep"},
			"limit":       2,
			"cursorByColumn": map[string]any{
				store.DefaultColumnBacklog: cursor,
			},
		})
		if status != http.StatusOK {
			t.Fatalf("continuation status=%d body=%#v", status, out2)
		}
		second := boardGetBacklogTitles(t, data2)
		if len(second) != 1 {
			t.Fatalf("second page = %#v", second)
		}
		combined := append(append([]string{}, first...), second...)
		if !containsAll(combined, "page a", "page b", "page c") {
			t.Fatalf("paged titles = %#v", combined)
		}
	})

	t.Run("unknown one-of-N tag produces empty board", func(t *testing.T) {
		_, _, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "does-not-exist"},
		})
		if len(boardGetBacklogTitles(t, data)) != 0 {
			t.Fatalf("unknown tag should empty the board: %#v", boardGetBacklogTitles(t, data))
		}
	})

	t.Run("output shape is unchanged", func(t *testing.T) {
		_, _, tagged := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature",
		})
		_, _, multi := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"feature", "ux"},
		})
		if _, ok := tagged["tags"]; ok {
			t.Fatalf("board_get data must not grow a tags projection: %#v", tagged)
		}
		if _, ok := multi["tags"]; ok {
			t.Fatalf("board_get data must not grow a tags projection: %#v", multi)
		}
		if !reflect.DeepEqual(sortedMapKeys(tagged["project"].(map[string]any)), sortedMapKeys(multi["project"].(map[string]any))) {
			t.Fatalf("project keys changed")
		}
		taggedCol := tagged["columns"].([]any)[0].(map[string]any)
		multiCol := multi["columns"].([]any)[0].(map[string]any)
		if !reflect.DeepEqual(sortedMapKeys(taggedCol), sortedMapKeys(multiCol)) {
			t.Fatalf("column keys changed: %v vs %v", sortedMapKeys(taggedCol), sortedMapKeys(multiCol))
		}
	})

	t.Run("both transports and permanent alias match", func(t *testing.T) {
		input := map[string]any{"projectSlug": slug, "tags": []any{"feature", "ux"}}
		var titles [][]string
		for _, name := range []string{"board_get", "board.get"} {
			status, out, data := callBoardGetLegacy(t, client, ts.URL, name, input)
			if status != http.StatusOK {
				t.Fatalf("%s legacy status=%d body=%#v", name, status, out)
			}
			titles = append(titles, sortedStrings(boardGetBacklogTitles(t, data)))
			_, structured, isError := callBoardGetJSONRPC(t, client, ts.URL, name, input)
			if isError {
				t.Fatalf("%s json-rpc error %#v", name, structured)
			}
			titles = append(titles, sortedStrings(boardGetBacklogTitles(t, structured)))
		}
		for i := 1; i < len(titles); i++ {
			if !reflect.DeepEqual(titles[0], titles[i]) {
				t.Fatalf("transport titles differ: %#v vs %#v", titles[0], titles[i])
			}
		}
	})
}

func TestMCPBoardGetMultiTagDurableAliasAndTemporaryExactName(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()

	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	st := store.New(sqlDB, nil)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)

	t.Run("durable canonical alias qualifies", func(t *testing.T) {
		resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
			"name": "MCP Alias Board",
		}, &map[string]any{})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create project status=%d", resp.StatusCode)
		}
		slug := projectSlugByName(t, sqlDB, "MCP Alias Board")
		projectID := projectIDBySlug(t, sqlDB, slug)
		canonical, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "canonical pair",
			ColumnKey: store.DefaultColumnBacklog,
			Tags:      []string{"make-space", "ux"},
		}, store.ModeFull)
		if err != nil {
			t.Fatalf("canonical todo: %v", err)
		}
		legacy, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "legacy pair",
			ColumnKey: store.DefaultColumnBacklog,
			Tags:      []string{"ux"},
		}, store.ModeFull)
		if err != nil {
			t.Fatalf("legacy todo: %v", err)
		}
		attachPersonalTag(t, sqlDB, ownerID, legacy.ID, "make space")
		_ = canonical

		_, _, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"make-space", "ux"},
		})
		got := boardGetBacklogTitles(t, data)
		if !containsAll(got, "canonical pair", "legacy pair") {
			t.Fatalf("durable alias titles = %#v", got)
		}
	})

	t.Run("temporary board keeps exact stored names", func(t *testing.T) {
		resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
			"name": "MCP Temp Board",
		}, &map[string]any{})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create project status=%d", resp.StatusCode)
		}
		slug := projectSlugByName(t, sqlDB, "MCP Temp Board")
		projectID := projectIDBySlug(t, sqlDB, slug)
		if _, err := sqlDB.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(24*time.Hour).UnixMilli(), projectID); err != nil {
			t.Fatalf("mark temporary: %v", err)
		}
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "canonical",
			ColumnKey: store.DefaultColumnBacklog,
			Tags:      []string{"make-space", "ux"},
		}, store.ModeFull); err != nil {
			t.Fatalf("canonical temp todo: %v", err)
		}
		legacy, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "legacy",
			ColumnKey: store.DefaultColumnBacklog,
			Tags:      []string{"ux"},
		}, store.ModeFull)
		if err != nil {
			t.Fatalf("legacy temp todo: %v", err)
		}
		attachPersonalTag(t, sqlDB, ownerID, legacy.ID, "make space")

		_, _, data := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tags":        []any{"make-space", "ux"},
		})
		got := boardGetBacklogTitles(t, data)
		if !reflect.DeepEqual(got, []string{"canonical"}) && !containsAll(got, "canonical") {
			t.Fatalf("temporary exact-name titles = %#v", got)
		}
		if containsAny(got, "legacy") {
			t.Fatalf("temporary board leaked alias match: %#v", got)
		}
	})
}

func TestMCPBoardGetMultiTagTransportValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	resp := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{
		"name": "MCP Tag Validation Board",
	}, &map[string]any{})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d", resp.StatusCode)
	}
	slug := projectSlugByName(t, sqlDB, "MCP Tag Validation Board")

	t.Run("legacy exclusive error envelope", func(t *testing.T) {
		status, out, _ := callBoardGetLegacy(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature",
			"tags":        []any{"ux"},
		})
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%#v", status, out)
		}
		errObj := out["error"].(map[string]any)
		if errObj["code"] != "VALIDATION_ERROR" || errObj["message"] != "tag and tags are mutually exclusive" {
			t.Fatalf("error = %#v", errObj)
		}
	})

	t.Run("json-rpc exclusive error", func(t *testing.T) {
		result, structured, isError := callBoardGetJSONRPC(t, client, ts.URL, "board_get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature",
			"tags":        []any{"ux"},
		})
		if !isError {
			t.Fatalf("result = %#v, want tool error", result)
		}
		if structured["code"] != "VALIDATION_ERROR" || structured["message"] != "tag and tags are mutually exclusive" {
			t.Fatalf("structured = %#v", structured)
		}
	})

	t.Run("scalar comma is literal over json-rpc", func(t *testing.T) {
		st := store.New(sqlDB, nil)
		ctx := store.WithUserID(context.Background(), firstUserID(t, sqlDB))
		projectID := projectIDBySlug(t, sqlDB, slug)
		if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{
			Title:     "both tags",
			ColumnKey: store.DefaultColumnBacklog,
			Tags:      []string{"feature", "ux"},
		}, store.ModeFull); err != nil {
			t.Fatalf("create both-tags todo: %v", err)
		}
		_, structured, isError := callBoardGetJSONRPC(t, client, ts.URL, "board.get", map[string]any{
			"projectSlug": slug,
			"tag":         "feature,ux",
		})
		if isError {
			t.Fatalf("json-rpc error %#v", structured)
		}
		if len(boardGetBacklogTitles(t, structured)) != 0 {
			t.Fatalf("literal comma must not become two filters: %#v", boardGetBacklogTitles(t, structured))
		}
	})
}

func attachPersonalTag(t *testing.T, sqlDB *sql.DB, userID, todoID int64, name string) {
	t.Helper()
	var tagID int64
	err := sqlDB.QueryRow(`SELECT id FROM tags WHERE user_id = ? AND name = ?`, userID, name).Scan(&tagID)
	if err == sql.ErrNoRows {
		res, insertErr := sqlDB.Exec(
			`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES(?, ?, ?, NULL, NULL)`,
			userID, name, time.Now().UTC().UnixMilli(),
		)
		if insertErr != nil {
			t.Fatalf("insert personal tag %q: %v", name, insertErr)
		}
		tagID, err = res.LastInsertId()
		if err != nil {
			t.Fatalf("personal tag id: %v", err)
		}
	} else if err != nil {
		t.Fatalf("lookup personal tag %q: %v", name, err)
	}
	if _, err := sqlDB.Exec(`INSERT OR IGNORE INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagID); err != nil {
		t.Fatalf("attach personal tag: %v", err)
	}
}

func containsAll(got []string, want ...string) bool {
	set := map[string]struct{}{}
	for _, item := range got {
		set[item] = struct{}{}
	}
	for _, item := range want {
		if _, ok := set[item]; !ok {
			return false
		}
	}
	return true
}

func containsAny(got []string, want ...string) bool {
	set := map[string]struct{}{}
	for _, item := range got {
		set[item] = struct{}{}
	}
	for _, item := range want {
		if _, ok := set[item]; ok {
			return true
		}
	}
	return false
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
