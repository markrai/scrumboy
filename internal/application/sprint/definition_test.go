package sprint

import (
	"testing"
	"time"

	"scrumboy/internal/store"
)

var _ DefinitionStore = (*store.Store)(nil)
var _ RoleStore = (*store.Store)(nil)

func TestUpdateCommandHasFieldsPreservesSyntacticPresence(t *testing.T) {
	emptyName := ""
	zeroTime := time.Time{}
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)

	tests := []struct {
		name    string
		command UpdateCommand
		want    bool
	}{
		{name: "all omitted", command: UpdateCommand{}, want: false},
		{name: "name", command: UpdateCommand{Name: &emptyName}, want: true},
		{name: "planned start", command: UpdateCommand{PlannedStartAt: &start}, want: true},
		{name: "planned end", command: UpdateCommand{PlannedEndAt: &end}, want: true},
		{name: "zero time remains present", command: UpdateCommand{PlannedStartAt: &zeroTime}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.command.HasFields(); got != test.want {
				t.Fatalf("HasFields()=%t, want %t for command %+v", got, test.want, test.command)
			}
		})
	}
}

func TestMaterializeUpdateInputPreservesPresenceAndValues(t *testing.T) {
	empty := MaterializeUpdateInput(UpdateCommand{})
	if empty.Name != nil || empty.PlannedStartAt != nil || empty.PlannedEndAt != nil {
		t.Fatalf("empty materialized input=%+v, want all fields omitted", empty)
	}

	emptyName := ""
	zeroTime := time.Time{}
	startLocation := time.FixedZone("checkpoint-two", 2*60*60)
	start := time.Date(2026, time.August, 10, 12, 30, 45, 123000000, startLocation)
	end := start.Add(9 * 24 * time.Hour)

	tests := []struct {
		name    string
		command UpdateCommand
		assert  func(*testing.T, store.UpdateSprintInput)
	}{
		{
			name:    "empty name remains supplied",
			command: UpdateCommand{Name: &emptyName},
			assert: func(t *testing.T, got store.UpdateSprintInput) {
				t.Helper()
				if got.Name == nil || *got.Name != "" || got.PlannedStartAt != nil || got.PlannedEndAt != nil {
					t.Fatalf("materialized input=%+v", got)
				}
			},
		},
		{
			name:    "zero start remains supplied",
			command: UpdateCommand{PlannedStartAt: &zeroTime},
			assert: func(t *testing.T, got store.UpdateSprintInput) {
				t.Helper()
				if got.Name != nil || got.PlannedStartAt == nil || !got.PlannedStartAt.IsZero() || got.PlannedEndAt != nil {
					t.Fatalf("materialized input=%+v", got)
				}
			},
		},
		{
			name:    "planned end maps independently",
			command: UpdateCommand{PlannedEndAt: &end},
			assert: func(t *testing.T, got store.UpdateSprintInput) {
				t.Helper()
				if got.Name != nil || got.PlannedStartAt != nil || got.PlannedEndAt == nil || *got.PlannedEndAt != end {
					t.Fatalf("materialized input=%+v", got)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.assert(t, MaterializeUpdateInput(test.command))
		})
	}

	name := "Definition values"
	all := MaterializeUpdateInput(UpdateCommand{
		Name:           &name,
		PlannedStartAt: &start,
		PlannedEndAt:   &end,
	})
	if all.Name == nil || *all.Name != name ||
		all.PlannedStartAt == nil || *all.PlannedStartAt != start ||
		all.PlannedEndAt == nil || *all.PlannedEndAt != end {
		t.Fatalf("fully materialized input=%+v", all)
	}
}

func TestMaterializeUpdateInputIsolatesPointers(t *testing.T) {
	name := "Original name"
	location := time.FixedZone("pointer-isolation", -5*60*60)
	start := time.Date(2026, time.August, 10, 9, 15, 30, 456000000, location)
	end := start.Add(7 * 24 * time.Hour)
	command := UpdateCommand{
		Name:           &name,
		PlannedStartAt: &start,
		PlannedEndAt:   &end,
	}

	input := MaterializeUpdateInput(command)
	if input.Name == command.Name || input.PlannedStartAt == command.PlannedStartAt || input.PlannedEndAt == command.PlannedEndAt {
		t.Fatalf("materialized input retained command pointers: command=%+v input=%+v", command, input)
	}

	originalName := *input.Name
	originalStart := *input.PlannedStartAt
	originalEnd := *input.PlannedEndAt
	*command.Name = "Changed command name"
	*command.PlannedStartAt = command.PlannedStartAt.Add(24 * time.Hour)
	*command.PlannedEndAt = command.PlannedEndAt.Add(48 * time.Hour)
	if *input.Name != originalName || *input.PlannedStartAt != originalStart || *input.PlannedEndAt != originalEnd {
		t.Fatalf("command mutation changed materialized input: command=%+v input=%+v", command, input)
	}

	inputName := "Changed input name"
	inputStart := originalStart.Add(-24 * time.Hour)
	inputEnd := originalEnd.Add(-48 * time.Hour)
	*input.Name = inputName
	*input.PlannedStartAt = inputStart
	*input.PlannedEndAt = inputEnd
	if *command.Name == inputName || *command.PlannedStartAt == inputStart || *command.PlannedEndAt == inputEnd {
		t.Fatalf("materialized-input mutation changed command: command=%+v input=%+v", command, input)
	}
}
