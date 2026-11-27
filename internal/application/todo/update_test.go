package todo

import (
	"reflect"
	"testing"
	"time"

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
	priority := "high"
	existing := store.Todo{
		Title:            "existing title",
		Body:             "existing body",
		Tags:             []string{"backend", "urgent"},
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
		SprintID:         &sprint,
		PriorityKey:      &priority,
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
	if in.PriorityKey == nil || *in.PriorityKey != "high" || in.PriorityKey == existing.PriorityKey || in.PriorityKeyPresent {
		t.Fatalf("omitted priority materialization=%+v", in)
	}
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
		PriorityKey:      Field[*string]{Present: true, Value: updateTestString("urgent")},
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
	if !in.PriorityKeyPresent || in.PriorityKey == nil || *in.PriorityKey != "urgent" {
		t.Fatalf("priority set=%+v", in)
	}
	if in.ClearSprint {
		t.Fatal("explicit sprint set also requested sprint clearing")
	}
}

func TestMaterializeUpdateInputExplicitClear(t *testing.T) {
	patch := UpdatePatch{
		EstimationPoints: Field[*int64]{Present: true, Value: nil},
		AssigneeUserID:   Field[*int64]{Present: true, Value: nil},
		SprintID:         Field[*int64]{Present: true, Value: nil},
		PriorityKey:      Field[*string]{Present: true, Value: nil},
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
	if !in.PriorityKeyPresent || in.PriorityKey != nil {
		t.Fatalf("priority clear=%+v", in)
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
	if !(UpdatePatch{CreatedByUserID: Field[*int64]{Present: true}}).HasFields() {
		t.Fatal("explicitly invalid nil creator was treated as absent")
	}
	if (UpdatePatch{SprintID: Field[*int64]{}}).HasFields() {
		t.Fatal("omitted sprint reported as supplied")
	}
	if !(UpdatePatch{SprintID: Field[*int64]{Present: true, Value: existing.SprintID}}).HasFields() {
		t.Fatal("sprint explicitly set to its existing value was treated as absent")
	}
}

func TestMaterializeUpdateInputCreatorPresenceAndCopy(t *testing.T) {
	existingCreator := int64(17)
	existing := fullyPopulatedUpdateTodo()
	existing.CreatedByUserID = &existingCreator

	omitted := MaterializeUpdateInput(existing, UpdatePatch{})
	if omitted.CreatedByUserIDPresent || omitted.CreatedByUserID != nil {
		t.Fatalf("omitted creator materialized as a mutation: %+v", omitted)
	}

	creator := int64(29)
	patch := UpdatePatch{CreatedByUserID: Field[*int64]{Present: true, Value: &creator}}
	set := MaterializeUpdateInput(existing, patch)
	if !set.CreatedByUserIDPresent || set.CreatedByUserID == nil || *set.CreatedByUserID != 29 {
		t.Fatalf("explicit creator was not materialized: %+v", set)
	}
	if set.CreatedByUserID == patch.CreatedByUserID.Value {
		t.Fatal("materialized creator pointer aliases the patch")
	}
	creator = 41
	if *set.CreatedByUserID != 29 {
		t.Fatalf("materialized creator changed with patch source: %d", *set.CreatedByUserID)
	}

	invalid := MaterializeUpdateInput(existing, UpdatePatch{CreatedByUserID: Field[*int64]{Present: true}})
	if !invalid.CreatedByUserIDPresent || invalid.CreatedByUserID != nil {
		t.Fatalf("explicit nil creator was converted to omission: %+v", invalid)
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
	priority := "high"
	return store.Todo{
		Title:            "existing title",
		Body:             "existing body",
		Tags:             []string{"existing-tag"},
		EstimationPoints: &estimation,
		AssigneeUserID:   &assignee,
		SprintID:         &sprint,
		PriorityKey:      &priority,
	}
}

func updateTestInt64(value int64) *int64 {
	return &value
}

func updateTestString(value string) *string { return &value }

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

func TestMaterializeUpdateInputBodyAppend(t *testing.T) {
	existing := fullyPopulatedUpdateTodo()
	app := Field[string]{Present: true, Value: "more"}

	// The store joins the text inside its transaction; Materialize only carries it.
	got := MaterializeUpdateInput(existing, UpdatePatch{BodyAppend: app})
	if got.BodyAppend != "more" || got.BodyReplaces || got.Body != existing.Body {
		t.Fatalf("append only: %+v", got)
	}
	if !got.KeepTitle || !got.KeepBody || !got.KeepTags || !got.KeepEstimationPoints || !got.KeepAssigneeUserID {
		t.Fatalf("append-only preservation flags: %+v", got)
	}
	if got.SprintID != nil || got.ClearSprint {
		t.Fatalf("append-only sprint omission requested mutation: %+v", got)
	}
	if got.PriorityKeyPresent || got.PriorityKey == nil || *got.PriorityKey != *existing.PriorityKey {
		t.Fatalf("append-only priority omission lost presence semantics: %+v", got)
	}

	estimation := int64(8)
	assignee := int64(52)
	sprint := int64(11)
	priority := "urgent"
	both := UpdatePatch{
		Title:            Field[string]{Present: true, Value: "new title"},
		Body:             Field[string]{Present: true, Value: "new body"},
		BodyAppend:       app,
		Tags:             Field[[]string]{Present: true, Value: []string{"new-tag"}},
		EstimationPoints: Field[*int64]{Present: true, Value: &estimation},
		AssigneeUserID:   Field[*int64]{Present: true, Value: &assignee},
		SprintID:         Field[*int64]{Present: true, Value: &sprint},
		PriorityKey:      Field[*string]{Present: true, Value: &priority},
	}
	got = MaterializeUpdateInput(existing, both)
	if got.BodyAppend != "more" || !got.BodyReplaces || got.Body != "new body" {
		t.Fatalf("body then append: %+v", got)
	}
	if got.KeepTitle || got.KeepBody || got.KeepTags || got.KeepEstimationPoints || got.KeepAssigneeUserID {
		t.Fatalf("explicit replacements received preservation flags: %+v", got)
	}
	if got.Title != "new title" || !reflect.DeepEqual(got.Tags, []string{"new-tag"}) {
		t.Fatalf("explicit replacements were not retained: %+v", got)
	}
	assertUpdatePointerValue(t, "explicit estimation", got.EstimationPoints, estimation)
	assertUpdatePointerValue(t, "explicit assignee", got.AssigneeUserID, assignee)
	assertUpdatePointerValue(t, "explicit sprint", got.SprintID, sprint)
	if got.ClearSprint || !got.PriorityKeyPresent || got.PriorityKey == nil || *got.PriorityKey != priority {
		t.Fatalf("explicit sprint/priority presence semantics: %+v", got)
	}
	if got := MaterializeUpdateInput(existing, UpdatePatch{}); got.BodyAppend != "" || got.BodyReplaces {
		t.Fatalf("no append carried: %+v", got)
	}
	ignored := MaterializeUpdateInput(existing, UpdatePatch{
		Title:      Field[string]{Present: true, Value: "new title"},
		BodyAppend: Field[string]{Value: "must not append"},
	})
	if ignored.BodyAppend != "" {
		t.Fatalf("omitted append value was carried: %+v", ignored)
	}
	if !(UpdatePatch{BodyAppend: app}).HasFields() {
		t.Fatal("BodyAppend must count as a present field")
	}
}

func TestMaterializeUpdateInput_DueAt(t *testing.T) {
	existingDue := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
	existing := store.Todo{Title: "t", DueAt: &existingDue}

	// Omitted: preserved, and not marked present so the store leaves it alone.
	in := MaterializeUpdateInput(existing, UpdatePatch{Title: Field[string]{Present: true, Value: "x"}})
	if in.DueAtPresent {
		t.Fatal("omitted dueAt must not be marked present")
	}

	// Present nil: clear.
	in = MaterializeUpdateInput(existing, UpdatePatch{DueAt: Field[*time.Time]{Present: true}})
	if !in.DueAtPresent || in.DueAt != nil {
		t.Fatalf("clear: present=%v value=%v", in.DueAtPresent, in.DueAt)
	}

	// Present value: set, and the input is a copy, not an alias.
	next := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	in = MaterializeUpdateInput(existing, UpdatePatch{DueAt: Field[*time.Time]{Present: true, Value: &next}})
	if !in.DueAtPresent || in.DueAt == nil || !in.DueAt.Equal(next) {
		t.Fatalf("set: present=%v value=%v", in.DueAtPresent, in.DueAt)
	}
	if in.DueAt == &next {
		t.Fatal("materialized DueAt aliases the patch value")
	}
}

func TestUpdatePatchHasFields_DueAtAlone(t *testing.T) {
	if !(UpdatePatch{DueAt: Field[*time.Time]{Present: true}}).HasFields() {
		t.Fatal("dueAt-only patch must report present fields")
	}
}

func TestMaterializeCreateInput_DueAtIsCopied(t *testing.T) {
	due := time.Date(2026, 11, 1, 17, 0, 0, 0, time.UTC)
	in := MaterializeCreateInput(CreateCommand{Values: CreateValues{Title: "t", DueAt: &due}})
	if in.DueAt == nil || !in.DueAt.Equal(due) || in.DueAt == &due {
		t.Fatalf("DueAt = %v (aliased=%v)", in.DueAt, in.DueAt == &due)
	}
}
