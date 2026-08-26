package wall

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type restNoteRawContextKey struct{}
type restNoteMutationContextKey struct{}

const (
	restNoteMutatedFloat  = 987654.25
	restNoteMutatedColor  = "store-mutated-color"
	restNoteMutatedText   = "store-mutated-text"
	restNoteTestProjectID = int64(73)
	restNoteTestActorID   = int64(41)
)

type restNoteCreateCall struct {
	ctx       context.Context
	projectID int64
	input     store.CreateNoteInput
}

type restNotePatchCall struct {
	ctx                 context.Context
	projectID           int64
	noteID              string
	inputBeforeMutation store.PatchNoteInput
	retainedInput       store.PatchNoteInput
}

type restNoteDeleteCall struct {
	ctx       context.Context
	projectID int64
	noteID    string
}

type restNoteRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    RefreshReason
}

type restNoteFake struct {
	mu sync.Mutex

	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls  []restNoteCreateCall
	createResult store.WallNote
	createWall   store.Wall
	createErr    error

	patchCalls       []restNotePatchCall
	patchResult      store.WallNote
	patchWall        store.Wall
	patchErr         error
	mutatePatchInput bool

	deleteCalls []restNoteDeleteCall
	deleteWall  store.Wall
	deleteErr   error

	refreshCalls []restNoteRefreshCall

	honorMutationContextError bool
}

func (f *restNoteFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "role")
	f.roleCalls++
	f.roleCtx = ctx
	f.rolePID = projectID
	f.roleUID = userID
	return f.role, f.roleErr
}

func (f *restNoteFake) CreateNote(
	ctx context.Context,
	projectID int64,
	input store.CreateNoteInput,
) (store.WallNote, store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "create")
	f.createCalls = append(f.createCalls, restNoteCreateCall{ctx: ctx, projectID: projectID, input: input})
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.WallNote{}, store.Wall{}, ctx.Err()
	}
	return f.createResult, f.createWall, f.createErr
}

func (f *restNoteFake) PatchNote(
	ctx context.Context,
	projectID int64,
	noteID string,
	input store.PatchNoteInput,
) (store.WallNote, store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "patch")
	f.patchCalls = append(f.patchCalls, restNotePatchCall{
		ctx:                 ctx,
		projectID:           projectID,
		noteID:              noteID,
		inputBeforeMutation: cloneRestNotePatchInput(input),
		retainedInput:       input,
	})
	if f.mutatePatchInput {
		mutateRestNotePatchInput(input)
	}
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.WallNote{}, store.Wall{}, ctx.Err()
	}
	return f.patchResult, f.patchWall, f.patchErr
}

func (f *restNoteFake) DeleteNote(
	ctx context.Context,
	projectID int64,
	noteID string,
) (store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "delete")
	f.deleteCalls = append(f.deleteCalls, restNoteDeleteCall{ctx: ctx, projectID: projectID, noteID: noteID})
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.Wall{}, ctx.Err()
	}
	return f.deleteWall, f.deleteErr
}

func (f *restNoteFake) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason RefreshReason,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "refresh")
	f.refreshCalls = append(f.refreshCalls, restNoteRefreshCall{ctx: ctx, projectID: projectID, reason: reason})
}

type restNoteFakeSnapshot struct {
	trace []string

	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls  []restNoteCreateCall
	patchCalls   []restNotePatchCall
	deleteCalls  []restNoteDeleteCall
	refreshCalls []restNoteRefreshCall
}

func (f *restNoteFake) snapshot() restNoteFakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return restNoteFakeSnapshot{
		trace:        append([]string(nil), f.trace...),
		roleCalls:    f.roleCalls,
		roleCtx:      f.roleCtx,
		rolePID:      f.rolePID,
		roleUID:      f.roleUID,
		createCalls:  append([]restNoteCreateCall(nil), f.createCalls...),
		patchCalls:   append([]restNotePatchCall(nil), f.patchCalls...),
		deleteCalls:  append([]restNoteDeleteCall(nil), f.deleteCalls...),
		refreshCalls: append([]restNoteRefreshCall(nil), f.refreshCalls...),
	}
}

