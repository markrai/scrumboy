package project

import (
	"context"
	"time"

	"scrumboy/internal/projectcolor"
	"scrumboy/internal/store"
)

// RESTUpdatePublisher publishes the semantic invalidation required after a
// successful non-empty REST project update.
type RESTUpdatePublisher interface {
	PublishProjectUpdated(ctx context.Context, projectID int64)
}

type nopRESTUpdatePublisher struct{}

func (nopRESTUpdatePublisher) PublishProjectUpdated(context.Context, int64) {}

// RESTUpdateServiceDependencies contains only the read, sequential mutation,
// and publication capabilities used by REST project update.
type RESTUpdateServiceDependencies struct {
	Projects  ProjectByIDReadStore
	Images    ProjectImageMutationStore
	Names     ProjectNameMutationStore
	Publisher RESTUpdatePublisher
}

// RESTUpdateService preserves REST's independent image/name transactions,
// post-read, partial-success behavior, and success publication.
type RESTUpdateService struct {
	projects  ProjectByIDReadStore
	images    ProjectImageMutationStore
	names     ProjectNameMutationStore
	publisher RESTUpdatePublisher
}

// NewRESTUpdateService constructs the additive REST update service.
func NewRESTUpdateService(deps RESTUpdateServiceDependencies) *RESTUpdateService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTUpdatePublisher{}
	}
	return &RESTUpdateService{
		projects:  deps.Projects,
		images:    deps.Images,
		names:     deps.Names,
		publisher: publisher,
	}
}

// PreparedRESTUpdate binds the mode-specific target decision and optional
// trusted actor before the adapter decodes the update body.
type PreparedRESTUpdate struct {
	ctx       context.Context
	service   *RESTUpdateService
	projectID int64
	actorID   *int64
}

// Prepare preserves the characterized Anonymous-Mode target read before body
// decoding. Full Mode preparation performs no project read.
func (s *RESTUpdateService) Prepare(
	ctx context.Context,
	target RESTUpdateTarget,
) (*PreparedRESTUpdate, error) {
	if target.Mode == store.ModeAnonymous {
		project, err := s.projects.GetProject(ctx, target.ProjectID)
		if err != nil {
			return nil, err
		}
		if project.ExpiresAt == nil ||
			project.CreatorUserID != nil ||
			!project.ExpiresAt.After(time.Now().UTC()) {
			return nil, store.ErrNotFound
		}
	}

	var actorID *int64
	if userID, ok := store.UserIDFromContext(ctx); ok {
		actorID = &userID
	}
	return &PreparedRESTUpdate{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
		actorID:   cloneInt64(actorID),
	}, nil
}

// Update executes image then name as separate store mutations, performs the
// required post-read, and publishes only after a successful non-empty update.
func (p *PreparedRESTUpdate) Update(command RESTUpdateCommand) (store.Project, error) {
	name := cloneString(command.Name)
	image := cloneString(command.Image)

	if image != nil {
		if p.actorID == nil {
			return store.Project{}, ErrActorRequired
		}
		dominantColor := projectcolor.ExtractFromDataURL(*image)
		if err := p.service.images.UpdateProjectImage(
			p.ctx,
			p.projectID,
			*p.actorID,
			cloneString(image),
			dominantColor,
		); err != nil {
			return store.Project{}, err
		}
	}

	if name != nil {
		actorID := int64(0)
		if p.actorID != nil {
			actorID = *p.actorID
		}
		if err := p.service.names.UpdateProjectName(
			p.ctx,
			p.projectID,
			actorID,
			*name,
		); err != nil {
			return store.Project{}, err
		}
	}

	project, err := p.service.projects.GetProject(p.ctx, p.projectID)
	if err != nil {
		return store.Project{}, err
	}
	if image != nil || name != nil {
		p.service.publisher.PublishProjectUpdated(p.ctx, p.projectID)
	}
	return cloneProject(project), nil
}
