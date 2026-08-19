package store

import (
	"context"
	"math"
	"testing"
)

func mustPriorityFilter(t *testing.T, raw string) PriorityFilter {
	t.Helper()
	filter, err := ParsePriorityFilter(raw)
	if err != nil {
		t.Fatalf("ParsePriorityFilter(%q): %v", raw, err)
	}
	return filter
}

func TestParsePriorityFilter(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantMode priorityFilterMode
		wantKey  string
	}{
		{name: "empty", raw: "", wantMode: priorityFilterNone},
		{name: "whitespace only", raw: " \t ", wantMode: priorityFilterNone},
		{name: "no priority sentinel", raw: PriorityFilterNoPriorityValue, wantMode: priorityFilterNoPriority},
		{name: "no priority sentinel trims whitespace", raw: " " + PriorityFilterNoPriorityValue + " ", wantMode: priorityFilterNoPriority},
		{name: "old sentinel is a literal key", raw: "none", wantMode: priorityFilterKey, wantKey: "none"},
		{name: "known key", raw: "urgent", wantMode: priorityFilterKey, wantKey: "urgent"},
		{name: "unknown key is not validated", raw: "not-a-real-key", wantMode: priorityFilterKey, wantKey: "not-a-real-key"},
		{name: "uppercase none is a literal key", raw: "None", wantMode: priorityFilterKey, wantKey: "None"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePriorityFilter(tc.raw)
			if err != nil {
				t.Fatalf("ParsePriorityFilter(%q): %v", tc.raw, err)
			}
			if got.mode != tc.wantMode || got.key != tc.wantKey {
				t.Fatalf("ParsePriorityFilter(%q) = %+v, want mode=%d key=%q", tc.raw, got, tc.wantMode, tc.wantKey)
			}
		})
	}
}

func TestPriorityFilterArgs_InvalidInternalStateFailsClosed(t *testing.T) {
	cond, args := priorityFilterArgs(PriorityFilter{mode: priorityFilterKey})
	if cond != " AND 1 = 0" || len(args) != 0 {
		t.Fatalf("invalid key filter returned cond=%q args=%v, want fail-closed condition", cond, args)
	}
	cond, args = priorityFilterArgs(PriorityFilter{mode: priorityFilterMode(255)})
	if cond != " AND 1 = 0" || len(args) != 0 {
		t.Fatalf("unknown filter mode returned cond=%q args=%v, want fail-closed condition", cond, args)
	}
}

func TestGetBoard_PriorityFilter(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "priority-filter-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	noneTier, err := st.AddPriorityTier(ctxOwner, p.ID, "None")
	if err != nil {
		t.Fatalf("AddPriorityTier None: %v", err)
	}
	if noneTier.Key != "none" {
		t.Fatalf("None tier key = %q, want none", noneTier.Key)
	}

	urgent := "urgent"
	high := "high"
	none := noneTier.Key
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "Urgent todo", Tags: []string{"focus"}, PriorityKey: &urgent}, ModeFull); err != nil {
		t.Fatalf("CreateTodo urgent: %v", err)
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "High todo", Tags: []string{"focus"}, PriorityKey: &high}, ModeFull); err != nil {
		t.Fatalf("CreateTodo high: %v", err)
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "No priority"}, ModeFull); err != nil {
		t.Fatalf("CreateTodo no priority: %v", err)
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "None tier todo", PriorityKey: &none}, ModeFull); err != nil {
		t.Fatalf("CreateTodo none tier: %v", err)
	}

	pc, err := st.GetProjectContextForRead(ctxOwner, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}

	t.Run("filters by specific priority key", func(t *testing.T) {
		filter := mustPriorityFilter(t, "urgent")
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Urgent todo" {
			t.Fatalf("expected only 'Urgent todo', got %+v", todos)
		}
	})

	t.Run("filters real tier whose key is none", func(t *testing.T) {
		filter := mustPriorityFilter(t, "none")
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "None tier todo" {
			t.Fatalf("expected only 'None tier todo', got %+v", todos)
		}
	})

	t.Run("filters no-priority", func(t *testing.T) {
		filter := mustPriorityFilter(t, PriorityFilterNoPriorityValue)
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "No priority" {
			t.Fatalf("expected only 'No priority', got %+v", todos)
		}
	})

	t.Run("unmatched key returns empty board", func(t *testing.T) {
		filter := mustPriorityFilter(t, "not-a-real-key")
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		if len(cols[DefaultColumnBacklog]) != 0 {
			t.Fatalf("expected no todos for unmatched key, got %+v", cols[DefaultColumnBacklog])
		}
	})

	t.Run("no filter returns all", func(t *testing.T) {
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, PriorityFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		if len(cols[DefaultColumnBacklog]) != 4 {
			t.Fatalf("expected 4 todos, got %d", len(cols[DefaultColumnBacklog]))
		}
	})

	t.Run("paged path combines priority tag and search", func(t *testing.T) {
		filter := mustPriorityFilter(t, "urgent")
		_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "focus", "urgent", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault, 1)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Urgent todo" {
			t.Fatalf("expected only 'Urgent todo', got %+v", todos)
		}
		laneMeta := meta[DefaultColumnBacklog]
		if laneMeta.TotalCount != 1 || laneMeta.HasMore {
			t.Fatalf("unexpected filtered lane meta: %+v", laneMeta)
		}
	})

	t.Run("paged path filters no-priority", func(t *testing.T) {
		filter := mustPriorityFilter(t, PriorityFilterNoPriorityValue)
		_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault, 1)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "No priority" {
			t.Fatalf("expected only 'No priority', got %+v", todos)
		}
		if got := meta[DefaultColumnBacklog].TotalCount; got != 1 {
			t.Fatalf("no-priority total count = %d, want 1", got)
		}
	})
}

func TestListTodosForBoardLane_PriorityFilter(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "priority-filter-lane-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	urgent := "urgent"
	for _, title := range []string{"Urgent 1", "Urgent 2", "Urgent 3"} {
		if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: title, PriorityKey: &urgent}, ModeFull); err != nil {
			t.Fatalf("CreateTodo %q: %v", title, err)
		}
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "No priority"}, ModeFull); err != nil {
		t.Fatalf("CreateTodo no priority: %v", err)
	}

	filter := mustPriorityFilter(t, "urgent")
	items, cursor, hasMore, err := st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, 2, math.MinInt64, 0, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 2 || !hasMore || cursor == "" {
		t.Fatalf("expected two filtered items and another page, got items=%+v hasMore=%v cursor=%q", items, hasMore, cursor)
	}

	afterRank, afterID := ParseLaneCursor(cursor)
	items, _, hasMore, err = st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, 2, afterRank, afterID, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"}, SortOrderDefault)
	if err != nil {
		t.Fatalf("ListTodosForBoardLane second page: %v", err)
	}
	if len(items) != 1 || hasMore || items[0].PriorityKey == nil || *items[0].PriorityKey != "urgent" {
		t.Fatalf("unexpected second filtered page: items=%+v hasMore=%v", items, hasMore)
	}

	count, err := st.CountTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, "", "", AssigneeFilter{}, filter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("CountTodosForBoardLane urgent: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 urgent todos, got %d", count)
	}

	noPriorityFilter := mustPriorityFilter(t, PriorityFilterNoPriorityValue)
	count, err = st.CountTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, "", "", AssigneeFilter{}, noPriorityFilter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("CountTodosForBoardLane: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 no-priority todo, got %d", count)
	}
}
