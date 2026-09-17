package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func multiTagLanePlanQuery(cte string) string {
	return cte + `
SELECT t.id
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ? AND t.column_key = ? AND t.archived_at IS NULL
ORDER BY t.rank ASC, t.id ASC
LIMIT ?`
}

func TestBoardMultiTagFilterQueryPlanBakeoff(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "multi-tag-plan@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "multi-tag-plan-other@example.com", "password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "multi tag plan")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "rare", Tags: []string{"rare", "common", "third", "fourth", "fifth"}}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	tagIDs := make(map[string]int64)
	for _, name := range []string{"rare", "common", "third", "fourth", "fifth"} {
		var tagID int64
		if err := st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE user_id = ? AND name = ?`, owner.ID, name).Scan(&tagID); err != nil {
			t.Fatal(err)
		}
		tagIDs[name] = tagID
	}
	aliasID := plInsertPersonalTagRow(t, st, other.ID, "rare")
	if _, err := st.db.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, seed.ID, aliasID); err != nil {
		t.Fatal(err)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().UnixMilli()
	for i := 2; i <= 250; i++ {
		result, err := tx.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, created_at, updated_at)
VALUES (?, ?, 'common', '', ?, ?, ?, ?)`, project.ID, i, DefaultColumnBacklog, int64(i)*1000, now, now)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		todoID, _ := result.LastInsertId()
		if _, err := tx.ExecContext(ctx, `INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?)`, todoID, tagIDs["common"]); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `ANALYZE`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		groups [][]int64
	}{
		{name: "rare plus common with alias backing", groups: [][]int64{{tagIDs["rare"], aliasID}, {tagIDs["common"]}}},
		{name: "three logical tags", groups: [][]int64{{tagIDs["rare"]}, {tagIDs["common"]}, {tagIDs["third"]}}},
		{name: "five logical tags", groups: [][]int64{{tagIDs["rare"]}, {tagIDs["common"]}, {tagIDs["third"]}, {tagIDs["fourth"]}, {tagIDs["fifth"]}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intersectSQL, intersectArgs := intersectBoardTagFilterCTE(tc.groups)
			intersectArgs = append(intersectArgs, project.ID, DefaultColumnBacklog, 51)
			intersectPlan := explainTodoArchivalPlan(t, st, multiTagLanePlanQuery(intersectSQL), intersectArgs...)

			groupedSQL, groupedArgs := groupedValuesBoardTagFilterCTE(tc.groups)
			groupedArgs = append(groupedArgs, project.ID, DefaultColumnBacklog, 51)
			groupedPlan := explainTodoArchivalPlan(t, st, multiTagLanePlanQuery(groupedSQL), groupedArgs...)

			t.Logf("INTERSECT: %s", intersectPlan)
			t.Logf("VALUES+HAVING: %s", groupedPlan)
			for candidate, plan := range map[string]string{"INTERSECT": intersectPlan, "VALUES+HAVING": groupedPlan} {
				if !strings.Contains(plan, "idx_todo_tags_tag") {
					t.Fatalf("%s lost indexed tag lookup: %s", candidate, plan)
				}
				if strings.Contains(plan, "SCAN tt") {
					t.Fatalf("%s scans todo_tags: %s", candidate, plan)
				}
				if strings.Contains(plan, "SCAN t") && !strings.Contains(plan, "SCAN tagged_todos") {
					t.Fatalf("%s scans todos: %s", candidate, plan)
				}
			}
			if strings.Contains(intersectPlan, "GROUP BY") || strings.Contains(intersectPlan, "count(DISTINCT)") {
				t.Fatalf("INTERSECT unexpectedly gained grouped aggregation: %s", intersectPlan)
			}
			if !strings.Contains(groupedPlan, "GROUP BY") {
				t.Fatalf("VALUES+HAVING plan no longer exposes its grouped aggregation cost: %s", groupedPlan)
			}
		})
	}

	t.Run("soft-cap threshold count", func(t *testing.T) {
		groups := [][]int64{{tagIDs["rare"], aliasID}, {tagIDs["common"]}}
		for candidate, build := range map[string]func([][]int64) (string, []any){
			"INTERSECT":     intersectBoardTagFilterCTE,
			"VALUES+HAVING": groupedValuesBoardTagFilterCTE,
		} {
			cte, args := build(groups)
			args = append(args, project.ID)
			plan := explainTodoArchivalPlan(t, st, cte+`
SELECT COUNT(*)
FROM todos t
INNER JOIN tagged_todos ft ON ft.todo_id = t.id
WHERE t.project_id = ? AND t.archived_at IS NULL`, args...)
			t.Logf("%s soft-cap count: %s", candidate, plan)
			if !strings.Contains(plan, "idx_todo_tags_tag") || strings.Contains(plan, "SCAN tt") {
				t.Fatalf("%s soft-cap count lost indexed tag matching: %s", candidate, plan)
			}
		}
	})
}

func TestBoardTagFilterCTEPreservesSingleTagAndSelectsIntersectForMultipleTags(t *testing.T) {
	one := boardTagFilter{TagIDGroups: [][]int64{{11, 12}}}
	oneSQL, oneArgs := boardTagFilterCTE(one)
	if !strings.Contains(oneSQL, "SELECT DISTINCT tt.todo_id") || strings.Contains(oneSQL, "INTERSECT") {
		t.Fatalf("single-tag SQL shape changed: %s", oneSQL)
	}
	if !reflect.DeepEqual(oneArgs, []any{int64(11), int64(12)}) {
		t.Fatalf("single-tag args=%v", oneArgs)
	}

	multiple := boardTagFilter{TagIDGroups: [][]int64{{11}, {21, 22}, {31}}}
	gotSQL, gotArgs := boardTagFilterCTE(multiple)
	wantSQL, wantArgs := intersectBoardTagFilterCTE(multiple.TagIDGroups)
	if gotSQL != wantSQL || !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("production multi-tag CTE is not the baked-off INTERSECT candidate\ngot: %s %v\nwant: %s %v", gotSQL, gotArgs, wantSQL, wantArgs)
	}
}
