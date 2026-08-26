package wall

import (
	"context"
	"sync"

	"scrumboy/internal/store"
)

// RESTNoteServiceDependencies contains only the fresh writer-role read, note
// persistence, and semantic refresh capabilities used by REST note mutations.
type RESTNoteServiceDependencies struct {
	Roles     RESTWriterRoleStore
	Mutations NoteMutationStore
	Refresh   WallRefreshPublisher
}

// RESTNoteService owns authorization, note persistence sequencing, and
// success-only refresh publication for REST Wall note mutations.
type RESTNoteService struct {
	roles     RESTWriterRoleStore
	mutations NoteMutationStore
	refresh   WallRefreshPublisher
}

// NewRESTNoteService constructs an unwired REST Wall note service.
func NewRESTNoteService(deps RESTNoteServiceDependencies) *RESTNoteService {
	return &RESTNoteService{
		roles:     deps.Roles,
		mutations: deps.Mutations,
		refresh:   deps.Refresh,
	}
}

// PreparedRESTNoteMutation binds authorized project identity and the exact
// mutation/effect contexts for one note-family execution.
type PreparedRESTNoteMutation struct {
	writer      preparedRESTWriter
	service     *RESTNoteService
	executeOnce sync.Once
}

// Prepare performs the fresh Contributor gate before mutation-specific input
// is parsed. mutationCtx carries actor enrichment for role/store calls;
// effectCtx is the raw request context retained for refresh publication.
func (s *RESTNoteService) Prepare(
	mutationCtx context.Context,
	effectCtx context.Context,
	target ResolvedRESTTarget,
) (*PreparedRESTNoteMutation, error) {
	writer, err := prepareRESTWriter(mutationCtx, effectCtx, target, s.roles)
	if err != nil {
		return nil, err
	}
	return &PreparedRESTNoteMutation{writer: writer, service: s}, nil
}

// begin consumes the prepared note-family execution guard. The guard remains
// consumed even when the selected store mutation fails.
func (p *PreparedRESTNoteMutation) begin() error {
	started := false
	p.executeOnce.Do(func() { started = true })
	if !started {
		return ErrPreparedMutationAlreadyExecuted
	}
	return nil
}

// Create executes exactly one note creation and publishes its refresh only
// after persistence succeeds.
func (p *PreparedRESTNoteMutation) Create(command CreateNoteCommand) (store.WallNote, error) {
	if err := p.begin(); err != nil {
		return store.WallNote{}, err
	}

	note, _, err := p.service.mutations.CreateNote(
		p.writer.mutationCtx,
		p.writer.projectID,
		store.CreateNoteInput{
			X:      command.X,
			Y:      command.Y,
			Width:  command.Width,
			Height: command.Height,
			Color:  command.Color,
			Text:   command.Text,
		},
	)
	if err != nil {
		return store.WallNote{}, err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshNoteCreated)
	return note, nil
}

// Patch executes exactly one note patch. Optional command values are copied so
// dependencies cannot retain or mutate adapter-owned pointers.
func (p *PreparedRESTNoteMutation) Patch(command PatchNoteCommand) (store.WallNote, error) {
	if err := p.begin(); err != nil {
		return store.WallNote{}, err
	}

	note, _, err := p.service.mutations.PatchNote(
		p.writer.mutationCtx,
		p.writer.projectID,
		command.NoteID,
		store.PatchNoteInput{
			IfVersion: command.IfVersion,
			X:         copyOptionalFloat64(command.X),
			Y:         copyOptionalFloat64(command.Y),
			Width:     copyOptionalFloat64(command.Width),
			Height:    copyOptionalFloat64(command.Height),
			Color:     copyOptionalString(command.Color),
			Text:      copyOptionalString(command.Text),
		},
	)
	if err != nil {
		return store.WallNote{}, err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshNoteUpdated)
	return note, nil
}

// Delete executes exactly one note deletion and publishes its refresh only
// after persistence succeeds.
func (p *PreparedRESTNoteMutation) Delete(command DeleteNoteCommand) error {
	if err := p.begin(); err != nil {
		return err
	}

	if _, err := p.service.mutations.DeleteNote(
		p.writer.mutationCtx,
		p.writer.projectID,
		command.NoteID,
	); err != nil {
		return err
	}

	p.service.refresh.PublishWallRefresh(p.writer.effectCtx, p.writer.projectID, RefreshNoteDeleted)
	return nil
}

func copyOptionalFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
