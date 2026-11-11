package wall

import (
	"context"
	"encoding/json"
	"sync"

	"scrumboy/internal/store"
)

const RefreshDrawingsChanged RefreshReason = "wall_drawings_changed"

type DrawingMutationStore interface {
	ApplyWallDrawingOps(
		ctx context.Context,
		projectID int64,
		actorID int64,
		ops store.WallDrawingOps,
	) (store.WallDrawingResult, error)
}

type RESTDrawingServiceDependencies struct {
	Roles     RESTWriterRoleStore
	Mutations DrawingMutationStore
	Refresh   WallRefreshPublisher
}

type RESTDrawingService struct {
	roles     RESTWriterRoleStore
	mutations DrawingMutationStore
	refresh   WallRefreshPublisher
}

func NewRESTDrawingService(deps RESTDrawingServiceDependencies) *RESTDrawingService {
	return &RESTDrawingService{roles: deps.Roles, mutations: deps.Mutations, refresh: deps.Refresh}
}

type PreparedRESTDrawingMutation struct {
	writer      preparedRESTWriter
	service     *RESTDrawingService
	executeOnce sync.Once
}

type ApplyDrawingsCommand struct {
	BaseVersion int64
	Add         []json.RawMessage
	Erase       []string
}

func (s *RESTDrawingService) Prepare(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
) (*PreparedRESTDrawingMutation, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTDrawingMutation{writer: writer, service: s}, nil
}

func (p *PreparedRESTDrawingMutation) Apply(command ApplyDrawingsCommand) (store.WallDrawingResult, error) {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return store.WallDrawingResult{}, ErrPreparedMutationAlreadyExecuted
	}
	result, err := p.service.mutations.ApplyWallDrawingOps(
		p.writer.mutationCtx,
		p.writer.projectID,
		p.writer.actorID,
		store.WallDrawingOps{BaseVersion: command.BaseVersion, Add: command.Add, Erase: command.Erase},
	)
	if err != nil {
		return store.WallDrawingResult{}, err
	}
	if result.Applied {
		p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshDrawingsChanged)
	}
	return result, nil
}
