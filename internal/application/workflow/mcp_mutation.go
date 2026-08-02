package workflow

import (
	"context"
	"errors"
	"fmt"

	"scrumboy/internal/store"
)

var ErrWorkflowProjectionFailed = errors.New("workflow mutation projection failed")

// ErrWorkflowProjectionColumnMissing identifies the internal inconsistency in
// which a successful update is absent from the workflow returned immediately
// afterward. It also satisfies ErrWorkflowProjectionFailed.
var ErrWorkflowProjectionColumnMissing = errors.New("updated workflow column missing from projection")

// workflowProjectionError marks failures that occur after a successful MCP
// update mutation. Its cause remains available so adapters can preserve their
// established read-error mapping while distinguishing projection inconsistencies.
type workflowProjectionError struct {
	message        string
	cause          error
	classification error
}

func (e *workflowProjectionError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

func (e *workflowProjectionError) Unwrap() error {
	return e.cause
}

func (e *workflowProjectionError) Is(target error) bool {
	return target == ErrWorkflowProjectionFailed || target == e.classification
}

// MCPMutationAccessStore resolves the project-slug access boundary used by MCP
// workflow mutations.
type MCPMutationAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

type MCPMutationServiceDependencies struct {
	Access    MCPMutationAccessStore
	Mutations MutationStore
	Workflow  WorkflowReadStore
}

// MCPMutationService owns MCP project access, role authorization, workflow
// persistence, and update post-read projection. It deliberately has no refresh
// or event dependency.
type MCPMutationService struct {
	access    MCPMutationAccessStore
	mutations MutationStore
	workflow  WorkflowReadStore
}

func NewMCPMutationService(deps MCPMutationServiceDependencies) *MCPMutationService {
	return &MCPMutationService{
		access:    deps.Access,
		mutations: deps.Mutations,
		workflow:  deps.Workflow,
	}
}

// MCPMutationTarget identifies the slug whose access must be resolved after
// transport authentication, capability checks, and semantic validation.
type MCPMutationTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// PreparedMCPMutation binds the exact access context and resolved project ID to
// subsequent workflow mutations.
type PreparedMCPMutation struct {
	ctx       context.Context
	service   *MCPMutationService
	projectID int64
}

// Prepare resolves slug access exactly once and authorizes from the role in the
// returned ProjectContext. Unlike REST, MCP performs no second role lookup.
func (s *MCPMutationService) Prepare(
	ctx context.Context,
	target MCPMutationTarget,
) (*PreparedMCPMutation, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	if !projectContext.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}
	return &PreparedMCPMutation{
		ctx:       ctx,
		service:   s,
		projectID: projectContext.Project.ID,
	}, nil
}

// Create persists one adapter-prepared command and returns the created column
// without performing a projection read or publishing realtime events.
func (p *PreparedMCPMutation) Create(command CreateCommand) (store.WorkflowColumn, error) {
	column, err := p.service.mutations.AddWorkflowColumn(p.ctx, p.projectID, command.Name)
	if err != nil {
		return store.WorkflowColumn{}, err
	}
	return column, nil
}

// Update persists before reading the workflow used by MCP response projection.
// Post-write read and lookup failures are classified separately while retaining
// any underlying store cause.
func (p *PreparedMCPMutation) Update(command UpdateCommand) (store.WorkflowColumn, error) {
	if err := p.service.mutations.UpdateWorkflowColumn(
		p.ctx,
		p.projectID,
		command.Key,
		command.Name,
		command.Color,
	); err != nil {
		return store.WorkflowColumn{}, err
	}

	workflow, err := p.service.workflow.GetProjectWorkflow(p.ctx, p.projectID)
	if err != nil {
		return store.WorkflowColumn{}, &workflowProjectionError{
			message: "read updated workflow",
			cause:   err,
		}
	}
	for _, column := range workflow {
		if column.Key == command.Key {
			return column, nil
		}
	}
	return store.WorkflowColumn{}, &workflowProjectionError{
		message:        fmt.Sprintf("updated workflow column %q not found in post-read", command.Key),
		classification: ErrWorkflowProjectionColumnMissing,
	}
}

// Delete persists one adapter-prepared command without a projection read or
// realtime publication.
func (p *PreparedMCPMutation) Delete(command DeleteCommand) error {
	return p.service.mutations.DeleteWorkflowColumn(p.ctx, p.projectID, command.Key)
}
