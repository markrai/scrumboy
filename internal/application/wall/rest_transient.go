package wall

import (
	"context"
	"sync"
)

// RESTTransientServiceDependencies contains only the fresh writer-role read
// and typed transient publication capabilities used by REST Wall transients.
type RESTTransientServiceDependencies struct {
	Roles     RESTWriterRoleStore
	Publisher WallTransientPublisher
}

// RESTTransientService owns authorization, actor attribution, and publication
// sequencing for ephemeral REST Wall note-position updates.
type RESTTransientService struct {
	roles     RESTWriterRoleStore
	publisher WallTransientPublisher
}

// NewRESTTransientService constructs a REST Wall transient service.
func NewRESTTransientService(deps RESTTransientServiceDependencies) *RESTTransientService {
	return &RESTTransientService{
		roles:     deps.Roles,
		publisher: deps.Publisher,
	}
}

// PreparedRESTTransient binds authorized project identity, trusted actor, and
// the exact mutation/effect contexts for one transient publication.
type PreparedRESTTransient struct {
	writer      preparedRESTWriter
	service     *RESTTransientService
	executeOnce sync.Once
}

// Prepare performs the fresh Contributor gate before transient input is
// parsed. mutationCtx carries actor enrichment for authorization; effectCtx is
// the raw request context retained for publication.
func (s *RESTTransientService) Prepare(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
) (*PreparedRESTTransient, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTTransient{writer: writer, service: s}, nil
}

// begin consumes the prepared transient execution guard. The guard remains
// consumed even when publication fails.
func (p *PreparedRESTTransient) begin() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}
	return nil
}

// Publish emits exactly one typed transient event using the prepared actor and
// raw effect context. Publication is the transient mutation, so its exact error
// is the operation result.
func (p *PreparedRESTTransient) Publish(command TransientCommand) error {
	if err := p.begin(); err != nil {
		return err
	}

	return p.service.publisher.PublishWallTransient(
		p.writer.effectCtx,
		p.writer.projectID,
		TransientEvent{
			NoteID: command.NoteID,
			X:      command.X,
			Y:      command.Y,
			By:     p.writer.actorID,
		},
	)
}
