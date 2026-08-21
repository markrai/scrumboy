package tag

import (
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var (
	_ MineTagReadStore              = (*store.Store)(nil)
	_ ProjectTagReadStore           = (*store.Store)(nil)
	_ ProjectScopedTagReadStore     = (*store.Store)(nil)
	_ BoardScopedTagNameReadStore   = (*store.Store)(nil)
	_ PersonalTagNameReadStore      = (*store.Store)(nil)
	_ MineColorStore                = (*store.Store)(nil)
	_ LegacyRowColorStore           = (*store.Store)(nil)
	_ DurableProjectIDColorStore    = (*store.Store)(nil)
	_ TemporaryBoardIDColorStore    = (*store.Store)(nil)
	_ DurableProjectNameColorStore  = (*store.Store)(nil)
	_ TemporaryBoardNameColorStore  = (*store.Store)(nil)
	_ MineIDDeletionStore           = (*store.Store)(nil)
	_ MineNameDeletionStore         = (*store.Store)(nil)
	_ DurableProjectIDDeletionStore = (*store.Store)(nil)
	_ LegacyRowDeletionStore        = (*store.Store)(nil)
)

func TestColorIntentPreservesPreparedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   *string
		isClear bool
		want    *string
	}{
		{name: "nil clear", isClear: true},
		{name: "valid color", value: stringPointer("#12aBcF"), want: stringPointer("#12aBcF")},
		{name: "surrounding whitespace", value: stringPointer("  #12aBcF  "), want: stringPointer("  #12aBcF  ")},
		{name: "empty string", value: stringPointer(""), want: stringPointer("")},
		{name: "whitespace only", value: stringPointer(" \t "), want: stringPointer(" \t ")},
		{name: "malformed color", value: stringPointer("not-a-color"), want: stringPointer("not-a-color")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			intent := NewColorIntent(tt.value)
			if got := intent.IsClear(); got != tt.isClear {
				t.Fatalf("IsClear() = %v, want %v", got, tt.isClear)
			}
			if got := intent.StoreValue(); !equalStringPointers(got, tt.want) {
				t.Fatalf("StoreValue() = %s, want %s", describeStringPointer(got), describeStringPointer(tt.want))
			}
		})
	}
}

func TestColorIntentOwnsAndReturnsCopies(t *testing.T) {
	t.Parallel()

	source := "  #12aBcF  "
	intent := NewColorIntent(&source)
	source = "changed after construction"

	first := intent.StoreValue()
	second := intent.StoreValue()
	if first == nil || second == nil {
		t.Fatal("StoreValue() returned nil for a supplied color")
	}
	if first == second {
		t.Fatal("StoreValue() returned the same pointer twice")
	}
	if got, want := *first, "  #12aBcF  "; got != want {
		t.Fatalf("first StoreValue() = %q, want %q", got, want)
	}
	if got, want := *second, "  #12aBcF  "; got != want {
		t.Fatalf("second StoreValue() = %q, want %q", got, want)
	}

	*first = "mutated by store"
	if got, want := *intent.StoreValue(), "  #12aBcF  "; got != want {
		t.Fatalf("StoreValue() after caller mutation = %q, want %q", got, want)
	}

	clear := NewColorIntent(nil)
	if got := clear.StoreValue(); got != nil {
		t.Fatalf("clear StoreValue() = %q, want nil", *got)
	}
}

func TestDeletionResultOwnsAffectedProjectIDs(t *testing.T) {
	t.Parallel()

	input := []int64{7, 7, 3}
	result := NewDeletionResult(input)
	input[0] = 99

	first := result.AffectedProjectIDs()
	want := []int64{7, 7, 3}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("AffectedProjectIDs() = %v, want %v", first, want)
	}

	first[1] = 88
	if got := result.AffectedProjectIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("AffectedProjectIDs() after caller mutation = %v, want %v", got, want)
	}
}

func TestProjectKindsAreDistinct(t *testing.T) {
	t.Parallel()

	kinds := []ProjectKind{
		DurableProject,
		CreatorOwnedTemporaryBoard,
		AnonymousTemporaryBoard,
	}
	seen := make(map[ProjectKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if kind == 0 {
			t.Fatal("declared ProjectKind must not use the zero value")
		}
		if _, exists := seen[kind]; exists {
			t.Fatalf("duplicate ProjectKind value %d", kind)
		}
		seen[kind] = struct{}{}
	}
}

func TestCommandsPreservePreparedIdentityValues(t *testing.T) {
	t.Parallel()

	actorUserID := int64(-3)
	viewerUserID := int64(0)
	project := ResolvedProject{ProjectID: -4, Kind: ProjectKind(99)}
	color := NewColorIntent(stringPointer(" "))

	mineColor := MineIDColorCommand{
		ActorUserID: actorUserID,
		TagID:       -9,
		Color:       color,
	}
	if mineColor.ActorUserID != actorUserID || mineColor.TagID != -9 {
		t.Fatalf("MineIDColorCommand changed prepared identity values: %#v", mineColor)
	}
	assertColorValue(t, mineColor.Color, " ")

	projectIDColor := ProjectIDColorCommand{
		Project:      project,
		ViewerUserID: &viewerUserID,
		TagID:        -9,
		Color:        color,
	}
	if projectIDColor.Project != project || projectIDColor.ViewerUserID == nil || *projectIDColor.ViewerUserID != viewerUserID || projectIDColor.TagID != -9 {
		t.Fatalf("ProjectIDColorCommand changed prepared identity values: %#v", projectIDColor)
	}
	assertColorValue(t, projectIDColor.Color, " ")

	projectNameColor := ProjectNameColorCommand{
		Project:      project,
		ViewerUserID: nil,
		Name:         "",
		Color:        color,
	}
	if projectNameColor.Project != project || projectNameColor.ViewerUserID != nil || projectNameColor.Name != "" {
		t.Fatalf("ProjectNameColorCommand changed prepared identity values: %#v", projectNameColor)
	}
	assertColorValue(t, projectNameColor.Color, " ")

	mineDelete := MineIDDeleteCommand{ActorUserID: actorUserID, TagID: -9}
	if mineDelete.ActorUserID != actorUserID || mineDelete.TagID != -9 {
		t.Fatalf("MineIDDeleteCommand changed prepared identity values: %#v", mineDelete)
	}

	projectIDDelete := ProjectIDDeleteCommand{
		Project:     project,
		ActorUserID: &actorUserID,
		TagID:       -9,
	}
	if projectIDDelete.Project != project || projectIDDelete.ActorUserID == nil || *projectIDDelete.ActorUserID != actorUserID || projectIDDelete.TagID != -9 {
		t.Fatalf("ProjectIDDeleteCommand changed prepared identity values: %#v", projectIDDelete)
	}

	projectNameDelete := ProjectNameDeleteCommand{
		Project:     project,
		ActorUserID: nil,
		Name:        "",
	}
	if projectNameDelete.Project != project || projectNameDelete.ActorUserID != nil || projectNameDelete.Name != "" {
		t.Fatalf("ProjectNameDeleteCommand changed prepared identity values: %#v", projectNameDelete)
	}
}

func assertColorValue(t *testing.T, intent ColorIntent, want string) {
	t.Helper()

	got := intent.StoreValue()
	if got == nil || *got != want {
		t.Fatalf("ColorIntent.StoreValue() = %s, want %q", describeStringPointer(got), want)
	}
}

func stringPointer(value string) *string {
	return &value
}

func equalStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}

	return *left == *right
}

func describeStringPointer(value *string) string {
	if value == nil {
		return "nil"
	}

	return `"` + *value + `"`
}
