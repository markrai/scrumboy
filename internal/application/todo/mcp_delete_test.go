package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var (
	_ MCPDeleteAccessStore = (*store.Store)(nil)
	_ DeleteStore          = (*store.Store)(nil)
)

type mcpDeleteAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpDeleteAccessFake struct {
	calls          []mcpDeleteAccessCall
	projectContext store.ProjectContext
	err            error
	trace          *[]string
}

func (f *mcpDeleteAccessFake) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	f.calls = append(f.calls, mcpDeleteAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.trace != nil {
		*f.trace = append(*f.trace, "access")
	}
	if f.err != nil {
		return store.ProjectContext{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return store.ProjectContext{}, err
	}
	return f.projectContext, nil
}

type mcpDeleteStoreCall struct {
	ctx       context.Context
	projectID int64
	localID   int64
	mode      store.Mode
}

type mcpDeleteStoreFake struct {
	calls []mcpDeleteStoreCall
	err   error
	trace *[]string
}

func (f *mcpDeleteStoreFake) DeleteTodoByLocalID(
	ctx context.Context,
	projectID int64,
	localID int64,
	mode store.Mode,
) error {
	f.calls = append(f.calls, mcpDeleteStoreCall{
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

func TestMCPDeleteServicePrepareBindsAccessTargetAndDeletesResolvedTodo(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "bound")
	trace := []string{}
	access := &mcpDeleteAccessFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 7, Slug: "canonical"},
			Role:    store.RoleMaintainer,
		},
		trace: &trace,
	}
	deletes := &mcpDeleteStoreFake{trace: &trace}
	service := NewMCPDeleteService(MCPDeleteServiceDependencies{Access: access, Delete: deletes})
	target := SlugDeleteTarget{Slug: "requested", Mode: store.ModeFull}

	prepared, err := service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prepared == nil {
		t.Fatal("Prepare returned nil capability")
	}
	if len(access.calls) != 1 {
		t.Fatalf("access calls = %d, want 1", len(access.calls))
	}
	accessCall := access.calls[0]
	if accessCall.ctx != ctx || accessCall.ctx.Value(key) != "bound" || accessCall.slug != "requested" || accessCall.mode != store.ModeFull {
		t.Fatalf("access call = %+v, want bound context, requested slug, full mode", accessCall)
	}
	if len(deletes.calls) != 0 {
		t.Fatalf("delete calls during preparation = %d, want 0", len(deletes.calls))
	}

	access.projectContext.Project.ID = 99
	access.projectContext.Project.Slug = "mutated"
	access.projectContext.Role = store.RoleViewer
	target.Slug = "mutated"
	target.Mode = store.ModeAnonymous

	if err := prepared.Delete(DeleteCommand{LocalID: 4}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if prepared.projectContext.Project.Slug != "canonical" || prepared.projectContext.Role != store.RoleMaintainer {
		t.Fatalf("prepared project context changed after source mutation: %+v", prepared.projectContext)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	deleteCall := deletes.calls[0]
	if deleteCall.ctx != ctx || deleteCall.ctx.Value(key) != "bound" || deleteCall.projectID != 7 || deleteCall.localID != 4 || deleteCall.mode != store.ModeFull {
		t.Fatalf("delete call = %+v, want bound context, project 7, local 4, full mode", deleteCall)
	}
	if !reflect.DeepEqual(trace, []string{"access", "delete"}) {
		t.Fatalf("call trace = %#v, want access then delete", trace)
	}
}

func TestMCPDeleteServiceAccessFailureReturnsNoCapabilityOrPersistence(t *testing.T) {
	wantErr := errors.New("access failed")
	trace := []string{}
	access := &mcpDeleteAccessFake{err: wantErr, trace: &trace}
	deletes := &mcpDeleteStoreFake{trace: &trace}
	service := NewMCPDeleteService(MCPDeleteServiceDependencies{Access: access, Delete: deletes})

	prepared, err := service.Prepare(context.Background(), SlugDeleteTarget{Slug: "hidden", Mode: store.ModeFull})
	if err != wantErr {
		t.Fatalf("Prepare error = %v, want identical error %v", err, wantErr)
	}
	if prepared != nil {
		t.Fatalf("prepared capability = %+v, want nil", prepared)
	}
	if len(access.calls) != 1 || len(deletes.calls) != 0 {
		t.Fatalf("calls after access failure: access=%d delete=%d, want 1/0", len(access.calls), len(deletes.calls))
	}
	if !reflect.DeepEqual(trace, []string{"access"}) {
		t.Fatalf("call trace = %#v, want access only", trace)
	}
}

func TestPreparedMCPDeleteStoreFailureReturnsSameErrorWithoutRetry(t *testing.T) {
	wantErr := errors.New("delete failed")
	access := &mcpDeleteAccessFake{projectContext: store.ProjectContext{Project: store.Project{ID: 7}}}
	deletes := &mcpDeleteStoreFake{err: wantErr}
	prepared, err := NewMCPDeleteService(MCPDeleteServiceDependencies{Access: access, Delete: deletes}).Prepare(
		context.Background(),
		SlugDeleteTarget{Slug: "requested", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	err = prepared.Delete(DeleteCommand{LocalID: 4})
	if err != wantErr {
		t.Fatalf("Delete error = %v, want identical error %v", err, wantErr)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
}

func TestMCPDeleteServiceCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	type contextKey string
	const key contextKey = "request"
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "bound"))
	access := &mcpDeleteAccessFake{projectContext: store.ProjectContext{Project: store.Project{ID: 7}}}
	deletes := &mcpDeleteStoreFake{}
	prepared, err := NewMCPDeleteService(MCPDeleteServiceDependencies{Access: access, Delete: deletes}).Prepare(
		ctx,
		SlugDeleteTarget{Slug: "requested", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	err = prepared.Delete(DeleteCommand{LocalID: 4})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v, want context canceled", err)
	}
	if len(deletes.calls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(deletes.calls))
	}
	if got := deletes.calls[0].ctx; got != ctx || got.Value(key) != "bound" || !errors.Is(got.Err(), context.Canceled) {
		t.Fatalf("delete context = %v, want bound cancelled context", got)
	}
}

func TestPreparedMCPDeleteForwardsUnvalidatedLocalID(t *testing.T) {
	access := &mcpDeleteAccessFake{projectContext: store.ProjectContext{Project: store.Project{ID: 7}}}
	deletes := &mcpDeleteStoreFake{}
	prepared, err := NewMCPDeleteService(MCPDeleteServiceDependencies{Access: access, Delete: deletes}).Prepare(
		context.Background(),
		SlugDeleteTarget{Slug: "requested", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := prepared.Delete(DeleteCommand{LocalID: 0}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deletes.calls) != 1 || deletes.calls[0].localID != 0 {
		t.Fatalf("delete calls = %+v, want one call with local ID 0", deletes.calls)
	}
}
