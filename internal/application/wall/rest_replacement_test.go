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

type restReplacementRawContextKey struct{}
type restReplacementMutationContextKey struct{}

const (
	restReplacementTestProjectID = int64(137)
	restReplacementTestActorID   = int64(59)
)

var restReplacementMutatedNote = store.WallNote{
	ID: "store-mutated-id", X: 987654.25, Y: -987654.5,
	Width: 333, Height: 444, Color: "store-mutated-color",
	Text: "store-mutated-text", Version: 765,
}

type restReplacementCall struct {
	ctx                 context.Context
	projectID           int64
	notesBeforeMutation []store.WallNote
	retainedNotes       []store.WallNote
}

type restReplacementRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    RefreshReason
}

type restReplacementFake struct {
	mu sync.Mutex

	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	replaceCalls        []restReplacementCall
	replaceResult       store.Wall
	replaceErr          error
	mutateReceivedNotes bool

	refreshCalls []restReplacementRefreshCall

	honorMutationContextError bool
}

var (
	_ RESTWriterRoleStore  = (*restReplacementFake)(nil)
	_ WallReplacementStore = (*restReplacementFake)(nil)
	_ WallRefreshPublisher = (*restReplacementFake)(nil)
)

func (f *restReplacementFake) GetProjectRole(
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

func (f *restReplacementFake) ReplaceWall(
	ctx context.Context,
	projectID int64,
	notes []store.WallNote,
) (store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "replace")
	f.replaceCalls = append(f.replaceCalls, restReplacementCall{
		ctx:                 ctx,
		projectID:           projectID,
		notesBeforeMutation: cloneRestReplacementNotes(notes),
		retainedNotes:       notes,
	})
	if f.mutateReceivedNotes && len(notes) > 0 {
		notes[0] = restReplacementMutatedNote
	}
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.Wall{}, ctx.Err()
	}
	return f.replaceResult, f.replaceErr
}

func (f *restReplacementFake) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason RefreshReason,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "refresh")
	f.refreshCalls = append(f.refreshCalls, restReplacementRefreshCall{
		ctx: ctx, projectID: projectID, reason: reason,
	})
}

type restReplacementFakeSnapshot struct {
	trace []string

	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	replaceCalls []restReplacementCall
	refreshCalls []restReplacementRefreshCall
}

func (f *restReplacementFake) snapshot() restReplacementFakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	replaceCalls := make([]restReplacementCall, len(f.replaceCalls))
	for i, call := range f.replaceCalls {
		replaceCalls[i] = call
		replaceCalls[i].notesBeforeMutation = cloneRestReplacementNotes(call.notesBeforeMutation)
		replaceCalls[i].retainedNotes = cloneRestReplacementNotes(call.retainedNotes)
	}
	return restReplacementFakeSnapshot{
		trace:        append([]string(nil), f.trace...),
		roleCalls:    f.roleCalls,
		roleCtx:      f.roleCtx,
		rolePID:      f.rolePID,
		roleUID:      f.roleUID,
		replaceCalls: replaceCalls,
		refreshCalls: append([]restReplacementRefreshCall(nil), f.refreshCalls...),
	}
}

func cloneRestReplacementNotes(notes []store.WallNote) []store.WallNote {
	if notes == nil {
		return nil
	}
	cloned := make([]store.WallNote, len(notes))
	copy(cloned, notes)
	return cloned
}

func cloneRestReplacementDrafts(notes []NoteDraft) []NoteDraft {
	if notes == nil {
		return nil
	}
	cloned := make([]NoteDraft, len(notes))
	copy(cloned, notes)
	return cloned
}

func newRESTReplacementTestService(fake *restReplacementFake) *RESTReplacementService {
	return NewRESTReplacementService(RESTReplacementServiceDependencies{
		Roles:        fake,
		Replacements: fake,
		Refresh:      fake,
	})
}

func restReplacementContexts(actorID int64, marker string) (context.Context, context.Context) {
	effectCtx := context.WithValue(context.Background(), restReplacementRawContextKey{}, marker+"-raw")
	mutationCtx := context.WithValue(effectCtx, restReplacementMutationContextKey{}, marker+"-mutation")
	mutationCtx = store.WithUserID(mutationCtx, actorID)
	return mutationCtx, effectCtx
}

func assertRestReplacementTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func isZeroRestReplacementWall(wall store.Wall) bool {
	return reflect.DeepEqual(wall, store.Wall{})
}

func mustPrepareRESTReplacement(
	t *testing.T,
	fake *restReplacementFake,
	marker string,
) *PreparedRESTReplacement {
	t.Helper()
	service := newRESTReplacementTestService(fake)
	mutationCtx, effectCtx := restReplacementContexts(restReplacementTestActorID, marker)
	prepared, err := service.Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restReplacementTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func TestRESTReplacementPreparationAuthorization(t *testing.T) {
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
		{name: "role read error", withMutationActor: true, mutationActor: 61, roleErr: roleFailure, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 61},
		{name: "empty role", withMutationActor: true, mutationActor: 62, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 62},
		{name: "unknown role", withMutationActor: true, mutationActor: 63, role: store.ProjectRole("unknown"), wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 63},
		{name: "viewer", withMutationActor: true, mutationActor: 64, role: store.RoleViewer, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 64},
		{name: "contributor", withMutationActor: true, mutationActor: 65, role: store.RoleContributor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 65},
		{name: "maintainer", withMutationActor: true, mutationActor: 66, role: store.RoleMaintainer, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 66},
		{name: "deprecated editor", withMutationActor: true, mutationActor: 67, role: store.RoleEditor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 67},
		{name: "deprecated owner", withMutationActor: true, mutationActor: 68, role: store.RoleOwner, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 68},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restReplacementFake{role: tt.role, roleErr: tt.roleErr}
			service := newRESTReplacementTestService(fake)
			mutationCtx := context.WithValue(context.Background(), restReplacementMutationContextKey{}, tt.name)
			if tt.withMutationActor {
				mutationCtx = store.WithUserID(mutationCtx, tt.mutationActor)
			}
			effectCtx := context.WithValue(context.Background(), restReplacementRawContextKey{}, tt.name)
			if tt.effectActor != 0 {
				effectCtx = store.WithUserID(effectCtx, tt.effectActor)
			}

			prepared, err := service.Prepare(
				mutationCtx,
				effectCtx,
				ResolvedRESTTarget{ProjectID: restReplacementTestProjectID},
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if prepared != nil {
				if prepared.writer.mutationCtx != mutationCtx || prepared.writer.effectCtx != effectCtx ||
					prepared.writer.actorID != tt.wantMutationActorID || prepared.writer.projectID != restReplacementTestProjectID {
					t.Fatalf("bound writer=%+v", prepared.writer)
				}
			}

			snapshot := fake.snapshot()
			if snapshot.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", snapshot.roleCalls, tt.wantRoleCalls)
			}
			if tt.wantRoleCalls == 1 {
				if snapshot.roleCtx != mutationCtx || snapshot.rolePID != restReplacementTestProjectID || snapshot.roleUID != tt.wantMutationActorID {
					t.Fatalf("role call context/project/user mismatch: ctxSame=%v project=%d user=%d", snapshot.roleCtx == mutationCtx, snapshot.rolePID, snapshot.roleUID)
				}
				assertRestReplacementTrace(t, snapshot.trace, "role")
			} else {
				assertRestReplacementTrace(t, snapshot.trace)
			}
			if len(snapshot.replaceCalls) != 0 || len(snapshot.refreshCalls) != 0 {
				t.Fatalf("preparation caused effects: %+v", snapshot)
			}
		})
	}
}

