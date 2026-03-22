package store

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

func TestTodoLocalID_ConcurrentCreatesAreUniqueAndContiguous(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Sanity: migration/backfill should guarantee no NULL local_id.
	var nullCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE local_id IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count null local_id: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("expected 0 todos with NULL local_id, got %d", nullCount)
	}

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)

	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
				Title:  "t",
				Body:   "",
				Tags:   nil,
				ColumnKey: DefaultColumnBacklog,
			}, ModeFull)
			if err != nil {
				errCh <- err
				return
			}
			_ = i
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("CreateTodo error: %v", err)
	}

	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	all := append([]Todo{}, cols[DefaultColumnBacklog]...)

	if len(all) != n {
		t.Fatalf("expected %d todos, got %d", n, len(all))
	}

	seen := make(map[int64]bool, n)
	var min, max int64
	for idx, td := range all {
		if td.LocalID <= 0 {
			t.Fatalf("todo LocalID must be >0, got %d", td.LocalID)
		}
		if seen[td.LocalID] {
			t.Fatalf("duplicate localId=%d", td.LocalID)
		}
		seen[td.LocalID] = true
		if idx == 0 || td.LocalID < min {
			min = td.LocalID
		}
		if idx == 0 || td.LocalID > max {
			max = td.LocalID
		}
	}
	if min != 1 || max != n {
		t.Fatalf("expected localId range 1..%d, got %d..%d", n, min, max)
	}
	for i := int64(1); i <= n; i++ {
		if !seen[i] {
			t.Fatalf("missing localId=%d", i)
		}
	}

	// Extra safety: ensure UNIQUE(project_id, local_id) is enforced.
	var dupCount int
	if err := st.db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM (
  SELECT project_id, local_id, COUNT(*) AS c
  FROM todos
  WHERE project_id = ? AND local_id IS NOT NULL
  GROUP BY project_id, local_id
  HAVING c > 1
)`, p.ID).Scan(&dupCount); err != nil && err != sql.ErrNoRows {
		t.Fatalf("check dup local_id: %v", err)
	}
	if dupCount != 0 {
		t.Fatalf("expected no duplicate local_id rows, got %d duplicates", dupCount)
	}
}

