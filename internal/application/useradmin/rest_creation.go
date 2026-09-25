package useradmin

import (
	"context"

	"scrumboy/internal/store"
)

// RESTCreationServiceDependencies contains only the coherent persistence
// operation that owns local-user creation and its transactional seeds.
type RESTCreationServiceDependencies struct {
	Creations UserCreationStore
}

// RESTCreationService forwards one already-decoded command to the existing
// store operation without moving normalization, hashing, or seeding outward.
type RESTCreationService struct {
	creations UserCreationStore
}

// NewRESTCreationService constructs the REST user-creation service.
func NewRESTCreationService(deps RESTCreationServiceDependencies) *RESTCreationService {
	return &RESTCreationService{creations: deps.Creations}
}

// Create invokes the store-owned creation transaction exactly once and
// returns its established user value or error unchanged.
func (s *RESTCreationService) Create(
	ctx context.Context,
	command CreateCommand,
) (store.User, error) {
	return s.creations.CreateUser(
		ctx,
		command.Email,
		command.Password,
		command.Name,
	)
}
