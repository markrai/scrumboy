package todo

import (
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ CreateStore = (*store.Store)(nil)

func TestMaterializeCreateInputMapsResolvedCommand(t *testing.T) {
	estimation := int64(8)
	assignee := int64(31)
	sprint := int64(12)
	afterTodoID := int64(104)
	beforeTodoID := int64(209)
	command := CreateCommand{
		Values: CreateValues{
			Title:            "create title",
			Body:             "create body",
			Tags:             []string{"backend", "urgent"},
			ColumnKey:        "doing",
			EstimationPoints: &estimation,
			AssigneeUserID:   &assignee,
			SprintID:         &sprint,
		},
		Position: ResolvedCreatePosition{
			AfterTodoID:  &afterTodoID,
			BeforeTodoID: &beforeTodoID,
		},
	}

	in := MaterializeCreateInput(command)

	if in.Title != command.Values.Title || in.Body != command.Values.Body || in.ColumnKey != command.Values.ColumnKey {
		t.Fatalf("scalar values were not mapped exactly: %+v", in)
	}
	if !reflect.DeepEqual(in.Tags, command.Values.Tags) {
		t.Fatalf("tags = %#v, want %#v", in.Tags, command.Values.Tags)
	}
	assertCopiedCreatePointer(t, "estimation", in.EstimationPoints, command.Values.EstimationPoints, estimation)
	assertCopiedCreatePointer(t, "assignee", in.AssigneeUserID, command.Values.AssigneeUserID, assignee)
	assertCopiedCreatePointer(t, "sprint", in.SprintID, command.Values.SprintID, sprint)
	assertCopiedCreatePointer(t, "after todo ID", in.AfterID, command.Position.AfterTodoID, afterTodoID)
	assertCopiedCreatePointer(t, "before todo ID", in.BeforeID, command.Position.BeforeTodoID, beforeTodoID)
}

func TestMaterializeCreateInputPreservesNilOptionalValues(t *testing.T) {
	in := MaterializeCreateInput(CreateCommand{})

	if in.Tags != nil {
		t.Fatalf("nil tags became %#v", in.Tags)
	}
	if in.EstimationPoints != nil || in.AssigneeUserID != nil || in.SprintID != nil || in.AfterID != nil || in.BeforeID != nil {
		t.Fatalf("nil optional values were not preserved: %+v", in)
	}
}

func TestMaterializeCreateInputPreservesNonnilEmptyTags(t *testing.T) {
	tags := []string{}
	in := MaterializeCreateInput(CreateCommand{Values: CreateValues{Tags: tags}})

	if in.Tags == nil || len(in.Tags) != 0 {
		t.Fatalf("empty tags = %#v, want nonnil empty slice", in.Tags)
	}
}

func TestMaterializeCreateInputDoesNotNormalizeValues(t *testing.T) {
	estimation := int64(-99)
	command := CreateCommand{Values: CreateValues{
		Title:            "  untrimmed title  ",
		Body:             "  untrimmed body  ",
		Tags:             []string{" Alpha ", "alpha", "Alpha"},
		ColumnKey:        "MiXeD_Lane",
		EstimationPoints: &estimation,
	}}

	in := MaterializeCreateInput(command)

	if in.Title != command.Values.Title || in.Body != command.Values.Body || in.ColumnKey != command.Values.ColumnKey {
		t.Fatalf("materializer normalized scalar values: %+v", in)
	}
	if !reflect.DeepEqual(in.Tags, command.Values.Tags) {
		t.Fatalf("materializer normalized tags: got %#v, want %#v", in.Tags, command.Values.Tags)
	}
	assertCreatePointerValue(t, "estimation", in.EstimationPoints, estimation)
}

func TestMaterializeCreateInputDoesNotAliasSourcesOrResult(t *testing.T) {
	estimation := int64(5)
	assignee := int64(17)
	sprint := int64(9)
	afterTodoID := int64(301)
	beforeTodoID := int64(405)
	tags := []string{"source-tag"}
	command := CreateCommand{
		Values: CreateValues{
			Tags:             tags,
			EstimationPoints: &estimation,
			AssigneeUserID:   &assignee,
			SprintID:         &sprint,
		},
		Position: ResolvedCreatePosition{
			AfterTodoID:  &afterTodoID,
			BeforeTodoID: &beforeTodoID,
		},
	}
	in := MaterializeCreateInput(command)

	tags[0] = "mutated source tag"
	estimation = 105
	assignee = 117
	sprint = 109
	afterTodoID = 1301
	beforeTodoID = 1405
	if in.Tags[0] != "source-tag" || *in.EstimationPoints != 5 || *in.AssigneeUserID != 17 || *in.SprintID != 9 || *in.AfterID != 301 || *in.BeforeID != 405 {
		t.Fatalf("materialized input changed after source mutation: %+v", in)
	}

	secondEstimation := int64(6)
	secondAssignee := int64(18)
	secondSprint := int64(10)
	secondAfter := int64(501)
	secondBefore := int64(605)
	second := CreateCommand{
		Values: CreateValues{
			Tags:             []string{"second-source-tag"},
			EstimationPoints: &secondEstimation,
			AssigneeUserID:   &secondAssignee,
			SprintID:         &secondSprint,
		},
		Position: ResolvedCreatePosition{
			AfterTodoID:  &secondAfter,
			BeforeTodoID: &secondBefore,
		},
	}
	secondInput := MaterializeCreateInput(second)
	secondInput.Tags[0] = "mutated result tag"
	*secondInput.EstimationPoints = 206
	*secondInput.AssigneeUserID = 218
	*secondInput.SprintID = 210
	*secondInput.AfterID = 1501
	*secondInput.BeforeID = 1605
	if second.Values.Tags[0] != "second-source-tag" || secondEstimation != 6 || secondAssignee != 18 || secondSprint != 10 || secondAfter != 501 || secondBefore != 605 {
		t.Fatalf("command changed after materialized result mutation: %+v", second)
	}
}

func TestCreateCommandsKeepResolvedAndLocalPositionIdentitiesSeparate(t *testing.T) {
	afterLocalID := int64(7)
	beforeLocalID := int64(8)
	mcpCommand := MCPCreateCommand{
		Values:        CreateValues{Title: "MCP create"},
		AfterLocalID:  &afterLocalID,
		BeforeLocalID: &beforeLocalID,
	}
	if mcpCommand.AfterLocalID == nil || *mcpCommand.AfterLocalID != 7 || mcpCommand.BeforeLocalID == nil || *mcpCommand.BeforeLocalID != 8 {
		t.Fatalf("MCP command lost project-local position identities: %+v", mcpCommand)
	}

	// Same numeric values on purpose: separation is structural (distinct command
	// types / field names), not "local IDs happen to differ from store IDs".
	afterTodoID := int64(7)
	beforeTodoID := int64(8)
	resolved := CreateCommand{
		Values: mcpCommand.Values,
		Position: ResolvedCreatePosition{
			AfterTodoID:  &afterTodoID,
			BeforeTodoID: &beforeTodoID,
		},
	}
	if _, ok := any(mcpCommand).(CreateCommand); ok {
		t.Fatal("MCPCreateCommand must not be assignable to CreateCommand")
	}
	if _, ok := any(resolved).(MCPCreateCommand); ok {
		t.Fatal("CreateCommand must not be assignable to MCPCreateCommand")
	}

	in := MaterializeCreateInput(resolved)
	if in.AfterID == nil || *in.AfterID != 7 || in.BeforeID == nil || *in.BeforeID != 8 {
		t.Fatalf("resolved internal identities were not materialized: %+v", in)
	}
}

func TestCreateResultCarriesApplicationDomainValues(t *testing.T) {
	result := CreateResult{
		Project: store.Project{ID: 14, Slug: "create-project"},
		Todo:    store.Todo{ID: 82, LocalID: 3, ProjectID: 14},
	}

	if result.Project.ID != 14 || result.Project.Slug != "create-project" || result.Todo.ID != 82 || result.Todo.LocalID != 3 || result.Todo.ProjectID != 14 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func assertCopiedCreatePointer(t *testing.T, name string, got, source *int64, want int64) {
	t.Helper()
	assertCreatePointerValue(t, name, got, want)
	if got == source {
		t.Fatalf("%s pointer aliases its source", name)
	}
}

func assertCreatePointerValue(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s pointer is nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}