func cloneRestNotePatchInput(input store.PatchNoteInput) store.PatchNoteInput {
	return store.PatchNoteInput{
		IfVersion: input.IfVersion,
		X:         cloneRestNoteFloat(input.X),
		Y:         cloneRestNoteFloat(input.Y),
		Width:     cloneRestNoteFloat(input.Width),
		Height:    cloneRestNoteFloat(input.Height),
		Color:     cloneRestNoteString(input.Color),
		Text:      cloneRestNoteString(input.Text),
	}
}

func cloneRestNoteFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneRestNoteString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func mutateRestNotePatchInput(input store.PatchNoteInput) {
	for _, value := range []*float64{input.X, input.Y, input.Width, input.Height} {
		if value != nil {
			*value = restNoteMutatedFloat
		}
	}
	if input.Color != nil {
		*input.Color = restNoteMutatedColor
	}
	if input.Text != nil {
		*input.Text = restNoteMutatedText
	}
}

func newRESTNoteTestService(fake *restNoteFake) *RESTNoteService {
	return NewRESTNoteService(RESTNoteServiceDependencies{
		Roles:     fake,
		Mutations: fake,
		Refresh:   fake,
	})
}

func restNoteContexts(actorID int64, marker string) (context.Context, context.Context) {
	effectCtx := context.WithValue(context.Background(), restNoteRawContextKey{}, marker+"-raw")
	mutationCtx := context.WithValue(effectCtx, restNoteMutationContextKey{}, marker+"-mutation")
	mutationCtx = store.WithUserID(mutationCtx, actorID)
	return mutationCtx, effectCtx
}

func assertRestNoteTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func TestRESTWriterPreparationAuthorization(t *testing.T) {
	roleFailure := errors.New("role lookup failed")
	tests := []struct {
		name                string
		mutationActor       int64
		withMutationActor   bool
		effectActor         int64
		role                store.ProjectRole
		roleErr             error
		wantErr             error
		wantPrepared        bool
		wantRoleCalls       int
		wantMutationActorID int64
	}{
		{name: "missing actor", wantErr: ErrActorRequired},
		{name: "zero actor", withMutationActor: true, mutationActor: 0, wantErr: ErrActorRequired},
		{name: "negative actor", withMutationActor: true, mutationActor: -1, wantErr: ErrActorRequired},
		{name: "actor only in effect context", effectActor: 99, wantErr: ErrActorRequired},
		{name: "role read error", withMutationActor: true, mutationActor: 41, roleErr: roleFailure, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 41},
		{name: "empty role", withMutationActor: true, mutationActor: 42, role: store.ProjectRole(""), wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 42},
		{name: "unknown role", withMutationActor: true, mutationActor: 42, role: store.ProjectRole("unknown"), wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 42},
		{name: "viewer", withMutationActor: true, mutationActor: 43, role: store.RoleViewer, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 43},
		{name: "contributor", withMutationActor: true, mutationActor: 44, role: store.RoleContributor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 44},
		{name: "maintainer", withMutationActor: true, mutationActor: 45, role: store.RoleMaintainer, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 45},
		{name: "deprecated editor", withMutationActor: true, mutationActor: 46, role: store.RoleEditor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 46},
		{name: "deprecated owner", withMutationActor: true, mutationActor: 47, role: store.RoleOwner, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 47},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restNoteFake{role: tt.role, roleErr: tt.roleErr}
			service := newRESTNoteTestService(fake)
			mutationCtx := context.WithValue(context.Background(), restNoteMutationContextKey{}, tt.name)
			if tt.withMutationActor {
				mutationCtx = store.WithUserID(mutationCtx, tt.mutationActor)
			}
			effectCtx := context.WithValue(context.Background(), restNoteRawContextKey{}, tt.name)
			if tt.effectActor != 0 {
				effectCtx = store.WithUserID(effectCtx, tt.effectActor)
			}

			prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if prepared != nil {
				if prepared.writer.mutationCtx != mutationCtx || prepared.writer.effectCtx != effectCtx ||
					prepared.writer.actorID != tt.wantMutationActorID || prepared.writer.projectID != restNoteTestProjectID {
					t.Fatalf("bound writer=%+v", prepared.writer)
				}
			}

			snapshot := fake.snapshot()
			if snapshot.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", snapshot.roleCalls, tt.wantRoleCalls)
			}
			if tt.wantRoleCalls == 1 {
				if snapshot.roleCtx != mutationCtx || snapshot.rolePID != restNoteTestProjectID || snapshot.roleUID != tt.wantMutationActorID {
					t.Fatalf("role call context/project/user mismatch: ctxSame=%v project=%d user=%d", snapshot.roleCtx == mutationCtx, snapshot.rolePID, snapshot.roleUID)
				}
				assertRestNoteTrace(t, snapshot.trace, "role")
			} else {
				assertRestNoteTrace(t, snapshot.trace)
			}
			if len(snapshot.createCalls) != 0 || len(snapshot.patchCalls) != 0 || len(snapshot.deleteCalls) != 0 || len(snapshot.refreshCalls) != 0 {
				t.Fatalf("preparation caused effects: %+v", snapshot)
			}
		})
	}
}

