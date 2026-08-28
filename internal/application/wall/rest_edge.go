package wall

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// RESTEdgeServiceDependencies contains only the fresh writer-role read, edge
// persistence, and semantic refresh capabilities used by REST edge mutations.
type RESTEdgeServiceDependencies struct {
	Roles     RESTWriterRoleStore
	Mutations EdgeMutationStore
	Refresh   WallRefreshPublisher
}

// RESTEdgeService owns authorization, edge persistence sequencing, and
// success-only refresh publication for REST Wall edge mutations.
type RESTEdgeService struct {
	roles     RESTWriterRoleStore
	mutations EdgeMutationStore
	refresh   WallRefreshPublisher
}

// NewRESTEdgeService constructs a REST Wall edge service.
func NewRESTEdgeService(deps RESTEdgeServiceDependencies) *RESTEdgeService {
	return &RESTEdgeService{
		roles:     deps.Roles,
		mutations: deps.Mutations,
		refresh:   deps.Refresh,
	}
}

// PreparedRESTEdgeMutation binds authorized project identity and the exact
// mutation/effect contexts for one edge-family execution.
type PreparedRESTEdgeMutation struct {
	writer      preparedRESTWriter
	service     *RESTEdgeService
	executeOnce sync.Once
}

// Prepare performs the fresh Contributor gate before edge-specific input is
// parsed. mutationCtx carries actor enrichment for role/store calls; effectCtx
// is the raw request context retained for refresh publication.
func (s *RESTEdgeService) Prepare(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
) (*PreparedRESTEdgeMutation, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTEdgeMutation{writer: writer, service: s}, nil
}

// begin consumes the prepared edge-family execution guard. The guard remains
// consumed even when the selected edge persistence operation fails.
func (p *PreparedRESTEdgeMutation) begin() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}
	return nil
}

// Create executes exactly one edge creation and publishes its refresh after
// every nil store error, including the store's duplicate-edge no-op.
func (p *PreparedRESTEdgeMutation) Create(command CreateEdgeCommand) (store.WallEdge, error) {
	if err := p.begin(); err != nil {
		return store.WallEdge{}, err
	}

	edge, _, err := p.service.mutations.CreateEdge(
		p.writer.mutationCtx,
		p.writer.projectID,
		command.From,
		command.To,
	)
	if err != nil {
		return store.WallEdge{}, err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshEdgeCreated)
	return edge, nil
}

// Delete executes exactly one edge deletion and publishes its refresh only
// after persistence succeeds.
func (p *PreparedRESTEdgeMutation) Delete(command DeleteEdgeCommand) error {
	if err := p.begin(); err != nil {
		return err
	}

	if _, err := p.service.mutations.DeleteEdge(
		p.writer.mutationCtx,
		p.writer.projectID,
		command.EdgeID,
	); err != nil {
		return err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshEdgeDeleted)
	return nil
}
