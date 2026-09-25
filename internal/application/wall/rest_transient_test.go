package wall

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type restTransientRawContextKey struct{}
type restTransientMutationContextKey struct{}

const (
	restTransientTestProjectID = int64(181)
	restTransientTestActorID   = int64(71)
)

type restTransientPublishCall struct {
	ctx       context.Context
	projectID int64
	event     TransientEvent
}

type restTransientFake struct {
	mu sync.Mutex

	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	publishCalls            []restTransientPublishCall
	publishErr              error
	honorEffectContextError bool
}

var (
	_ RESTWriterRoleStore    = (*restTransientFake)(nil)
	_ WallTransientPublisher = (*restTransientFake)(nil)
)

func (f *restTransientFake) GetProjectRole(
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

func (f *restTransientFake) PublishWallTransient(
	ctx context.Context,
	projectID int64,
	event TransientEvent,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "publish")
	f.publishCalls = append(f.publishCalls, restTransientPublishCall{
		ctx: ctx, projectID: projectID, event: event,
	})
	if f.honorEffectContextError && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.publishErr
}

type restTransientFakeSnapshot struct {
	trace []string

	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	publishCalls []restTransientPublishCall
}

func (f *restTransientFake) snapshot() restTransientFakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return restTransientFakeSnapshot{
		trace:        append([]string(nil), f.trace...),
		roleCalls:    f.roleCalls,
		roleCtx:      f.roleCtx,
		rolePID:      f.rolePID,
		roleUID:      f.roleUID,
		publishCalls: append([]restTransientPublishCall(nil), f.publishCalls...),
	}
}

func newRESTTransientTestService(fake *restTransientFake) *RESTTransientService {
	return NewRESTTransientService(RESTTransientServiceDependencies{
		Roles:     fake,
		Publisher: fake,
	})
}

func restTransientContexts(actorID int64, marker string) (context.Context, context.Context) {
	effectCtx := context.WithValue(context.Background(), restTransientRawContextKey{}, marker+"-raw")
	mutationCtx := context.WithValue(effectCtx, restTransientMutationContextKey{}, marker+"-mutation")
	mutationCtx = store.WithUserID(mutationCtx, actorID)
	return mutationCtx, effectCtx
}

func mustPrepareRESTTransient(
	t *testing.T,
	fake *restTransientFake,
	marker string,
) *PreparedRESTTransient {
	t.Helper()
	mutationCtx, effectCtx := restTransientContexts(restTransientTestActorID, marker)
	prepared, err := newRESTTransientTestService(fake).Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restTransientTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func assertRestTransientTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func TestRESTTransientPreparationAuthorization(t *testing.T) {
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
		{name: "role read error", withMutationActor: true, mutationActor: 72, roleErr: roleFailure, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 72},
		{name: "empty role", withMutationActor: true, mutationActor: 73, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 73},
		{name: "unknown role", withMutationActor: true, mutationActor: 74, role: store.ProjectRole("unknown"), wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 74},
		{name: "viewer", withMutationActor: true, mutationActor: 75, role: store.RoleViewer, wantErr: ErrContributorRequired, wantRoleCalls: 1, wantMutationActorID: 75},
		{name: "contributor", withMutationActor: true, mutationActor: 76, role: store.RoleContributor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 76},
		{name: "maintainer", withMutationActor: true, mutationActor: 77, role: store.RoleMaintainer, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 77},
		{name: "deprecated editor", withMutationActor: true, mutationActor: 78, role: store.RoleEditor, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 78},
		{name: "deprecated owner", withMutationActor: true, mutationActor: 79, role: store.RoleOwner, wantPrepared: true, wantRoleCalls: 1, wantMutationActorID: 79},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restTransientFake{role: tt.role, roleErr: tt.roleErr}
			mutationCtx := context.WithValue(context.Background(), restTransientMutationContextKey{}, tt.name)
			if tt.withMutationActor {
				mutationCtx = store.WithUserID(mutationCtx, tt.mutationActor)
			}
			effectCtx := context.WithValue(context.Background(), restTransientRawContextKey{}, tt.name)
			if tt.effectActor != 0 {
				effectCtx = store.WithUserID(effectCtx, tt.effectActor)
			}

			prepared, err := newRESTTransientTestService(fake).Prepare(
				mutationCtx,
				effectCtx,
				ResolvedRESTTarget{ProjectID: restTransientTestProjectID},
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if prepared != nil {
				if prepared.writer.mutationCtx != mutationCtx || prepared.writer.effectCtx != effectCtx ||
					prepared.writer.actorID != tt.wantMutationActorID || prepared.writer.projectID != restTransientTestProjectID {
					t.Fatalf("bound writer=%+v", prepared.writer)
				}
			}

			snapshot := fake.snapshot()
			if snapshot.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", snapshot.roleCalls, tt.wantRoleCalls)
			}
			if tt.wantRoleCalls == 1 {
				if snapshot.roleCtx != mutationCtx || snapshot.rolePID != restTransientTestProjectID || snapshot.roleUID != tt.wantMutationActorID {
					t.Fatalf("role call context/project/user mismatch: ctxSame=%v project=%d user=%d", snapshot.roleCtx == mutationCtx, snapshot.rolePID, snapshot.roleUID)
				}
				assertRestTransientTrace(t, snapshot.trace, "role")
			} else {
				assertRestTransientTrace(t, snapshot.trace)
			}
			if len(snapshot.publishCalls) != 0 {
				t.Fatalf("preparation published: %+v", snapshot.publishCalls)
			}
		})
	}
}