func TestRESTWriterPreparationBindsDistinctContextsActorAndProject(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	rawMarked := context.WithValue(context.Background(), restNoteRawContextKey{}, "raw")
	effectCtx, cancel := context.WithDeadline(rawMarked, deadline)
	defer cancel()
	mutationCtx := context.WithValue(effectCtx, restNoteMutationContextKey{}, "mutation")
	mutationCtx = store.WithUserID(mutationCtx, restNoteTestActorID)
	target := ResolvedRESTTarget{ProjectID: restNoteTestProjectID}
	fake := &restNoteFake{role: store.RoleContributor}
	service := newRESTNoteTestService(fake)

	prepared, err := service.Prepare(mutationCtx, effectCtx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectID = 999
	if err := prepared.Delete(DeleteNoteCommand{NoteID: " raw-note-id "}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	snapshot := fake.snapshot()
	if prepared.writer.actorID != restNoteTestActorID || prepared.writer.projectID != restNoteTestProjectID {
		t.Fatalf("prepared writer actor/project=%d/%d", prepared.writer.actorID, prepared.writer.projectID)
	}
	if snapshot.roleCalls != 1 || snapshot.roleCtx != mutationCtx || snapshot.rolePID != restNoteTestProjectID || snapshot.roleUID != restNoteTestActorID {
		t.Fatalf("role call=%+v", snapshot)
	}
	if len(snapshot.deleteCalls) != 1 || snapshot.deleteCalls[0].ctx != mutationCtx || snapshot.deleteCalls[0].projectID != restNoteTestProjectID {
		t.Fatalf("delete calls=%+v", snapshot.deleteCalls)
	}
	if len(snapshot.refreshCalls) != 1 || snapshot.refreshCalls[0].ctx != effectCtx || snapshot.refreshCalls[0].projectID != restNoteTestProjectID {
		t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
	}
	if _, ok := store.UserIDFromContext(effectCtx); ok {
		t.Fatal("raw effect context unexpectedly contains actor enrichment")
	}
	if effectCtx.Value(restNoteRawContextKey{}) != "raw" || mutationCtx.Value(restNoteMutationContextKey{}) != "mutation" {
		t.Fatal("bound contexts lost marker values")
	}
	gotDeadline, ok := snapshot.refreshCalls[0].ctx.Deadline()
	if !ok || !gotDeadline.Equal(deadline) {
		t.Fatalf("refresh deadline=%v,%v want=%v", gotDeadline, ok, deadline)
	}
	cancel()
	if snapshot.refreshCalls[0].ctx.Err() != context.Canceled {
		t.Fatalf("refresh context cancellation=%v want=%v", snapshot.refreshCalls[0].ctx.Err(), context.Canceled)
	}
	assertRestNoteTrace(t, snapshot.trace, "role", "delete", "refresh")
}

func TestPreparedRESTNoteCreate(t *testing.T) {
	wantNote := store.WallNote{ID: "created", X: 1, Y: 2, Width: 3, Height: 4, Color: "#abcdef", Text: "result", Version: 7}
	wantInput := store.CreateNoteInput{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t raw text \n "}

	t.Run("success passes result then publishes on raw context", func(t *testing.T) {
		fake := &restNoteFake{role: store.RoleContributor, createResult: wantNote, createWall: store.Wall{Version: 99}}
		service := newRESTNoteTestService(fake)
		mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "create")
		prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateNoteCommand{
			X: wantInput.X, Y: wantInput.Y, Width: wantInput.Width, Height: wantInput.Height,
			Color: wantInput.Color, Text: wantInput.Text,
		})
		if err != nil || got != wantNote {
			t.Fatalf("Create=(%+v,%v) want=(%+v,nil)", got, err, wantNote)
		}
		snapshot := fake.snapshot()
		if len(snapshot.createCalls) != 1 {
			t.Fatalf("create calls=%+v", snapshot.createCalls)
		}
		call := snapshot.createCalls[0]
		if call.ctx != mutationCtx || call.projectID != restNoteTestProjectID || call.input != wantInput {
			t.Fatalf("create call=%+v wantInput=%+v", call, wantInput)
		}
		if len(snapshot.refreshCalls) != 1 || snapshot.refreshCalls[0] != (restNoteRefreshCall{ctx: effectCtx, projectID: restNoteTestProjectID, reason: RefreshNoteCreated}) {
			t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
		}
		assertRestNoteTrace(t, snapshot.trace, "role", "create", "refresh")
	})

	t.Run("store failure returns zero note and suppresses refresh", func(t *testing.T) {
		storeFailure := errors.New("create failed")
		fake := &restNoteFake{role: store.RoleContributor, createResult: wantNote, createErr: storeFailure}
		service := newRESTNoteTestService(fake)
		mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "create-failure")
		prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateNoteCommand{Color: "bad"})
		if got != (store.WallNote{}) || err != storeFailure {
			t.Fatalf("Create=(%+v,%v) want=(zero,%v)", got, err, storeFailure)
		}
		snapshot := fake.snapshot()
		if len(snapshot.createCalls) != 1 || len(snapshot.refreshCalls) != 0 {
			t.Fatalf("failure calls=%+v refreshes=%+v", snapshot.createCalls, snapshot.refreshCalls)
		}
		assertRestNoteTrace(t, snapshot.trace, "role", "create")
	})
}

