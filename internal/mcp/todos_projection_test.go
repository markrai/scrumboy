package mcp

import (
	"testing"

	"scrumboy/internal/store"
)

func TestTodoToItemForProjectSuppressesDormantSprintAssignment(t *testing.T) {
	sprintID := int64(91)
	todo := store.Todo{LocalID: 7, SprintID: &sprintID}

	disabled := todoToItemForProject("alpha", store.Project{SprintsEnabled: false}, todo)
	if disabled.SprintId != nil {
		t.Fatalf("disabled projection sprintId = %v, want nil", disabled.SprintId)
	}
	if todo.SprintID == nil || *todo.SprintID != sprintID {
		t.Fatalf("projection mutated stored value copy: %+v", todo)
	}

	enabled := todoToItemForProject("alpha", store.Project{SprintsEnabled: true}, todo)
	if enabled.SprintId == nil || *enabled.SprintId != sprintID {
		t.Fatalf("enabled projection sprintId = %v, want %d", enabled.SprintId, sprintID)
	}
}
