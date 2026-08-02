package todo

import (
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ UpdateStore = (*store.Store)(nil)

func TestFieldPreservesPresenceIndependentlyFromValue(t *testing.T) {
	var omitted Field[string]
	if omitted.Present {
		t.Fatal("zero-value field should be omitted")
	}

	empty := Field[string]{Present: true, Value: ""}
	if !empty.Present || empty.Value != "" {
		t.Fatalf("explicit empty value should be present: %+v", empty)
	}

	clear := Field[*int64]{Present: true, Value: nil}
	if !clear.Present || clear.Value != nil {
		t.Fatalf("explicit nil should be present and clear: %+v", clear)
	}

	setValue := updateTestInt64(13)
	set := Field[*int64]{Present: true, Value: setValue}
	if !set.Present || set.Value == nil || *set.Value != 13 {
		t.Fatalf("nonnil pointer should be present and set: %+v", set)
	}
}

func TestMaterializeUpdateInputCompleteOmission(t *testing.T) {
	estimation := int64(5)
	assignee := int64(41)
	sprint := int64(7)
	existing := store.Todo{
		Title:            "existing title",
		Body:             "existing body",
		Tags:             []string{"backend", "urgent"},
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
		SprintID:         &sprint,
	}

	in := MaterializeUpdateInput(existing, UpdatePatch{})

	if in.Title != existing.Title || in.Body != existing.Body {
		t.Fatalf("omitted scalar replacements were not preserved: %+v", in)
	}
	if !reflect.DeepEqual(in.Tags, existing.Tags) {
		t.Fatalf("omitted tags = %#v, want %#v", in.Tags, existing.Tags)
	}
	if len(in.Tags) > 0 && &in.Tags[0] == &existing.Tags[0] {
		t.Fatal("omitted tags share backing storage with the existing todo")
	}
	assertCopiedUpdatePointer(t, "estimation", in.EstimationPoints, existing.EstimationPoints, estimation)
	assertCopiedUpdatePointer(t, "assignee", in.AssigneeUserID, existing.AssigneeUserID, assignee)
	if in.SprintID != nil || in.ClearSprint {
		t.Fatalf("omitted sprint requested a mutation: SprintID=%v ClearSprint=%v", in.SprintID, in.ClearSprint)
	}
}

func TestMaterializeUpdateInputExplicitReplacement(t *testing.T) {
	estimation := int64(8)
	assignee := int64(52)
	sprint := int64(11)
	patch := UpdatePatch{
		Title:            Field[string]{Present: true, Value: ""},
		Body:             Field[string]{Present: true, Value: ""},
		Tags:             Field[[]string]{Present: true, Value: []string{}},
		EstimationPoints: Field[*int64]{Present: true, Value: &estimation},
		AssigneeUserID:   Field[*int64]{Present: true, Value: &assignee},
		SprintID:         Field[*int64]{Present: true, Value: &sprint},
	}
	existing := fullyPopulatedUpdateTodo()

	in := MaterializeUpdateInput(existing, patch)

	if in.Title != "" || in.Body != "" {
		t.Fatalf("explicit empty strings were not retained: %+v", in)
	}
	if in.Tags == nil || len(in.Tags) != 0 {
		t.Fatalf("explicit empty tags = %#v, want nonnil empty slice", in.Tags)
	}
	assertUpdatePointerValue(t, "estimation", in.EstimationPoints, estimation)
	assertUpdatePointerValue(t, "assignee", in.AssigneeUserID, assignee)
	assertUpdatePointerValue(t, "sprint", in.SprintID, sprint)
	if in.ClearSprint {
		t.Fatal("explicit sprint set also requested sprint clearing")
	}
}

func TestMaterializeUpdateInputExplicitClear(t *testing.T) {
	patch := UpdatePatch{
		EstimationPoints: Field[*int64]{Present: true, Value: nil},
		AssigneeUserID:   Field[*int64]{Present: true, Value: nil},
		SprintID:         Field[*int64]{Present: true, Value: nil},
	}

	in := MaterializeUpdateInput(fullyPopulatedUpdateTodo(), patch)
	if in.EstimationPoints != nil {
		t.Fatalf("estimation clear produced %v", *in.EstimationPoints)
	}
	if in.AssigneeUserID != nil {
		t.Fatalf("assignee clear produced %v", *in.AssigneeUserID)
	}
	if in.SprintID != nil || !in.ClearSprint {
		t.Fatalf("sprint clear = SprintID %v, ClearSprint %v", in.SprintID, in.ClearSprint)
	}

	omitted := MaterializeUpdateInput(fullyPopulatedUpdateTodo(), UpdatePatch{})
	if omitted.SprintID != nil || omitted.ClearSprint {
		t.Fatalf("sprint omission = SprintID %v, ClearSprint %v", omitted.SprintID, omitted.ClearSprint)
	}
}

func TestMaterializeUpdateInputDoesNotAliasSources(t *testing.T) {
	existing := fullyPopulatedUpdateTodo()
	omitted := MaterializeUpdateInput(existing, UpdatePatch{})
	existing.Tags[0] = "mutated existing tag"
	*existing.EstimationPoints = 101
	*existing.AssigneeUserID = 102
	if omitted.Tags[0] != "existing-tag" || *omitted.EstimationPoints != 3 || *omitted.AssigneeUserID != 21 {
		t.Fatalf("materialized omission changed after existing todo mutation: %+v", omitted)
	}

	estimation := int64(6)
	assignee := int64(31)
	sprint := int64(9)
	patchTags := []string{"patch-tag"}
	patch := UpdatePatch{
		Tags:             Field[[]string]{Present: true, Value: patchTags},
		EstimationPoints: Field[*int64]{Present: true, Value: &estimation},
		AssigneeUserID:   Field[*int64]{Present: true, Value: &assignee},
		SprintID:         Field[*int64]{Present: true, Value: &sprint},
	}
	set := MaterializeUpdateInput(fullyPopulatedUpdateTodo(), patch)
	patchTags[0] = "mutated patch tag"
	estimation = 201
	assignee = 202
	sprint = 203
	if set.Tags[0] != "patch-tag" || *set.EstimationPoints != 6 || *set.AssigneeUserID != 31 || *set.SprintID != 9 {
		t.Fatalf("materialized replacement changed after patch mutation: %+v", set)
	}
}

func TestUpdatePatchHasFieldsIsSyntactic(t *testing.T) {
	existing := fullyPopulatedUpdateTodo()
	if (UpdatePatch{}).HasFields() {
		t.Fatal("empty patch reported supplied fields")
	}
	if !(UpdatePatch{Title: Field[string]{Present: true, Value: existing.Title}}).HasFields() {
		t.Fatal("title explicitly set to its existing value was treated as absent")
	}
	if !(UpdatePatch{AssigneeUserID: Field[*int64]{Present: true, Value: existing.AssigneeUserID}}).HasFields() {
		t.Fatal("assignee explicitly set to its existing value was treated as absent")
	}
	if (UpdatePatch{SprintID: Field[*int64]{}}).HasFields() {
		t.Fatal("omitted sprint reported as supplied")
	}
	if !(UpdatePatch{SprintID: Field[*int64]{Present: true, Value: existing.SprintID}}).HasFields() {
		t.Fatal("sprint explicitly set to its existing value was treated as absent")
	}
}

func TestMaterializeUpdateInputSupportsRESTReplacementAndMCPSparsePatches(t *testing.T) {
	existing := fullyPopulatedUpdateTodo()
	restEstimation := int64(13)
	restAssignee := int64(34)
	restPatch := UpdatePatch{
		Title:            Field[string]{Present: true, Value: "REST title"},
		Body:             Field[string]{Present: true, Value: "REST body"},
		Tags:             Field[[]string]{Present: true, Value: []string{"rest"}},
		EstimationPoints: Field[*int64]{Present: true, Value: &restEstimation},
		AssigneeUserID:   Field[*int64]{Present: true, Value: &restAssignee},
	}
	restInput := MaterializeUpdateInput(existing, restPatch)
	if restInput.Title != "REST title" || restInput.Body != "REST body" || !reflect.DeepEqual(restInput.Tags, []string{"rest"}) {
		t.Fatalf("REST replacement patch did not replace scalar fields: %+v", restInput)
	}
	assertUpdatePointerValue(t, "REST estimation", restInput.EstimationPoints, restEstimation)
	assertUpdatePointerValue(t, "REST assignee", restInput.AssigneeUserID, restAssignee)
	if restInput.SprintID != nil || restInput.ClearSprint {
		t.Fatalf("REST sprint omission requested mutation: %+v", restInput)
	}

	mcpPatch := UpdatePatch{Body: Field[string]{Present: true, Value: "MCP body"}}
	mcpInput := MaterializeUpdateInput(existing, mcpPatch)
	if mcpInput.Title != existing.Title || mcpInput.Body != "MCP body" || !reflect.DeepEqual(mcpInput.Tags, existing.Tags) {
		t.Fatalf("MCP sparse patch did not preserve omitted replacements: %+v", mcpInput)
	}
	assertUpdatePointerValue(t, "MCP estimation", mcpInput.EstimationPoints, *existing.EstimationPoints)
	assertUpdatePointerValue(t, "MCP assignee", mcpInput.AssigneeUserID, *existing.AssigneeUserID)
	if mcpInput.SprintID != nil || mcpInput.ClearSprint {
		t.Fatalf("MCP sprint omission requested mutation: %+v", mcpInput)
	}
}

func TestUpdateCommandAndResultCarryApplicationDomainValues(t *testing.T) {
	command := UpdateCommand{LocalID: 17, Patch: UpdatePatch{Title: Field[string]{Present: true, Value: "updated"}}}
	if command.LocalID != 17 || !command.Patch.Title.Present || command.Patch.Title.Value != "updated" {
		t.Fatalf("unexpected command: %+v", command)
	}

	result := UpdateResult{Project: store.Project{ID: 4}, Todo: store.Todo{LocalID: 17}}
	if result.Project.ID != 4 || result.Todo.LocalID != 17 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func fullyPopulatedUpdateTodo() store.Todo {
	estimation := int64(3)
	assignee := int64(21)
	sprint := int64(5)
	return store.Todo{
		Title:            "existing title",
		Body:             "existing body",
		Tags:             []string{"existing-tag"},
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
		SprintID:         &sprint,
	}
}

func updateTestInt64(value int64) *int64 {
	return &value
}

func assertCopiedUpdatePointer(t *testing.T, name string, got, source *int64, want int64) {
	t.Helper()
	assertUpdatePointerValue(t, name, got, want)
	if got == source {
		t.Fatalf("%s pointer aliases its source", name)
	}
}

func assertUpdatePointerValue(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s pointer is nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}
