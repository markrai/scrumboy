package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

type restDeleteStoreCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	mode      store.Mode
}

type restDeleteStoreFake struct {
	calls []restDeleteStoreCall
	err   error
	trace *[]string
}

func (f *restDeleteStoreFake) DeleteTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) error {
	f.calls = append(f.calls, restDeleteStoreCall{
		ctx:       ctx,
		projectID: projectID,
		localID:   localID,
		mode:      mode,
	})
	if f.trace != nil {
		*f.trace = append(*f.trace, "delete")
	}
	if f.err != nil {
		return f.err
	}
	return ctx.Err()
}

type restDeleteRefreshCall struct {
	ctx       context.Context
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type restDeleteRefreshFake struct {
	calls []restDeleteRefreshCall
	trace *[]string
}

func (f *restDeleteRefreshFake) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, restDeleteRefreshCall{ctx: ctx, projectID: projectID, reason: reason, entity: entity})
	if f.trace != nil {
		*f.trace = append(*f.trace, "refresh")
	}
}

func TestDeleteServicePreparedDeleteBindsTargetAndPublishesOnce(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")

	trace := []string{}
	deletes := &restDeleteStoreFake{trace: &trace}
	refresh := &restDeleteRefreshFake{trace: &trace}
	service := NewDeleteService(DeleteServiceDependencies{Delete: deletes, Refresh: refresh})
	target := ResolvedDeleteTarget{
		ProjectContext: store.ProjectContext{
			Project: store.Project{ID: 7, Slug: "canonical"},
			Role:    store.RoleMaintainer,
		},
		Mode: store.ModeFull,
	}
	prepared := service.Prepare(ctx, target)

	// PreparedDelete owns value copies of the resolved target and mode.
	target.ProjectContext.Project.ID = 99
	target.ProjectContext.Project.Slug = "mutated"
	target.ProjectContext.Role = store.RoleViewer
	target.Mode = store.ModeAnonymous

	if err := prepared.Delete(DeleteCommand{LocalID: 4}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if prepared.projectContext.Project.Slug != "canonical" || prepared.projectContext.Role != store.RoleMaintainer {
		t.Fatalf("prepared project context = %+v, want original slug and role", prepared.projectContext)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	call := deletes.calls[0]
	if call.ctx != ctx || call.ctx.Value(key) != "bound" {
		t.Fatalf("delete context = %v, want prepared context", call.ctx)
	}
	if call.projectID != 7 || call.localID != 4 || call.mode != store.ModeFull {
		t.Fatalf("delete call = %+v, want project 7 local 4 mode %q", call, store.ModeFull)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("refresh calls = %d, want 1", len(refresh.calls))
	}
	if got := refresh.calls[0]; got.ctx != ctx || got.ctx.Value(key) != "bound" || got.projectID != 7 || got.reason != RefreshReasonTodoDeleted || got.entity != (apprefresh.Entity{}) {
		t.Fatalf("refresh = %+v, want bound context, project 7, reason %q, zero entity", got, RefreshReasonTodoDeleted)
	}
	if !reflect.DeepEqual(trace, []string{"delete", "refresh"}) {
		t.Fatalf("call trace = %#v, want delete then refresh", trace)
	}
}

func TestDeleteServiceStoreFailureReturnsSameErrorAndSkipsRefresh(t *testing.T) {
	wantErr := errors.New("delete failed")
	trace := []string{}
	deletes := &restDeleteStoreFake{err: wantErr, trace: &trace}
	refresh := &restDeleteRefreshFake{trace: &trace}
	prepared := NewDeleteService(DeleteServiceDependencies{Delete: deletes, Refresh: refresh}).Prepare(
		context.Background(),
		ResolvedDeleteTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	err := prepared.Delete(DeleteCommand{LocalID: 4})
	if err != wantErr {
		t.Fatalf("Delete error = %v, want identical error %v", err, wantErr)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
	if !reflect.DeepEqual(trace, []string{"delete"}) {
		t.Fatalf("call trace = %#v, want only delete", trace)
	}
}

func TestDeleteServiceCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	deletes := &restDeleteStoreFake{}
	refresh := &restDeleteRefreshFake{}
	prepared := NewDeleteService(DeleteServiceDependencies{Delete: deletes, Refresh: refresh}).Prepare(
		ctx,
		ResolvedDeleteTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)
	cancel()

	err := prepared.Delete(DeleteCommand{LocalID: 4})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v, want context canceled", err)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	if got := deletes.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("store context = %v, want bound cancelled context", got)
	}
	if len(refresh.calls) != 0 {
		t.Fatalf("refresh calls = %d, want 0", len(refresh.calls))
	}
}

func TestDeleteServiceNilRefreshIsNoOpAndLocalIDValidationRemainsTransportOwned(t *testing.T) {
	deletes := &restDeleteStoreFake{}
	prepared := NewDeleteService(DeleteServiceDependencies{Delete: deletes}).Prepare(
		context.Background(),
		ResolvedDeleteTarget{ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}}, Mode: store.ModeFull},
	)

	if err := prepared.Delete(DeleteCommand{LocalID: 0}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	if got := deletes.calls[0]; got.projectID != 7 || got.localID != 0 || got.mode != store.ModeFull {
		t.Fatalf("delete call = %+v, want unvalidated local ID forwarded unchanged", got)
	}
}
