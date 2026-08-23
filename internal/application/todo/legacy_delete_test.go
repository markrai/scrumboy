package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

var (
	_ LegacyDeleteProjectStore = (*store.Store)(nil)
	_ LegacyGlobalDeleteStore  = (*store.Store)(nil)
)

type legacyDeleteProjectLookupCall struct {
	ctx    context.Context
	todoID int64
}

type legacyDeleteProjectLookupFake struct {
	calls     []legacyDeleteProjectLookupCall
	projectID int64
	err       error
	trace     *[]string
}

func (f *legacyDeleteProjectLookupFake) GetProjectIDForTodo(ctx context.Context, todoID int64) (int64, error) {
	f.calls = append(f.calls, legacyDeleteProjectLookupCall{ctx: ctx, todoID: todoID})
	if f.trace != nil {
		*f.trace = append(*f.trace, "lookup")
	}
	if f.err != nil {
		return 0, f.err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return f.projectID, nil
}

type legacyDeleteStoreCall struct {
	ctx    context.Context
	todoID int64
	mode   store.Mode
}

type legacyDeleteStoreFake struct {
	calls []legacyDeleteStoreCall
	err   error
	trace *[]string
}

func (f *legacyDeleteStoreFake) DeleteTodo(ctx context.Context, todoID int64, mode store.Mode) error {
	f.calls = append(f.calls, legacyDeleteStoreCall{ctx: ctx, todoID: todoID, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, "delete")
	}
	if f.err != nil {
		return f.err
	}
	return ctx.Err()
}

type legacyDeleteRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type legacyDeleteRefreshFake struct {
	calls []legacyDeleteRefreshCall
	trace *[]string
}

func (f *legacyDeleteRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, legacyDeleteRefreshCall{ctx: ctx, projectID: projectID, reason: reason, entity: entity})
	if f.trace != nil {
		*f.trace = append(*f.trace, "refresh")
	}
}

func TestLegacyDeleteServicePrepareBindsGlobalIDProjectMode(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	projects := &legacyDeleteProjectLookupFake{projectID: 17}
	deletes := &legacyDeleteStoreFake{}
	refresh := &legacyDeleteRefreshFake{}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})
	target := LegacyDeleteTarget{TodoID: 0, Mode: store.ModeAnonymous}

	prepared, err := service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(projects.calls) != 1 {
		t.Fatalf("project lookup calls = %d, want 1", len(projects.calls))
	}
	lookup := projects.calls[0]
	if lookup.ctx != ctx || lookup.ctx.Value(key) != "bound" || lookup.todoID != 0 {
		t.Fatalf("project lookup = %+v, want bound context and unvalidated global Todo ID 0", lookup)
	}
	if len(deletes.calls) != 0 || len(refresh.calls) != 0 {
		t.Fatalf("Prepare calls = delete %d refresh %d, want 0 each", len(deletes.calls), len(refresh.calls))
	}

	target.TodoID = 91
	target.Mode = store.ModeFull
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	deleteCall := deletes.calls[0]
	if deleteCall.ctx != ctx || deleteCall.ctx.Value(key) != "bound" || deleteCall.todoID != 0 || deleteCall.mode != store.ModeAnonymous {
		t.Fatalf("delete call = %+v, want bound context, global Todo ID 0, mode %q", deleteCall, store.ModeAnonymous)
	}
	if len(refresh.calls) != 1 || refresh.calls[0].projectID != 17 {
		t.Fatalf("refresh calls = %+v, want pre-read project 17", refresh.calls)
	}
}

func TestLegacyDeleteServiceLookupFailureStopsBeforeDelete(t *testing.T) {
	wantErr := errors.New("legacy delete project lookup failed")
	projects := &legacyDeleteProjectLookupFake{err: wantErr}
	deletes := &legacyDeleteStoreFake{}
	refresh := &legacyDeleteRefreshFake{}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})

	prepared, err := service.Prepare(context.Background(), LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if err != wantErr {
		t.Fatalf("Prepare error = %v, want identical %v", err, wantErr)
	}
	if prepared != nil {
		t.Fatalf("prepared = %#v, want nil", prepared)
	}
	if len(projects.calls) != 1 || len(deletes.calls) != 0 || len(refresh.calls) != 0 {
		t.Fatalf("calls = lookup %d delete %d refresh %d, want 1, 0, 0", len(projects.calls), len(deletes.calls), len(refresh.calls))
	}
}

