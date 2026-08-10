package todo

import (
	"context"

	"scrumboy/internal/store"
)

// Field preserves whether a transport supplied a patch field independently
// from the field's value. For pointer values, a present nil represents an
// explicit clear rather than omission.
type Field[T any] struct {
	Present bool
	Value   T
}

// UpdatePatch is the transport-independent vocabulary for the canonical todo
// update fields. It carries syntactic presence without applying transport
// decoding, validation, or semantic no-op policy.
type UpdatePatch struct {
	Title            Field[string]
	Body             Field[string]
	Tags             Field[[]string]
	EstimationPoints Field[*int64]
	AssigneeUserID   Field[*int64]
	SprintID         Field[*int64]
}

// HasFields reports syntactic patch presence. It intentionally does not
// compare supplied values with the existing todo; persistence remains
// authoritative for semantic differences and audit behavior.
func (p UpdatePatch) HasFields() bool {
	return p.Title.Present ||
		p.Body.Present ||
		p.Tags.Present ||
		p.EstimationPoints.Present ||
		p.AssigneeUserID.Present ||
		p.SprintID.Present
}

// UpdateCommand identifies a todo by its project-local ID and carries its
// lossless update patch.
type UpdateCommand struct {
	LocalID int64
	Patch   UpdatePatch
}

// UpdateResult contains domain values for transport-owned projection.
type UpdateResult struct {
	Project store.Project
	Todo    store.Todo
}

// UpdateStore is the single persistence capability shared by future REST and
// MCP update services.
type UpdateStore interface {
	UpdateTodoByLocalID(
		ctx context.Context,
		projectID int64,
		localID int64,
		in store.UpdateTodoInput,
		mode store.Mode,
	) (store.Todo, error)
}

// MaterializeUpdateInput converts explicit patch presence into the store's
// existing update vocabulary. It deliberately performs no validation or
// authorization. Omitted replacement fields clone the existing values, while
// an omitted sprint must remain a no-op because the store already represents
// sprint set, clear, and omission separately.
func MaterializeUpdateInput(existing store.Todo, patch UpdatePatch) store.UpdateTodoInput {
	title := existing.Title
	if patch.Title.Present {
		title = patch.Title.Value
	}

	body := existing.Body
	if patch.Body.Present {
		body = patch.Body.Value
	}

	tags := cloneUpdateStrings(existing.Tags)
	if patch.Tags.Present {
		tags = cloneUpdateStrings(patch.Tags.Value)
	}

	estimationPoints := cloneUpdateInt64Ptr(existing.EstimationPoints)
	if patch.EstimationPoints.Present {
		estimationPoints = cloneUpdateInt64Ptr(patch.EstimationPoints.Value)
	}

	assigneeUserID := cloneUpdateInt64Ptr(existing.AssigneeUserID)
	if patch.AssigneeUserID.Present {
		assigneeUserID = cloneUpdateInt64Ptr(patch.AssigneeUserID.Value)
	}

	var sprintID *int64
	clearSprint := false
	if patch.SprintID.Present {
		if patch.SprintID.Value == nil {
			clearSprint = true
		} else {
			sprintID = cloneUpdateInt64Ptr(patch.SprintID.Value)
		}
	}

	return store.UpdateTodoInput{
		Title:            title,
		Body:             body,
		Tags:             tags,
		EstimationPoints: estimationPoints,
		AssigneeUserID:   assigneeUserID,
		SprintID:         sprintID,
		ClearSprint:      clearSprint,
	}
}

func cloneUpdateStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneUpdateInt64Ptr(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
