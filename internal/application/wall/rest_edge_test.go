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

type restEdgeRawContextKey struct{}
type restEdgeMutationContextKey struct{}

const (
	restEdgeTestProjectID = int64(163)
	restEdgeTestActorID   = int64(71)
)

type restEdgeCreateCall struct {
	ctx       context.Context
	projectID int64
	from      string
	to        string
}

type restEdgeDeleteCall struct {
	ctx       context.Context
	projectID int64
	edgeID    string
}

type restEdgeRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    RefreshReason
}

type restEdgeFake struct {
	mu sync.Mutex

	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls []restEdgeCreateCall
	createEdge  store.WallEdge
	createWall  store.Wall
	createErr   error

	deleteCalls []restEdgeDeleteCall
	deleteWall  store.Wall
	deleteErr   error

	refreshCalls []restEdgeRefreshCall

	honorMutationContextError bool
}

var (
	_ RESTWriterRoleStore  = (*restEdgeFake)(nil)
	_ EdgeMutationStore    = (*restEdgeFake)(nil)
	_ WallRefreshPublisher = (*restEdgeFake)(nil)
)

func (f *restEdgeFake) GetProjectRole(
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

func (f *restEdgeFake) CreateEdge(
	ctx context.Context,
	projectID int64,
	fromNoteID string,
	toNoteID string,
) (store.WallEdge, store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "create")
	f.createCalls = append(f.createCalls, restEdgeCreateCall{
		ctx: ctx, projectID: projectID, from: fromNoteID, to: toNoteID,
	})
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.WallEdge{}, store.Wall{}, ctx.Err()
	}
	return f.createEdge, f.createWall, f.createErr
}

func (f *restEdgeFake) DeleteEdge(
	ctx context.Context,
	projectID int64,
	edgeID string,
) (store.Wall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "delete")
	f.deleteCalls = append(f.deleteCalls, restEdgeDeleteCall{
		ctx: ctx, projectID: projectID, edgeID: edgeID,
	})
	if f.honorMutationContextError && ctx.Err() != nil {
		return store.Wall{}, ctx.Err()
	}
	return f.deleteWall, f.deleteErr
}

func (f *restEdgeFake) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason RefreshReason,
) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "refresh")
	f.refreshCalls = append(f.refreshCalls, restEdgeRefreshCall{
		ctx: ctx, projectID: projectID, reason: reason,
	})
}

type restEdgeFakeSnapshot struct {
	trace []string

	roleCalls int
	roleCtx   context.Context
	rolePID   int64
	roleUID   int64

	createCalls  []restEdgeCreateCall
	deleteCalls  []restEdgeDeleteCall
	refreshCalls []restEdgeRefreshCall
}

func (f *restEdgeFake) snapshot() restEdgeFakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return restEdgeFakeSnapshot{
		trace:        append([]string(nil), f.trace...),
		roleCalls:    f.roleCalls,
		roleCtx:      f.roleCtx,
		rolePID:      f.rolePID,
		roleUID:      f.roleUID,
		createCalls:  append([]restEdgeCreateCall(nil), f.createCalls...),
		deleteCalls:  append([]restEdgeDeleteCall(nil), f.deleteCalls...),
		refreshCalls: append([]restEdgeRefreshCall(nil), f.refreshCalls...),
	}
}

func newRESTEdgeTestService(fake *restEdgeFake) *RESTEdgeService {
	return NewRESTEdgeService(RESTEdgeServiceDependencies{
		Roles:     fake,
		Mutations: fake,
		Refresh:   fake,
	})
}

func restEdgeContexts(actorID int64, marker string) (context.Context, context.Context) {
	effectCtx := context.WithValue(context.Background(), restEdgeRawContextKey{}, marker+"-raw")
	mutationCtx := context.WithValue(effectCtx, restEdgeMutationContextKey{}, marker+"-mutation")
	mutationCtx = store.WithUserID(mutationCtx, actorID)
	return mutationCtx, effectCtx
}