func TestPreparedRESTTransientBindsDistinctContextsActorAndProject(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	rawMarked := context.WithValue(context.Background(), restTransientRawContextKey{}, "raw-marker")
	effectCtx, cancel := context.WithDeadline(rawMarked, deadline)
	defer cancel()
	mutationCtx := context.WithValue(effectCtx, restTransientMutationContextKey{}, "mutation-marker")
	mutationCtx = store.WithUserID(mutationCtx, restTransientTestActorID)
	target := ResolvedRESTTarget{ProjectID: restTransientTestProjectID}
	fake := &restTransientFake{role: store.RoleContributor}

	prepared, err := newRESTTransientTestService(fake).Prepare(mutationCtx, effectCtx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectID = 999
	command := TransientCommand{NoteID: " context-note ", X: -12.5, Y: 44.25}
	if err := prepared.Publish(command); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	snapshot := fake.snapshot()
	if prepared.writer.actorID != restTransientTestActorID || prepared.writer.projectID != restTransientTestProjectID {
		t.Fatalf("prepared writer actor/project=%d/%d", prepared.writer.actorID, prepared.writer.projectID)
	}
	if snapshot.roleCalls != 1 || snapshot.roleCtx != mutationCtx ||
		snapshot.rolePID != restTransientTestProjectID || snapshot.roleUID != restTransientTestActorID {
		t.Fatalf("role call=%+v", snapshot)
	}
	if len(snapshot.publishCalls) != 1 {
		t.Fatalf("publish calls=%+v", snapshot.publishCalls)
	}
	publish := snapshot.publishCalls[0]
	wantEvent := TransientEvent{NoteID: command.NoteID, X: command.X, Y: command.Y, By: restTransientTestActorID}
	if publish.ctx != effectCtx || publish.projectID != restTransientTestProjectID || publish.event != wantEvent {
		t.Fatalf("publish=%+v want context same/project=%d/event=%+v", publish, restTransientTestProjectID, wantEvent)
	}
	if mutationCtx.Value(restTransientRawContextKey{}) != "raw-marker" ||
		mutationCtx.Value(restTransientMutationContextKey{}) != "mutation-marker" {
		t.Fatal("mutation context lost marker values")
	}
	if _, ok := store.UserIDFromContext(effectCtx); ok {
		t.Fatal("raw effect context unexpectedly contains actor enrichment")
	}
	for name, ctx := range map[string]context.Context{"role": snapshot.roleCtx, "publish": publish.ctx} {
		gotDeadline, ok := ctx.Deadline()
		if !ok || !gotDeadline.Equal(deadline) {
			t.Fatalf("%s deadline=%v,%v want=%v,true", name, gotDeadline, ok, deadline)
		}
	}
	assertRestTransientTrace(t, snapshot.trace, "role", "publish")
	cancel()
	if snapshot.roleCtx.Err() != context.Canceled || publish.ctx.Err() != context.Canceled {
		t.Fatalf("stored context cancellation role/publish=%v/%v", snapshot.roleCtx.Err(), publish.ctx.Err())
	}
}

func TestPreparedRESTTransientPreservesRawCommandAndBuildsExactEvent(t *testing.T) {
	tests := []struct {
		name    string
		command TransientCommand
	}{
		{name: "surrounding whitespace", command: TransientCommand{NoteID: " \t note-id \n ", X: -1.25, Y: 2.5}},
		{name: "empty note ID", command: TransientCommand{NoteID: "", X: 0, Y: 0}},
		{name: "whitespace-only note ID", command: TransientCommand{NoteID: " \r\n\t ", X: -0.5, Y: 0.75}},
		{name: "unicode note ID", command: TransientCommand{NoteID: "ノート-🗒️-α", X: 12.125, Y: -99.5}},
		{name: "arbitrary nonexistent-looking ID", command: TransientCommand{NoteID: "missing/note?not-found#x", X: 1e100, Y: -1e-100}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restTransientFake{role: store.RoleContributor}
			prepared := mustPrepareRESTTransient(t, fake, tt.name)

			if err := prepared.Publish(tt.command); err != nil {
				t.Fatalf("Publish: %v", err)
			}
			snapshot := fake.snapshot()
			if snapshot.roleCalls != 1 || len(snapshot.publishCalls) != 1 {
				t.Fatalf("role/publish=%d/%d", snapshot.roleCalls, len(snapshot.publishCalls))
			}
			call := snapshot.publishCalls[0]
			wantEvent := TransientEvent{
				NoteID: tt.command.NoteID,
				X:      tt.command.X,
				Y:      tt.command.Y,
				By:     restTransientTestActorID,
			}
			if call.projectID != restTransientTestProjectID || call.event != wantEvent {
				t.Fatalf("publish=%+v want project=%d/event=%+v", call, restTransientTestProjectID, wantEvent)
			}
			assertRestTransientTrace(t, snapshot.trace, "role", "publish")
		})
	}
}

