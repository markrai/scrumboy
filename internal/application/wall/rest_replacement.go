package wall

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// RESTReplacementServiceDependencies contains only the fresh writer-role
// read, Wall replacement persistence, and semantic refresh capabilities used
// by REST replacement mutations.
type RESTReplacementServiceDependencies struct {
	Roles        RESTWriterRoleStore
	Replacements WallReplacementStore
	Refresh      WallRefreshPublisher
}

// RESTReplacementService owns authorization, replacement persistence
// sequencing, and success-only refresh publication for REST Wall replacement.
type RESTReplacementService struct {
	roles        RESTWriterRoleStore
	replacements WallReplacementStore
	refresh      WallRefreshPublisher
}

// NewRESTReplacementService constructs a REST Wall replacement
// service.
func NewRESTReplacementService(deps RESTReplacementServiceDependencies) *RESTReplacementService {
	return &RESTReplacementService{
		roles:        deps.Roles,
		replacements: deps.Replacements,
		refresh:      deps.Refresh,
	}
}

// PreparedRESTReplacement binds authorized project identity and the exact
// mutation/effect contexts for one replacement execution.
type PreparedRESTReplacement struct {
	writer      preparedRESTWriter
	service     *RESTReplacementService
	executeOnce sync.Once
}

// Prepare performs the fresh Contributor gate before replacement input is
// parsed. mutationCtx carries actor enrichment for role/store calls; effectCtx
// is the raw request context retained for refresh publication.
func (s *RESTReplacementService) Prepare(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
) (*PreparedRESTReplacement, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTReplacement{writer: writer, service: s}, nil
}

// begin consumes the prepared replacement execution guard. The guard remains
// consumed even when replacement persistence fails.
func (p *PreparedRESTReplacement) begin() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}
	return nil
}

// Replace executes exactly one full note-list replacement and publishes its
// refresh only after persistence succeeds.
func (p *PreparedRESTReplacement) Replace(command ReplaceWallCommand) (store.Wall, error) {
	if err := p.begin(); err != nil {
		return store.Wall{}, err
	}

	var notes []store.WallNote
	if command.Notes != nil {
		notes = make([]store.WallNote, len(command.Notes))
		for i, note := range command.Notes {
			notes[i] = store.WallNote{
				X:      note.X,
				Y:      note.Y,
				Width:  note.Width,
				Height: note.Height,
				Color:  note.Color,
				Text:   note.Text,
			}
		}
	}

	wall, err := p.service.replacements.ReplaceWall(
		p.writer.mutationCtx,
		p.writer.projectID,
		notes,
	)
	if err != nil {
		return store.Wall{}, err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshReplaced)
	return wall, nil
}