func mustPrepareRESTEdge(
	t *testing.T,
	fake *restEdgeFake,
	marker string,
) *PreparedRESTEdgeMutation {
	t.Helper()
	mutationCtx, effectCtx := restEdgeContexts(restEdgeTestActorID, marker)
	prepared, err := newRESTEdgeTestService(fake).Prepare(
		mutationCtx,
		effectCtx,
		ResolvedRESTTarget{ProjectID: restEdgeTestProjectID},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func assertRestEdgeTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace=%v want=%v", got, want)
	}
}

func isZeroRestEdge(edge store.WallEdge) bool {
	return edge == (store.WallEdge{})
}

func TestRESTEdgePreparationAuthorization(t *testing.T) {
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
			fake := &restEdgeFake{role: tt.role, roleErr: tt.roleErr}
			mutationCtx := context.WithValue(context.Background(), restEdgeMutationContextKey{}, tt.name)
			if tt.withMutationActor {
				mutationCtx = store.WithUserID(mutationCtx, tt.mutationActor)
			}
			effectCtx := context.WithValue(context.Background(), restEdgeRawContextKey{}, tt.name)
			if tt.effectActor != 0 {
				effectCtx = store.WithUserID(effectCtx, tt.effectActor)
			}

			prepared, err := newRESTEdgeTestService(fake).Prepare(
				mutationCtx,
				effectCtx,
				ResolvedRESTTarget{ProjectID: restEdgeTestProjectID},
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if prepared != nil {
				if prepared.writer.mutationCtx != mutationCtx || prepared.writer.effectCtx != effectCtx ||
					prepared.writer.actorID != tt.wantMutationActorID || prepared.writer.projectID != restEdgeTestProjectID {
					t.Fatalf("bound writer=%+v", prepared.writer)
				}
			}

			snapshot := fake.snapshot()
			if snapshot.roleCalls != tt.wantRoleCalls {
				t.Fatalf("role calls=%d want=%d", snapshot.roleCalls, tt.wantRoleCalls)
			}
			if tt.wantRoleCalls == 1 {
				if snapshot.roleCtx != mutationCtx || snapshot.rolePID != restEdgeTestProjectID || snapshot.roleUID != tt.wantMutationActorID {
					t.Fatalf("role call context/project/user mismatch: ctxSame=%v project=%d user=%d", snapshot.roleCtx == mutationCtx, snapshot.rolePID, snapshot.roleUID)
				}
				assertRestEdgeTrace(t, snapshot.trace, "role")
			} else {
				assertRestEdgeTrace(t, snapshot.trace)
			}
			if len(snapshot.createCalls) != 0 || len(snapshot.deleteCalls) != 0 || len(snapshot.refreshCalls) != 0 {
				t.Fatalf("preparation caused effects: %+v", snapshot)
			}
		})
	}
}

