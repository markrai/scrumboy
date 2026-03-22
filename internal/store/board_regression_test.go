package store

import (
	"context"
	"testing"
	"time"
)

// TestGetBoard_NoHang is a regression test for the listTagCounts hang issue.
// The hang was caused by using OR over LEFT JOINs with GROUP BY in SQLite.
// This test ensures GetBoard completes without timeout.
func TestGetBoard_NoHang(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Create a project with some todos and tags
	p, err := st.CreateProject(ctx, "Test Board")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create a few todos
	for i := 0; i < 5; i++ {
		_, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
			Title:  "Todo " + string(rune('A'+i)),
			Body:   "Test body",
			Tags:   []string{"tag1", "tag2"},
			ColumnKey: DefaultColumnBacklog,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo %d: %v", i, err)
		}
	}

	// GetBoard should complete within a reasonable time (not hang indefinitely)
	done := make(chan struct{})
	var boardErr error
	go func() {
		defer close(done)
		pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
		_, _, _, _, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
		boardErr = err
	}()

	select {
	case <-done:
		if boardErr != nil {
			t.Fatalf("GetBoard failed: %v", boardErr)
		}
		// Success - GetBoard completed
	case <-time.After(5 * time.Second):
		t.Fatal("GetBoard hung for >5 seconds (regression: listTagCounts query hanging)")
	}
}
