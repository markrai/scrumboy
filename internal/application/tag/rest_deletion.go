package tag

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrInvalidDeletionProjectKind reports an unknown project-kind value in an
	// already prepared REST deletion command.
	ErrInvalidDeletionProjectKind = errors.New("invalid tag deletion project kind")
	// ErrNameDeletionNotAllowed reports the characterized creator-owned
	// temporary-board rejection for REST name-addressed deletion.
	ErrNameDeletionNotAllowed = errors.New("tag name deletion not allowed for project kind")
)

// RESTDeletionPublisher publishes the semantic invalidation required after a
// successful REST tag deletion. An empty name identifies an ID-addressed
// deletion.
type RESTDeletionPublisher interface {
	PublishTagDeleted(ctx context.Context, projectID int64, name string)
}

type nopRESTDeletionPublisher struct{}

func (nopRESTDeletionPublisher) PublishTagDeleted(context.Context, int64, string) {}

// RESTDeletionServiceDependencies contains only the lookup, deletion, and
// publication capabilities required by REST tag deletion operations.
type RESTDeletionServiceDependencies struct {
	MineID        MineIDDeletionStore
	MineName      MineNameDeletionStore
	DurableID     DurableProjectIDDeletionStore
	Rows          LegacyRowDeletionStore
	BoardNames    BoardScopedTagNameReadStore
	PersonalNames PersonalTagNameReadStore
	Publisher     RESTDeletionPublisher
}

// RESTDeletionService prepares REST-specific tag deletion operations and owns
// their post-success refresh fanout.
type RESTDeletionService struct {
	mineID        MineIDDeletionStore
	mineName      MineNameDeletionStore
	durableID     DurableProjectIDDeletionStore
	rows          LegacyRowDeletionStore
	boardNames    BoardScopedTagNameReadStore
	personalNames PersonalTagNameReadStore
	publisher     RESTDeletionPublisher
}

// NewRESTDeletionService constructs the additive REST deletion service.
func NewRESTDeletionService(deps RESTDeletionServiceDependencies) *RESTDeletionService {
	publisher := deps.Publisher
	if publisher == nil {
		publisher = nopRESTDeletionPublisher{}
	}

	return &RESTDeletionService{
		mineID:        deps.MineID,
		mineName:      deps.MineName,
		durableID:     deps.DurableID,
		rows:          deps.Rows,
		boardNames:    deps.BoardNames,
		personalNames: deps.PersonalNames,
		publisher:     publisher,
	}
}

// PreparedRESTMineIDDeletion binds one caller-owned personal tag deletion.
type PreparedRESTMineIDDeletion struct {
	ctx     context.Context
	service *RESTDeletionService
	actorID int64
	tagID   int64
}

// PrepareMineID binds a global mine deletion without adding a pre-read or
// project origin.
func (s *RESTDeletionService) PrepareMineID(
	ctx context.Context,
	command MineIDDeleteCommand,
) *PreparedRESTMineIDDeletion {
	return &PreparedRESTMineIDDeletion{
		ctx:     ctx,
		service: s,
		actorID: command.ActorUserID,
		tagID:   command.TagID,
	}
}

// Delete performs the personal deletion once and publishes only the distinct
// affected projects returned by the store.
func (p *PreparedRESTMineIDDeletion) Delete() error {
	affected, err := p.service.mineID.DeleteMyTagByID(p.ctx, p.actorID, p.tagID)
	if err != nil {
		return err
	}

	p.service.publishAffected(p.ctx, nil, NewDeletionResult(affected), "")
	return nil
}

// PreparedRESTProjectIDDeletion binds one resolved REST project-ID deletion,
// including a copied optional actor identity.
type PreparedRESTProjectIDDeletion struct {
	ctx     context.Context
	service *RESTDeletionService
	project ResolvedProject
	actorID *int64
	tagID   int64
}

// PrepareProjectID preserves durable actor requirements while retaining the
// optional actor used by both temporary-board paths.
func (s *RESTDeletionService) PrepareProjectID(
	ctx context.Context,
	command ProjectIDDeleteCommand,
) (*PreparedRESTProjectIDDeletion, error) {
	switch command.Project.Kind {
	case DurableProject:
		if command.ActorUserID == nil {
			return nil, ErrActorRequired
		}
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		// Temporary REST deletion passes an optional actor through to the
		// legacy row operation. The store remains authoritative.
	default:
		return nil, ErrInvalidDeletionProjectKind
	}

	return &PreparedRESTProjectIDDeletion{
		ctx:     ctx,
		service: s,
		project: command.Project,
		actorID: cloneInt64(command.ActorUserID),
		tagID:   command.TagID,
	}, nil
}