func TestPreparedRESTEdgeBindsDistinctContextsActorAndProject(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "create", method: "create"},
		{name: "delete", method: "delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deadline := time.Now().Add(time.Minute)
			rawMarked := context.WithValue(context.Background(), restEdgeRawContextKey{}, tt.name+"-raw")
			effectCtx, cancel := context.WithDeadline(rawMarked, deadline)
			defer cancel()
			mutationCtx := context.WithValue(effectCtx, restEdgeMutationContextKey{}, tt.name+"-mutation")
			mutationCtx = store.WithUserID(mutationCtx, restEdgeTestActorID)
			target := ResolvedRESTTarget{ProjectID: restEdgeTestProjectID}
			wantEdge := store.WallEdge{ID: "store-edge", From: "stored-from", To: "stored-to"}
			fake := &restEdgeFake{role: store.RoleContributor, createEdge: wantEdge}

			prepared, err := newRESTEdgeTestService(fake).Prepare(mutationCtx, effectCtx, target)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			target.ProjectID = 999
			if tt.method == "create" {
				got, err := prepared.Create(CreateEdgeCommand{From: " raw-from ", To: " raw-to "})
				if err != nil || got != wantEdge {
					t.Fatalf("Create=(%+v,%v) want=(%+v,nil)", got, err, wantEdge)
				}
			} else if err := prepared.Delete(DeleteEdgeCommand{EdgeID: " raw-edge-id "}); err != nil {
				t.Fatalf("Delete: %v", err)
			}

			snapshot := fake.snapshot()
			if prepared.writer.actorID != restEdgeTestActorID || prepared.writer.projectID != restEdgeTestProjectID {
				t.Fatalf("prepared writer actor/project=%d/%d", prepared.writer.actorID, prepared.writer.projectID)
			}
			if snapshot.roleCalls != 1 || snapshot.roleCtx != mutationCtx ||
				snapshot.rolePID != restEdgeTestProjectID || snapshot.roleUID != restEdgeTestActorID {
				t.Fatalf("role call=%+v", snapshot)
			}

			var storeCtx context.Context
			wantReason := RefreshEdgeCreated
			if tt.method == "create" {
				if len(snapshot.createCalls) != 1 || len(snapshot.deleteCalls) != 0 {
					t.Fatalf("create/delete calls=%+v/%+v", snapshot.createCalls, snapshot.deleteCalls)
				}
				call := snapshot.createCalls[0]
				storeCtx = call.ctx
				if call.ctx != mutationCtx || call.projectID != restEdgeTestProjectID || call.from != " raw-from " || call.to != " raw-to " {
					t.Fatalf("create call=%+v", call)
				}
				assertRestEdgeTrace(t, snapshot.trace, "role", "create", "refresh")
			} else {
				wantReason = RefreshEdgeDeleted
				if len(snapshot.deleteCalls) != 1 || len(snapshot.createCalls) != 0 {
					t.Fatalf("create/delete calls=%+v/%+v", snapshot.createCalls, snapshot.deleteCalls)
				}
				call := snapshot.deleteCalls[0]
				storeCtx = call.ctx
				if call.ctx != mutationCtx || call.projectID != restEdgeTestProjectID || call.edgeID != " raw-edge-id " {
					t.Fatalf("delete call=%+v", call)
				}
				assertRestEdgeTrace(t, snapshot.trace, "role", "delete", "refresh")
			}

			if len(snapshot.refreshCalls) != 1 {
				t.Fatalf("refresh calls=%+v", snapshot.refreshCalls)
			}
			refresh := snapshot.refreshCalls[0]
			if refresh.ctx != effectCtx || refresh.projectID != restEdgeTestProjectID || refresh.reason != wantReason {
				t.Fatalf("refresh=%+v", refresh)
			}
			if mutationCtx.Value(restEdgeRawContextKey{}) != tt.name+"-raw" ||
				mutationCtx.Value(restEdgeMutationContextKey{}) != tt.name+"-mutation" {
				t.Fatal("mutation context lost marker values")
			}
			actorID, ok := store.UserIDFromContext(mutationCtx)
			if !ok || actorID != restEdgeTestActorID {
				t.Fatalf("mutation actor=%d,%v", actorID, ok)
			}
			if _, ok := store.UserIDFromContext(effectCtx); ok {
				t.Fatal("raw effect context unexpectedly contains actor enrichment")
			}
			for name, ctx := range map[string]context.Context{
				"role": snapshot.roleCtx, "store": storeCtx, "refresh": refresh.ctx,
			} {
				gotDeadline, ok := ctx.Deadline()
				if !ok || !gotDeadline.Equal(deadline) {
					t.Fatalf("%s deadline=%v,%v want=%v,true", name, gotDeadline, ok, deadline)
				}
			}
			cancel()
			for name, ctx := range map[string]context.Context{
				"role": snapshot.roleCtx, "store": storeCtx, "refresh": refresh.ctx,
			} {
				if ctx.Err() != context.Canceled {
					t.Fatalf("%s cancellation=%v want=%v", name, ctx.Err(), context.Canceled)
				}
			}
		})
	}
}