func TestLegacyDeleteServiceSuccessSequencesLookupDeleteRefresh(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	projects := &legacyDeleteProjectLookupFake{projectID: 17, trace: &trace}
	deletes := &legacyDeleteStoreFake{trace: &trace}
	refresh := &legacyDeleteRefreshFake{trace: &trace}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})

	prepared, err := service.Prepare(ctx, LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !reflect.DeepEqual(trace, []string{"lookup", "delete", "refresh"}) {
		t.Fatalf("trace = %#v, want lookup, delete, refresh", trace)
	}
	if len(projects.calls) != 1 || len(deletes.calls) != 1 || len(refresh.calls) != 1 {
		t.Fatalf("calls = lookup %d delete %d refresh %d, want 1 each", len(projects.calls), len(deletes.calls), len(refresh.calls))
	}
	if projects.calls[0].ctx != ctx || deletes.calls[0].ctx != ctx || refresh.calls[0].ctx != ctx {
		t.Fatal("lookup, delete, and refresh did not receive the bound context")
	}
	if projects.calls[0].todoID != 7001 || deletes.calls[0].todoID != 7001 || deletes.calls[0].mode != store.ModeFull {
		t.Fatalf("global identity/mode = lookup %+v delete %+v, want Todo 7001 mode %q", projects.calls[0], deletes.calls[0], store.ModeFull)
	}
	if got := refresh.calls[0]; got.ctx.Value(key) != "bound" || got.projectID != 17 || got.reason != RefreshReasonTodoDeleted || got.entity != (apprefresh.Entity{}) {
		t.Fatalf("refresh = %+v, want bound context, pre-read project 17, reason %q, zero entity", got, RefreshReasonTodoDeleted)
	}
}

func TestLegacyDeleteServiceDeleteFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("legacy global delete failed")
	trace := []string{}
	projects := &legacyDeleteProjectLookupFake{projectID: 17, trace: &trace}
	deletes := &legacyDeleteStoreFake{err: wantErr, trace: &trace}
	refresh := &legacyDeleteRefreshFake{trace: &trace}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})

	prepared, err := service.Prepare(context.Background(), LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = prepared.Delete()
	if err != wantErr {
		t.Fatalf("Delete error = %v, want identical %v", err, wantErr)
	}
	if !reflect.DeepEqual(trace, []string{"lookup", "delete"}) {
		t.Fatalf("trace = %#v, want lookup then delete", trace)
	}
	if len(projects.calls) != 1 || len(deletes.calls) != 1 || len(refresh.calls) != 0 {
		t.Fatalf("calls = lookup %d delete %d refresh %d, want 1, 1, 0", len(projects.calls), len(deletes.calls), len(refresh.calls))
	}
}

func TestLegacyDeleteServiceCancellationBeforePrepareLookup(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	cancel()
	projects := &legacyDeleteProjectLookupFake{projectID: 17}
	deletes := &legacyDeleteStoreFake{}
	refresh := &legacyDeleteRefreshFake{}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})

	prepared, err := service.Prepare(ctx, LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare error = %v, want context canceled", err)
	}
	if prepared != nil {
		t.Fatalf("prepared = %#v, want nil", prepared)
	}
	if len(projects.calls) != 1 {
		t.Fatalf("project lookup calls = %d, want 1", len(projects.calls))
	}
	if got := projects.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("lookup context = %v, want bound cancelled context", got)
	}
	if len(deletes.calls) != 0 || len(refresh.calls) != 0 {
		t.Fatalf("calls = delete %d refresh %d, want 0 each", len(deletes.calls), len(refresh.calls))
	}
}

func TestLegacyDeleteServiceCancellationAfterPrepareBeforeDelete(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	projects := &legacyDeleteProjectLookupFake{projectID: 17}
	deletes := &legacyDeleteStoreFake{}
	refresh := &legacyDeleteRefreshFake{}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
		Refresh:  refresh,
	})

	prepared, err := service.Prepare(ctx, LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	err = prepared.Delete()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v, want context canceled", err)
	}
	if len(projects.calls) != 1 || len(deletes.calls) != 1 {
		t.Fatalf("calls = lookup %d delete %d, want 1 each", len(projects.calls), len(deletes.calls))
	}
	if got := deletes.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("delete context = %v, want bound cancelled context", got)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}

func TestLegacyDeleteServiceNilRefreshIsSafe(t *testing.T) {
	projects := &legacyDeleteProjectLookupFake{projectID: 17}
	deletes := &legacyDeleteStoreFake{}
	service := NewLegacyDeleteService(LegacyDeleteServiceDependencies{
		Projects: projects,
		Delete:   deletes,
	})

	prepared, err := service.Prepare(context.Background(), LegacyDeleteTarget{TodoID: 7001, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(projects.calls) != 1 || len(deletes.calls) != 1 {
		t.Fatalf("calls = lookup %d delete %d, want 1 each", len(projects.calls), len(deletes.calls))
	}
}
