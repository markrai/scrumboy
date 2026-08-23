package tag

import (
	"context"
	"errors"
)

// ErrInvalidProjectKind reports an unknown project-kind value in an already
// prepared REST project command.
var ErrInvalidProjectKind = errors.New("invalid tag color project kind")

// RESTColorPublisher publishes the semantic invalidation required after a
// successful REST project color mutation. An empty name identifies an
// ID-addressed mutation.
type RESTColorPublisher interface {
	PublishTagColorUpdated(ctx context.Context, projectID int64, name string)
}

type nopRESTColorPublisher struct{}

func (nopRESTColorPublisher) PublishTagColorUpdated(context.Context, int64, string) {}

// RESTColorServiceDependencies contains only the persistence and publication
// capabilities used by REST tag color operations.
type RESTColorServiceDependencies struct {
	MineColor          MineColorStore
	DurableIDColor     DurableProjectIDColorStore
	TemporaryIDColor   TemporaryBoardIDColorStore
	DurableNameColor   DurableProjectNameColorStore
	TemporaryNameColor TemporaryBoardNameColorStore
	Publisher          RESTColorPublisher
}

// RESTColorService prepares REST-specific tag color operations. It performs no
// project lookup or tag read because those are not part of the current REST
// mutation sequences.
type RESTColorService struct {
	mineColor          MineColorStore
	durableIDColor     DurableProjectIDColorStore
	temporaryIDColor   TemporaryBoardIDColorStore
	durableNameColor   DurableProjectNameColorStore
	temporaryNameColor TemporaryBoardNameColorStore
	publisher          RESTColorPublisher
}

// NewRESTColorService constructs the additive REST color application service.
func NewRESTColorService(deps RESTColorServiceDependencies) *RESTColorService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTColorPublisher{}
	}

	return &RESTColorService{
		mineColor:          deps.MineColor,
		durableIDColor:     deps.DurableIDColor,
		temporaryIDColor:   deps.TemporaryIDColor,
		durableNameColor:   deps.DurableNameColor,
		temporaryNameColor: deps.TemporaryNameColor,
		publisher:          publisher,
	}
}

// PreparedRESTMineIDColor binds the exact context and adapter-prepared mine
// command used by one personal-library color mutation.
type PreparedRESTMineIDColor struct {
	ctx     context.Context
	service *RESTColorService
	actorID int64
	tagID   int64
	color   ColorIntent
}

// PrepareMineID binds a mine color command without adding reads,
// authorization, or publication.
func (s *RESTColorService) PrepareMineID(
	ctx context.Context,
	command MineIDColorCommand,
) *PreparedRESTMineIDColor {
	return &PreparedRESTMineIDColor{
		ctx:     ctx,
		service: s,
		actorID: command.ActorUserID,
		tagID:   command.TagID,
		color:   command.Color,
	}
}

// Update performs the prepared mine color mutation exactly once and never
// publishes a project invalidation.
func (p *PreparedRESTMineIDColor) Update() error {
	return p.service.mineColor.UpdateMyTagColor(
		p.ctx,
		p.actorID,
		p.tagID,
		p.color.StoreValue(),
	)
}

// PreparedRESTProjectIDColor binds one resolved REST project-ID color
// operation, including a copied optional viewer identity.
type PreparedRESTProjectIDColor struct {
	ctx      context.Context
	service  *RESTColorService
	project  ResolvedProject
	viewerID *int64
	tagID    int64
	color    ColorIntent
}

// PrepareProjectID validates only project-kind combinations owned by the
// application boundary and binds all adapter-prepared values.
func (s *RESTColorService) PrepareProjectID(
	ctx context.Context,
	command ProjectIDColorCommand,
) (*PreparedRESTProjectIDColor, error) {
	switch command.Project.Kind {
	case DurableProject:
		if command.ViewerUserID == nil {
			return nil, ErrActorRequired
		}
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		// Temporary REST paths preserve their optional viewer identity.
	default:
		return nil, ErrInvalidProjectKind
	}

	return &PreparedRESTProjectIDColor{
		ctx:      ctx,
		service:  s,
		project:  command.Project,
		viewerID: cloneInt64(command.ViewerUserID),
		tagID:    command.TagID,
		color:    command.Color,
	}, nil
}

// Update dispatches to the durable or temporary ID capability exactly once,
// then publishes the project invalidation after success.
func (p *PreparedRESTProjectIDColor) Update() error {
	var err error
	switch p.project.Kind {
	case DurableProject:
		err = p.service.durableIDColor.UpdateTagColorForDurableProjectByID(
			p.ctx,
			p.project.ProjectID,
			*p.viewerID,
			p.tagID,
			p.color.StoreValue(),
		)
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		err = p.service.temporaryIDColor.UpdateTagColorForTemporaryBoard(
			p.ctx,
			p.project.ProjectID,
			cloneInt64(p.viewerID),
			p.tagID,
			p.color.StoreValue(),
		)
	}
	if err != nil {
		return err
	}

	p.service.publisher.PublishTagColorUpdated(p.ctx, p.project.ProjectID, "")
	return nil
}

// PreparedRESTProjectNameColor binds one resolved REST project-name color
// operation, including a copied optional viewer identity.
type PreparedRESTProjectNameColor struct {
	ctx      context.Context
	service  *RESTColorService
	project  ResolvedProject
	viewerID *int64
	name     string
	color    ColorIntent
}

// PrepareProjectName validates only project-kind combinations owned by the
// application boundary and binds all adapter-prepared values.
func (s *RESTColorService) PrepareProjectName(
	ctx context.Context,
	command ProjectNameColorCommand,
) (*PreparedRESTProjectNameColor, error) {
	switch command.Project.Kind {
	case DurableProject:
		if command.ViewerUserID == nil {
			return nil, ErrActorRequired
		}
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		// Temporary REST paths preserve their optional viewer identity.
	default:
		return nil, ErrInvalidProjectKind
	}

	return &PreparedRESTProjectNameColor{
		ctx:      ctx,
		service:  s,
		project:  command.Project,
		viewerID: cloneInt64(command.ViewerUserID),
		name:     command.Name,
		color:    command.Color,
	}, nil
}

// Update dispatches to the durable or temporary name capability exactly once,
// then publishes the exact prepared name after success.
func (p *PreparedRESTProjectNameColor) Update() error {
	var err error
	switch p.project.Kind {
	case DurableProject:
		err = p.service.durableNameColor.SetViewerTagColorByName(
			p.ctx,
			p.project.ProjectID,
			*p.viewerID,
			p.name,
			p.color.StoreValue(),
		)
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		err = p.service.temporaryNameColor.UpdateTagColorForProject(
			p.ctx,
			p.project.ProjectID,
			cloneInt64(p.viewerID),
			p.name,
			p.color.StoreValue(),
			true,
		)
	}
	if err != nil {
		return err
	}

	p.service.publisher.PublishTagColorUpdated(p.ctx, p.project.ProjectID, p.name)
	return nil
}