func TestPreparedRESTReplacementBindsDistinctContextsActorAndProject(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	rawMarked := context.WithValue(context.Background(), restReplacementRawContextKey{}, "raw")
	effectCtx, cancel := context.WithDeadline(rawMarked, deadline)
	defer cancel()
	mutationCtx := context.WithValue(effectCtx, restReplacementMutationContextKey{}, "mutation")
	mutationCtx = store.WithUserID(mutationCtx, restReplacementTestActorID)
	target := ResolvedRESTTarget{ProjectID: restReplacementTestProjectID}
	wantWall := store.Wall{
		Notes:   []store.WallNote{{ID: "new", Text: "result", Version: 1}},
		Edges:   []store.WallEdge{{ID: "retained", From: "old-a", To: "old-b"}},
		Version: 17, UpdatedAt: 123456789,
	}
	fake := &restReplacementFake{role: store.RoleContributor, replaceResult: wantWall}
	service := newRESTReplacementTestService(fake)

	prepared, err := service.Prepare(mutationCtx, effectCtx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectID = 999
	got, err := prepared.Replace(ReplaceWallCommand{Notes: []NoteDraft{{Text: "input"}}})
	if err != nil || !reflect.DeepEqual(got, wantWall) {
		t.Fatalf("Replace=(%+v,%v) want=(%+v,nil)", got, err, wantWall)
	}

	snapshot := fake.snapshot()
	if snapshot.roleCalls != 1 || snapshot.roleCtx != mutationCtx ||
		snapshot.rolePID != restReplacementTestProjectID || snapshot.roleUID != restReplacementTestActorID {
		t.Fatalf("role call=%+v", snapshot)
	}
	if len(snapshot.replaceCalls) != 1 {
		t.Fatalf("replace calls=%+v", snapshot.replaceCalls)
	}
	replace := snapshot.replaceCalls[0]
	if replace.ctx != mutationCtx || replace.projectID != restReplacementTestProjectID {
		t.Fatalf("replace context/project=%v/%d", replace.ctx == mutationCtx, replace.projectID)
	}
	if len(snapshot.refreshCalls) != 1 {
		t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
	}
	refresh := snapshot.refreshCalls[0]
	if refresh != (restReplacementRefreshCall{
		ctx: effectCtx, projectID: restReplacementTestProjectID, reason: RefreshReplaced,
	}) {
		t.Fatalf("refresh=%+v", refresh)
	}
	if mutationCtx.Value(restReplacementRawContextKey{}) != "raw" ||
		mutationCtx.Value(restReplacementMutationContextKey{}) != "mutation" {
		t.Fatal("mutation context lost marker values")
	}
	actorID, ok := store.UserIDFromContext(mutationCtx)
	if !ok || actorID != restReplacementTestActorID {
		t.Fatalf("mutation actor=%d,%v", actorID, ok)
	}
	if _, ok := store.UserIDFromContext(effectCtx); ok {
		t.Fatal("raw effect context unexpectedly contains actor enrichment")
	}
	for name, ctx := range map[string]context.Context{
		"role": snapshot.roleCtx, "replace": replace.ctx, "refresh": refresh.ctx,
	} {
		gotDeadline, ok := ctx.Deadline()
		if !ok || !gotDeadline.Equal(deadline) {
			t.Fatalf("%s deadline=%v,%v want=%v,true", name, gotDeadline, ok, deadline)
		}
	}
	assertRestReplacementTrace(t, snapshot.trace, "role", "replace", "refresh")
	cancel()
	if refresh.ctx.Err() != context.Canceled {
		t.Fatalf("refresh cancellation=%v want=%v", refresh.ctx.Err(), context.Canceled)
	}
}

func TestPreparedRESTReplacementPreservesNotePresenceOrderValuesAndIsolation(t *testing.T) {
	tests := []struct {
		name        string
		notes       []NoteDraft
		want        []store.WallNote
		mutateInput bool
	}{
		{name: "nil notes remain nil"},
		{name: "supplied empty notes remain non-nil", notes: []NoteDraft{}, want: []store.WallNote{}},
		{
			name: "raw ordered values are isolated",
			notes: []NoteDraft{
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t first \n "},
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: ""},
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t first \n "},
			},
			want: []store.WallNote{
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t first \n "},
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: ""},
				{X: -100001.25, Y: 100001.5, Width: 0, Height: -1, Color: " NOT-A-COLOR ", Text: " \t first \n "},
			},
			mutateInput: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := cloneRestReplacementDrafts(tt.notes)
			command := ReplaceWallCommand{Notes: cloneRestReplacementDrafts(tt.notes)}
			fake := &restReplacementFake{
				role: store.RoleContributor, mutateReceivedNotes: tt.mutateInput,
			}
			prepared := mustPrepareRESTReplacement(t, fake, tt.name)

			if _, err := prepared.Replace(command); err != nil {
				t.Fatalf("Replace: %v", err)
			}
			// ReplaceWall has already mutated its received store slice here. The
			// caller-owned command must still contain the original values.
			if !reflect.DeepEqual(command.Notes, original) {
				t.Fatalf("store mutation changed caller command: got=%+v want=%+v", command.Notes, original)
			}

			snapshot := fake.snapshot()
			if len(snapshot.replaceCalls) != 1 || len(snapshot.refreshCalls) != 1 {
				t.Fatalf("replace/refresh calls=%d/%d", len(snapshot.replaceCalls), len(snapshot.refreshCalls))
			}
			call := snapshot.replaceCalls[0]
			if (call.notesBeforeMutation == nil) != (tt.want == nil) || !reflect.DeepEqual(call.notesBeforeMutation, tt.want) {
				t.Fatalf("store notes=%#v want=%#v", call.notesBeforeMutation, tt.want)
			}
			if tt.mutateInput {
				if len(call.retainedNotes) == 0 || call.retainedNotes[0] != restReplacementMutatedNote {
					t.Fatalf("fake did not mutate and retain received store slice: %+v", call.retainedNotes)
				}
				command.Notes[0] = NoteDraft{Text: "caller changed after execution"}
				afterCallerMutation := fake.snapshot().replaceCalls[0].retainedNotes
				if len(afterCallerMutation) == 0 || afterCallerMutation[0] != restReplacementMutatedNote {
					t.Fatalf("caller mutation changed retained store slice: %+v", afterCallerMutation)
				}
			}
			if snapshot.refreshCalls[0].reason != RefreshReplaced {
				t.Fatalf("refresh reason=%q want=%q", snapshot.refreshCalls[0].reason, RefreshReplaced)
			}
			assertRestReplacementTrace(t, snapshot.trace, "role", "replace", "refresh")
		})
	}
}

