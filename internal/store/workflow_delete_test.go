package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteLane_EmptyNonDoneLaneRemoved(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-delete-empty")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(ctx, project.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, added.Key); err != nil {
		t.Fatalf("DeleteWorkflowColumn: %v", err)
	}

	workflow, err := st.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	for _, col := range workflow {
		if col.Key == added.Key {
			t.Fatalf("expected lane %q to be deleted", added.Key)
		}
	}
}

func TestDeleteLane_NonEmptyLaneRejected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-delete-nonempty")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	added, err := st.AddWorkflowColumn(ctx, project.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn: %v", err)
	}
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Review me",
		ColumnKey: added.Key,
	}, ModeAnonymous); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, added.Key); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestDeleteLane_ArchivedReferenceStillRejected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-delete-archived")
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.AddWorkflowColumn(ctx, project.ID, "Archived Review")
	if err != nil {
		t.Fatal(err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "archived reference", ColumnKey: added.Key}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteWorkflowColumn(ctx, project.ID, added.Key); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete archived-referenced lane err=%v want conflict", err)
	}
}

func TestDeleteLane_DoneLaneRejected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-delete-done")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, DefaultColumnDone); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestDeleteLane_MinLanesEnforced(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProjectWithWorkflow(ctx, "workflow-delete-min", []WorkflowColumn{
		{Key: "todo_custom", Name: "Todo", Color: "#9CA3AF", Position: 0},
		{Key: "done_custom", Name: "Done", Color: "#EF4444", Position: 1, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, "todo_custom"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

func TestDeleteLane_PositionsReindexed(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-delete-positions")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	first, err := st.AddWorkflowColumn(ctx, project.ID, "Review")
	if err != nil {
		t.Fatalf("AddWorkflowColumn first: %v", err)
	}
	second, err := st.AddWorkflowColumn(ctx, project.ID, "QA")
	if err != nil {
		t.Fatalf("AddWorkflowColumn second: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, first.Key); err != nil {
		t.Fatalf("DeleteWorkflowColumn: %v", err)
	}

	workflow, err := st.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	want := []string{
		DefaultColumnBacklog,
		DefaultColumnNotStarted,
		DefaultColumnDoing,
		DefaultColumnTesting,
		second.Key,
		DefaultColumnDone,
	}
	if len(workflow) != len(want) {
		t.Fatalf("expected %d workflow columns, got %d", len(want), len(workflow))
	}
	for i, col := range workflow {
		if col.Key != want[i] {
			t.Fatalf("expected lane %d key %q, got %q", i, want[i], col.Key)
		}
		if col.Position != i {
			t.Fatalf("expected lane %q position %d, got %d", col.Key, i, col.Position)
		}
	}
}

func TestDeleteLane_PreservesSingleDoneLane(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProjectWithWorkflow(ctx, "workflow-delete-custom-done", []WorkflowColumn{
		{Key: "backlog_custom", Name: "Backlog", Color: "#9CA3AF", Position: 0},
		{Key: "review_custom", Name: "Review", Color: "#10B981", Position: 1},
		{Key: "shipped_custom", Name: "Shipped", Color: "#EF4444", Position: 2, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow: %v", err)
	}

	if err := st.DeleteWorkflowColumn(ctx, project.ID, "review_custom"); err != nil {
		t.Fatalf("DeleteWorkflowColumn: %v", err)
	}

	workflow, err := st.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	doneCount := 0
	for i, col := range workflow {
		if col.Position != i {
			t.Fatalf("expected lane %q position %d, got %d", col.Key, i, col.Position)
		}
		if col.IsDone {
			doneCount++
			if col.Key != "shipped_custom" {
				t.Fatalf("expected done lane key %q, got %q", "shipped_custom", col.Key)
			}
		}
	}
	if doneCount != 1 {
		t.Fatalf("expected exactly one done lane, got %d", doneCount)
	}
}
