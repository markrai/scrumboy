package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

var _ MCPMutationAccessStore = (*store.Store)(nil)

type mcpMutationTestContextKey struct{}

type mcpMutationAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpMutationStoreCall struct {
	operation string
	ctx       context.Context
	projectID int64
	key       string
	name      string
	color     string
}

type mcpMutationReadCall struct {
	ctx       context.Context
	projectID int64
}

type mcpMutationFake struct {
	trace []string

	accessCalls    []mcpMutationAccessCall
	projectContext store.ProjectContext
	accessErr      error

	mutationCalls   []mcpMutationStoreCall
	createResult    store.WorkflowColumn
	createErr       error
	updateErr       error
	deleteErr       error
	honorContextErr bool

	readCalls []mcpMutationReadCall
	workflow  []store.WorkflowColumn
	readErr   error
}

func (f *mcpMutationFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.accessCalls = append(f.accessCalls, mcpMutationAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpMutationFake) AddWorkflowColumn(
	ctx context.Context,
	projectID int64,
	name string,
) (store.WorkflowColumn, error) {
	f.trace = append(f.trace, "create")
	f.mutationCalls = append(f.mutationCalls, mcpMutationStoreCall{
		operation: "create",
		ctx:       ctx,
		projectID: projectID,
		name:      name,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return store.WorkflowColumn{}, ctx.Err()
	}
	return f.createResult, f.createErr
}

func (f *mcpMutationFake) UpdateWorkflowColumn(
	ctx context.Context,
	projectID int64,
	key string,
	name string,
	color string,
) error {
	f.trace = append(f.trace, "update")
	f.mutationCalls = append(f.mutationCalls, mcpMutationStoreCall{
		operation: "update",
		ctx:       ctx,
		projectID: projectID,
		key:       key,
		name:      name,
		color:     color,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.updateErr
}

func (f *mcpMutationFake) DeleteWorkflowColumn(
	ctx context.Context,
	projectID int64,
	key string,
) error {
	f.trace = append(f.trace, "delete")
	f.mutationCalls = append(f.mutationCalls, mcpMutationStoreCall{
		operation: "delete",
		ctx:       ctx,
		projectID: projectID,
		key:       key,
	})
	if f.honorContextErr && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.deleteErr
}

func (f *mcpMutationFake) GetProjectWorkflow(
	ctx context.Context,
	projectID int64,
) ([]store.WorkflowColumn, error) {
	f.trace = append(f.trace, "read")
	f.readCalls = append(f.readCalls, mcpMutationReadCall{ctx: ctx, projectID: projectID})
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.workflow, nil
}

func newMCPMutationTestService(f *mcpMutationFake) *MCPMutationService {
	return NewMCPMutationService(MCPMutationServiceDependencies{
		Access:    f,
		Mutations: f,
		Workflow:  f,
	})
}

func mcpMutationContext(marker string) context.Context {
	return context.WithValue(context.Background(), mcpMutationTestContextKey{}, marker)
}

func assertMCPMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

func TestMCPMutationPrepareAccessAndRole(t *testing.T) {
	accessFailure := errors.New("access failed")
	tests := []struct {
		name         string
		role         store.ProjectRole
		accessErr    error
		wantErr      error
		wantPrepared bool
	}{
		{name: "access failure", accessErr: accessFailure, wantErr: accessFailure},
		{name: "contributor", role: store.RoleContributor, wantErr: ErrMaintainerRequired},
		{name: "viewer", role: store.RoleViewer, wantErr: ErrMaintainerRequired},
		{name: "maintainer", role: store.RoleMaintainer, wantPrepared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpMutationFake{
				projectContext: store.ProjectContext{
					Project: store.Project{ID: 71},
					Role:    tt.role,
				},
				accessErr: tt.accessErr,
			}
			service := newMCPMutationTestService(fake)
			ctx := mcpMutationContext(tt.name)

			prepared, err := service.Prepare(ctx, MCPMutationTarget{
				ProjectSlug: "  requested-slug  ",
				Mode:        store.ModeFull,
			})
			if err != tt.wantErr {
				t.Fatalf("Prepare error=%v want=%v", err, tt.wantErr)
			}
			if (prepared != nil) != tt.wantPrepared {
				t.Fatalf("prepared=%v wantPresent=%v", prepared, tt.wantPrepared)
			}
			if len(fake.accessCalls) != 1 {
				t.Fatalf("access calls=%d want=1", len(fake.accessCalls))
			}
			call := fake.accessCalls[0]
			if call.ctx != ctx || call.slug != "  requested-slug  " || call.mode != store.ModeFull {
				t.Fatalf("access call=%+v", call)
			}
			if got := call.ctx.Value(mcpMutationTestContextKey{}); got != tt.name {
				t.Fatalf("context marker=%v want=%q", got, tt.name)
			}
			if len(fake.mutationCalls) != 0 || len(fake.readCalls) != 0 {
				t.Fatalf("preparation caused work: mutations=%+v reads=%+v", fake.mutationCalls, fake.readCalls)
			}
			assertMCPMutationTrace(t, fake.trace, "access")
		})
	}
}

func TestPreparedMCPMutationBindsContextAndResolvedProjectID(t *testing.T) {
	fake := &mcpMutationFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 101},
			Role:    store.RoleMaintainer,
		},
	}
	service := newMCPMutationTestService(fake)
	ctx := mcpMutationContext("bound")
	target := MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull}
	prepared, err := service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectSlug = "replacement"
	target.Mode = store.ModeAnonymous
	fake.projectContext.Project.ID = 202

	if err := prepared.Delete(DeleteCommand{Key: "  review  "}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fake.mutationCalls) != 1 {
		t.Fatalf("mutation calls=%d want=1", len(fake.mutationCalls))
	}
	call := fake.mutationCalls[0]
	if call.operation != "delete" || call.ctx != ctx || call.projectID != 101 || call.key != "  review  " {
		t.Fatalf("delete call=%+v", call)
	}
	if got := call.ctx.Value(mcpMutationTestContextKey{}); got != "bound" {
		t.Fatalf("context marker=%v want=bound", got)
	}
	if len(fake.readCalls) != 0 {
		t.Fatalf("read calls=%+v want none", fake.readCalls)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "delete")
}

