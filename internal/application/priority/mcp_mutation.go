package priority

import (
	"context"
	"errors"
	"fmt"

	"scrumboy/internal/store"
)

var ErrPriorityProjectionFailed = errors.New("priority mutation projection failed")

// ErrPriorityProjectionTierMissing identifies the internal inconsistency in
// which a successful update is absent from the priority list returned
// immediately afterward. It also satisfies ErrPriorityProjectionFailed.
var ErrPriorityProjectionTierMissing = errors.New("updated priority tier missing from projection")

// priorityProjectionError marks failures that occur after a successful MCP
// update mutation. Its cause remains available so adapters can preserve their
// established read-error mapping while distinguishing projection
// inconsistencies.
type priorityProjectionError struct {
	message        string
	cause          error
	classification error
}

func (e *priorityProjectionError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}

func (e *priorityProjectionError) Unwrap() error {
	return e.cause
}

func (e *priorityProjectionError) Is(target error) bool {
	return target == ErrPriorityProjectionFailed || target == e.classification
}

// MCPMutationAccessStore resolves the project-slug access boundary used by
// MCP priority-tier mutations.
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
	Priority  PriorityReadStore
}

// MCPMutationService owns MCP project access, role authorization,
// priority-tier persistence, and update post-read projection. It
// deliberately has no refresh or event dependency.
type MCPMutationService struct {
	access    MCPMutationAccessStore
	mutations MutationStore
	priority  PriorityReadStore
}

func NewMCPMutationService(deps MCPMutationServiceDependencies) *MCPMutationService {
	return &MCPMutationService{
		access:    deps.Access,
		mutations: deps.Mutations,
		priority:  deps.Priority,
	}
}

// MCPMutationTarget identifies the slug whose access must be resolved after
// transport authentication, capability checks, and semantic validation.
type MCPMutationTarget struct {
	ProjectSlug string
	Mode        store.Mode
}

// PreparedMCPMutation binds the exact access context and resolved project ID
// to subsequent priority-tier mutations.
type PreparedMCPMutation struct {
	ctx       context.Context
	service   *MCPMutationService
	projectID int64
}

// Prepare resolves slug access exactly once and authorizes from the role in
// the returned ProjectContext. Unlike REST, MCP performs no second role
// lookup.
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

// Create persists one adapter-prepared command and returns the created tier
// without performing a projection read or publishing realtime events.
func (p *PreparedMCPMutation) Create(command CreateCommand) (store.PriorityTier, error) {
	tier, err := p.service.mutations.AddPriorityTier(p.ctx, p.projectID, command.Name)
	if err != nil {
		return store.PriorityTier{}, err
	}
	return tier, nil
}

// Update persists before reading the priority list used by MCP response
// projection. Post-write read and lookup failures are classified separately
// while retaining any underlying store cause.
func (p *PreparedMCPMutation) Update(command UpdateCommand) (store.PriorityTier, error) {
	if err := p.service.mutations.UpdatePriorityTier(
		p.ctx,
		p.projectID,
		command.Key,
		command.Name,
		command.Color,
	); err != nil {
		return store.PriorityTier{}, err
	}

	tiers, err := p.service.priority.GetProjectPriorities(p.ctx, p.projectID)
	if err != nil {
		return store.PriorityTier{}, &priorityProjectionError{
			message: "read updated priority tiers",
			cause:   err,
		}
	}
	for _, tier := range tiers {
		if tier.Key == command.Key {
			return tier, nil
		}
	}
	return store.PriorityTier{}, &priorityProjectionError{
		message:        fmt.Sprintf("updated priority tier %q not found in post-read", command.Key),
		classification: ErrPriorityProjectionTierMissing,
	}
}

// Delete persists one adapter-prepared command without a projection read or
// realtime publication.
func (p *PreparedMCPMutation) Delete(command DeleteCommand) error {
	return p.service.mutations.DeletePriorityTier(p.ctx, p.projectID, command.Key)
}