func TestPreparedRESTEdgeCreatePreservesRawEndpointsAndPassesResult(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "surrounding whitespace", from: " \tfrom\n ", to: " \rto\t "},
		{name: "empty endpoints", from: "", to: ""},
		{name: "equal endpoints", from: "same", to: "same"},
		{name: "reverse looking order", from: "z-note", to: "a-note"},
		{name: "unicode", from: "ノート-α", to: "🗒️-β"},
		{name: "missing looking IDs", from: "missing/from?", to: "not-found#to"},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantEdge := store.WallEdge{
				ID: fmt.Sprintf("returned-edge-%d", i), From: "store/from", To: "store/to",
			}
			fake := &restEdgeFake{
				role:       store.RoleContributor,
				createEdge: wantEdge,
				createWall: store.Wall{
					Notes:   []store.WallNote{{ID: "opaque-note", Version: 91}},
					Edges:   []store.WallEdge{{ID: "opaque-edge", From: "x", To: "y"}},
					Version: int64(100 + i), UpdatedAt: int64(200 + i),
				},
			}
			prepared := mustPrepareRESTEdge(t, fake, tt.name)

			got, err := prepared.Create(CreateEdgeCommand{From: tt.from, To: tt.to})
			if err != nil || got != wantEdge {
				t.Fatalf("Create=(%+v,%v) want=(%+v,nil)", got, err, wantEdge)
			}
			snapshot := fake.snapshot()
			if snapshot.roleCalls != 1 || len(snapshot.createCalls) != 1 ||
				len(snapshot.deleteCalls) != 0 || len(snapshot.refreshCalls) != 1 {
				t.Fatalf("role/create/delete/refresh=%d/%d/%d/%d", snapshot.roleCalls, len(snapshot.createCalls), len(snapshot.deleteCalls), len(snapshot.refreshCalls))
			}
			call := snapshot.createCalls[0]
			if call.projectID != restEdgeTestProjectID || call.from != tt.from || call.to != tt.to {
				t.Fatalf("create call=%+v want from/to=%q/%q", call, tt.from, tt.to)
			}
			refresh := snapshot.refreshCalls[0]
			if refresh.projectID != restEdgeTestProjectID || refresh.reason != RefreshEdgeCreated {
				t.Fatalf("refresh=%+v", refresh)
			}
			assertRestEdgeTrace(t, snapshot.trace, "role", "create", "refresh")
		})
	}
}

func TestPreparedRESTEdgeDuplicateNoOpStillPublishes(t *testing.T) {
	wantExistingEdge := store.WallEdge{ID: "existing-edge", From: "canonical-a", To: "canonical-b"}
	unchangedWall := store.Wall{
		Notes: []store.WallNote{
			{ID: "canonical-a", Text: "first", Version: 4},
			{ID: "canonical-b", Text: "second", Version: 6},
		},
		Edges:     []store.WallEdge{wantExistingEdge},
		Version:   27,
		UpdatedAt: 987654321,
	}
	fake := &restEdgeFake{
		role: store.RoleContributor, createEdge: wantExistingEdge, createWall: unchangedWall,
	}
	prepared := mustPrepareRESTEdge(t, fake, "duplicate-no-op")

	got, err := prepared.Create(CreateEdgeCommand{From: "canonical-b", To: "canonical-a"})
	if err != nil || got != wantExistingEdge {
		t.Fatalf("Create=(%+v,%v) want=(%+v,nil)", got, err, wantExistingEdge)
	}
	snapshot := fake.snapshot()
	if snapshot.roleCalls != 1 || len(snapshot.createCalls) != 1 || len(snapshot.deleteCalls) != 0 || len(snapshot.refreshCalls) != 1 {
		t.Fatalf("role/create/delete/refresh=%d/%d/%d/%d", snapshot.roleCalls, len(snapshot.createCalls), len(snapshot.deleteCalls), len(snapshot.refreshCalls))
	}
	call := snapshot.createCalls[0]
	if call.from != "canonical-b" || call.to != "canonical-a" {
		t.Fatalf("duplicate create reordered endpoints: %+v", call)
	}
	if snapshot.refreshCalls[0].reason != RefreshEdgeCreated {
		t.Fatalf("refresh reason=%q want=%q", snapshot.refreshCalls[0].reason, RefreshEdgeCreated)
	}
	assertRestEdgeTrace(t, snapshot.trace, "role", "create", "refresh")
}