func TestPreparedRESTNotePatchPreservesOptionalPresence(t *testing.T) {
	x, y, width, height := 0.0, -100001.5, 0.0, -1.0
	color, text := " \t ", ""
	tests := []struct {
		name    string
		command PatchNoteCommand
		want    store.PatchNoteInput
	}{
		{name: "empty unconditional patch", command: PatchNoteCommand{NoteID: "empty", IfVersion: 0}, want: store.PatchNoteInput{IfVersion: 0}},
		{name: "empty conditional patch", command: PatchNoteCommand{NoteID: "empty-conditional", IfVersion: 11}, want: store.PatchNoteInput{IfVersion: 11}},
		{name: "x", command: PatchNoteCommand{NoteID: "x", IfVersion: 7, X: &x}, want: store.PatchNoteInput{IfVersion: 7, X: &x}},
		{name: "y", command: PatchNoteCommand{NoteID: "y", IfVersion: 7, Y: &y}, want: store.PatchNoteInput{IfVersion: 7, Y: &y}},
		{name: "width", command: PatchNoteCommand{NoteID: "width", IfVersion: 7, Width: &width}, want: store.PatchNoteInput{IfVersion: 7, Width: &width}},
		{name: "height", command: PatchNoteCommand{NoteID: "height", IfVersion: 7, Height: &height}, want: store.PatchNoteInput{IfVersion: 7, Height: &height}},
		{name: "color", command: PatchNoteCommand{NoteID: "color", IfVersion: 7, Color: &color}, want: store.PatchNoteInput{IfVersion: 7, Color: &color}},
		{name: "text", command: PatchNoteCommand{NoteID: "text", IfVersion: 7, Text: &text}, want: store.PatchNoteInput{IfVersion: 7, Text: &text}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restNoteFake{role: store.RoleContributor, patchResult: store.WallNote{ID: tt.command.NoteID}}
			service := newRESTNoteTestService(fake)
			mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, tt.name)
			prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}

			if _, err := prepared.Patch(tt.command); err != nil {
				t.Fatalf("Patch: %v", err)
			}
			snapshot := fake.snapshot()
			if len(snapshot.patchCalls) != 1 {
				t.Fatalf("patch calls=%+v", snapshot.patchCalls)
			}
			call := snapshot.patchCalls[0]
			if call.ctx != mutationCtx || call.projectID != restNoteTestProjectID || call.noteID != tt.command.NoteID || !reflect.DeepEqual(call.inputBeforeMutation, tt.want) {
				t.Fatalf("patch call=%+v wantInput=%+v", call, tt.want)
			}
			if len(snapshot.refreshCalls) != 1 || snapshot.refreshCalls[0] != (restNoteRefreshCall{ctx: effectCtx, projectID: restNoteTestProjectID, reason: RefreshNoteUpdated}) {
				t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
			}
			assertRestNoteTrace(t, snapshot.trace, "role", "patch", "refresh")
		})
	}
}