func TestPreparedRESTTransientPublisherErrorPassesThrough(t *testing.T) {
	publisherFailure := errors.New("transient publisher failed")
	fake := &restTransientFake{role: store.RoleContributor, publishErr: publisherFailure}
	prepared := mustPrepareRESTTransient(t, fake, "publisher-failure")
	command := TransientCommand{NoteID: " raw-note ", X: 3.25, Y: -8.5}

	if err := prepared.Publish(command); err != publisherFailure {
		t.Fatalf("Publish error=%v want exact %v", err, publisherFailure)
	}
	snapshot := fake.snapshot()
	if snapshot.roleCalls != 1 || len(snapshot.publishCalls) != 1 {
		t.Fatalf("role/publish=%d/%d", snapshot.roleCalls, len(snapshot.publishCalls))
	}
	wantEvent := TransientEvent{NoteID: command.NoteID, X: command.X, Y: command.Y, By: restTransientTestActorID}
	if snapshot.publishCalls[0].event != wantEvent {
		t.Fatalf("event=%+v want=%+v", snapshot.publishCalls[0].event, wantEvent)
	}
	assertRestTransientTrace(t, snapshot.trace, "role", "publish")
}

func TestPreparedRESTTransientAtMostOnceAfterSuccessAndFailure(t *testing.T) {
	publisherFailure := errors.New("first publication failed")
	for _, tt := range []struct {
		name     string
		firstErr error
	}{
		{name: "success"},
		{name: "failure", firstErr: publisherFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restTransientFake{role: store.RoleContributor, publishErr: tt.firstErr}
			prepared := mustPrepareRESTTransient(t, fake, tt.name)
			firstErr := prepared.Publish(TransientCommand{NoteID: "first", X: 1, Y: 2})
			if firstErr != tt.firstErr {
				t.Fatalf("first error=%v want=%v", firstErr, tt.firstErr)
			}
			first := fake.snapshot()

			for i := range 3 {
				err := prepared.Publish(TransientCommand{NoteID: fmt.Sprintf("repeat-%d", i), X: float64(i), Y: -float64(i)})
				if !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
					t.Fatalf("repeat %d error=%v", i, err)
				}
			}
			if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
				t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
			}
			if first.roleCalls != 1 || len(first.publishCalls) != 1 {
				t.Fatalf("role/publish=%d/%d", first.roleCalls, len(first.publishCalls))
			}
		})
	}
}

func TestPreparedRESTTransientConcurrentExecutionPublishesOnce(t *testing.T) {
	fake := &restTransientFake{role: store.RoleContributor}
	prepared := mustPrepareRESTTransient(t, fake, "concurrent-success")
	runConcurrentRESTTransientPublishes(t, prepared, fake, nil)
}

func TestPreparedRESTTransientConcurrentPublisherFailureAttemptsOnce(t *testing.T) {
	publisherFailure := errors.New("winning publication failed")
	fake := &restTransientFake{role: store.RoleContributor, publishErr: publisherFailure}
	prepared := mustPrepareRESTTransient(t, fake, "concurrent-failure")
	runConcurrentRESTTransientPublishes(t, prepared, fake, publisherFailure)
}

