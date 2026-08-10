package todolink

import (
	"context"

	"scrumboy/internal/store"
)

// RESTMutationPublisher exposes the single semantic invalidation produced by
// successful canonical REST todo-link mutations. The HTTP adapter remains
// responsible for translating it to the runtime refresh reason.
type RESTMutationPublisher interface {
	PublishTodoLinksUpdated(ctx context.Context, projectID int64)
}

type nopRESTMutationPublisher struct{}

func (nopRESTMutationPublisher) PublishTodoLinksUpdated(context.Context, int64) {}

// RESTMutationServiceDependencies names the source gate, persistence, and
// ancillary publication capabilities used by canonical REST link mutations.
type RESTMutationServiceDependencies struct {
	Sources   SourceLookupStore
	Mutations MutationStore
	Publisher RESTMutationPublisher
}

// RESTMutationService owns source-gate preparation and post-persistence
// publication. Transport parsing and public validation remain adapter-owned.
type RESTMutationService struct {
	sources   SourceLookupStore
	mutations MutationStore
	publisher RESTMutationPublisher
}

func NewRESTMutationService(deps RESTMutationServiceDependencies) *RESTMutationService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTMutationPublisher{}
	}
	return &RESTMutationService{
		sources:   deps.Sources,
		mutations: deps.Mutations,
		publisher: publisher,
	}
}

// ResolvedRESTMutationTarget contains the project and source identities already
// resolved by the REST router, plus the current persistence mode.
type ResolvedRESTMutationTarget struct {
	ProjectID     int64
	SourceLocalID int64
	Mode          store.Mode
}

// PreparedRESTMutation binds the exact request context and a value copy of the
// resolved target to subsequent directed link mutations.
type PreparedRESTMutation struct {
	ctx           context.Context
	service       *RESTMutationService
	projectID     int64
	sourceLocalID int64
	mode          store.Mode
}

// Prepare performs the characterized source existence/access gate. The
// returned todo is intentionally discarded: persistence remains authoritative
// for endpoint and write policy, while the resolved target supplies identity.
func (s *RESTMutationService) Prepare(
	ctx context.Context,
	target ResolvedRESTMutationTarget,
) (*PreparedRESTMutation, error) {
	if _, err := s.sources.GetTodoByLocalID(
		ctx,
		target.ProjectID,
		target.SourceLocalID,
		target.Mode,
	); err != nil {
		return nil, err
	}

	return &PreparedRESTMutation{
		ctx:           ctx,
		service:       s,
		projectID:     target.ProjectID,
		sourceLocalID: target.SourceLocalID,
		mode:          target.Mode,
	}, nil
}

// Add persists one adapter-prepared directed link and publishes the semantic
// REST invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Add(command AddCommand) error {
	if err := p.service.mutations.AddLink(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		command.TargetLocalID,
		command.LinkType,
		p.mode,
	); err != nil {
		return err
	}

	p.service.publisher.PublishTodoLinksUpdated(p.ctx, p.projectID)
	return nil
}

// Remove persists one adapter-prepared directed link removal and publishes the
// semantic REST invalidation only after persistence succeeds.
func (p *PreparedRESTMutation) Remove(command RemoveCommand) error {
	if err := p.service.mutations.RemoveLink(
		p.ctx,
		p.projectID,
		p.sourceLocalID,
		command.TargetLocalID,
		p.mode,
	); err != nil {
		return err
	}

	p.service.publisher.PublishTodoLinksUpdated(p.ctx, p.projectID)
	return nil
}
