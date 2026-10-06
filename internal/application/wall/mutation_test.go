package wall

import (
	"testing"

	"scrumboy/internal/store"
)

var (
	_ RESTWriterRoleStore  = (*store.Store)(nil)
	_ NoteMutationStore    = (*store.Store)(nil)
	_ WallReplacementStore = (*store.Store)(nil)
	_ EdgeMutationStore    = (*store.Store)(nil)
)

func TestResolvedRESTTargetPreservesProjectID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		projectID int64
	}{
		{name: "positive", projectID: 41},
		{name: "zero", projectID: 0},
		{name: "negative", projectID: -7},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := ResolvedRESTTarget{ProjectID: tt.projectID}
			if target.ProjectID != tt.projectID {
				t.Fatalf("ProjectID = %d, want %d", target.ProjectID, tt.projectID)
			}
		})
	}
}

func TestNoteCommandsPreserveRawValuesAndPresence(t *testing.T) {
	t.Parallel()

	create := CreateNoteCommand{
		X:      -100001.25,
		Y:      100001.5,
		Width:  -60,
		Height: 0,
		Color:  " NOT-A-COLOR ",
		Text:   " \t raw text \n ",
	}
	if create.X != -100001.25 || create.Y != 100001.5 || create.Width != -60 || create.Height != 0 ||
		create.Color != " NOT-A-COLOR " || create.Text != " \t raw text \n " {
		t.Fatalf("CreateNoteCommand changed raw values: %#v", create)
	}
	for _, tt := range []struct {
		name string
		text string
	}{
		{name: "empty", text: ""},
		{name: "whitespace", text: " \t\n "},
	} {
		tt := tt
		t.Run("create text "+tt.name, func(t *testing.T) {
			t.Parallel()

			command := CreateNoteCommand{Text: tt.text}
			if command.Text != tt.text {
				t.Fatalf("CreateNoteCommand.Text = %q, want %q", command.Text, tt.text)
			}
		})
	}

	emptyPatch := PatchNoteCommand{NoteID: "  note-id  ", IfVersion: -3}
	if emptyPatch.NoteID != "  note-id  " || emptyPatch.IfVersion != -3 {
		t.Fatalf("PatchNoteCommand changed identity/version: %#v", emptyPatch)
	}
	if emptyPatch.X != nil || emptyPatch.Y != nil || emptyPatch.Width != nil || emptyPatch.Height != nil ||
		emptyPatch.Color != nil || emptyPatch.Text != nil {
		t.Fatalf("PatchNoteCommand changed omitted fields: %#v", emptyPatch)
	}

	x := 0.0
	y := -100001.5
	width := 0.0
	height := -1.0
	color := " #ABCDEF "
	text := ""
	patch := PatchNoteCommand{
		NoteID:    "raw-note",
		IfVersion: 0,
		X:         &x,
		Y:         &y,
		Width:     &width,
		Height:    &height,
		Color:     &color,
		Text:      &text,
	}
	if patch.X == nil || *patch.X != 0 || patch.Y == nil || *patch.Y != -100001.5 ||
		patch.Width == nil || *patch.Width != 0 || patch.Height == nil || *patch.Height != -1 ||
		patch.Color == nil || *patch.Color != " #ABCDEF " || patch.Text == nil || *patch.Text != "" {
		t.Fatalf("PatchNoteCommand changed supplied fields: %#v", patch)
	}

	whitespace := " \t "
	for _, tt := range []struct {
		name    string
		command PatchNoteCommand
		want    any
	}{
		{name: "x", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, X: &x}, want: x},
		{name: "y", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, Y: &y}, want: y},
		{name: "width", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, Width: &width}, want: width},
		{name: "height", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, Height: &height}, want: height},
		{name: "color", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, Color: &whitespace}, want: whitespace},
		{name: "text", command: PatchNoteCommand{NoteID: " independent ", IfVersion: 7, Text: &text}, want: text},
	} {
		tt := tt
		t.Run("patch independent "+tt.name, func(t *testing.T) {
			t.Parallel()

			command := tt.command
			if command.NoteID != " independent " || command.IfVersion != 7 {
				t.Fatalf("PatchNoteCommand changed identity/version: %#v", command)
			}
			provided := 0
			if command.X != nil {
				provided++
				if tt.name != "x" || *command.X != tt.want.(float64) {
					t.Fatalf("PatchNoteCommand.X = %#v, want only x=%v", command.X, tt.want)
				}
			}
			if command.Y != nil {
				provided++
				if tt.name != "y" || *command.Y != tt.want.(float64) {
					t.Fatalf("PatchNoteCommand.Y = %#v, want only y=%v", command.Y, tt.want)
				}
			}
			if command.Width != nil {
				provided++
				if tt.name != "width" || *command.Width != tt.want.(float64) {
					t.Fatalf("PatchNoteCommand.Width = %#v, want only width=%v", command.Width, tt.want)
				}
			}
			if command.Height != nil {
				provided++
				if tt.name != "height" || *command.Height != tt.want.(float64) {
					t.Fatalf("PatchNoteCommand.Height = %#v, want only height=%v", command.Height, tt.want)
				}
			}
			if command.Color != nil {
				provided++
				if tt.name != "color" || *command.Color != tt.want.(string) {
					t.Fatalf("PatchNoteCommand.Color = %#v, want only color=%q", command.Color, tt.want)
				}
			}
			if command.Text != nil {
				provided++
				if tt.name != "text" || *command.Text != tt.want.(string) {
					t.Fatalf("PatchNoteCommand.Text = %#v, want only text=%q", command.Text, tt.want)
				}
			}
			if provided != 1 {
				t.Fatalf("PatchNoteCommand supplied field count = %d, want 1: %#v", provided, command)
			}
		})
	}

	for _, tt := range []struct {
		name   string
		noteID string
	}{
		{name: "ordinary", noteID: "note-17"},
		{name: "blank", noteID: ""},
		{name: "whitespace", noteID: " \t "},
		{name: "raw", noteID: "  note/raw  "},
	} {
		tt := tt
		t.Run("delete "+tt.name, func(t *testing.T) {
			t.Parallel()

			command := DeleteNoteCommand{NoteID: tt.noteID}
			if command.NoteID != tt.noteID {
				t.Fatalf("DeleteNoteCommand.NoteID = %q, want %q", command.NoteID, tt.noteID)
			}
		})
	}
}

