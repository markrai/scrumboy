package board

import (
	"context"

	"scrumboy/internal/store"
)

// SlugReadTarget identifies the normalized slug whose read access must be
// resolved before transport query validation runs.
type SlugReadTarget struct {
	Slug string
	Mode store.Mode
}

// SlugReadAccessStore is the persistence capability required to resolve an
// authorized project context for a slug-based board read.
type SlugReadAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// SlugReadSprintStore is the persistence capability required by the initial
// slug read's data-dependent sprint validation.
type SlugReadSprintStore interface {
	HasSprints(ctx context.Context, projectID int64) (bool, error)
}

// PreparedSlugRead is a short-lived, request-scoped capability that binds the
// context used to authorize a slug read to its subsequent store operations.
type PreparedSlugRead struct {
	ctx            context.Context
	initial        *Service
	lane           *LaneService
	sprints        SlugReadSprintStore
	projectContext store.ProjectContext
}

// PrepareSlugRead resolves access before transport query validation and owns a
// value copy of the resulting project context.
func (s *ReadService) PrepareSlugRead(
	ctx context.Context,
	target SlugReadTarget,
) (*PreparedSlugRead, error) {
	pc, err := s.slugAccess.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return nil, err
	}

	return &PreparedSlugRead{
		ctx:            ctx,
		initial:        s.initial,
		lane:           s.lane,
		sprints:        s.slugSprints,
		projectContext: pc,
	}, nil
}

// HasSprints reports whether the authorized project has any sprints.
func (r *PreparedSlugRead) HasSprints() (bool, error) {
	return r.sprints.HasSprints(r.ctx, r.projectContext.Project.ID)
}

// ReadInitial executes the initial board read with the authorization context
// and owned project context.
func (r *PreparedSlugRead) ReadInitial(query Query) (Result, error) {
	return r.initial.ReadInitial(r.ctx, &r.projectContext, query)
}

// ReadLane executes a lane continuation read with the authorization context
// and owned project context.
func (r *PreparedSlugRead) ReadLane(query LaneQuery) (LaneResult, error) {
	return r.lane.Read(r.ctx, &r.projectContext, query)
}
