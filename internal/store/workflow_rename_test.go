package store

import (
	"context"
	"reflect"
	"testing"
)

func TestRenameLane_UpdatesNamePreservesKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-rename")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Keep key stable",
		ColumnKey: DefaultColumnDoing,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := st.UpdateWorkflowColumn(ctx, project.ID, DefaultColumnDoing, "Working", "#10B981"); err != nil {
		t.Fatalf("UpdateWorkflowColumn: %v", err)
	}

	workflow, err := st.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	var renamed *WorkflowColumn
	for i := range workflow {
		if workflow[i].Key == DefaultColumnDoing {
			renamed = &workflow[i]
			break
		}
	}
	if renamed == nil {
		t.Fatalf("expected workflow column %q", DefaultColumnDoing)
	}
	if renamed.Name != "Working" {
		t.Fatalf("expected renamed lane label %q, got %q", "Working", renamed.Name)
	}
	if renamed.Key != DefaultColumnDoing {
		t.Fatalf("expected lane key to remain %q, got %q", DefaultColumnDoing, renamed.Key)
	}
	if todo.ColumnKey != DefaultColumnDoing {
		t.Fatalf("expected todo column key to remain %q, got %q", DefaultColumnDoing, todo.ColumnKey)
	}

	pc, err := st.GetProjectContextForRead(ctx, project.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	_, _, _, cols, err := st.GetBoard(ctx, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("GetBoard: %v", err)
	}
	doing := cols[DefaultColumnDoing]
	if len(doing) != 1 || doing[0].ID != todo.ID {
		t.Fatalf("expected todo %d to remain in %q, got %+v", todo.ID, DefaultColumnDoing, doing)
	}
}

func TestRenameLane_BurndownUnaffected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "workflow-burndown")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Incomplete",
		ColumnKey: DefaultColumnDoing,
	}, ModeFull); err != nil {
		t.Fatalf("CreateTodo incomplete: %v", err)
	}
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:     "Completed",
		ColumnKey: DefaultColumnDone,
	}, ModeFull); err != nil {
		t.Fatalf("CreateTodo completed: %v", err)
	}

	before, err := st.GetBacklogSize(ctx, project.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetBacklogSize before rename: %v", err)
	}
	if err := st.UpdateWorkflowColumn(ctx, project.ID, DefaultColumnDone, "Shipped", "#EF4444"); err != nil {
		t.Fatalf("UpdateWorkflowColumn: %v", err)
	}
	after, err := st.GetBacklogSize(ctx, project.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetBacklogSize after rename: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("expected burndown to be unchanged after lane rename\nbefore=%+v\nafter=%+v", before, after)
	}
}
