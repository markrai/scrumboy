// Package wall defines the application boundary for selected Wall mutations.
package wall

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrActorRequired reports that the exact context supplied to Wall mutation
	// preparation does not contain a trusted authenticated actor.
	ErrActorRequired = errors.New("wall mutation actor required")

	// ErrContributorRequired reports both a failed fresh role read and a role
	// below Contributor, preserving the current REST writer gate's collapsed
	// forbidden result.
	ErrContributorRequired = errors.New("wall contributor required")

	// ErrPreparedMutationAlreadyExecuted reports that execution has already
	// begun for one prepared Wall mutation.
	ErrPreparedMutationAlreadyExecuted = errors.New("prepared wall mutation already executed")
)

// ResolvedRESTTarget carries only the numeric project identity already
// resolved and admitted by the REST board router.
type ResolvedRESTTarget struct {
	ProjectID int64
}

// CreateNoteCommand contains the REST-decoded values supplied for note
// creation. Store validation and normalization remain authoritative.
type CreateNoteCommand struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
	Color  string
	Text   string
}

// PatchNoteCommand identifies a note and preserves every optional value
// already decoded by the REST adapter. IfVersion is the note-scoped optimistic
// concurrency value; zero retains the store's unconditional-update behavior.
type PatchNoteCommand struct {
	NoteID    string
	IfVersion int64
	X         *float64
	Y         *float64
	Width     *float64
	Height    *float64
	Color     *string
	Text      *string
}

// DeleteNoteCommand identifies the note selected for deletion.
type DeleteNoteCommand struct {
	NoteID string
}

// NoteDraft contains one REST-decoded replacement note. IDs and versions are
// intentionally absent because the public replacement input cannot supply
// them and the store remains their source.
type NoteDraft struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
	Color  string
	Text   string
}

// ReplaceWallCommand contains the replacement-note list. Existing edges and
// all persistence behavior remain owned by Store.ReplaceWall.
type ReplaceWallCommand struct {
	Notes []NoteDraft
}

// CreateEdgeCommand contains the two adapter-decoded note endpoints. Endpoint
// validation, undirected duplicate detection, and ID generation remain in the
// store.
type CreateEdgeCommand struct {
	From string
	To   string
}

// DeleteEdgeCommand identifies the edge selected for deletion.
type DeleteEdgeCommand struct {
	EdgeID string
}

// TransientCommand contains an accepted ephemeral note-position update. Actor
// identity is deliberately absent and will be bound from prepared context.
type TransientCommand struct {
	NoteID string
	X      float64
	Y      float64
}

// RESTWriterRoleStore reads the caller's fresh project role at the REST Wall
// mutation boundary.
type RESTWriterRoleStore interface {
	GetProjectRole(
		ctx context.Context,
		projectID int64,
		userID int64,
	) (store.ProjectRole, error)
}

// NoteMutationStore exposes only the persistence capabilities required by the
// REST note mutation service.
type NoteMutationStore interface {
	CreateNote(
		ctx context.Context,
		projectID int64,
		in store.CreateNoteInput,
	) (store.WallNote, store.Wall, error)
	PatchNote(
		ctx context.Context,
		projectID int64,
		noteID string,
		in store.PatchNoteInput,
	) (store.WallNote, store.Wall, error)
	DeleteNote(
		ctx context.Context,
		projectID int64,
		noteID string,
	) (store.Wall, error)
}

// WallReplacementStore exposes only full note-list replacement. The store
// retains validation, note identity/version generation, and edge preservation.
type WallReplacementStore interface {
	ReplaceWall(
		ctx context.Context,
		projectID int64,
		notes []store.WallNote,
	) (store.Wall, error)
}

// EdgeMutationStore exposes only the persistence capabilities required by the
// REST edge mutation service.
type EdgeMutationStore interface {
	CreateEdge(
		ctx context.Context,
		projectID int64,
		fromNoteID string,
		toNoteID string,
	) (store.WallEdge, store.Wall, error)
	DeleteEdge(
		ctx context.Context,
		projectID int64,
		edgeID string,
	) (store.Wall, error)
}

// RefreshReason identifies the semantic reason for a durable Wall refresh.
// Concrete eventbus and transport projection remain adapter-owned.
type RefreshReason string

const (
	// RefreshNoteCreated follows one successful note creation.
	RefreshNoteCreated RefreshReason = "wall_note_created"
	// RefreshNoteUpdated follows one successful note patch, including an empty
	// patch that the store accepts as a versioned mutation.
	RefreshNoteUpdated RefreshReason = "wall_note_updated"
	// RefreshNoteDeleted follows one successful note deletion.
	RefreshNoteDeleted RefreshReason = "wall_note_deleted"
	// RefreshReplaced follows one successful full note-list replacement.
	RefreshReplaced RefreshReason = "wall_replaced"
	// RefreshEdgeCreated follows every successful edge-create call, including
	// the store's undirected duplicate no-op.
	RefreshEdgeCreated RefreshReason = "wall_edge_created"
	// RefreshEdgeDeleted follows one successful edge deletion.
	RefreshEdgeDeleted RefreshReason = "wall_edge_deleted"
)

// WallRefreshPublisher publishes the semantic refresh required after one
// successful durable Wall mutation. Current downstream fanout is best effort,
// so publication has no application error result.
type WallRefreshPublisher interface {
	PublishWallRefresh(
		ctx context.Context,
		projectID int64,
		reason RefreshReason,
	)
}

// TransientEvent is the semantic payload for one ephemeral note-position
// publication. By is supplied from the trusted actor bound during preparation.
type TransientEvent struct {
	NoteID string
	X      float64
	Y      float64
	By     int64
}

// WallTransientPublisher publishes one ephemeral Wall movement. Its error is
// reserved for synchronous payload/event preparation; downstream fanout
// remains best effort.
type WallTransientPublisher interface {
	PublishWallTransient(
		ctx context.Context,
		projectID int64,
		event TransientEvent,
	) error
}
