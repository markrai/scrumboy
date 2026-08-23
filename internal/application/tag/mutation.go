// Package tag defines the application-layer vocabulary and persistence
// capabilities used to converge tag mutations across REST and MCP.
package tag

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrActorRequired reports that a prepared tag mutation requiring an
	// authenticated actor did not receive one.
	ErrActorRequired = errors.New("tag mutation actor required")
	// ErrMaintainerRequired reports that a prepared tag mutation requiring
	// durable project authority did not receive it.
	ErrMaintainerRequired = errors.New("tag mutation maintainer required")
)

// ColorIntent preserves the adapter-prepared color value. A nil value means
// clear; every non-nil value, including an empty or whitespace-only string, is
// a supplied value whose interpretation remains with the selected store
// method.
//
// ColorIntent owns its value. StoreValue returns a fresh pointer because some
// existing store methods normalize color values in place.
type ColorIntent struct {
	value *string
}

// NewColorIntent copies value without trimming, normalizing, or validating it.
func NewColorIntent(value *string) ColorIntent {
	return ColorIntent{value: cloneString(value)}
}

// IsClear reports whether the prepared color represents a clear operation.
func (c ColorIntent) IsClear() bool {
	return c.value == nil
}

// StoreValue returns a copy suitable for passing to an existing store method.
func (c ColorIntent) StoreValue() *string {
	return cloneString(c.value)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// ProjectKind records the project distinction already resolved by an adapter
// or future prepared mutation service.
type ProjectKind uint8

const (
	// DurableProject is a persisted project governed by durable membership and
	// role rules.
	DurableProject ProjectKind = iota + 1
	// CreatorOwnedTemporaryBoard is a temporary board whose creator identity is
	// available to the mutation path.
	CreatorOwnedTemporaryBoard
	// AnonymousTemporaryBoard is an unowned temporary board operating under
	// anonymous-board rules.
	AnonymousTemporaryBoard
)

// ResolvedProject identifies a project and the project-kind distinction that
// later orchestration must preserve. Expiry is not a project kind; the store
// remains authoritative for temporary-board validity.
type ResolvedProject struct {
	ProjectID int64
	Kind      ProjectKind
}

// MineIDColorCommand contains prepared values for changing a caller-owned
// personal tag color by tag ID.
type MineIDColorCommand struct {
	ActorUserID int64
	TagID       int64
	Color       ColorIntent
}

// ProjectIDColorCommand contains prepared values for changing a project tag
// color by tag ID. ViewerUserID is nil for anonymous-board operation.
type ProjectIDColorCommand struct {
	Project      ResolvedProject
	ViewerUserID *int64
	TagID        int64
	Color        ColorIntent
}

// ProjectNameColorCommand contains prepared values for changing a project tag
// color by name. ViewerUserID is nil for anonymous-board operation.
type ProjectNameColorCommand struct {
	Project      ResolvedProject
	ViewerUserID *int64
	Name         string
	Color        ColorIntent
}

// MineIDDeleteCommand contains prepared values for deleting a caller-owned
// personal tag by tag ID.
type MineIDDeleteCommand struct {
	ActorUserID int64
	TagID       int64
}

// ProjectIDDeleteCommand contains prepared values for deleting a project tag
// by tag ID. ActorUserID is nil for anonymous-board operation.
type ProjectIDDeleteCommand struct {
	Project     ResolvedProject
	ActorUserID *int64
	TagID       int64
}

// ProjectNameDeleteCommand contains prepared values for deleting a project tag
// by name. ActorUserID is nil for anonymous-board operation.
type ProjectNameDeleteCommand struct {
	Project     ResolvedProject
	ActorUserID *int64
	Name        string
}

// DeletionResult preserves the affected-project sequence returned by existing
// store methods while preventing callers from mutating application-owned state.
type DeletionResult struct {
	affectedProjectIDs []int64
}

// NewDeletionResult copies affectedProjectIDs without sorting or deduplicating
// it. Existing store semantics remain observable to later orchestration.
func NewDeletionResult(affectedProjectIDs []int64) DeletionResult {
	return DeletionResult{affectedProjectIDs: cloneProjectIDs(affectedProjectIDs)}
}

// AffectedProjectIDs returns a defensive copy of the affected project IDs.
func (r DeletionResult) AffectedProjectIDs() []int64 {
	return cloneProjectIDs(r.affectedProjectIDs)
}

func cloneProjectIDs(projectIDs []int64) []int64 {
	if projectIDs == nil {
		return nil
	}

	return append([]int64{}, projectIDs...)
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

// MineTagReadStore reads the caller-owned tag projection used by mine mutation
// responses.
type MineTagReadStore interface {
	ListUserTags(ctx context.Context, userID int64) ([]store.TagWithColor, error)
}

// ProjectTagReadStore reads the project tag projection used by project mutation
// responses.
type ProjectTagReadStore interface {
	ListTagCounts(ctx context.Context, pc *store.ProjectContext) ([]store.TagCount, error)
}

// MCPProjectAccessStore resolves the project-slug access boundary used by MCP
// project tag mutations.
type MCPProjectAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// ProjectScopedTagReadStore reads a project-scoped tag by ID without widening
// the mutation interfaces below.
type ProjectScopedTagReadStore interface {
	GetProjectScopedTagByID(ctx context.Context, projectID, tagID int64) (store.TagWithColor, error)
}

// BoardScopedTagNameReadStore resolves a board-scoped tag name to its ID.
type BoardScopedTagNameReadStore interface {
	GetBoardScopedTagIDByName(ctx context.Context, projectID int64, tagName string) (int64, error)
}

// PersonalTagNameReadStore resolves a caller-owned personal tag name to its ID.
type PersonalTagNameReadStore interface {
	GetTagIDByName(ctx context.Context, userID int64, tagName string) (int64, error)
}

// MineColorStore mutates the caller-owned personal color preference by tag ID.
type MineColorStore interface {
	UpdateMyTagColor(ctx context.Context, userID, tagID int64, color *string) error
}

// LegacyRowColorStore exposes the existing row-oriented color operation needed
// to preserve compatibility while orchestration is moved inward.
type LegacyRowColorStore interface {
	UpdateTagColor(ctx context.Context, viewerUserID *int64, tagID int64, color *string) error
}

// DurableProjectIDColorStore mutates a durable-project tag color by tag ID.
type DurableProjectIDColorStore interface {
	UpdateTagColorForDurableProjectByID(
		ctx context.Context,
		projectID int64,
		viewerUserID int64,
		tagID int64,
		color *string,
	) error
}

// TemporaryBoardIDColorStore mutates a temporary-board tag color by tag ID.
type TemporaryBoardIDColorStore interface {
	UpdateTagColorForTemporaryBoard(
		ctx context.Context,
		projectID int64,
		viewerUserID *int64,
		tagID int64,
		color *string,
	) error
}

// DurableProjectNameColorStore mutates a durable-project viewer color by tag
// name.
type DurableProjectNameColorStore interface {
	SetViewerTagColorByName(
		ctx context.Context,
		projectID int64,
		viewerUserID int64,
		name string,
		color *string,
	) error
}

// TemporaryBoardNameColorStore exposes the existing temporary-board name
// operation. Later orchestration is responsible for deriving the
// linkTemporaryBoard flag rather than normalizing this store distinction here.
type TemporaryBoardNameColorStore interface {
	UpdateTagColorForProject(
		ctx context.Context,
		projectID int64,
		viewerUserID *int64,
		tagName string,
		color *string,
		linkTemporaryBoard bool,
	) error
}

// MineIDDeletionStore deletes a caller-owned personal tag by ID and reports the
// affected projects in the store-provided order.
type MineIDDeletionStore interface {
	DeleteMyTagByID(ctx context.Context, userID, tagID int64) ([]int64, error)
}

// MineNameDeletionStore deletes a caller-owned personal tag by name in the
// context of a project and reports the affected projects.
type MineNameDeletionStore interface {
	DeleteMyTagByName(
		ctx context.Context,
		projectID int64,
		userID int64,
		name string,
	) ([]int64, error)
}

// DurableProjectIDDeletionStore deletes a durable-project tag by ID and reports
// the affected projects.
type DurableProjectIDDeletionStore interface {
	DeleteTagForDurableProjectByID(
		ctx context.Context,
		projectID int64,
		userID int64,
		tagID int64,
	) ([]int64, error)
}

// LegacyRowDeletionStore exposes the existing row-oriented delete operation.
// Later orchestration derives isAnonymousBoard from the resolved project kind.
type LegacyRowDeletionStore interface {
	DeleteTag(ctx context.Context, userID int64, tagID int64, isAnonymousBoard bool) error
}