// Delete dispatches exactly one durable or temporary ID deletion and
// publishes the characterized fanout only after success.
func (p *PreparedRESTProjectIDDeletion) Delete() error {
	switch p.project.Kind {
	case DurableProject:
		affected, err := p.service.durableID.DeleteTagForDurableProjectByID(
			p.ctx,
			p.project.ProjectID,
			*p.actorID,
			p.tagID,
		)
		if err != nil {
			return err
		}
		origin := p.project.ProjectID
		p.service.publishAffected(p.ctx, &origin, NewDeletionResult(affected), "")
		return nil
	case CreatorOwnedTemporaryBoard, AnonymousTemporaryBoard:
		actorID := int64(0)
		if p.actorID != nil {
			actorID = *p.actorID
		}
		isAnonymousBoard := p.project.Kind == AnonymousTemporaryBoard
		if err := p.service.rows.DeleteTag(p.ctx, actorID, p.tagID, isAnonymousBoard); err != nil {
			return err
		}
		p.service.publisher.PublishTagDeleted(p.ctx, p.project.ProjectID, "")
		return nil
	default:
		return ErrInvalidDeletionProjectKind
	}
}

// PreparedRESTProjectNameDeletion binds one resolved REST project-name
// deletion, including a copied optional actor identity and exact name.
type PreparedRESTProjectNameDeletion struct {
	ctx     context.Context
	service *RESTDeletionService
	project ResolvedProject
	actorID *int64
	name    string
}

// PrepareProjectName preserves durable personal-name deletion, rejects the
// creator-owned temporary name route, and retains optional anonymous fallback
// identity.
func (s *RESTDeletionService) PrepareProjectName(
	ctx context.Context,
	command ProjectNameDeleteCommand,
) (*PreparedRESTProjectNameDeletion, error) {
	switch command.Project.Kind {
	case DurableProject:
		if command.ActorUserID == nil {
			return nil, ErrActorRequired
		}
	case CreatorOwnedTemporaryBoard:
		return nil, ErrNameDeletionNotAllowed
	case AnonymousTemporaryBoard:
		// Actor absence is checked only if the board-scoped lookup misses.
	default:
		return nil, ErrInvalidDeletionProjectKind
	}

	return &PreparedRESTProjectNameDeletion{
		ctx:     ctx,
		service: s,
		project: command.Project,
		actorID: cloneInt64(command.ActorUserID),
		name:    command.Name,
	}, nil
}

// Delete performs exactly one durable personal-name or anonymous row deletion
// and publishes only after that deletion succeeds.
func (p *PreparedRESTProjectNameDeletion) Delete() error {
	switch p.project.Kind {
	case DurableProject:
		affected, err := p.service.mineName.DeleteMyTagByName(
			p.ctx,
			p.project.ProjectID,
			*p.actorID,
			p.name,
		)
		if err != nil {
			return err
		}
		origin := p.project.ProjectID
		p.service.publishAffected(p.ctx, &origin, NewDeletionResult(affected), p.name)
		return nil
	case AnonymousTemporaryBoard:
		return p.deleteAnonymousName()
	case CreatorOwnedTemporaryBoard:
		return ErrNameDeletionNotAllowed
	default:
		return ErrInvalidDeletionProjectKind
	}
}

func (p *PreparedRESTProjectNameDeletion) deleteAnonymousName() error {
	tagID, err := p.service.boardNames.GetBoardScopedTagIDByName(
		p.ctx,
		p.project.ProjectID,
		p.name,
	)
	if err == nil {
		if err := p.service.rows.DeleteTag(p.ctx, 0, tagID, true); err != nil {
			return err
		}
		p.service.publisher.PublishTagDeleted(p.ctx, p.project.ProjectID, p.name)
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if p.actorID == nil {
		return ErrActorRequired
	}

	tagID, err = p.service.personalNames.GetTagIDByName(p.ctx, *p.actorID, p.name)
	if err != nil {
		return err
	}
	if err := p.service.rows.DeleteTag(p.ctx, *p.actorID, tagID, true); err != nil {
		return err
	}
	p.service.publisher.PublishTagDeleted(p.ctx, p.project.ProjectID, p.name)
	return nil
}

func (s *RESTDeletionService) publishAffected(
	ctx context.Context,
	originProjectID *int64,
	result DeletionResult,
	name string,
) {
	seen := make(map[int64]struct{})
	publish := func(projectID int64) {
		if _, exists := seen[projectID]; exists {
			return
		}
		seen[projectID] = struct{}{}
		s.publisher.PublishTagDeleted(ctx, projectID, name)
	}

	if originProjectID != nil {
		publish(*originProjectID)
	}
	for _, projectID := range result.AffectedProjectIDs() {
		publish(projectID)
	}
}