func TestPreparedRESTEdgeDeletePreservesRawID(t *testing.T) {
	tests := []struct {
		name   string
		edgeID string
	}{
		{name: "surrounding whitespace", edgeID: " \t edge-id \n "},
		{name: "empty", edgeID: ""},
		{name: "unicode", edgeID: "辺-🧵-α"},
		{name: "missing looking ID", edgeID: "missing/edge?not-found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restEdgeFake{
				role: store.RoleContributor,
				deleteWall: store.Wall{
					Notes:   []store.WallNote{{ID: "opaque-note", Version: 12}},
					Edges:   []store.WallEdge{{ID: "other-edge", From: "a", To: "b"}},
					Version: 33, UpdatedAt: 44,
				},
			}
			prepared := mustPrepareRESTEdge(t, fake, tt.name)

			if err := prepared.Delete(DeleteEdgeCommand{EdgeID: tt.edgeID}); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			snapshot := fake.snapshot()
			if snapshot.roleCalls != 1 || len(snapshot.createCalls) != 0 ||
				len(snapshot.deleteCalls) != 1 || len(snapshot.refreshCalls) != 1 {
				t.Fatalf("role/create/delete/refresh=%d/%d/%d/%d", snapshot.roleCalls, len(snapshot.createCalls), len(snapshot.deleteCalls), len(snapshot.refreshCalls))
			}
			call := snapshot.deleteCalls[0]
			if call.projectID != restEdgeTestProjectID || call.edgeID != tt.edgeID {
				t.Fatalf("delete call=%+v want ID=%q", call, tt.edgeID)
			}
			if snapshot.refreshCalls[0].reason != RefreshEdgeDeleted {
				t.Fatalf("refresh reason=%q want=%q", snapshot.refreshCalls[0].reason, RefreshEdgeDeleted)
			}
			assertRestEdgeTrace(t, snapshot.trace, "role", "delete", "refresh")
		})
	}
}

func TestPreparedRESTEdgeStoreFailuresReturnZeroAndSuppressRefresh(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		storeFailure := errors.New("edge create failed")
		fake := &restEdgeFake{
			role:       store.RoleContributor,
			createEdge: store.WallEdge{ID: "must-not-return", From: "x", To: "y"},
			createWall: store.Wall{Edges: []store.WallEdge{{ID: "must-not-expose"}}, Version: 99, UpdatedAt: 100},
			createErr:  storeFailure,
		}
		prepared := mustPrepareRESTEdge(t, fake, "create-failure")

		got, err := prepared.Create(CreateEdgeCommand{From: " raw-from ", To: " raw-to "})
		if !isZeroRestEdge(got) || err != storeFailure {
			t.Fatalf("Create=(%+v,%v) want=(zero,%v)", got, err, storeFailure)
		}
		snapshot := fake.snapshot()
		if snapshot.roleCalls != 1 || len(snapshot.createCalls) != 1 || len(snapshot.deleteCalls) != 0 || len(snapshot.refreshCalls) != 0 {
			t.Fatalf("role/create/delete/refresh=%d/%d/%d/%d", snapshot.roleCalls, len(snapshot.createCalls), len(snapshot.deleteCalls), len(snapshot.refreshCalls))
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "create")
	})

	t.Run("delete", func(t *testing.T) {
		storeFailure := errors.New("edge delete failed")
		fake := &restEdgeFake{
			role:       store.RoleContributor,
			deleteWall: store.Wall{Edges: []store.WallEdge{{ID: "must-not-expose"}}, Version: 101, UpdatedAt: 102},
			deleteErr:  storeFailure,
		}
		prepared := mustPrepareRESTEdge(t, fake, "delete-failure")

		err := prepared.Delete(DeleteEdgeCommand{EdgeID: " raw-edge "})
		if err != storeFailure {
			t.Fatalf("Delete error=%v want=%v", err, storeFailure)
		}
		snapshot := fake.snapshot()
		if snapshot.roleCalls != 1 || len(snapshot.createCalls) != 0 || len(snapshot.deleteCalls) != 1 || len(snapshot.refreshCalls) != 0 {
			t.Fatalf("role/create/delete/refresh=%d/%d/%d/%d", snapshot.roleCalls, len(snapshot.createCalls), len(snapshot.deleteCalls), len(snapshot.refreshCalls))
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "delete")
	})
}

