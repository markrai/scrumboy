package sprint

import "context"

// RESTDeletionPublisher exposes the semantic invalidation produced by a
// successful canonical REST sprint deletion. The HTTP adapter remains
// responsible for translating it to the runtime refresh reason.
type RESTDeletionPublisher interface {
	PublishSprintDeleted(ctx context.Context, projectID int64)
}

type nopRESTDeletionPublisher struct{}

func (nopRESTDeletionPublisher) PublishSprintDeleted(context.Context, int64) {}

// RESTDeletionServiceDependencies names the fresh-role, deletion-write, and
// semantic publication capabilities used by canonical REST sprint deletion.
// Target lookup is intentionally absent because the shared REST item route has
// already completed that existence/project gate before deletion preparation.
type RESTDeletionServiceDependencies struct {
	Roles     RoleStore
	Deletions DeletionStore
	Publisher RESTDeletionPublisher
}

// RESTDeletionService owns canonical REST actor/fresh-role authorization,
// deletion sequencing, and post-write semantic publication.
type RESTDeletionService struct {
	roles     RoleStore
	deletions DeletionStore
	publisher RESTDeletionPublisher
}

func NewRESTDeletionService(deps RESTDeletionServiceDependencies) *RESTDeletionService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTDeletionPublisher{}
	}
	return &RESTDeletionService{
		roles:     deps.Roles,
		deletions: deps.Deletions,
		publisher: publisher,
	}
}

// PreparedRESTDelete binds the exact authorized context and copied numeric
// identities to one subsequent REST sprint deletion.
type PreparedRESTDelete struct {
	ctx       context.Context
	service   *RESTDeletionService
	projectID int64
	sprintID  int64
}

// PrepareDelete preserves the DELETE-specific actor and fresh-role gate after
// the adapter-owned shared item-route target read and project comparison.
func (s *RESTDeletionService) PrepareDelete(
	ctx context.Context,
	target DeletionTarget,
) (*PreparedRESTDelete, error) {
	if err := authorizeRESTLifecycleMutation(ctx, s.roles, target.ProjectID); err != nil {
		return nil, err
	}
	return &PreparedRESTDelete{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
		sprintID:  target.SprintID,
	}, nil
}

// Delete performs exactly one project-scoped deletion and publishes only after
// persistence succeeds.
func (p *PreparedRESTDelete) Delete() error {
	if err := p.service.deletions.DeleteSprint(p.ctx, p.projectID, p.sprintID); err != nil {
		return err
	}
	p.service.publisher.PublishSprintDeleted(p.ctx, p.projectID)
	return nil
}
