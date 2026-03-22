package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	return New(sqlDB, nil), func() { _ = sqlDB.Close() }
}

func ids(ts []Todo) []int64 {
	out := make([]int64, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

func mustCreateTodo(t *testing.T, st *Store, projectID int64, title, columnKey string) Todo {
	t.Helper()

	todo, err := st.CreateTodo(context.Background(), projectID, CreateTodoInput{Title: title, ColumnKey: columnKey}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo %q: %v", title, err)
	}
	return todo
}

func mustFilteredLanePage(t *testing.T, st *Store, projectID int64, status, search string, limit int) ([]Todo, LaneMeta) {
	t.Helper()

	ctx := context.Background()
	pc, err := st.GetProjectContextForRead(ctx, projectID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	_, _, _, cols, meta, err := st.GetBoardPaged(ctx, &pc, "", search, SprintFilter{Mode: "none"}, limit)
	if err != nil {
		t.Fatalf("GetBoardPaged: %v", err)
	}
	return cols[status], meta[status]
}

func mustFirstHiddenLaneTodo(t *testing.T, st *Store, projectID int64, status, search string, meta LaneMeta) Todo {
	t.Helper()

	afterRank, afterID := ParseLaneCursor(meta.NextCursor)
	items, _, _, err := st.ListTodosForBoardLane(context.Background(), projectID, status, 1, afterRank, afterID, "", search, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 hidden lane todo, got %d", len(items))
	}
	return items[0]
}

func assertTodoIDs(t *testing.T, got, want []int64) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestMoveTodo_ReorderWithinColumn(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo a: %v", err)
	}
	b, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "b", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo b: %v", err)
	}
	c, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "c", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo c: %v", err)
	}

	_, err = st.MoveTodo(ctx, c.ID, DefaultColumnBacklog, nil, &a.ID, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	got := ids(cols[DefaultColumnBacklog])
	want := []int64{c.ID, a.ID, b.ID}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestMoveTodo_MoveAcrossColumns(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo a: %v", err)
	}

	_, err = st.MoveTodo(ctx, a.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	if len(cols[DefaultColumnBacklog]) != 0 {
		t.Fatalf("expected backlog empty, got %v", ids(cols[DefaultColumnBacklog]))
	}
	if len(cols[DefaultColumnDoing]) != 1 || cols[DefaultColumnDoing][0].ID != a.ID {
		t.Fatalf("expected in progress [%d], got %v", a.ID, ids(cols[DefaultColumnDoing]))
	}
}

func TestMoveTodo_RebalanceWhenGapTooSmall(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo a: %v", err)
	}
	b, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "b", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo b: %v", err)
	}
	c, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "c", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo c: %v", err)
	}

	// Force a tiny gap between a and b.
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET rank=? WHERE id=?`, int64(1000), a.ID); err != nil {
		t.Fatalf("update rank a: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET rank=? WHERE id=?`, int64(1001), b.ID); err != nil {
		t.Fatalf("update rank b: %v", err)
	}

	_, err = st.MoveTodo(ctx, c.ID, DefaultColumnBacklog, &a.ID, &b.ID, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	got := ids(cols[DefaultColumnBacklog])
	// a should be before c before b; the remaining item (if any) comes after.
	if len(got) < 3 || got[0] != a.ID || got[1] != c.ID || got[2] != b.ID {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestMoveTodo_MoveToTopRepeatedly_NoNegativeRank(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnDoing}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo a: %v", err)
	}
	b, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "b", ColumnKey: DefaultColumnDoing}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo b: %v", err)
	}
	c, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "c", ColumnKey: DefaultColumnDoing}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo c: %v", err)
	}

	// Move c to the top (before a): rank = a.rank - rankStep = 1000 - 1000 = 0
	_, err = st.MoveTodo(ctx, c.ID, DefaultColumnDoing, nil, &a.ID, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo c to top: %v", err)
	}

	// Move b to the top (before c): this used to produce a negative rank
	_, err = st.MoveTodo(ctx, b.ID, DefaultColumnDoing, nil, &c.ID, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo b to top: %v", err)
	}

	// Verify all 3 items are returned by GetBoardPaged (the API path that was broken)
	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, meta, err := st.GetBoardPaged(ctx, &pc, "", "", SprintFilter{Mode: "none"}, 20)
	if err != nil {
		t.Fatalf("GetBoardPaged: %v", err)
	}

	got := ids(cols[DefaultColumnDoing])
	if len(got) != 3 {
		t.Fatalf("expected 3 items in IN_PROGRESS from GetBoardPaged, got %d: %v", len(got), got)
	}

	// Order should be b, c, a (most recently moved to top first)
	want := []int64{b.ID, c.ID, a.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("position %d: got id=%d want id=%d (full: %v)", i, got[i], want[i], got)
		}
	}

	// totalCount should also be 3
	if meta[DefaultColumnDoing].TotalCount != 3 {
		t.Fatalf("expected totalCount=3, got %d", meta[DefaultColumnDoing].TotalCount)
	}

	// Also verify via ListTodosForBoardLane with the initial cursor (math.MinInt64, 0)
	items, _, _, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnDoing, 20, math.MinInt64, 0, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items from ListTodosForBoardLane, got %d", len(items))
	}

	// Verify no rank is negative
	for _, item := range items {
		if item.Rank < 0 {
			t.Errorf("item %d (%s) has negative rank %d", item.ID, item.Title, item.Rank)
		}
	}
}