func TestPreparedRESTEdgeAtMostOnceAcrossFamilyMethods(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		failure bool
	}{
		{name: "create success", method: "create"},
		{name: "create failure", method: "create", failure: true},
		{name: "delete success", method: "delete"},
		{name: "delete failure", method: "delete", failure: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeFailure := errors.New(tt.name)
			wantEdge := store.WallEdge{ID: "first-edge", From: "first-from", To: "first-to"}
			fake := &restEdgeFake{role: store.RoleContributor, createEdge: wantEdge}
			if tt.failure {
				fake.createErr = storeFailure
				fake.deleteErr = storeFailure
			}
			prepared := mustPrepareRESTEdge(t, fake, tt.name)

			if tt.method == "create" {
				got, err := prepared.Create(CreateEdgeCommand{From: "first-from", To: "first-to"})
				if tt.failure {
					if !isZeroRestEdge(got) || err != storeFailure {
						t.Fatalf("first Create=(%+v,%v) want=(zero,%v)", got, err, storeFailure)
					}
				} else if err != nil || got != wantEdge {
					t.Fatalf("first Create=(%+v,%v) want=(%+v,nil)", got, err, wantEdge)
				}
			} else {
				err := prepared.Delete(DeleteEdgeCommand{EdgeID: "first-edge"})
				if tt.failure {
					if err != storeFailure {
						t.Fatalf("first Delete error=%v want=%v", err, storeFailure)
					}
				} else if err != nil {
					t.Fatalf("first Delete: %v", err)
				}
			}
			first := fake.snapshot()

			for i := range 2 {
				got, err := prepared.Create(CreateEdgeCommand{From: fmt.Sprintf("repeat-from-%d", i), To: "repeat-to"})
				if !isZeroRestEdge(got) || !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
					t.Fatalf("repeat Create %d=(%+v,%v)", i, got, err)
				}
				if err := prepared.Delete(DeleteEdgeCommand{EdgeID: fmt.Sprintf("repeat-edge-%d", i)}); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
					t.Fatalf("repeat Delete %d error=%v", i, err)
				}
			}
			if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
				t.Fatalf("repeat execution changed dependencies: first=%+v after=%+v", first, after)
			}
			wantRefreshes := 1
			if tt.failure {
				wantRefreshes = 0
			}
			if first.roleCalls != 1 || len(first.createCalls)+len(first.deleteCalls) != 1 || len(first.refreshCalls) != wantRefreshes {
				t.Fatalf("role/mutation/refresh=%d/%d/%d", first.roleCalls, len(first.createCalls)+len(first.deleteCalls), len(first.refreshCalls))
			}
		})
	}
}

