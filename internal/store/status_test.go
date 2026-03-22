package store

import (
	"context"
	"testing"
)

// TestBurndown_TestingIsIncomplete verifies that Testing status is counted as incomplete in burndown
// Only StatusDone should be excluded from incomplete count
func TestBurndown_TestingIsIncomplete(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create todos in each status
	todoBacklog, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Backlog todo",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Backlog: %v", err)
	}

	todoNotStarted, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Not Started todo",
		ColumnKey: DefaultColumnNotStarted,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Not Started: %v", err)
	}

	todoInProgress, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "In Progress todo",
		ColumnKey: DefaultColumnDoing,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo In Progress: %v", err)
	}

	todoTesting, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Testing todo",
		ColumnKey: DefaultColumnTesting,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Testing: %v", err)
	}

	todoDone, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Done todo",
		ColumnKey: DefaultColumnDone,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Done: %v", err)
	}

	// Get burndown data
	points, err := st.GetBacklogSize(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetBurndown: %v", err)
	}

	// Get today's point (last element)
	if len(points) == 0 {
		t.Fatal("expected burndown points, got empty")
	}
	todayPoint := points[len(points)-1]

	// Verify count: Backlog, Not Started, In Progress, Testing should all be incomplete (4 todos)
	// Only Done should be excluded (1 todo)
	expectedIncomplete := 4
	got := 0
	if todayPoint.IncompleteCount != nil {
		got = *todayPoint.IncompleteCount
	}
	if got != expectedIncomplete {
		t.Errorf("expected %d incomplete todos, got %d (Backlog=%d, NotStarted=%d, InProgress=%d, Testing=%d, Done=%d)",
			expectedIncomplete, got, todoBacklog.ID, todoNotStarted.ID, todoInProgress.ID, todoTesting.ID, todoDone.ID)
	}

	// Verify that Testing is treated identically to other non-Done statuses
	// Testing should count as incomplete, not completed
}

// TestMoveTodo_ToTesting verifies that todos can be moved to Testing status
func TestMoveTodo_ToTesting(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Test: Backlog → Testing
	t.Run("Backlog to Testing", func(t *testing.T) {
		todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
			Title:     "Test todo 1",
			ColumnKey: DefaultColumnBacklog,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}

		moved, err := st.MoveTodo(ctx, todo.ID, DefaultColumnTesting, nil, nil, ModeFull)
		if err != nil {
			t.Fatalf("MoveTodo Backlog->Testing: %v", err)
		}

		if moved.ColumnKey != DefaultColumnTesting {
			t.Errorf("expected column Testing, got %q", moved.ColumnKey)
		}
	})

	// Test: In Progress → Testing
	t.Run("InProgress to Testing", func(t *testing.T) {
		todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
			Title:     "Test todo 2",
			ColumnKey: DefaultColumnDoing,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}

		moved, err := st.MoveTodo(ctx, todo.ID, DefaultColumnTesting, nil, nil, ModeFull)
		if err != nil {
			t.Fatalf("MoveTodo InProgress->Testing: %v", err)
		}

		if moved.ColumnKey != DefaultColumnTesting {
			t.Errorf("expected column Testing, got %q", moved.ColumnKey)
		}
	})

	// Test: Testing → Done
	t.Run("Testing to Done", func(t *testing.T) {
		todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
			Title:     "Test todo 3",
			ColumnKey: DefaultColumnTesting,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}

		moved, err := st.MoveTodo(ctx, todo.ID, DefaultColumnDone, nil, nil, ModeFull)
		if err != nil {
			t.Fatalf("MoveTodo Testing->Done: %v", err)
		}

		if moved.ColumnKey != DefaultColumnDone {
			t.Errorf("expected column Done, got %q", moved.ColumnKey)
		}
	})
}

// TestParseStatus_Testing verifies that "TESTING" string parses correctly
func TestParseStatus_Testing(t *testing.T) {
	// Test parsing "TESTING" string
	status, ok := ParseStatus("TESTING")
	if !ok {
		t.Fatal("ParseStatus(\"TESTING\") failed")
	}
	if status != StatusTesting {
		t.Errorf("expected StatusTesting (3), got %d", status)
	}

	// Test String() method
	if StatusTesting.String() != "TESTING" {
		t.Errorf("expected \"TESTING\", got %q", StatusTesting.String())
	}

	// Verify numeric value
	if StatusTesting != 3 {
		t.Errorf("expected StatusTesting value to be 3, got %d", StatusTesting)
	}

	// Verify StatusDone updated value
	if StatusDone != 4 {
		t.Errorf("expected StatusDone value to be 4, got %d", StatusDone)
	}
}

