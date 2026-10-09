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

type PublicationServiceOptions struct {
	Mutations             PublicationMutationStore
	Mode                  store.Mode
	PublicProjectsEnabled bool
}

type PublicationService struct {
	mutations             PublicationMutationStore
	mode                  store.Mode
	publicProjectsEnabled bool
}

func NewPublicationService(opts PublicationServiceOptions) *PublicationService {
	return &PublicationService{
		mutations:             opts.Mutations,
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
	return PublicationResult{
		ProjectID: state.ProjectID,
		Slug:      state.Slug,
		Enabled:   state.Enabled,
		Changed:   state.Changed,
	}, nil
}