func TestPreparedRESTNotePatchDefensivelyCopiesValuesAndPassesResult(t *testing.T) {
	x, y, width, height := -11.25, 22.5, 0.0, -3.0
	color, text := " malformed color ", " \t raw text \n "
	command := PatchNoteCommand{
		NoteID: "  raw-note-id  ", IfVersion: -9,
		X: &x, Y: &y, Width: &width, Height: &height, Color: &color, Text: &text,
	}
	wantInput := store.PatchNoteInput{
		IfVersion: command.IfVersion,
		X:         &x, Y: &y, Width: &width, Height: &height, Color: &color, Text: &text,
	}
	wantNote := store.WallNote{ID: "patched", Text: "result", Version: 17}
	fake := &restNoteFake{role: store.RoleContributor, patchResult: wantNote, patchWall: store.Wall{Version: 44}, mutatePatchInput: true}
	service := newRESTNoteTestService(fake)
	mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "patch-copy")
	prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := prepared.Patch(command)
	if err != nil || got != wantNote {
		t.Fatalf("Patch=(%+v,%v) want=(%+v,nil)", got, err, wantNote)
	}
	if x != -11.25 || y != 22.5 || width != 0 || height != -3 || color != " malformed color " || text != " \t raw text \n " {
		t.Fatalf("store mutated caller values: x=%v y=%v width=%v height=%v color=%q text=%q", x, y, width, height, color, text)
	}
	snapshot := fake.snapshot()
	if len(snapshot.patchCalls) != 1 {
		t.Fatalf("patch calls=%+v", snapshot.patchCalls)
	}
	call := snapshot.patchCalls[0]
	if call.ctx != mutationCtx || call.projectID != restNoteTestProjectID || call.noteID != command.NoteID || !reflect.DeepEqual(call.inputBeforeMutation, wantInput) {
		t.Fatalf("patch call=%+v wantInput=%+v", call, wantInput)
	}
	if *call.retainedInput.X != restNoteMutatedFloat || *call.retainedInput.Y != restNoteMutatedFloat ||
		*call.retainedInput.Width != restNoteMutatedFloat || *call.retainedInput.Height != restNoteMutatedFloat ||
		*call.retainedInput.Color != restNoteMutatedColor || *call.retainedInput.Text != restNoteMutatedText {
		t.Fatalf("fake did not retain independently mutable input: %+v", call.retainedInput)
	}
	x, y, width, height = 1, 2, 3, 4
	color, text = "caller changed", "caller changed"
	if *call.retainedInput.X != restNoteMutatedFloat || *call.retainedInput.Color != restNoteMutatedColor || *call.retainedInput.Text != restNoteMutatedText {
		t.Fatalf("caller mutation changed retained store input: %+v", call.retainedInput)
	}
	if len(snapshot.refreshCalls) != 1 || snapshot.refreshCalls[0] != (restNoteRefreshCall{ctx: effectCtx, projectID: restNoteTestProjectID, reason: RefreshNoteUpdated}) {
		t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
	}
	assertRestNoteTrace(t, snapshot.trace, "role", "patch", "refresh")
}