func runConcurrentRESTTransientPublishes(
	t *testing.T,
	prepared *PreparedRESTTransient,
	fake *restTransientFake,
	winnerErr error,
) {
	t.Helper()
	type outcome struct {
		command TransientCommand
		err     error
	}

	start := make(chan struct{})
	results := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		command := TransientCommand{
			NoteID: fmt.Sprintf("candidate-%d", i),
			X:      float64(i) + 0.25,
			Y:      -float64(i) - 0.75,
		}
		wg.Add(1)
		go func(command TransientCommand) {
			defer wg.Done()
			<-start
			results <- outcome{command: command, err: prepared.Publish(command)}
		}(command)
	}
	close(start)
	wg.Wait()
	close(results)

	winners, repeats := 0, 0
	var winnerCommand TransientCommand
	for result := range results {
		switch {
		case result.err == winnerErr:
			winners++
			winnerCommand = result.command
		case errors.Is(result.err, ErrPreparedMutationAlreadyExecuted):
			repeats++
		default:
			t.Fatalf("unexpected concurrent result=%+v", result)
		}
	}

	snapshot := fake.snapshot()
	if winners != 1 || repeats != 7 || snapshot.roleCalls != 1 || len(snapshot.publishCalls) != 1 {
		t.Fatalf("winner/repeat/role/publish=%d/%d/%d/%d", winners, repeats, snapshot.roleCalls, len(snapshot.publishCalls))
	}
	wantEvent := TransientEvent{
		NoteID: winnerCommand.NoteID,
		X:      winnerCommand.X,
		Y:      winnerCommand.Y,
		By:     restTransientTestActorID,
	}
	if snapshot.publishCalls[0].event != wantEvent {
		t.Fatalf("published event=%+v want winner event=%+v", snapshot.publishCalls[0].event, wantEvent)
	}
	assertRestTransientTrace(t, snapshot.trace, "role", "publish")
}

func TestPreparedRESTTransientCanceledEffectContext(t *testing.T) {
	effectBase := context.WithValue(context.Background(), restTransientRawContextKey{}, "canceled-effect")
	effectCtx, cancelEffect := context.WithCancel(effectBase)
	defer cancelEffect()
	mutationCtx := store.WithUserID(
		context.WithValue(context.Background(), restTransientMutationContextKey{}, "live-mutation"),
		restTransientTestActorID,
	)
	fake := &restTransientFake{role: store.RoleContributor, honorEffectContextError: true}
	prepared, err := newRESTTransientTestService(fake).Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restTransientTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancelEffect()
	command := TransientCommand{NoteID: "canceled-effect-note", X: 7, Y: 8}
	if err := prepared.Publish(command); err != context.Canceled {
		t.Fatalf("Publish error=%v want=%v", err, context.Canceled)
	}

	first := fake.snapshot()
	if first.roleCalls != 1 || len(first.publishCalls) != 1 {
		t.Fatalf("role/publish=%d/%d", first.roleCalls, len(first.publishCalls))
	}
	call := first.publishCalls[0]
	wantEvent := TransientEvent{NoteID: command.NoteID, X: command.X, Y: command.Y, By: restTransientTestActorID}
	if call.ctx != effectCtx || call.ctx.Err() != context.Canceled || call.event != wantEvent {
		t.Fatalf("publish call=%+v want exact canceled effect context/event=%+v", call, wantEvent)
	}
	if mutationCtx.Err() != nil {
		t.Fatalf("mutation context unexpectedly canceled: %v", mutationCtx.Err())
	}
	if err := prepared.Publish(TransientCommand{NoteID: "repeat"}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
		t.Fatalf("repeat error=%v", err)
	}
	if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
		t.Fatalf("repeat after cancellation changed dependencies: first=%+v after=%+v", first, after)
	}
}

func TestPreparedRESTTransientCanceledMutationContextStillPublishes(t *testing.T) {
	mutationBase := context.WithValue(context.Background(), restTransientMutationContextKey{}, "canceled-mutation")
	mutationCancelable, cancelMutation := context.WithCancel(mutationBase)
	defer cancelMutation()
	mutationCtx := store.WithUserID(mutationCancelable, restTransientTestActorID)
	effectCtx := context.WithValue(context.Background(), restTransientRawContextKey{}, "live-effect")
	fake := &restTransientFake{role: store.RoleContributor, honorEffectContextError: true}
	prepared, err := newRESTTransientTestService(fake).Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restTransientTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancelMutation()
	if mutationCtx.Err() != context.Canceled || effectCtx.Err() != nil {
		t.Fatalf("mutation/effect cancellation=%v/%v", mutationCtx.Err(), effectCtx.Err())
	}
	command := TransientCommand{NoteID: "mutation-canceled-but-publish", X: -4, Y: 12}
	if err := prepared.Publish(command); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	snapshot := fake.snapshot()
	if snapshot.roleCalls != 1 || len(snapshot.publishCalls) != 1 {
		t.Fatalf("role/publish=%d/%d", snapshot.roleCalls, len(snapshot.publishCalls))
	}
	call := snapshot.publishCalls[0]
	wantEvent := TransientEvent{NoteID: command.NoteID, X: command.X, Y: command.Y, By: restTransientTestActorID}
	if call.ctx != effectCtx || call.ctx.Err() != nil || call.event != wantEvent {
		t.Fatalf("publish call=%+v want exact live effect context/event=%+v", call, wantEvent)
	}
	assertRestTransientTrace(t, snapshot.trace, "role", "publish")
}
