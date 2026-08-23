package project

import (
	"context"

	"scrumboy/internal/store"
)

// RESTDeletionPublisher publishes the semantic project-deleted effects driven
// by the committed store snapshot.
type RESTDeletionPublisher interface {
	PublishProjectDeleted(ctx context.Context, snapshot store.DeletedProjectSnapshot)
}

type nopRESTDeletionPublisher struct{}

func (nopRESTDeletionPublisher) PublishProjectDeleted(context.Context, store.DeletedProjectSnapshot) {
}

// RESTDeletionServiceDependencies contains only the deletion and publication
// capabilities required by numeric REST project deletion.
type RESTDeletionServiceDependencies struct {
	Projects  ProjectDeletionStore
	Publisher RESTDeletionPublisher
}

// RESTDeletionService owns exactly-one deletion followed by REST-only semantic
// publication from the committed snapshot.
type RESTDeletionService struct {
	projects  ProjectDeletionStore
	publisher RESTDeletionPublisher
}

// NewRESTDeletionService constructs the additive REST deletion service.
func NewRESTDeletionService(deps RESTDeletionServiceDependencies) *RESTDeletionService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTDeletionPublisher{}
	}
	return &RESTDeletionService{projects: deps.Projects, publisher: publisher}
}

// PreparedRESTDeletion binds the exact numeric identity and actor for one REST
// deletion without a pre-read or authorization check.
type PreparedRESTDeletion struct {
	ctx         context.Context
	service     *RESTDeletionService
	projectID   int64
	actorUserID int64
}

// Prepare binds adapter-established scalar values without store access.
func (s *RESTDeletionService) Prepare(
	ctx context.Context,
	command RESTDeletionCommand,
) *PreparedRESTDeletion {
	return &PreparedRESTDeletion{
		ctx:         ctx,
		service:     s,
		projectID:   command.ProjectID,
		actorUserID: command.ActorUserID,
	}
}

// Delete performs one store deletion and publishes its committed snapshot only
// after success. The publisher remains void/best-effort by compatibility.
func (p *PreparedRESTDeletion) Delete() error {
	deleted, err := p.service.projects.DeleteProject(p.ctx, p.projectID, p.actorUserID)
	if err != nil {
		return err
	}
	p.service.publisher.PublishProjectDeleted(p.ctx, cloneDeletedProjectSnapshot(deleted))
	return nil
}