func TestPreparedMCPMutationCreate(t *testing.T) {
	mutationFailure := errors.New("create failed")
	wantColumn := store.WorkflowColumn{ID: 3, ProjectID: 31, Key: "review", Name: "Review", Color: "#123456", Position: 4}

	t.Run("success", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 31}, Role: store.RoleMaintainer},
			createResult:   wantColumn,
		}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationContext("create"),
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "  Review  "})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got != wantColumn {
			t.Fatalf("column=%+v want=%+v", got, wantColumn)
		}
		if len(fake.mutationCalls) != 1 || fake.mutationCalls[0].name != "  Review  " {
			t.Fatalf("mutation calls=%+v", fake.mutationCalls)
		}
		if len(fake.readCalls) != 0 {
			t.Fatalf("read calls=%+v want none", fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "create")
	})

	t.Run("mutation failure", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 32}, Role: store.RoleMaintainer},
			createErr:      mutationFailure,
		}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationContext("create failure"),
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "Review"})
		if err != mutationFailure {
			t.Fatalf("Create error=%v want=%v", err, mutationFailure)
		}
		if got != (store.WorkflowColumn{}) {
			t.Fatalf("column=%+v want zero", got)
		}
		if len(fake.mutationCalls) != 1 || len(fake.readCalls) != 0 {
			t.Fatalf("mutations=%+v reads=%+v", fake.mutationCalls, fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "create")
	})
}