func TestMoveTodo_MoveToTopProducesPositiveRank(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	a, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "a", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo a: %v", err)
	}
	b, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "b", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo b: %v", err)
	}

	// Move b to top 10 times in a row (each time before the current first item)
	for i := 0; i < 10; i++ {
		first := a.ID
		if i%2 == 0 {
			first = a.ID
		} else {
			first = b.ID
		}
		other := b.ID
		if first == b.ID {
			other = a.ID
		}
		_, err = st.MoveTodo(ctx, other, DefaultColumnBacklog, nil, &first, ModeFull)
		if err != nil {
			t.Fatalf("MoveTodo iteration %d: %v", i, err)
		}
	}

	items, _, _, err := st.ListTodosForBoardLane(ctx, p.ID, DefaultColumnBacklog, 20, math.MinInt64, 0, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, item := range items {
		if item.Rank <= 0 {
			t.Errorf("item %d (%s) has non-positive rank %d", item.ID, item.Title, item.Rank)
		}
	}
}

func TestMoveTodo_FilteredVisibleSameColumn(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	m1 := mustCreateTodo(t, st, p.ID, "match a", DefaultColumnBacklog)
	_ = mustCreateTodo(t, st, p.ID, "other a", DefaultColumnBacklog)
	m2 := mustCreateTodo(t, st, p.ID, "match b", DefaultColumnBacklog)
	_ = mustCreateTodo(t, st, p.ID, "other b", DefaultColumnBacklog)
	m3 := mustCreateTodo(t, st, p.ID, "match c", DefaultColumnBacklog)

	if _, err := st.MoveTodo(ctx, m3.ID, DefaultColumnBacklog, &m1.ID, &m2.ID, ModeFull); err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	items, _ := mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 20)
	assertTodoIDs(t, ids(items), []int64{m1.ID, m3.ID, m2.ID})
}

func TestMoveTodo_FilteredTopBoundaryAcrossColumns(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	m1 := mustCreateTodo(t, st, p.ID, "match a", DefaultColumnBacklog)
	m2 := mustCreateTodo(t, st, p.ID, "match b", DefaultColumnBacklog)
	_ = mustCreateTodo(t, st, p.ID, "match c", DefaultColumnBacklog)
	x := mustCreateTodo(t, st, p.ID, "match moved", DefaultColumnDoing)

	if _, err := st.MoveTodo(ctx, x.ID, DefaultColumnBacklog, nil, &m1.ID, ModeFull); err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	items, _ := mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 3)
	assertTodoIDs(t, ids(items), []int64{x.ID, m1.ID, m2.ID})
}