func TestReplaceWallCommandPreservesNotePresenceOrderAndValues(t *testing.T) {
	t.Parallel()

	var nilNotes []NoteDraft
	nilCommand := ReplaceWallCommand{Notes: nilNotes}
	if nilCommand.Notes != nil {
		t.Fatalf("nil Notes = %#v, want nil", nilCommand.Notes)
	}

	emptyCommand := ReplaceWallCommand{Notes: []NoteDraft{}}
	if emptyCommand.Notes == nil || len(emptyCommand.Notes) != 0 {
		t.Fatalf("supplied empty Notes = %#v, want non-nil empty", emptyCommand.Notes)
	}

	command := ReplaceWallCommand{Notes: []NoteDraft{
		{X: -1, Y: 2, Width: 0, Height: -3, Color: " raw ", Text: "first"},
		{X: -1, Y: 2, Width: 0, Height: -3, Color: " raw ", Text: ""},
		{X: -1, Y: 2, Width: 0, Height: -3, Color: " raw ", Text: "first"},
	}}
	if len(command.Notes) != 3 {
		t.Fatalf("len(Notes) = %d, want 3", len(command.Notes))
	}
	first, second, duplicate := command.Notes[0], command.Notes[1], command.Notes[2]
	if first.X != -1 || first.Y != 2 || first.Width != 0 || first.Height != -3 ||
		first.Color != " raw " || first.Text != "first" {
		t.Fatalf("first NoteDraft changed raw values: %#v", first)
	}
	if second.X != -1 || second.Y != 2 || second.Width != 0 || second.Height != -3 ||
		second.Color != " raw " || second.Text != "" {
		t.Fatalf("second NoteDraft changed raw values or order: %#v", second)
	}
	if duplicate != first {
		t.Fatalf("duplicate-looking NoteDraft changed or reordered: first=%#v duplicate=%#v", first, duplicate)
	}
}

func TestEdgeAndTransientContractsPreserveRawValues(t *testing.T) {
	t.Parallel()

	createEdge := CreateEdgeCommand{From: "  note-b  ", To: "  note-a  "}
	if createEdge.From != "  note-b  " || createEdge.To != "  note-a  " {
		t.Fatalf("CreateEdgeCommand changed endpoints/order: %#v", createEdge)
	}

	deleteEdge := DeleteEdgeCommand{EdgeID: " \t "}
	if deleteEdge.EdgeID != " \t " {
		t.Fatalf("DeleteEdgeCommand.EdgeID = %q, want raw whitespace", deleteEdge.EdgeID)
	}

	command := TransientCommand{NoteID: "  transient-note  ", X: -1.25, Y: 2.5}
	if command.NoteID != "  transient-note  " || command.X != -1.25 || command.Y != 2.5 {
		t.Fatalf("TransientCommand changed raw values: %#v", command)
	}

	for _, by := range []int64{-9, 0, 27} {
		event := TransientEvent{NoteID: command.NoteID, X: command.X, Y: command.Y, By: by}
		if event.NoteID != "  transient-note  " || event.X != -1.25 || event.Y != 2.5 || event.By != by {
			t.Fatalf("TransientEvent changed semantic values: %#v", event)
		}
	}
}

func TestRefreshReasonsMatchCompatibilityValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason RefreshReason
		want   string
	}{
		{name: "note created", reason: RefreshNoteCreated, want: "wall_note_created"},
		{name: "note updated", reason: RefreshNoteUpdated, want: "wall_note_updated"},
		{name: "note deleted", reason: RefreshNoteDeleted, want: "wall_note_deleted"},
		{name: "wall replaced", reason: RefreshReplaced, want: "wall_replaced"},
		{name: "edge created", reason: RefreshEdgeCreated, want: "wall_edge_created"},
		{name: "edge deleted", reason: RefreshEdgeDeleted, want: "wall_edge_deleted"},
	}

	seen := make(map[RefreshReason]string, len(tests))
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := string(tt.reason); got != tt.want {
				t.Fatalf("reason = %q, want %q", got, tt.want)
			}
		})

		if previous, ok := seen[tt.reason]; ok {
			t.Fatalf("reason %q is shared by %q and %q", tt.reason, previous, tt.name)
		}
		seen[tt.reason] = tt.name
	}
}