func TestPreparedMCPMutationUpdate(t *testing.T) {
	command := UpdateCommand{
		Key:   "  review  ",
		Name:  "  Ready for review  ",
		Color: "  #A1B2C3  ",
	}

	t.Run("success mutates before projection read", func(t *testing.T) {
		want := store.WorkflowColumn{ID: 9, ProjectID: 41, Key: command.Key, Name: "Ready", Color: "#A1B2C3", Position: 2}
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 41}, Role: store.RoleMaintainer},
			workflow: []store.WorkflowColumn{
				{ID: 8, ProjectID: 41, Key: "other"},
				want,
			},
		}
		ctx := mcpMutationContext("update")
		prepared, err := newMCPMutationTestService(fake).Prepare(
			ctx,
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Update(command)
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got != want {
			t.Fatalf("column=%+v want=%+v", got, want)
		}
		if len(fake.mutationCalls) != 1 {
			t.Fatalf("update calls=%d want=1", len(fake.mutationCalls))
		}
		call := fake.mutationCalls[0]
		if call.operation != "update" || call.ctx != ctx || call.projectID != 41 ||
			call.key != command.Key || call.name != command.Name || call.color != command.Color {
			t.Fatalf("update call=%+v", call)
		}
		if len(fake.readCalls) != 1 || fake.readCalls[0].ctx != ctx || fake.readCalls[0].projectID != 41 {
			t.Fatalf("read calls=%+v", fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "update", "read")
	})

	t.Run("mutation failure skips read", func(t *testing.T) {
		mutationFailure := errors.New("update failed")
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 42}, Role: store.RoleMaintainer},
			updateErr:      mutationFailure,
		}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationContext("update failure"),
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Update(command)
		if err != mutationFailure {
			t.Fatalf("Update error=%v want=%v", err, mutationFailure)
		}
		if got != (store.WorkflowColumn{}) || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 0 {
			t.Fatalf("column=%+v mutations=%+v reads=%+v", got, fake.mutationCalls, fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "update")
	})

	t.Run("read failure is classified and preserves cause", func(t *testing.T) {
		readFailure := fmt.Errorf("%w: workflow unavailable", store.ErrNotFound)
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 43}, Role: store.RoleMaintainer},
			readErr:        readFailure,
		}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationContext("read failure"),
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Update(command)
		if !errors.Is(err, ErrWorkflowProjectionFailed) {
			t.Fatalf("Update error=%v want projection classification", err)
		}
		if !errors.Is(err, readFailure) || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("projection error lost read cause: %v", err)
		}
		if got != (store.WorkflowColumn{}) || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 1 {
			t.Fatalf("column=%+v mutations=%+v reads=%+v", got, fake.mutationCalls, fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "update", "read")
	})

	t.Run("missing updated column is projection failure", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 44}, Role: store.RoleMaintainer},
			workflow:       []store.WorkflowColumn{{ID: 1, ProjectID: 44, Key: "other"}},
		}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationContext("missing column"),
			MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}

		got, err := prepared.Update(command)
		if !errors.Is(err, ErrWorkflowProjectionFailed) {
			t.Fatalf("Update error=%v want projection classification", err)
		}
		if !errors.Is(err, ErrWorkflowProjectionColumnMissing) {
			t.Fatalf("Update error=%v want missing-column classification", err)
		}
		if errors.Unwrap(err) != nil {
			t.Fatalf("missing-column projection error cause=%v want nil", errors.Unwrap(err))
		}
		if got != (store.WorkflowColumn{}) || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 1 {
			t.Fatalf("column=%+v mutations=%+v reads=%+v", got, fake.mutationCalls, fake.readCalls)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "update", "read")
	})
}

func TestPreparedMCPMutationDeleteFailure(t *testing.T) {
	mutationFailure := errors.New("delete failed")
	fake := &mcpMutationFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 51}, Role: store.RoleMaintainer},
		deleteErr:      mutationFailure,
	}
	prepared, err := newMCPMutationTestService(fake).Prepare(
		mcpMutationContext("delete failure"),
		MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if err := prepared.Delete(DeleteCommand{Key: "  review  "}); err != mutationFailure {
		t.Fatalf("Delete error=%v want=%v", err, mutationFailure)
	}
	if len(fake.mutationCalls) != 1 || fake.mutationCalls[0].key != "  review  " || len(fake.readCalls) != 0 {
		t.Fatalf("mutations=%+v reads=%+v", fake.mutationCalls, fake.readCalls)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "delete")
}

func TestPreparedMCPMutationUsesCancelledBoundContext(t *testing.T) {
	fake := &mcpMutationFake{
		projectContext:  store.ProjectContext{Project: store.Project{ID: 61}, Role: store.RoleMaintainer},
		honorContextErr: true,
	}
	service := newMCPMutationTestService(fake)
	ctx, cancel := context.WithCancel(mcpMutationContext("cancelled"))
	prepared, err := service.Prepare(ctx, MCPMutationTarget{ProjectSlug: "project", Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	got, err := prepared.Update(UpdateCommand{Key: "review", Name: "Review", Color: "#123456"})
	if err != context.Canceled {
		t.Fatalf("Update error=%v want=%v", err, context.Canceled)
	}
	if got != (store.WorkflowColumn{}) || len(fake.mutationCalls) != 1 || len(fake.readCalls) != 0 {
		t.Fatalf("column=%+v mutations=%+v reads=%+v", got, fake.mutationCalls, fake.readCalls)
	}
	if fake.mutationCalls[0].ctx != ctx {
		t.Fatalf("mutation context was not bound context")
	}
	assertMCPMutationTrace(t, fake.trace, "access", "update")
}
