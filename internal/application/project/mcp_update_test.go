package project

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type mcpUpdateFake struct {
	*mcpManagedProjectFake

	patchCalls     int
	patchCtx       context.Context
	patchProjectID int64
	patchActorID   int64
	patch          store.UpdateProjectPatch
	patchErr       error

	readCalls     int
	readCtx       context.Context
	readProjectID int64
	readResult    store.Project
	readErr       error
}

func (f *mcpUpdateFake) UpdateProjectPatch(
	ctx context.Context,
	projectID int64,
	actorID int64,
	patch store.UpdateProjectPatch,
) error {
	f.trace.add("patch")
	f.patchCalls++
	f.patchCtx = ctx
	f.patchProjectID = projectID
	f.patchActorID = actorID
	f.patch = store.UpdateProjectPatch{
		Name:               cloneString(patch.Name),
		DefaultSprintWeeks: cloneInt(patch.DefaultSprintWeeks),
	}
	if patch.Name != nil {
		*patch.Name = "mutated by patch store"
	}
	if patch.DefaultSprintWeeks != nil {
		*patch.DefaultSprintWeeks = 99
	}
	return f.patchErr
}

func (f *mcpUpdateFake) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	f.trace.add("post-read")
	f.readCalls++
	f.readCtx = ctx
	f.readProjectID = projectID
	return f.readResult, f.readErr
}

func newMCPUpdateTestService(fake *mcpUpdateFake) *MCPUpdateService {
	return NewMCPUpdateService(MCPUpdateServiceDependencies{
		Access:   fake,
		Manage:   fake,
		Patches:  fake,
		Projects: fake,
	})
}

func TestMCPUpdatePreservesAtomicPatchAndPostReadSequence(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 51)
	image := "projection image"
	managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{
		Project: store.Project{ID: 61, Slug: "canonical"},
		Role:    store.RoleOwner,
	}}
	fake := &mcpUpdateFake{
		mcpManagedProjectFake: managed,
		readResult:            store.Project{ID: 61, Slug: "canonical", Image: &image},
	}
	name := "  raw name  "
	weeks := 2
	prepared, err := newMCPUpdateTestService(fake).Prepare(
		ctx,
		ProjectSlugTarget{ProjectSlug: "requested", Mode: store.ModeFull},
		MCPUpdateCommand{Name: &name, DefaultSprintWeeks: &weeks},
	)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	assertProjectServiceTrace(t, &managed.trace, "access", "manage")
	if fake.patchCalls != 0 || fake.readCalls != 0 {
		t.Fatalf("Prepare() performed writes/reads = %d/%d", fake.patchCalls, fake.readCalls)
	}

	name = "changed after preparation"
	weeks = 1
	result, err := prepared.Update()
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertProjectServiceTrace(t, &managed.trace, "access", "manage", "patch", "post-read")
	if fake.patchCalls != 1 || fake.readCalls != 1 || fake.patchProjectID != 61 ||
		fake.patchActorID != 51 || fake.readProjectID != 61 {
		t.Fatalf("patch/read captures = %+v", fake)
	}
	if fake.patch.Name == nil || *fake.patch.Name != "  raw name  " ||
		fake.patch.DefaultSprintWeeks == nil || *fake.patch.DefaultSprintWeeks != 2 {
		t.Fatalf("patch = %+v, want prepared values", fake.patch)
	}
	assertProjectServiceContext(t, fake.patchCtx, ctx)
	assertProjectServiceContext(t, fake.readCtx, ctx)
	if result.Project.ID != 61 || result.Role != store.RoleOwner {
		t.Fatalf("result = %+v", result)
	}
	*result.Project.Image = "caller mutation"
	if *fake.readResult.Image != "projection image" {
		t.Fatal("MCP update result aliases post-read project")
	}
}

func TestMCPUpdatePreservesPartialSuccessFailuresWithoutRetry(t *testing.T) {
	t.Run("patch failure stops post-read", func(t *testing.T) {
		wantErr := errors.New("patch failed")
		managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 62}}}
		fake := &mcpUpdateFake{mcpManagedProjectFake: managed, patchErr: wantErr}
		prepared, err := newMCPUpdateTestService(fake).Prepare(
			store.WithUserID(context.Background(), 52),
			ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull},
			MCPUpdateCommand{Name: projectServiceString("name")},
		)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		_, err = prepared.Update()
		if err != wantErr {
			t.Fatalf("Update() error = %v, want exact patch error", err)
		}
		assertProjectServiceTrace(t, &managed.trace, "access", "manage", "patch")
		if fake.patchCalls != 1 || fake.readCalls != 0 {
			t.Fatalf("patch/read calls = %d/%d", fake.patchCalls, fake.readCalls)
		}
	})

	t.Run("post-read failure leaves one committed patch", func(t *testing.T) {
		wantErr := errors.New("post-read failed")
		managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 63}}}
		fake := &mcpUpdateFake{mcpManagedProjectFake: managed, readErr: wantErr}
		prepared, err := newMCPUpdateTestService(fake).Prepare(
			store.WithUserID(context.Background(), 53),
			ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull},
			MCPUpdateCommand{DefaultSprintWeeks: projectServiceInt(2)},
		)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		_, err = prepared.Update()
		if err != wantErr {
			t.Fatalf("Update() error = %v, want exact post-read error", err)
		}
		assertProjectServiceTrace(t, &managed.trace, "access", "manage", "patch", "post-read")
		if fake.patchCalls != 1 || fake.readCalls != 1 {
			t.Fatalf("patch/read calls = %d/%d", fake.patchCalls, fake.readCalls)
		}
	})
}

func TestMCPUpdatePreservesPatchFieldPresence(t *testing.T) {
	tests := []struct {
		name      string
		command   MCPUpdateCommand
		wantName  bool
		wantWeeks bool
	}{
		{name: "name only", command: MCPUpdateCommand{Name: projectServiceString("name")}, wantName: true},
		{name: "weeks only", command: MCPUpdateCommand{DefaultSprintWeeks: projectServiceInt(1)}, wantWeeks: true},
		{name: "both", command: MCPUpdateCommand{Name: projectServiceString("name"), DefaultSprintWeeks: projectServiceInt(2)}, wantName: true, wantWeeks: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managed := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 64}}}
			fake := &mcpUpdateFake{mcpManagedProjectFake: managed, readResult: store.Project{ID: 64}}
			prepared, err := newMCPUpdateTestService(fake).Prepare(
				store.WithUserID(context.Background(), 54),
				ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull},
				tt.command,
			)
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if _, err := prepared.Update(); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if (fake.patch.Name != nil) != tt.wantName ||
				(fake.patch.DefaultSprintWeeks != nil) != tt.wantWeeks {
				t.Fatalf("patch field presence = name:%v weeks:%v", fake.patch.Name != nil, fake.patch.DefaultSprintWeeks != nil)
			}
		})
	}
}
