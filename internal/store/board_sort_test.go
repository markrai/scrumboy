package store

import (
	"context"
	"testing"
	"time"
)

func TestParseSortOrder(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    SortOrder
		wantErr bool
	}{
		{name: "empty", raw: "", want: SortOrderDefault},
		{name: "whitespace only", raw: "  ", want: SortOrderDefault},
		{name: "newest", raw: "newest", want: SortOrderNewest},
		{name: "newest trims whitespace", raw: " newest ", want: SortOrderNewest},
		{name: "oldest", raw: "oldest", want: SortOrderOldest},
		{name: "uppercase newest rejected", raw: "Newest", wantErr: true},
		{name: "unknown value rejected", raw: "manual", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSortOrder(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSortOrder(%q) expected error, got %+v", tc.raw, got)
				}
				if err.Error() != "invalid sort" {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSortOrder(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("ParseSortOrder(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// insertSortTestTodo directly inserts a todo row with a caller-controlled
// created_at/rank so ordering tests can decouple "manual rank order" from
// "creation order" (CreateTodo always stamps created_at = time.Now()).
func insertSortTestTodo(t *testing.T, st *Store, projectID, localID int64, title string, rank, createdAtMs int64) {
	t.Helper()
	_, err := st.db.Exec(`
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
VALUES (?, ?, ?, '', ?, ?, NULL, NULL, NULL, ?, ?, NULL)`,
		projectID, localID, title, DefaultColumnBacklog, rank, createdAtMs, createdAtMs)
	if err != nil {
		t.Fatalf("insert sort test todo %q: %v", title, err)
	}
}

func TestGetBoard_SortOrder(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "sort-order-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Rank order deliberately disagrees with created_at order so the three
	// sort modes are distinguishable: rank order is A,B,C but creation order
	// (oldest to newest) is C,A,B.
	base := time.Now().UTC().UnixMilli()
	insertSortTestTodo(t, st, p.ID, 1, "A", 10, base+2000) // rank 1st, created 2nd
	insertSortTestTodo(t, st, p.ID, 2, "B", 20, base+3000) // rank 2nd, created 3rd (newest)
	insertSortTestTodo(t, st, p.ID, 3, "C", 30, base+1000) // rank 3rd, created 1st (oldest)

	pc, err := st.GetProjectContextForRead(ctxOwner, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}

	titlesOf := func(todos []Todo) []string {
		out := make([]string, len(todos))
		for i, td := range todos {
			out[i] = td.Title
		}
		return out
	}
	assertOrder := func(t *testing.T, got []string, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}

	t.Run("default preserves manual rank order", func(t *testing.T) {
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"}, SortOrderDefault)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		assertOrder(t, titlesOf(cols[DefaultColumnBacklog]), []string{"A", "B", "C"})
	})

	t.Run("newest first orders by created_at desc", func(t *testing.T) {
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"}, SortOrderNewest)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		assertOrder(t, titlesOf(cols[DefaultColumnBacklog]), []string{"B", "A", "C"})
	})

	t.Run("oldest first orders by created_at asc", func(t *testing.T) {
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"}, SortOrderOldest)
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		assertOrder(t, titlesOf(cols[DefaultColumnBacklog]), []string{"C", "A", "B"})
	})

	t.Run("paged fast path matches sort order", func(t *testing.T) {
		_, _, _, cols, _, err := st.GetBoardPaged(ctxOwner, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"}, SortOrderNewest, 10)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		assertOrder(t, titlesOf(cols[DefaultColumnBacklog]), []string{"B", "A", "C"})
	})
}

func TestListTodosForBoardLane_SortOrderPagination(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "sort-order-lane-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	base := time.Now().UTC().UnixMilli()
	// 5 todos, created_at strictly increasing with local_id/rank; titles name
	// their creation order (T1 oldest .. T5 newest) so page contents are easy
	// to assert regardless of direction.
	for i := int64(1); i <= 5; i++ {
		insertSortTestTodo(t, st, p.ID, i, titleFor(i), i*10, base+i*1000)
	}

	drainAll := func(t *testing.T, sortOrder SortOrder, pageSize int) []string {
		t.Helper()
		var titles []string
		var afterA, afterB int64
		if sortOrder == SortOrderNewest {
			afterA, afterB = 1<<62, 1<<62 // sentinel matching store's laneCursorSentinel for descending order
		}
		seen := map[string]bool{}
		for i := 0; i < 10; i++ { // hard cap so a pagination bug can't infinite-loop the test
			items, nextCursor, hasMore, err := st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, pageSize, afterA, afterB, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"}, sortOrder)
			if err != nil {
				t.Fatalf("ListTodosForBoardLane: %v", err)
			}
			for _, td := range items {
				if seen[td.Title] {
					t.Fatalf("duplicate item %q returned across pages (dup/gap bug)", td.Title)
				}
				seen[td.Title] = true
				titles = append(titles, td.Title)
			}
			if !hasMore {
				break
			}
			if nextCursor == "" {
				t.Fatalf("hasMore=true but nextCursor is empty")
			}
			afterA, afterB = ParseLaneCursor(nextCursor)
		}
		return titles
	}

	t.Run("newest first paginates without dup/gap", func(t *testing.T) {
		got := drainAll(t, SortOrderNewest, 2)
		want := []string{"T5", "T4", "T3", "T2", "T1"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("oldest first paginates without dup/gap", func(t *testing.T) {
		got := drainAll(t, SortOrderOldest, 2)
		want := []string{"T1", "T2", "T3", "T4", "T5"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})

	t.Run("default rank order paginates without dup/gap", func(t *testing.T) {
		got := drainAll(t, SortOrderDefault, 2)
		want := []string{"T1", "T2", "T3", "T4", "T5"}
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	})
}

func titleFor(i int64) string {
	return "T" + string(rune('0'+i))
}