func TestPreparedRESTEdgeConcurrentExecutionStartsOneMethod(t *testing.T) {
	wantEdge := store.WallEdge{ID: "winning-edge", From: "store-from", To: "store-to"}
	fake := &restEdgeFake{role: store.RoleContributor, createEdge: wantEdge}
	prepared := mustPrepareRESTEdge(t, fake, "concurrent-success")
	type outcome struct {
		method string
		edge   store.WallEdge
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		method := "create"
		if i%2 == 1 {
			method = "delete"
		}
		wg.Add(1)
		go func(index int, method string) {
			defer wg.Done()
			<-start
			if method == "create" {
				edge, err := prepared.Create(CreateEdgeCommand{
					From: fmt.Sprintf("from-%d", index), To: fmt.Sprintf("to-%d", index),
				})
				results <- outcome{method: method, edge: edge, err: err}
				return
			}
			results <- outcome{method: method, err: prepared.Delete(DeleteEdgeCommand{
				EdgeID: fmt.Sprintf("edge-%d", index),
			})}
		}(i, method)
	}
	close(start)
	wg.Wait()
	close(results)

	successes, repeats := 0, 0
	winner := ""
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			winner = result.method
			if result.method == "create" {
				if result.edge != wantEdge {
					t.Fatalf("winning Create edge=%+v want=%+v", result.edge, wantEdge)
				}
			} else if !isZeroRestEdge(result.edge) {
				t.Fatalf("winning Delete edge=%+v want zero", result.edge)
			}
		case errors.Is(result.err, ErrPreparedMutationAlreadyExecuted):
			repeats++
			if result.method == "create" && !isZeroRestEdge(result.edge) {
				t.Fatalf("losing Create edge=%+v want zero", result.edge)
			}
		default:
			t.Fatalf("unexpected concurrent result=%+v", result)
		}
	}
	snapshot := fake.snapshot()
	if successes != 1 || repeats != 7 || snapshot.roleCalls != 1 ||
		len(snapshot.createCalls)+len(snapshot.deleteCalls) != 1 || len(snapshot.refreshCalls) != 1 {
		t.Fatalf("success/repeat/role/mutation/refresh=%d/%d/%d/%d/%d", successes, repeats, snapshot.roleCalls, len(snapshot.createCalls)+len(snapshot.deleteCalls), len(snapshot.refreshCalls))
	}
	refresh := snapshot.refreshCalls[0]
	switch winner {
	case "create":
		if len(snapshot.createCalls) != 1 || len(snapshot.deleteCalls) != 0 || refresh.reason != RefreshEdgeCreated {
			t.Fatalf("create winner calls/refresh=%+v/%+v/%+v", snapshot.createCalls, snapshot.deleteCalls, refresh)
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "create", "refresh")
	case "delete":
		if len(snapshot.createCalls) != 0 || len(snapshot.deleteCalls) != 1 || refresh.reason != RefreshEdgeDeleted {
			t.Fatalf("delete winner calls/refresh=%+v/%+v/%+v", snapshot.createCalls, snapshot.deleteCalls, refresh)
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "delete", "refresh")
	default:
		t.Fatalf("unexpected winner=%q", winner)
	}
}

func TestPreparedRESTEdgeConcurrentStoreFailureSuppressesRefresh(t *testing.T) {
	storeFailure := errors.New("winning edge mutation failed")
	fake := &restEdgeFake{
		role: store.RoleContributor, createErr: storeFailure, deleteErr: storeFailure,
	}
	prepared := mustPrepareRESTEdge(t, fake, "concurrent-failure")
	type outcome struct {
		method string
		edge   store.WallEdge
		err    error
	}
	start := make(chan struct{})
	results := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := range 8 {
		method := "create"
		if i%2 == 1 {
			method = "delete"
		}
		wg.Add(1)
		go func(index int, method string) {
			defer wg.Done()
			<-start
			if method == "create" {
				edge, err := prepared.Create(CreateEdgeCommand{From: fmt.Sprintf("from-%d", index), To: "to"})
				results <- outcome{method: method, edge: edge, err: err}
				return
			}
			results <- outcome{method: method, err: prepared.Delete(DeleteEdgeCommand{EdgeID: fmt.Sprintf("edge-%d", index)})}
		}(i, method)
	}
	close(start)
	wg.Wait()
	close(results)

	failures, repeats := 0, 0
	winner := ""
	for result := range results {
		if result.method == "create" && !isZeroRestEdge(result.edge) {
			t.Fatalf("concurrent Create edge=%+v want zero", result.edge)
		}
		switch {
		case result.err == storeFailure:
			failures++
			winner = result.method
		case errors.Is(result.err, ErrPreparedMutationAlreadyExecuted):
			repeats++
		default:
			t.Fatalf("unexpected concurrent result=%+v", result)
		}
	}
	snapshot := fake.snapshot()
	if failures != 1 || repeats != 7 || snapshot.roleCalls != 1 ||
		len(snapshot.createCalls)+len(snapshot.deleteCalls) != 1 || len(snapshot.refreshCalls) != 0 {
		t.Fatalf("failure/repeat/role/mutation/refresh=%d/%d/%d/%d/%d", failures, repeats, snapshot.roleCalls, len(snapshot.createCalls)+len(snapshot.deleteCalls), len(snapshot.refreshCalls))
	}
	if winner == "create" {
		if len(snapshot.createCalls) != 1 || len(snapshot.deleteCalls) != 0 {
			t.Fatalf("create winner calls=%+v/%+v", snapshot.createCalls, snapshot.deleteCalls)
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "create")
	} else if winner == "delete" {
		if len(snapshot.createCalls) != 0 || len(snapshot.deleteCalls) != 1 {
			t.Fatalf("delete winner calls=%+v/%+v", snapshot.createCalls, snapshot.deleteCalls)
		}
		assertRestEdgeTrace(t, snapshot.trace, "role", "delete")
	} else {
		t.Fatalf("unexpected winner=%q", winner)
	}
}

