package project

import (
	"testing"

	"scrumboy/internal/store"
)

var (
	_ ProjectWithWorkflowCreationStore = (*store.Store)(nil)
	_ ProjectCreationStore             = (*store.Store)(nil)
	_ AnonymousBoardCreationStore      = (*store.Store)(nil)
	_ ProjectByIDReadStore             = (*store.Store)(nil)
	_ ProjectAccessStore               = (*store.Store)(nil)
	_ ProjectManageAuthorizationStore  = (*store.Store)(nil)
	_ ProjectNameMutationStore         = (*store.Store)(nil)
	_ ProjectImageMutationStore        = (*store.Store)(nil)
	_ ProjectPatchMutationStore        = (*store.Store)(nil)
	_ ProjectDeletionStore             = (*store.Store)(nil)
	_ TemporaryBoardClaimStore         = (*store.Store)(nil)
)

func TestCreationCommandsPreserveRawValuesAndWorkflowPresence(t *testing.T) {
	t.Parallel()

	t.Run("REST omitted workflow remains nil", func(t *testing.T) {
		t.Parallel()

		command := RESTDurableCreationCommand{Name: "  Raw Project  "}
		if got, want := command.Name, "  Raw Project  "; got != want {
			t.Fatalf("Name = %q, want %q", got, want)
		}
		if command.Workflow != nil {
			t.Fatalf("Workflow = %#v, want nil", command.Workflow)
		}
	})

	t.Run("REST supplied empty workflow remains non-nil empty", func(t *testing.T) {
		t.Parallel()

		command := RESTDurableCreationCommand{
			Name:     "",
			Workflow: []store.WorkflowColumn{},
		}
		if command.Workflow == nil {
			t.Fatal("Workflow = nil, want supplied empty slice")
		}
		if got := len(command.Workflow); got != 0 {
			t.Fatalf("len(Workflow) = %d, want 0", got)
		}
	})

	t.Run("REST custom workflow remains byte-for-byte and ordered", func(t *testing.T) {
		t.Parallel()

		command := RESTDurableCreationCommand{
			Name: " Project ",
			Workflow: []store.WorkflowColumn{
				{
					ID:        -1,
					ProjectID: -2,
					Key:       "  Mixed_Key  ",
					Name:      "  First  ",
					Color:     " NOT-NORMALIZED ",
					Position:  -3,
					IsDone:    true,
					System:    true,
				},
				{Key: "  Mixed_Key  ", Name: "", Position: -4},
			},
		}

		if got, want := command.Name, " Project "; got != want {
			t.Fatalf("Name = %q, want %q", got, want)
		}
		if got, want := len(command.Workflow), 2; got != want {
			t.Fatalf("len(Workflow) = %d, want %d", got, want)
		}
		first := command.Workflow[0]
		if first.ID != -1 || first.ProjectID != -2 || first.Key != "  Mixed_Key  " ||
			first.Name != "  First  " || first.Color != " NOT-NORMALIZED " ||
			first.Position != -3 || !first.IsDone || !first.System {
			t.Fatalf("first Workflow column changed raw values: %#v", first)
		}
		if second := command.Workflow[1]; second.Key != "  Mixed_Key  " || second.Name != "" || second.Position != -4 {
			t.Fatalf("second Workflow column changed raw values: %#v", second)
		}
	})

	t.Run("MCP creation name remains raw", func(t *testing.T) {
		t.Parallel()

		command := MCPDurableCreationCommand{Name: " \t "}
		if got, want := command.Name, " \t "; got != want {
			t.Fatalf("Name = %q, want %q", got, want)
		}
	})
}

func TestUpdateCommandsPreserveTargetsPresenceAndValues(t *testing.T) {
	t.Parallel()

	name := "  "
	image := "not-a-data-url"
	restTarget := RESTUpdateTarget{ProjectID: -7, Mode: store.Mode("future-mode")}
	restCommand := RESTUpdateCommand{Name: &name, Image: &image}

	if restTarget.ProjectID != -7 || restTarget.Mode != store.Mode("future-mode") {
		t.Fatalf("RESTUpdateTarget changed raw values: %#v", restTarget)
	}
	if restCommand.Name == nil || *restCommand.Name != "  " {
		t.Fatalf("RESTUpdateCommand.Name = %v, want supplied whitespace", restCommand.Name)
	}
	if restCommand.Image == nil || *restCommand.Image != "not-a-data-url" {
		t.Fatalf("RESTUpdateCommand.Image = %v, want supplied malformed value", restCommand.Image)
	}

	emptyREST := RESTUpdateCommand{}
	if emptyREST.Name != nil || emptyREST.Image != nil {
		t.Fatalf("empty RESTUpdateCommand changed omitted values: %#v", emptyREST)
	}

	weeks := -2
	mcpName := ""
	mcpTarget := ProjectSlugTarget{ProjectSlug: "  Project Slug  ", Mode: store.Mode("")}
	mcpCommand := MCPUpdateCommand{Name: &mcpName, DefaultSprintWeeks: &weeks}

	if mcpTarget.ProjectSlug != "  Project Slug  " || mcpTarget.Mode != store.Mode("") {
		t.Fatalf("ProjectSlugTarget changed raw values: %#v", mcpTarget)
	}
	if mcpCommand.Name == nil || *mcpCommand.Name != "" {
		t.Fatalf("MCPUpdateCommand.Name = %v, want supplied empty", mcpCommand.Name)
	}
	if mcpCommand.DefaultSprintWeeks == nil || *mcpCommand.DefaultSprintWeeks != -2 {
		t.Fatalf("MCPUpdateCommand.DefaultSprintWeeks = %v, want -2", mcpCommand.DefaultSprintWeeks)
	}

	emptyMCP := MCPUpdateCommand{}
	if emptyMCP.Name != nil || emptyMCP.DefaultSprintWeeks != nil {
		t.Fatalf("empty MCPUpdateCommand changed omitted values: %#v", emptyMCP)
	}
}

func TestDeletionAndClaimCommandsKeepLifecycleFamiliesExplicit(t *testing.T) {
	t.Parallel()

	restDelete := RESTDeletionCommand{ProjectID: -1, ActorUserID: 0}
	if restDelete.ProjectID != -1 || restDelete.ActorUserID != 0 {
		t.Fatalf("RESTDeletionCommand changed raw values: %#v", restDelete)
	}

	mcpDelete := MCPDeletionCommand{Project: ProjectSlugTarget{
		ProjectSlug: " ",
		Mode:        store.Mode("unknown"),
	}}
	if mcpDelete.Project.ProjectSlug != " " || mcpDelete.Project.Mode != store.Mode("unknown") {
		t.Fatalf("MCPDeletionCommand changed raw values: %#v", mcpDelete)
	}

	claim := ClaimCommand{ProjectID: 0, ActorUserID: -4}
	if claim.ProjectID != 0 || claim.ActorUserID != -4 {
		t.Fatalf("ClaimCommand changed raw values: %#v", claim)
	}
}