func TestPreparedRESTNotePatchFailureReturnsZeroAndSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("patch failed")
	fake := &restNoteFake{role: store.RoleContributor, patchResult: store.WallNote{ID: "must-not-return"}, patchErr: storeFailure}
	service := newRESTNoteTestService(fake)
	mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "patch-failure")
	prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := prepared.Patch(PatchNoteCommand{NoteID: "missing", IfVersion: 3})
	if got != (store.WallNote{}) || err != storeFailure {
		t.Fatalf("Patch=(%+v,%v) want=(zero,%v)", got, err, storeFailure)
	}
	snapshot := fake.snapshot()
	if len(snapshot.patchCalls) != 1 || len(snapshot.refreshCalls) != 0 {
		t.Fatalf("failure calls=%+v refreshes=%+v", snapshot.patchCalls, snapshot.refreshCalls)
	}
	assertRestNoteTrace(t, snapshot.trace, "role", "patch")
}

func TestPreparedRESTNoteDelete(t *testing.T) {
	t.Run("success publishes after raw delete", func(t *testing.T) {
		fake := &restNoteFake{role: store.RoleContributor, deleteWall: store.Wall{Version: 10}}
		service := newRESTNoteTestService(fake)
		mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "delete")
		prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if err := prepared.Delete(DeleteNoteCommand{NoteID: " \t raw-delete \n "}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		snapshot := fake.snapshot()
		if len(snapshot.deleteCalls) != 1 || snapshot.deleteCalls[0] != (restNoteDeleteCall{ctx: mutationCtx, projectID: restNoteTestProjectID, noteID: " \t raw-delete \n "}) {
			t.Fatalf("delete calls=%+v", snapshot.deleteCalls)
		}
		if len(snapshot.refreshCalls) != 1 || snapshot.refreshCalls[0] != (restNoteRefreshCall{ctx: effectCtx, projectID: restNoteTestProjectID, reason: RefreshNoteDeleted}) {
			t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
		}
		assertRestNoteTrace(t, snapshot.trace, "role", "delete", "refresh")
	})

	t.Run("store failure returns unchanged and suppresses refresh", func(t *testing.T) {
		storeFailure := errors.New("delete failed")
		fake := &restNoteFake{role: store.RoleContributor, deleteErr: storeFailure}
		service := newRESTNoteTestService(fake)
		mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, "delete-failure")
		prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		if err := prepared.Delete(DeleteNoteCommand{NoteID: "missing"}); err != storeFailure {
			t.Fatalf("Delete error=%v want=%v", err, storeFailure)
		}
		snapshot := fake.snapshot()
		if len(snapshot.deleteCalls) != 1 || len(snapshot.refreshCalls) != 0 {
			t.Fatalf("failure calls=%+v refreshes=%+v", snapshot.deleteCalls, snapshot.refreshCalls)
		}
		assertRestNoteTrace(t, snapshot.trace, "role", "delete")
	})
}

