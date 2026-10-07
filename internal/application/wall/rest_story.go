package wall

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

type RESTStoryServiceDependencies struct {
	Roles     RESTWriterRoleStore
	Mutations StoryMutationStore
	Refresh   WallRefreshPublisher
}

// RESTStoryService applies the same fresh Contributor gate as every other
// durable Wall mutation while keeping Todo workflow authorization separate.
type RESTStoryService struct {
	roles     RESTWriterRoleStore
	mutations StoryMutationStore
	refresh   WallRefreshPublisher
}

func NewRESTStoryService(deps RESTStoryServiceDependencies) *RESTStoryService {
	return &RESTStoryService{roles: deps.Roles, mutations: deps.Mutations, refresh: deps.Refresh}
}

type PreparedRESTStoryMutation struct {
	writer      preparedRESTWriter
	service     *RESTStoryService
	executeOnce sync.Once
}

func (s *RESTStoryService) Prepare(mutationCtx, effectCtx context.Context, target ResolvedRESTTarget) (*PreparedRESTStoryMutation, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTStoryMutation{writer: writer, service: s}, nil
}

func (p *PreparedRESTStoryMutation) begin() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}
	return nil
}

func (p *PreparedRESTStoryMutation) Pin(command PinStoryCommand) (store.WallStoryPlacement, bool, error) {
	if err := p.begin(); err != nil {
		return store.WallStoryPlacement{}, false, err
	}
	placement, created, err := p.service.mutations.PinWallStory(
		p.writer.mutationCtx, p.writer.projectID, command.LocalID, command.X, command.Y,
	)
	if err != nil {
		return store.WallStoryPlacement{}, false, err
	}
	if created {
		p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshStoryPinned)
	}
	return placement, created, nil
}

func (p *PreparedRESTStoryMutation) Patch(command PatchStoryCommand) (store.WallStoryPlacement, error) {
	if err := p.begin(); err != nil {
		return store.WallStoryPlacement{}, err
	}
	placement, err := p.service.mutations.PatchWallStory(
		p.writer.mutationCtx, p.writer.projectID, command.LocalID, command.IfVersion, command.X, command.Y,
	)
	if err != nil {
		return store.WallStoryPlacement{}, err
	}
	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshStoryMoved)
	return placement, nil
}

func (p *PreparedRESTStoryMutation) Unpin(command UnpinStoryCommand) error {
	if err := p.begin(); err != nil {
		return err
	}
	if err := p.service.mutations.UnpinWallStory(p.writer.mutationCtx, p.writer.projectID, command.LocalID); err != nil {
		return err
	}
	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshStoryUnpinned)
	return nil
}