func TestPreparedRESTEdgeCanceledMutationContext(t *testing.T) {
	for _, method := range []string{"create", "delete"} {
		t.Run(method, func(t *testing.T) {
			effectCtx := context.WithValue(context.Background(), restEdgeRawContextKey{}, method+"-raw-not-canceled")
			mutationBase, cancel := context.WithCancel(context.WithValue(
				context.Background(),
				restEdgeMutationContextKey{},
				method+"-mutation-canceled",
			))
			defer cancel()
			mutationCtx := store.WithUserID(mutationBase, restEdgeTestActorID)
			fake := &restEdgeFake{
				role: store.RoleContributor, honorMutationContextError: true,
			}
			prepared, err := newRESTEdgeTestService(fake).Prepare(
				mutationCtx,
				effectCtx,
				ResolvedRESTTarget{ProjectID: restEdgeTestProjectID},
			)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if mutationCtx.Err() != nil {
				t.Fatalf("mutation context canceled during Prepare: %v", mutationCtx.Err())
			}
			cancel()

			if method == "create" {
				got, err := prepared.Create(CreateEdgeCommand{From: "from", To: "to"})
				if !isZeroRestEdge(got) || err != context.Canceled {
					t.Fatalf("Create=(%+v,%v) want=(zero,%v)", got, err, context.Canceled)
				}
			} else if err := prepared.Delete(DeleteEdgeCommand{EdgeID: "edge"}); err != context.Canceled {
				t.Fatalf("Delete error=%v want=%v", err, context.Canceled)
			}

			first := fake.snapshot()
			if first.roleCalls != 1 || len(first.createCalls)+len(first.deleteCalls) != 1 || len(first.refreshCalls) != 0 {
				t.Fatalf("role/mutation/refresh=%d/%d/%d", first.roleCalls, len(first.createCalls)+len(first.deleteCalls), len(first.refreshCalls))
			}
			if method == "create" {
				if first.createCalls[0].ctx != mutationCtx {
					t.Fatal("Create did not receive exact canceled mutation context")
				}
				assertRestEdgeTrace(t, first.trace, "role", "create")
			} else {
				if first.deleteCalls[0].ctx != mutationCtx {
					t.Fatal("Delete did not receive exact canceled mutation context")
				}
				assertRestEdgeTrace(t, first.trace, "role", "delete")
			}
			if effectCtx.Err() != nil {
				t.Fatalf("effect context unexpectedly canceled: %v", effectCtx.Err())
			}

			got, createErr := prepared.Create(CreateEdgeCommand{From: "repeat", To: "repeat"})
			if !isZeroRestEdge(got) || !errors.Is(createErr, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("repeat Create=(%+v,%v)", got, createErr)
			}
			if deleteErr := prepared.Delete(DeleteEdgeCommand{EdgeID: "repeat"}); !errors.Is(deleteErr, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("repeat Delete error=%v", deleteErr)
			}
			if after := fake.snapshot(); !reflect.DeepEqual(after, first) {
				t.Fatalf("repeat after cancellation changed dependencies: first=%+v after=%+v", first, after)
			}
		})
	}
}
