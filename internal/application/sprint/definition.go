// Package sprint defines application boundaries for sprint use cases.
package sprint

import (
	"context"
	"time"

	"scrumboy/internal/store"
)

// CreateCommand contains definition values already decoded and converted by a
// transport adapter. A later prepared capability binds project and requester
// identity. The command performs no normalization or validation; the store
// remains authoritative for sprint-definition policy.
type CreateCommand struct {
	Name           string
	PlannedStartAt time.Time
	PlannedEndAt   time.Time
}

// UpdateCommand preserves syntactic presence for the definition fields shared
// by REST and MCP. Nil means omitted; a non-nil pointer remains supplied even
// when it points to an empty string or zero time. Transport decoding and public
// validation remain adapter-owned.
type UpdateCommand struct {
	Name           *string
	PlannedStartAt *time.Time
	PlannedEndAt   *time.Time
}

// HasFields reports syntactic field presence without applying validation,
// semantic comparison, or a transport-specific write/no-write policy.
func (c UpdateCommand) HasFields() bool {
	return c.Name != nil || c.PlannedStartAt != nil || c.PlannedEndAt != nil
}

// MaterializeUpdateInput converts an application-owned command to the current
// store vocabulary. It defensively copies supplied values and intentionally
// performs no normalization, validation, authorization, or persistence.
func MaterializeUpdateInput(command UpdateCommand) store.UpdateSprintInput {
	input := store.UpdateSprintInput{}
	if command.Name != nil {
		value := *command.Name
		input.Name = &value
	}
	if command.PlannedStartAt != nil {
		value := *command.PlannedStartAt
		input.PlannedStartAt = &value
	}
	if command.PlannedEndAt != nil {
		value := *command.PlannedEndAt
		input.PlannedEndAt = &value
	}
	return input
}

// DefinitionStore is the complete persistence capability for the create/update
// sprint-definition subfamily. The store retains name, uniqueness, date,
// lifecycle-state, numbering, persistence, and create-return-read policy.
type DefinitionStore interface {
	CreateSprint(
		ctx context.Context,
		projectID int64,
		name string,
		plannedStartAt time.Time,
		plannedEndAt time.Time,
	) (store.Sprint, error)

	UpdateSprint(
		ctx context.Context,
		sprintID int64,
		input store.UpdateSprintInput,
	) error
}

// RoleStore is kept separate from definition, lifecycle, and deletion
// persistence because their prepared REST and MCP services perform a fresh role
// lookup while retaining transport-specific error policies. The port returns
// the store result unchanged.
type RoleStore interface {
	GetProjectRole(
		ctx context.Context,
		projectID int64,
		userID int64,
	) (store.ProjectRole, error)
}
