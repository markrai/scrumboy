package todolink

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrMCPSourceLookupFailed classifies the source-todo gate within MCP
	// preparation while retaining the underlying store or context cause.
	ErrMCPSourceLookupFailed = errors.New("todo-link mutation source lookup failed")

	// ErrMCPProjectionFailed classifies a post-mutation link read while
	// retaining the underlying store or context cause.
	ErrMCPProjectionFailed = errors.New("todo-link mutation projection failed")
)

// mcpMutationStageError distinguishes application stages whose adapter error
// mappings differ. Error deliberately omits the cause so private dependency
// diagnostics cannot leak through the wrapper text.
type mcpMutationStageError struct {
	classification error
	cause          error
}

func (e *mcpMutationStageError) Error() string {
	return e.classification.Error()
}

func (e *mcpMutationStageError) Unwrap() error {
	return e.cause
}

func (e *mcpMutationStageError) Is(target error) bool {
	return target == e.classification
}

// MCPAccessStore resolves the project-slug access boundary used by canonical
// MCP todo-link mutations.
type MCPAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// LinkReadStore provides the outbound and inbound post-mutation projections
// required by canonical MCP todo-link mutation results.
type LinkReadStore interface {
	ListLinksForTodo(
		ctx context.Context,
		projectID int64,
		localID int64,
		mode store.Mode,
	) ([]store.TodoLinkTarget, error)

	ListBacklinksForTodo(
		ctx context.Context,
		projectID int64,
		localID int64,
		mode store.Mode,
	) ([]store.TodoLinkTarget, error)
}

// MCPMutationServiceDependencies names the access, source, persistence, and
// post-mutation read capabilities used by canonical MCP todo-link mutations.
type MCPMutationServiceDependencies struct {
	Access    MCPAccessStore
	Sources   SourceLookupStore
	Mutations MutationStore
	Links     LinkReadStore
}

// MCPMutationService owns MCP project access, the source gate, directed
// persistence, and ordered post-mutation reads. It deliberately has no
// publication or transport dependency.
type MCPMutationService struct {
	access    MCPAccessStore
	sources   SourceLookupStore
	mutations MutationStore
	links     LinkReadStore
}

func NewMCPMutationService(deps MCPMutationServiceDependencies) *MCPMutationService {
	return &MCPMutationService{
		access:    deps.Access,
		sources:   deps.Sources,
		mutations: deps.Mutations,
		links:     deps.Links,
	}
}

// MCPMutationTarget contains only the slug, project-local source identity, and
// mode needed to prepare an MCP todo-link mutation capability.
type MCPMutationTarget struct {
	ProjectSlug   string
	SourceLocalID int64
	Mode          store.Mode
}

// PreparedMCPMutation binds the exact request context and resolved project,
// source, and mode identities to subsequent directed link mutations.
type PreparedMCPMutation struct {
	ctx           context.Context
	service       *MCPMutationService
	projectID     int64
	sourceLocalID int64
	mode          store.Mode
}

// Prepare preserves the characterized MCP order: project-slug access followed
// by the source-todo existence/access gate. The returned source todo is
// intentionally discarded; the adapter target remains the source identity.
func (s *MCPMutationService) Prepare(
	ctx context.Context,
	target MCPMutationTarget,
) (*PreparedMCPMutation, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}

	if _, err := s.sources.GetTodoByLocalID(
		ctx,
		projectContext.Project.ID,
		target.SourceLocalID,
		target.Mode,
	); err != nil {
		return nil, &mcpMutationStageError{
			classification: ErrMCPSourceLookupFailed,
			cause:          err,
		}
	}

	return &PreparedMCPMutation{
		ctx:           ctx,
		service:       s,
		projectID:     projectContext.Project.ID,
		sourceLocalID: target.SourceLocalID,
		mode:          target.Mode,
	}, nil
}

// LinkSet contains the post-mutation domain values used by MCP transport
// projection. The service returns the read slices without copying them.
type LinkSet struct {
	Outbound []store.TodoLinkTarget
	Inbound  []store.TodoLinkTarget
}

// Add persists one adapter-prepared directed link, then reads outbound links
// followed by inbound backlinks for the bound source todo.
func (p *PreparedMCPMutation) Add(command AddCommand) (LinkSet, error) {
	if err := p.service.mutations.AddLink(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		command.TargetLocalID,
		command.LinkType,
		p.mode,
	); err != nil {
		return LinkSet{}, err
	}

	outbound, err := p.service.links.ListLinksForTodo(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		p.mode,
	)
	if err != nil {
		return LinkSet{}, &mcpMutationStageError{
			classification: ErrMCPProjectionFailed,
			cause:          err,
		}
	}

	inbound, err := p.service.links.ListBacklinksForTodo(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		p.mode,
	)
	if err != nil {
		return LinkSet{}, &mcpMutationStageError{
			classification: ErrMCPProjectionFailed,
			cause:          err,
		}
	}

	return LinkSet{Outbound: outbound, Inbound: inbound}, nil
}

// Remove persists one adapter-prepared directed link removal, then reads
// outbound links followed by inbound backlinks for the bound source todo.
func (p *PreparedMCPMutation) Remove(command RemoveCommand) (LinkSet, error) {
	if err := p.service.mutations.RemoveLink(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		command.TargetLocalID,
		p.mode,
	); err != nil {
		return LinkSet{}, err
	}

	outbound, err := p.service.links.ListLinksForTodo(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		p.mode,
	)
	if err != nil {
		return LinkSet{}, &mcpMutationStageError{
			classification: ErrMCPProjectionFailed,
			cause:          err,
		}
	}

	inbound, err := p.service.links.ListBacklinksForTodo(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		p.mode,
	)
	if err != nil {
		return LinkSet{}, &mcpMutationStageError{
			classification: ErrMCPProjectionFailed,
			cause:          err,
		}
	}

	return LinkSet{Outbound: outbound, Inbound: inbound}, nil
}
