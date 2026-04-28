package mcp

import (
	"encoding/json"
	"testing"

	"scrumboy/internal/store"
)

func TestBuildUpdateTodoInput_patchSprintId_assigns(t *testing.T) {
	t.Parallel()
	existing := store.Todo{
		Title:    "t",
		Body:     "b",
		Tags:     []string{},
		SprintID: nil,
	}
	patch := json.RawMessage(`{"sprintId":42}`)
	in, changed, aerr := buildUpdateTodoInput(existing, patch)
	if aerr != nil {
		t.Fatalf("buildUpdateTodoInput: %v", aerr)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if in.ClearSprint {
		t.Fatal("expected ClearSprint false when assigning a sprint")
	}
	if in.SprintID == nil || *in.SprintID != 42 {
		t.Fatalf("expected SprintID 42, got %#v", in.SprintID)
	}
}

func TestBuildUpdateTodoInput_patchSprintId_null_clears(t *testing.T) {
	t.Parallel()
	prev := int64(99)
	existing := store.Todo{
		Title:    "t",
		Body:     "b",
		Tags:     []string{},
		SprintID: &prev,
	}
	patch := json.RawMessage(`{"sprintId":null}`)
	in, changed, aerr := buildUpdateTodoInput(existing, patch)
	if aerr != nil {
		t.Fatalf("buildUpdateTodoInput: %v", aerr)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !in.ClearSprint {
		t.Fatal("expected ClearSprint true when patch.sprintId is null")
	}
}

func TestBuildUpdateTodoInput_patchSprintId_invalidJSON(t *testing.T) {
	t.Parallel()
	existing := store.Todo{Title: "t", Body: "b", Tags: []string{}}
	patch := json.RawMessage(`{"sprintId":"not-a-number"}`)
	_, _, aerr := buildUpdateTodoInput(existing, patch)
	if aerr == nil {
		t.Fatal("expected adapter error for invalid sprintId type")
	}
	if aerr.Code != CodeValidationError {
		t.Fatalf("expected validation error code, got %q", aerr.Code)
	}
}
