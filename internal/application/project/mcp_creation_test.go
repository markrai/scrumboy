package project

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type mcpDurableCreationFake struct {
	trace  projectServiceTrace
	calls  int
	ctx    context.Context
	name   string
	result store.Project
	err    error
}

func (f *mcpDurableCreationFake) CreateProject(ctx context.Context, name string) (store.Project, error) {
	f.trace.add("create-project")
	f.calls++
	f.ctx = ctx
	f.name = name
	return f.result, f.err
}

func TestMCPDurableCreationCallsOnceAndSynthesizesMaintainer(t *testing.T) {
	ctx := context.WithValue(context.Background(), struct{}{}, "mcp-create")
	image := "image"
	fake := &mcpDurableCreationFake{result: store.Project{ID: 9, Slug: "canonical", Image: &image}}

	result, err := NewMCPDurableCreationService(fake).Create(ctx, MCPDurableCreationCommand{Name: "  Raw MCP Name  "})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if fake.calls != 1 || fake.name != "  Raw MCP Name  " {
		t.Fatalf("create calls/name = %d/%q", fake.calls, fake.name)
	}
	assertProjectServiceContext(t, fake.ctx, ctx)
	assertProjectServiceTrace(t, &fake.trace, "create-project")
	if result.Project.ID != 9 || result.Project.Slug != "canonical" || result.Role != store.RoleMaintainer {
		t.Fatalf("result = %+v", result)
	}
	*result.Project.Image = "caller mutation"
	if *fake.result.Image != "image" {
		t.Fatal("MCP result aliases fake/store project image")
	}
}

func TestMCPDurableCreationReturnsStoreErrorUnchanged(t *testing.T) {
	wantErr := errors.New("MCP create failed")
	fake := &mcpDurableCreationFake{err: wantErr}
	_, err := NewMCPDurableCreationService(fake).Create(context.Background(), MCPDurableCreationCommand{Name: "x"})
	if err != wantErr || fake.calls != 1 {
		t.Fatalf("Create() error/calls = %v/%d, want exact error/1", err, fake.calls)
	}
}