func TestPreparedRESTReplacementStoreFailureReturnsZeroAndSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("replacement failed")
	fake := &restReplacementFake{
		role:          store.RoleContributor,
		replaceResult: store.Wall{Notes: []store.WallNote{{ID: "must-not-return"}}, Version: 99},
		replaceErr:    storeFailure,
	}
	prepared := mustPrepareRESTReplacement(t, fake, "failure")

	got, err := prepared.Replace(ReplaceWallCommand{Notes: []NoteDraft{{Text: "input"}}})
	if !isZeroRestReplacementWall(got) || err != storeFailure {
		t.Fatalf("Replace=(%+v,%v) want=(zero,%v)", got, err, storeFailure)
	}
	snapshot := fake.snapshot()
	if snapshot.roleCalls != 1 || len(snapshot.replaceCalls) != 1 || len(snapshot.refreshCalls) != 0 {
		t.Fatalf("role/replace/refresh=%d/%d/%d", snapshot.roleCalls, len(snapshot.replaceCalls), len(snapshot.refreshCalls))
	}
	assertRestReplacementTrace(t, snapshot.trace, "role", "replace")
}

func TestPreparedRESTReplacementAtMostOnceAfterSuccessAndFailure(t *testing.T) {
	t.Run("success consumes replacement", func(t *testing.T) {
		wantWall := store.Wall{Version: 21, UpdatedAt: 123}
		fake := &restReplacementFake{role: store.RoleContributor, replaceResult: wantWall}
		prepared := mustPrepareRESTReplacement(t, fake, "once-success")
		got, err := prepared.Replace(ReplaceWallCommand{})
		if err != nil || !reflect.DeepEqual(got, wantWall) {
			t.Fatalf("first Replace=(%+v,%v)", got, err)
		}
		first := fake.snapshot()
		got, err = prepared.Replace(ReplaceWallCommand{Notes: []NoteDraft{{Text: "must not run"}}})
		if !isZeroRestReplacementWall(got) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("repeat Replace=(%+v,%v)", got, err)
		}
		if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
			t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
		}
	})

	t.Run("failure consumes replacement", func(t *testing.T) {
		storeFailure := errors.New("first replacement failed")
		fake := &restReplacementFake{role: store.RoleContributor, replaceErr: storeFailure}
		prepared := mustPrepareRESTReplacement(t, fake, "once-failure")
		if got, err := prepared.Replace(ReplaceWallCommand{}); !isZeroRestReplacementWall(got) || err != storeFailure {
			t.Fatalf("first Replace=(%+v,%v)", got, err)
		}
		first := fake.snapshot()
		if got, err := prepared.Replace(ReplaceWallCommand{}); !isZeroRestReplacementWall(got) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
			t.Fatalf("repeat Replace=(%+v,%v)", got, err)
		}
		if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
			t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
		}
	})
}

