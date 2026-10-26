package customfield

import (
	"context"

	"scrumboy/internal/store"
)

// RESTService owns REST Maintainer authorization from the board-router
// ProjectContext role and the subsequent create/delete mutation. It performs
// no project-role lookup and publishes no refresh events.
type RESTService struct {
	mutations MutationStore
}

func NewRESTService(mutations MutationStore) *RESTService {
	return &RESTService{mutations: mutations}
}

// ResolvedRESTMutationTarget carries the project identity and role already
// resolved by the shared REST board router. Preparation performs no lookup.
type ResolvedRESTMutationTarget struct {
	ProjectID int64
	Role      store.ProjectRole
}

// PreparedRESTMutation binds the authorized project identity and request
// context to subsequent definition mutations.
type PreparedRESTMutation struct {
	ctx       context.Context
	service   *RESTService
	projectID int64
}

// Prepare checks the router-resolved role. Failed preparation returns
// ErrMaintainerRequired and no usable operation, so callers cannot mutate
// without authorization.
func (s *RESTService) Prepare(ctx context.Context, target ResolvedRESTMutationTarget) (*PreparedRESTMutation, error) {
	if err := authorizeMaintainer(target.Role); err != nil {
		return nil, err
	}
	return &PreparedRESTMutation{
		ctx:       ctx,
		service:   s,
		projectID: target.ProjectID,
	}, nil
}

func (p *PreparedRESTMutation) Create(command CreateCommand) (store.CustomField, error) {
	name, err := normalizeName(command.Name)
	if err != nil {
		return store.CustomField{}, err
	}
	typ, err := normalizeType(command.Type)
	if err != nil {
		return store.CustomField{}, err
	}
	return p.service.mutations.CreateCustomField(p.ctx, p.projectID, command.Key, name, typ)
}

func (p *PreparedRESTMutation) Delete(command DeleteCommand) error {
	key, err := normalizeDeleteKey(command.Key)
	if err != nil {
		return err
	}
	return p.service.mutations.DeleteCustomField(p.ctx, p.projectID, key)
}
