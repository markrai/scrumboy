package publicboard

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	ErrActorRequired                 = errors.New("publication mutation actor required")
	ErrPublicationCapabilityDisabled = errors.New("public project publication capability is disabled")
)

type PublicationMutationStore interface {
	UpdateProjectPublicViewing(ctx context.Context, projectID, actorUserID int64, enabled bool) (store.ProjectPublicationState, error)
}

// PublicationStatusStore reads the Maintainer-only publication status with
// the same eligibility and authorization rules as the mutation.
type PublicationStatusStore interface {
	GetProjectPublicationStatus(ctx context.Context, projectID, actorUserID int64) (store.ProjectPublicationStatus, error)
}

// PublicationRevoker is the synchronous, post-commit public-stream lifecycle
// seam. It deliberately accepts only the internal project key.
type PublicationRevoker interface {
	RevokePublicProject(projectID int64)
}

type PublicationServiceOptions struct {
	Mutations             PublicationMutationStore
	Status                PublicationStatusStore
	Revoker               PublicationRevoker
	Mode                  store.Mode
	PublicProjectsEnabled bool
}

type PublicationService struct {
	mutations             PublicationMutationStore
	status                PublicationStatusStore
	revoker               PublicationRevoker
	mode                  store.Mode
	publicProjectsEnabled bool
}

func NewPublicationService(opts PublicationServiceOptions) *PublicationService {
	return &PublicationService{
		mutations:             opts.Mutations,
		status:                opts.Status,
		revoker:               opts.Revoker,
		mode:                  opts.Mode,
		publicProjectsEnabled: opts.PublicProjectsEnabled,
	}
}

type PublicationCommand struct {
	ProjectID int64
	Enabled   bool
}

type PublicationResult struct {
	ProjectID int64
	Slug      string
	Enabled   bool
	Changed   bool
}

type PublicationStatus struct {
	ProjectID   int64
	Slug        string
	Enabled     bool
	Publishable bool
}

// GetPublication returns the publication status for an authenticated exact
// Maintainer, behind the same mode and operator gates as SetPublication.
func (s *PublicationService) GetPublication(ctx context.Context, projectID int64) (PublicationStatus, error) {
	if s.mode != store.ModeFull || !s.publicProjectsEnabled || s.status == nil {
		return PublicationStatus{}, ErrPublicationCapabilityDisabled
	}
	actorUserID, ok := store.UserIDFromContext(ctx)
	if !ok || actorUserID <= 0 {
		return PublicationStatus{}, ErrActorRequired
	}
	status, err := s.status.GetProjectPublicationStatus(ctx, projectID, actorUserID)
	if err != nil {
		return PublicationStatus{}, err
	}
	return PublicationStatus{
		ProjectID:   status.ProjectID,
		Slug:        status.Slug,
		Enabled:     status.Enabled,
		Publishable: status.Publishable,
	}, nil
}

// SetPublication applies the static operator gate before invoking the one
// authoritative store transaction. Changed is the Phase 3 post-commit seam:
// revocation can be published only after this method returns successfully.
func (s *PublicationService) SetPublication(ctx context.Context, command PublicationCommand) (PublicationResult, error) {
	if s.mode != store.ModeFull || !s.publicProjectsEnabled {
		return PublicationResult{}, ErrPublicationCapabilityDisabled
	}
	actorUserID, ok := store.UserIDFromContext(ctx)
	if !ok || actorUserID <= 0 {
		return PublicationResult{}, ErrActorRequired
	}
	state, err := s.mutations.UpdateProjectPublicViewing(ctx, command.ProjectID, actorUserID, command.Enabled)
	if err != nil {
		return PublicationResult{}, err
	}
	result := PublicationResult{
		ProjectID: state.ProjectID,
		Slug:      state.Slug,
		Enabled:   state.Enabled,
		Changed:   state.Changed,
	}
	if result.Changed && !result.Enabled && s.revoker != nil {
		s.revoker.RevokePublicProject(result.ProjectID)
	}
	return result, nil
}
