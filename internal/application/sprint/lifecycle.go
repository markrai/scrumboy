package sprint

import "context"

// TransitionTarget carries the resolved project identity and stored sprint
// identity that a prepared lifecycle operation binds. It does not itself
// authorize activation or close, and it intentionally carries no role, state,
// time, slug, or transport snapshot.
type TransitionTarget struct {
	ProjectID int64
	SprintID  int64
}

// DeletionTarget is distinct from TransitionTarget because sprint destruction
// has different preparation, result, and side-effect semantics. It carries only
// the resolved project identity and stored sprint identity; constructing it does
// not authorize deletion.
type DeletionTarget struct {
	ProjectID int64
	SprintID  int64
	Name      string // already-read display name; not a new acquisition
}

// TransitionStore is the project-scoped persistence capability shared by the
// activation and close transition subfamily. The store retains authoritative
// project, lifecycle-state, time, transaction, and timestamp invariants.
type TransitionStore interface {
	ActivateSprint(
		ctx context.Context,
		projectID int64,
		sprintID int64,
	) error

	CloseSprint(
		ctx context.Context,
		projectID int64,
		sprintID int64,
	) error
}

// DeletionStore is kept separate from transition persistence so future
// deletion services receive no activation or close capability. The store and
// database retain project ownership, lifecycle-state compatibility, deletion,
// and todo-detachment policy.
type DeletionStore interface {
	DeleteSprint(
		ctx context.Context,
		projectID int64,
		sprintID int64,
	) error
}