func TestMoveTodo_FilteredBottomBoundaryWithinColumn(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	m1 := mustCreateTodo(t, st, p.ID, "match a", DefaultColumnBacklog)
	m2 := mustCreateTodo(t, st, p.ID, "match b", DefaultColumnBacklog)
	m3 := mustCreateTodo(t, st, p.ID, "match c", DefaultColumnBacklog)
	_ = mustCreateTodo(t, st, p.ID, "other", DefaultColumnBacklog)
	m4 := mustCreateTodo(t, st, p.ID, "match d", DefaultColumnBacklog)

	items, meta := mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 3)
	if got := ids(items); len(got) != 3 || got[0] != m1.ID || got[1] != m2.ID || got[2] != m3.ID {
		t.Fatalf("unexpected initial filtered page: %v", got)
	}
	if !meta.HasMore {
		t.Fatalf("expected hidden filtered items")
	}
	hidden := mustFirstHiddenLaneTodo(t, st, p.ID, DefaultColumnBacklog, "match", meta)
	if hidden.ID != m4.ID {
		t.Fatalf("expected hidden todo %d, got %d", m4.ID, hidden.ID)
	}

	if _, err := st.MoveTodo(ctx, m1.ID, DefaultColumnBacklog, &m3.ID, &hidden.ID, ModeFull); err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	items, _ = mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 3)
	assertTodoIDs(t, ids(items), []int64{m2.ID, m3.ID, m1.ID})
}

func TestMoveTodo_FilteredBottomBoundaryAcrossColumns(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	m1 := mustCreateTodo(t, st, p.ID, "match a", DefaultColumnBacklog)
	m2 := mustCreateTodo(t, st, p.ID, "match b", DefaultColumnBacklog)
	m3 := mustCreateTodo(t, st, p.ID, "match c", DefaultColumnBacklog)
	m4 := mustCreateTodo(t, st, p.ID, "match d", DefaultColumnBacklog)
	x := mustCreateTodo(t, st, p.ID, "match moved", DefaultColumnDoing)

	items, meta := mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 3)
	if got := ids(items); len(got) != 3 || got[0] != m1.ID || got[1] != m2.ID || got[2] != m3.ID {
		t.Fatalf("unexpected initial filtered page: %v", got)
	}
	if !meta.HasMore {
		t.Fatalf("expected hidden filtered items")
	}
	hidden := mustFirstHiddenLaneTodo(t, st, p.ID, DefaultColumnBacklog, "match", meta)
	if hidden.ID != m4.ID {
		t.Fatalf("expected hidden todo %d, got %d", m4.ID, hidden.ID)
	}

	if _, err := st.MoveTodo(ctx, x.ID, DefaultColumnBacklog, &m3.ID, &hidden.ID, ModeFull); err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	items, _ = mustFilteredLanePage(t, st, p.ID, DefaultColumnBacklog, "match", 4)
	assertTodoIDs(t, ids(items), []int64{m1.ID, m2.ID, m3.ID, x.ID})
}

func TestMoveTodo_FilteredMoveToEmptyTargetColumn(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	x := mustCreateTodo(t, st, p.ID, "match moved", DefaultColumnBacklog)
	_ = mustCreateTodo(t, st, p.ID, "other done", DefaultColumnDone)

	if _, err := st.MoveTodo(ctx, x.ID, DefaultColumnDone, nil, nil, ModeFull); err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	items, _ := mustFilteredLanePage(t, st, p.ID, DefaultColumnDone, "match", 20)
	got := ids(items)
	if len(got) != 1 || got[0] != x.ID {
		t.Fatalf("filtered target lane got %v want [%d]", got, x.ID)
	}
}
