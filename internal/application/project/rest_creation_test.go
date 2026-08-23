package project

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type restDurableCreationFake struct {
	trace       projectServiceTrace
	calls       int
	ctx         context.Context
	name        string
	workflow    []store.WorkflowColumn
	workflowNil bool
	result      store.Project
	err         error
}

func (f *restDurableCreationFake) CreateProjectWithWorkflow(
	ctx context.Context,
	name string,
	workflow []store.WorkflowColumn,
) (store.Project, error) {
	f.trace.add("create-durable")
	f.calls++
	f.ctx = ctx
	f.name = name
	f.workflowNil = workflow == nil
	f.workflow = cloneWorkflowColumns(workflow)
	if len(workflow) > 0 {
		workflow[0].Key = "mutated-by-store"
	}
	return f.result, f.err
}

type anonymousCreationFake struct {
	trace  projectServiceTrace
	calls  int
	ctx    context.Context
	result store.Project
	err    error
}

func (f *anonymousCreationFake) CreateAnonymousBoard(ctx context.Context) (store.Project, error) {
	f.trace.add("create-anonymous")
	f.calls++
	f.ctx = ctx
	return f.result, f.err
}

func TestRESTDurableCreationPreservesWorkflowPresenceAndIsolation(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "creation")

	tests := []struct {
		name        string
		workflow    []store.WorkflowColumn
		wantNil     bool
		wantColumns []store.WorkflowColumn
	}{
		{name: "omitted", workflow: nil, wantNil: true},
		{name: "supplied empty", workflow: []store.WorkflowColumn{}, wantColumns: []store.WorkflowColumn{}},
		{
			name: "custom",
			workflow: []store.WorkflowColumn{
				{Key: " RAW ", Name: " Name ", Color: " color ", Position: 9, IsDone: true, System: true},
				{Key: "second", Position: -1},
			},
			wantColumns: []store.WorkflowColumn{
				{Key: " RAW ", Name: " Name ", Color: " color ", Position: 9, IsDone: true, System: true},
				{Key: "second", Position: -1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := cloneWorkflowColumns(tt.workflow)
			fake := &restDurableCreationFake{result: store.Project{ID: 7, Name: "Created"}}
			prepared := NewRESTDurableCreationService(fake).Prepare(ctx, RESTDurableCreationCommand{
				Name:     "  Raw Name  ",
				Workflow: source,
			})

			if len(source) > 0 {
				source[0].Key = "mutated-after-prepare"
			}
			created, err := prepared.Create()
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if fake.calls != 1 || fake.name != "  Raw Name  " {
				t.Fatalf("creation call count/name = %d/%q", fake.calls, fake.name)
			}
			assertProjectServiceContext(t, fake.ctx, ctx)
			if fake.workflowNil != tt.wantNil {
				t.Fatalf("workflow nil = %v, want %v", fake.workflowNil, tt.wantNil)
			}
			if !reflectWorkflowColumns(fake.workflow, tt.wantColumns) {
				t.Fatalf("workflow = %#v, want %#v", fake.workflow, tt.wantColumns)
			}
			if len(prepared.workflow) > 0 && prepared.workflow[0].Key != tt.wantColumns[0].Key {
				t.Fatalf("prepared workflow mutated by store: %#v", prepared.workflow)
			}
			if created.ID != 7 || created.Name != "Created" {
				t.Fatalf("created = %+v", created)
			}
			assertProjectServiceTrace(t, &fake.trace, "create-durable")
		})
	}
}

func TestRESTDurableCreationReturnsStoreErrorUnchanged(t *testing.T) {
	wantErr := errors.New("durable creation failed")
	fake := &restDurableCreationFake{err: wantErr}
	_, err := NewRESTDurableCreationService(fake).
		Prepare(context.Background(), RESTDurableCreationCommand{Name: "x"}).
		Create()
	if err != wantErr || fake.calls != 1 {
		t.Fatalf("Create() error/calls = %v/%d, want exact error/1", err, fake.calls)
	}
}

func TestAnonymousBoardCreationCallsOnceWithoutCompensation(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "anonymous")
	wantErr := errors.New("late workflow initialization failed")
	fake := &anonymousCreationFake{err: wantErr}

	_, err := NewAnonymousBoardCreationService(fake).Create(ctx)
	if err != wantErr || fake.calls != 1 {
		t.Fatalf("Create() error/calls = %v/%d, want exact late error/1", err, fake.calls)
	}
	assertProjectServiceContext(t, fake.ctx, ctx)
	assertProjectServiceTrace(t, &fake.trace, "create-anonymous")

	image := "image"
	fake = &anonymousCreationFake{result: store.Project{ID: 11, Slug: "anon", Image: &image}}
	created, err := NewAnonymousBoardCreationService(fake).Create(ctx)
	if err != nil || created.ID != 11 || created.Slug != "anon" || created.Image == nil || *created.Image != "image" {
		t.Fatalf("successful anonymous Create() = %+v, %v", created, err)
	}
	if fake.calls != 1 {
		t.Fatalf("successful anonymous Create() calls = %d, want 1", fake.calls)
	}
	assertProjectServiceTrace(t, &fake.trace, "create-anonymous")
	*created.Image = "caller mutation"
	if *fake.result.Image != "image" {
		t.Fatal("returned project aliases fake/store project image")
	}
}

func reflectWorkflowColumns(got, want []store.WorkflowColumn) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