func TestPreparedRESTReplacementConcurrentExecutionStartsOnce(t *testing.T) {
	wantWall := store.Wall{Version: 33, UpdatedAt: 456}
	fake := &restReplacementFake{role: store.RoleContributor, replaceResult: wantWall}
	prepared := mustPrepareRESTReplacement(t, fake, "concurrent-success")
	start := make(chan struct{})
	results := make(chan struct {
		wall store.Wall
		err  error
	}, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			wall, err := prepared.Replace(ReplaceWallCommand{Notes: []NoteDraft{{Text: "race"}}})
			results <- struct {
				wall store.Wall
				err  error
			}{wall: wall, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, repeats := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			if !reflect.DeepEqual(result.wall, wantWall) {
				t.Fatalf("success Wall=%+v want=%+v", result.wall, wantWall)
			}
		case errors.Is(result.err, ErrPreparedMutationAlreadyExecuted):
			repeats++
			if !isZeroRestReplacementWall(result.wall) {
				t.Fatalf("repeat Wall=%+v want zero", result.wall)
			}
		default:
			t.Fatalf("unexpected concurrent result=(%+v,%v)", result.wall, result.err)
		}
	}
	snapshot := fake.snapshot()
	if successes != 1 || repeats != 7 || snapshot.roleCalls != 1 ||
		len(snapshot.replaceCalls) != 1 || len(snapshot.refreshCalls) != 1 {
		t.Fatalf("success/repeat/role/replace/refresh=%d/%d/%d/%d/%d", successes, repeats, snapshot.roleCalls, len(snapshot.replaceCalls), len(snapshot.refreshCalls))
	}
	assertRestReplacementTrace(t, snapshot.trace, "role", "replace", "refresh")
}

func TestPreparedRESTReplacementConcurrentStoreFailureSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("winning replacement failed")
	fake := &restReplacementFake{role: store.RoleContributor, replaceErr: storeFailure}
	prepared := mustPrepareRESTReplacement(t, fake, "concurrent-failure")
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := prepared.Replace(ReplaceWallCommand{})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	failures, repeats := 0, 0
	for err := range errs {
		switch {
		case err == storeFailure:
			failures++
		case errors.Is(err, ErrPreparedMutationAlreadyExecuted):
			repeats++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	snapshot := fake.snapshot()
	if failures != 1 || repeats != 7 || snapshot.roleCalls != 1 ||
		len(snapshot.replaceCalls) != 1 || len(snapshot.refreshCalls) != 0 {
		t.Fatalf("failure/repeat/role/replace/refresh=%d/%d/%d/%d/%d", failures, repeats, snapshot.roleCalls, len(snapshot.replaceCalls), len(snapshot.refreshCalls))
	}
	assertRestReplacementTrace(t, snapshot.trace, "role", "replace")
}

func TestPreparedRESTReplacementCanceledMutationContext(t *testing.T) {
	effectCtx := context.WithValue(context.Background(), restReplacementRawContextKey{}, "raw-not-canceled")
	mutationBase, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		restReplacementMutationContextKey{},
		"mutation-canceled",
	))
	defer cancel()
	mutationCtx := store.WithUserID(mutationBase, restReplacementTestActorID)
	fake := &restReplacementFake{
		role: store.RoleContributor, honorMutationContextError: true,
	}
	service := newRESTReplacementTestService(fake)
	prepared, err := service.Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restReplacementTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if mutationCtx.Err() != nil {
		t.Fatalf("mutation context canceled during Prepare: %v", mutationCtx.Err())
	}
	cancel()

	if got, err := prepared.Replace(ReplaceWallCommand{}); !isZeroRestReplacementWall(got) || err != context.Canceled {
		t.Fatalf("Replace=(%+v,%v) want=(zero,%v)", got, err, context.Canceled)
	}
	first := fake.snapshot()
	if len(first.replaceCalls) != 1 || first.replaceCalls[0].ctx != mutationCtx || len(first.refreshCalls) != 0 {
		t.Fatalf("canceled sequence=%+v", first)
	}
	assertRestReplacementTrace(t, first.trace, "role", "replace")
	if got, err := prepared.Replace(ReplaceWallCommand{}); !isZeroRestReplacementWall(got) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
		t.Fatalf("repeat Replace=(%+v,%v)", got, err)
	}
	if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
		t.Fatalf("repeat after cancellation changed dependencies: first=%+v after=%+v", first, after)
	}
}