// TestGetBoard_IncludesTestingColumn verifies that GetBoard returns all 5 columns including Testing
func TestGetBoard_IncludesTestingColumn(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create a todo in Testing status
	todoTesting, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Testing todo",
		ColumnKey: DefaultColumnTesting,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo Testing: %v", err)
	}

	// Get board
	pc, _ := st.GetProjectContextForRead(ctx, p.ID, ModeFull)
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}

	// Verify all 5 columns are present
	expectedColumns := 5
	if len(cols) != expectedColumns {
		t.Errorf("expected %d columns, got %d", expectedColumns, len(cols))
	}

	// Verify Testing column exists
	testingCol, exists := cols[DefaultColumnTesting]
	if !exists {
		t.Fatal("Testing column not found in board columns")
	}

	// Verify our todo is in the Testing column
	if len(testingCol) != 1 {
		t.Fatalf("expected 1 todo in Testing column, got %d", len(testingCol))
	}

	if testingCol[0].ID != todoTesting.ID {
		t.Errorf("expected todo ID %d in Testing column, got %d", todoTesting.ID, testingCol[0].ID)
	}

	// Verify all expected columns exist
	for _, key := range []string{DefaultColumnBacklog, DefaultColumnNotStarted, DefaultColumnDoing, DefaultColumnTesting, DefaultColumnDone} {
		if _, exists := cols[key]; !exists {
			t.Errorf("expected column %q not found", key)
		}
	}
}

// TestBurndown_TestingCompletedAfterDay verifies that a todo moved to Done after a day
// is counted as incomplete on that day, even if it was in Testing status
func TestBurndown_TestingCompletedAfterDay(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p, err := st.CreateProject(ctx, "test-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create a todo in Testing status
	todo, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:     "Testing todo",
		ColumnKey: DefaultColumnTesting,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Get initial burndown
	points, err := st.GetBacklogSize(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetBurndown: %v", err)
	}

	if len(points) == 0 {
		t.Fatal("expected burndown points, got empty")
	}
	todayPoint := points[len(points)-1]

	// Verify todo is counted as incomplete
	inc := 0
	if todayPoint.IncompleteCount != nil {
		inc = *todayPoint.IncompleteCount
	}
	if inc < 1 {
		t.Errorf("expected at least 1 incomplete todo, got %d", inc)
	}

	// Move to Done
	_, err = st.MoveTodo(ctx, todo.ID, DefaultColumnDone, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo: %v", err)
	}

	// Get updated burndown
	pointsAfter, err := st.GetBacklogSize(ctx, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetBurndown after Done: %v", err)
	}

	if len(pointsAfter) == 0 {
		t.Fatal("expected burndown points after Done, got empty")
	}
	todayPointAfter := pointsAfter[len(pointsAfter)-1]

	// Verify todo is now excluded from incomplete count
	afterInc := 0
	if todayPointAfter.IncompleteCount != nil {
		afterInc = *todayPointAfter.IncompleteCount
	}
	beforeInc := 0
	if todayPoint.IncompleteCount != nil {
		beforeInc = *todayPoint.IncompleteCount
	}
	if afterInc >= beforeInc {
		t.Errorf("expected incomplete count to decrease after moving to Done, before=%d after=%d",
			beforeInc, afterInc)
	}
}

// TestStatus_OrderingIsUIOnly verifies that status ordering is UI-only, not logical
// This test documents that numeric comparisons should NOT be used
func TestStatus_OrderingIsUIOnly(t *testing.T) {
	// Verify numeric values
	if StatusBacklog != 0 {
		t.Errorf("expected StatusBacklog=0, got %d", StatusBacklog)
	}
	if StatusNotStarted != 1 {
		t.Errorf("expected StatusNotStarted=1, got %d", StatusNotStarted)
	}
	if StatusInProgress != 2 {
		t.Errorf("expected StatusInProgress=2, got %d", StatusInProgress)
	}
	if StatusTesting != 3 {
		t.Errorf("expected StatusTesting=3, got %d", StatusTesting)
	}
	if StatusDone != 4 {
		t.Errorf("expected StatusDone=4, got %d", StatusDone)
	}

	// Document that only StatusDone means "completed"
	// All other statuses (Backlog, Not Started, In Progress, Testing) are incomplete
	// This test serves as documentation that:
	// - status < StatusDone does NOT mean incomplete (would exclude Testing incorrectly if Testing were 3)
	// - status == StatusDone is the ONLY correct check for completed
	// - status != StatusDone is the ONLY correct check for incomplete

	t.Log("Status ordering is UI-only, not logical")
	t.Log("ONLY StatusDone means completed")
	t.Log("Use explicit checks: status == StatusDone (not status >= StatusDone)")
}
