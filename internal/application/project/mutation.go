// Package project defines the application-layer vocabulary and persistence
// capabilities used to converge project lifecycle mutations across REST and
// MCP.
package project

import (
	"context"

	"scrumboy/internal/store"
)

// RESTDurableCreationCommand contains already-decoded REST values for one
// durable project creation. Workflow deliberately preserves nil versus a
// non-nil empty slice; the selected store operation gives those states
// different meanings.
//
// CreateProjectWithWorkflow currently normalizes supplied workflow columns by
// mutating its input slice. The future creation service, rather than this dumb
// command value, is responsible for passing the store an owned copy.
type RESTDurableCreationCommand struct {
	Name     string
	Workflow []store.WorkflowColumn
}

// MCPDurableCreationCommand contains already-decoded MCP values for one
// durable project creation using the store's default workflow path.
type MCPDurableCreationCommand struct {
	Name string
}

// RESTUpdateTarget identifies the numeric project and established deployment
// mode used by future REST update preparation. Actor identity remains derived
// from the trusted context at the characterized preparation stage.
type RESTUpdateTarget struct {
	ProjectID int64
	Mode      store.Mode
}

// RESTUpdateCommand preserves the independent presence of name and image
// values decoded by REST. It does not combine the two existing store
// transactions into an atomic patch.
type RESTUpdateCommand struct {
	Name  *string
	Image *string
}

// ProjectSlugTarget contains the slug and established deployment mode needed
// by slug-resolved project lifecycle preparation.
type ProjectSlugTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// MCPUpdateCommand preserves the presence-aware fields accepted by the MCP
// project update tool. The future MCP service converts these values into a
// fresh store.UpdateProjectPatch for the existing atomic store mutation.
type MCPUpdateCommand struct {
	Name               *string
	DefaultSprintWeeks *int
}

// RESTDeletionCommand identifies one numeric REST project deletion and its
// already-established actor.
type RESTDeletionCommand struct {
	ProjectID   int64
	ActorUserID int64
}

// MCPDeletionCommand identifies one slug-resolved MCP project deletion.
type MCPDeletionCommand struct {
	Project ProjectSlugTarget
}

// ClaimCommand identifies one REST Temporary Board claim and its
// already-established actor.
type ClaimCommand struct {
	ProjectID   int64
	ActorUserID int64
}

// ProjectWithWorkflowCreationStore creates a durable project while preserving
// the existing nil/default versus supplied custom-workflow distinction.
type ProjectWithWorkflowCreationStore interface {
	CreateProjectWithWorkflow(
		ctx context.Context,
		name string,
		workflow []store.WorkflowColumn,
	) (store.Project, error)
}

// ProjectCreationStore creates a durable project using the existing default
// workflow path.
type ProjectCreationStore interface {
	CreateProject(ctx context.Context, name string) (store.Project, error)
}

// AnonymousBoardCreationStore creates one link-expiring board using the
// store-owned transaction and post-commit initialization behavior.
type AnonymousBoardCreationStore interface {
	CreateAnonymousBoard(ctx context.Context) (store.Project, error)
}

// ProjectByIDReadStore reads the project projection required by selected
// lifecycle preparation and post-write projection stages.
type ProjectByIDReadStore interface {
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
}

// ProjectAccessStore resolves the existing slug access boundary used by
// project lifecycle preparation.
type ProjectAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// ProjectManageAuthorizationStore applies the existing project-management
// authorization check without widening the access or mutation ports.
type ProjectManageAuthorizationStore interface {
	CheckCanManageProject(ctx context.Context, projectID, userID int64) error
}

// ProjectNameMutationStore mutates a project name through its existing
// persistence and authorization boundary.
type ProjectNameMutationStore interface {
	UpdateProjectName(
		ctx context.Context,
		projectID int64,
		userID int64,
		name string,
	) error
}

// ProjectImageMutationStore mutates a project image and dominant color through
// its existing persistence and authorization boundary.
type ProjectImageMutationStore interface {
	UpdateProjectImage(
		ctx context.Context,
		projectID int64,
		userID int64,
		image *string,
		dominantColor string,
	) error
}

// ProjectPatchMutationStore applies the existing atomic MCP project patch.
type ProjectPatchMutationStore interface {
	UpdateProjectPatch(
		ctx context.Context,
		projectID int64,
		userID int64,
		patch store.UpdateProjectPatch,
	) error
}

// ProjectDeletionStore deletes a project and returns the committed store
// snapshot required by later lifecycle projection and effects.
type ProjectDeletionStore interface {
	DeleteProject(
		ctx context.Context,
		projectID int64,
		userID int64,
	) (store.DeletedProjectSnapshot, error)
}

// TemporaryBoardClaimStore performs the authoritative conditional Temporary
// Board claim. Callers must not treat a preceding project or access read as
// sufficient authorization or concurrency validation; this mutation remains
// the final authority and must be invoked exactly once.
type TemporaryBoardClaimStore interface {
	ClaimTemporaryBoard(ctx context.Context, projectID, userID int64) error
}