func TestPreparedRESTNoteAtMostOnceAcrossFamilyMethods(t *testing.T) {
	t.Run("create success consumes family", func(t *testing.T) {
		fake := &restNoteFake{role: store.RoleContributor}
		prepared := mustPrepareRESTNote(t, fake, "once-create")
		if _, err := prepared.Create(CreateNoteCommand{}); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		first := fake.snapshot()
		if got, err := prepared.Patch(PatchNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("second Patch=(%+v,%v)", got, err)
		}
		if err := prepared.Delete(DeleteNoteCommand{}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("third Delete error=%v", err)
		}
		if got, err := prepared.Create(CreateNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("repeat Create=(%+v,%v)", got, err)
		}
		if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
			t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
		}
	})

	t.Run("patch failure consumes family", func(t *testing.T) {
		storeFailure := errors.New("first patch failed")
		fake := &restNoteFake{role: store.RoleContributor, patchErr: storeFailure}
		prepared := mustPrepareRESTNote(t, fake, "once-patch")
		if _, err := prepared.Patch(PatchNoteCommand{}); err != storeFailure {
			t.Fatalf("first Patch error=%v", err)
		}
		first := fake.snapshot()
		if got, err := prepared.Create(CreateNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("second Create=(%+v,%v)", got, err)
		}
		if err := prepared.Delete(DeleteNoteCommand{}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("third Delete error=%v", err)
		}
		if got, err := prepared.Patch(PatchNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("repeat Patch=(%+v,%v)", got, err)
		}
		if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
			t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
		}
	})

	t.Run("delete success consumes family", func(t *testing.T) {
		fake := &restNoteFake{role: store.RoleContributor}
		prepared := mustPrepareRESTNote(t, fake, "once-delete")
		if err := prepared.Delete(DeleteNoteCommand{}); err != nil {
			t.Fatalf("first Delete: %v", err)
		}
		first := fake.snapshot()
		if got, err := prepared.Create(CreateNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("second Create=(%+v,%v)", got, err)
		}
		if got, err := prepared.Patch(PatchNoteCommand{}); got != (store.WallNote{}) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("third Patch=(%+v,%v)", got, err)
		}
		if err := prepared.Delete(DeleteNoteCommand{}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("repeat Delete error=%v", err)
		}
		if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
			t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
		}
	})
}

func TestPreparedRESTNoteConcurrentExecutionStartsOneMethod(t *testing.T) {
	fake := &restNoteFake{role: store.RoleContributor}
	prepared := mustPrepareRESTNote(t, fake, "concurrent")
	start := make(chan struct{})
	errs := make(chan error, 3)
	var wg sync.WaitGroup
	for _, execute := range []func() error{
		func() error { _, err := prepared.Create(CreateNoteCommand{}); return err },
		func() error { _, err := prepared.Patch(PatchNoteCommand{}); return err },
		func() error { return prepared.Delete(DeleteNoteCommand{}) },
	} {
		execute := execute
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- execute()
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	successes, repeats := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrPreparedMutationAlreadyExecuted):
			repeats++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	snapshot := fake.snapshot()
	mutationCalls := len(snapshot.createCalls) + len(snapshot.patchCalls) + len(snapshot.deleteCalls)
	if successes != 1 || repeats != 2 || mutationCalls != 1 || len(snapshot.refreshCalls) != 1 || snapshot.roleCalls != 1 {
		t.Fatalf("success/repeat/mutation/refresh/role=%d/%d/%d/%d/%d snapshot=%+v", successes, repeats, mutationCalls, len(snapshot.refreshCalls), snapshot.roleCalls, snapshot)
	}
}

func TestPreparedRESTNoteCanceledMutationContext(t *testing.T) {
	effectCtx := context.WithValue(context.Background(), restNoteRawContextKey{}, "raw-not-canceled")
	mutationBase, cancel := context.WithCancel(context.WithValue(context.Background(), restNoteMutationContextKey{}, "mutation-canceled"))
	mutationCtx := store.WithUserID(mutationBase, restNoteTestActorID)
	cancel()
	fake := &restNoteFake{role: store.RoleContributor, honorMutationContextError: true}
	service := newRESTNoteTestService(fake)
	prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := prepared.Create(CreateNoteCommand{}); err != context.Canceled {
		t.Fatalf("Create error=%v want context.Canceled", err)
	}
	first := fake.snapshot()
	if len(first.createCalls) != 1 || first.createCalls[0].ctx != mutationCtx || len(first.refreshCalls) != 0 {
		t.Fatalf("canceled sequence=%+v", first)
	}
	if _, err := prepared.Create(CreateNoteCommand{}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
		t.Fatalf("repeat Create error=%v", err)
	}
	if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
		t.Fatalf("repeat after cancellation changed dependencies: first=%+v after=%+v", first, after)
	}
}

func mustPrepareRESTNote(t *testing.T, fake *restNoteFake, marker string) *PreparedRESTNoteMutation {
	t.Helper()
	service := newRESTNoteTestService(fake)
	mutationCtx, effectCtx := restNoteContexts(restNoteTestActorID, marker)
	prepared, err := service.Prepare(mutationCtx, effectCtx, ResolvedRESTTarget{ProjectID: restNoteTestProjectID})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}
