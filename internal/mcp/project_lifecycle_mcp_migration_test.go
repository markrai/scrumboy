package mcp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	projectapp "scrumboy/internal/application/project"
	"scrumboy/internal/store"
)

type projectLifecycleMCPMigrationStore struct {
	storeAPI

	trace []string

	createName   string
	createResult store.Project
	createErr    error

	accessSlug   string
	accessMode   store.Mode
	accessResult store.ProjectContext
	accessErr    error

	manageProjectID int64
	manageActorID   int64
	manageErr       error

	patchProjectID int64
	patchActorID   int64
	patch          store.UpdateProjectPatch
	patchErr       error

	readProjectID int64
	readResult    store.Project
	readErr       error

	deleteProjectID int64
	deleteActorID   int64
	deleteResult    store.DeletedProjectSnapshot
	deleteErr       error
}

func (s *projectLifecycleMCPMigrationStore) CountUsers(context.Context) (int, error) {
	return 1, nil
}

func (s *projectLifecycleMCPMigrationStore) CreateProject(
	_ context.Context,
	name string,
) (store.Project, error) {
	s.trace = append(s.trace, "create")
	s.createName = name
	return s.createResult, s.createErr
}

func (s *projectLifecycleMCPMigrationStore) GetProjectContextBySlug(
	_ context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	s.trace = append(s.trace, "access")
	s.accessSlug = slug
	s.accessMode = mode
	return s.accessResult, s.accessErr
}

func (s *projectLifecycleMCPMigrationStore) CheckCanManageProject(
	_ context.Context,
	projectID int64,
	actorID int64,
) error {
	s.trace = append(s.trace, "manage")
	s.manageProjectID = projectID
	s.manageActorID = actorID
	return s.manageErr
}

func (s *projectLifecycleMCPMigrationStore) UpdateProjectPatch(
	_ context.Context,
	projectID int64,
	actorID int64,
	patch store.UpdateProjectPatch,
) error {
	s.trace = append(s.trace, "patch")
	s.patchProjectID = projectID
	s.patchActorID = actorID
	s.patch = patch
	return s.patchErr
}

func (s *projectLifecycleMCPMigrationStore) GetProject(
	_ context.Context,
	projectID int64,
) (store.Project, error) {
	s.trace = append(s.trace, "post-read")
	s.readProjectID = projectID
	return s.readResult, s.readErr
}

func (s *projectLifecycleMCPMigrationStore) DeleteProject(
	_ context.Context,
	projectID int64,
	actorID int64,
) (store.DeletedProjectSnapshot, error) {
	s.trace = append(s.trace, "delete")
	s.deleteProjectID = projectID
	s.deleteActorID = actorID
	return s.deleteResult, s.deleteErr
}

func TestProjectLifecycleMCPMigrationNewComposesSeparateServices(t *testing.T) {
	adapter := New(&projectLifecycleMCPMigrationStore{}, Options{Mode: "full"})
	if adapter.projectCreations == nil || adapter.projectUpdates == nil || adapter.projectDeletions == nil {
		t.Fatalf(
			"project lifecycle services = creation:%p update:%p deletion:%p",
			adapter.projectCreations,
			adapter.projectUpdates,
			adapter.projectDeletions,
		)
	}
}

func TestProjectLifecycleMCPMigrationCreationPreservesRawNameBoundary(t *testing.T) {
	const rawName = "  Store Normalizes This Name  "
	fake := &projectLifecycleMCPMigrationStore{
		createResult: store.Project{
			ID:   101,
			Slug: "store-normalizes-this-name",
			Name: "Store Normalizes This Name",
		},
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(context.Background(), 11)

	data, meta, adapterErr := adapter.handleProjectsCreate(ctx, map[string]any{"name": rawName})
	if adapterErr != nil {
		t.Fatalf("handleProjectsCreate() error = %+v", adapterErr)
	}
	if !reflect.DeepEqual(fake.trace, []string{"create"}) {
		t.Fatalf("creation trace = %v", fake.trace)
	}
	if fake.createName != rawName {
		t.Fatalf("creation name = %q, want raw %q", fake.createName, rawName)
	}
	if len(meta) != 0 {
		t.Fatalf("creation metadata = %+v", meta)
	}
	project := data.(map[string]any)["project"].(projectItem)
	if project.ProjectID != 101 || project.ProjectSlug != "store-normalizes-this-name" ||
		project.Name != "Store Normalizes This Name" || project.Role != "maintainer" {
		t.Fatalf("creation projection = %+v", project)
	}

	fake.trace = nil
	_, _, adapterErr = adapter.handleProjectsCreate(ctx, map[string]any{"name": " \t "})
	if adapterErr == nil || adapterErr.Status != 400 || adapterErr.Code != CodeValidationError ||
		adapterErr.Message != "missing name" {
		t.Fatalf("whitespace creation error = %+v", adapterErr)
	}
	if len(fake.trace) != 0 {
		t.Fatalf("whitespace creation trace = %v", fake.trace)
	}
}

func TestProjectLifecycleMCPMigrationUpdateDelegatesOneExactSequence(t *testing.T) {
	fake := &projectLifecycleMCPMigrationStore{
		accessResult: store.ProjectContext{
			Project: store.Project{ID: 201, Slug: "canonical-before-update"},
			Role:    store.RoleOwner,
		},
		readResult: store.Project{
			ID:                 201,
			Slug:               "canonical-after-update",
			Name:               "Updated",
			DefaultSprintWeeks: 1,
		},
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(context.Background(), 21)

	data, meta, adapterErr := adapter.handleProjectsUpdate(ctx, map[string]any{
		"projectSlug": "  requested-slug  ",
		"patch": map[string]any{
			"name":               "Updated",
			"defaultSprintWeeks": 1,
		},
	})
	if adapterErr != nil {
		t.Fatalf("handleProjectsUpdate() error = %+v", adapterErr)
	}
	if !reflect.DeepEqual(fake.trace, []string{"access", "manage", "patch", "post-read"}) {
		t.Fatalf("update trace = %v", fake.trace)
	}
	if fake.accessSlug != "  requested-slug  " || fake.accessMode != store.ModeFull {
		t.Fatalf("update access = slug:%q mode:%q", fake.accessSlug, fake.accessMode)
	}
	if fake.manageProjectID != 201 || fake.manageActorID != 21 ||
		fake.patchProjectID != 201 || fake.patchActorID != 21 || fake.readProjectID != 201 {
		t.Fatalf("update IDs = %+v", fake)
	}
	if fake.patch.Name == nil || *fake.patch.Name != "Updated" ||
		fake.patch.DefaultSprintWeeks == nil || *fake.patch.DefaultSprintWeeks != 1 {
		t.Fatalf("update patch = %+v", fake.patch)
	}
	if len(meta) != 0 {
		t.Fatalf("update metadata = %+v", meta)
	}
	project := data.(map[string]any)["project"].(projectItem)
	if project.ProjectSlug != "canonical-after-update" || project.Role != "owner" {
		t.Fatalf("update projection = %+v", project)
	}
}

func TestProjectLifecycleMCPMigrationDeletionDelegatesOneExactSequence(t *testing.T) {
	fake := &projectLifecycleMCPMigrationStore{
		accessResult: store.ProjectContext{
			Project: store.Project{ID: 301, Slug: "canonical-delete-slug"},
			Role:    store.RoleMaintainer,
		},
		deleteResult: store.DeletedProjectSnapshot{ProjectID: 301},
	}
	adapter := New(fake, Options{Mode: "full"})
	ctx := store.WithUserID(context.Background(), 31)

	data, meta, adapterErr := adapter.handleProjectsDelete(ctx, map[string]any{
		"projectSlug": "  requested-delete-slug  ",
	})
	if adapterErr != nil {
		t.Fatalf("handleProjectsDelete() error = %+v", adapterErr)
	}
	if !reflect.DeepEqual(fake.trace, []string{"access", "manage", "delete"}) {
		t.Fatalf("deletion trace = %v", fake.trace)
	}
	if fake.accessSlug != "  requested-delete-slug  " || fake.accessMode != store.ModeFull ||
		fake.manageProjectID != 301 || fake.manageActorID != 31 ||
		fake.deleteProjectID != 301 || fake.deleteActorID != 31 {
		t.Fatalf("deletion captures = %+v", fake)
	}
	if len(meta) != 0 {
		t.Fatalf("deletion metadata = %+v", meta)
	}
	result := data.(map[string]any)
	if result["status"] != "deleted" || result["projectSlug"] != "canonical-delete-slug" ||
		result["projectId"] != int64(301) {
		t.Fatalf("deletion projection = %+v", result)
	}
}

func TestMapProjectApplicationErrorOwnsOnlyActorSentinel(t *testing.T) {
	actorErr := mapProjectApplicationError(projectapp.ErrActorRequired)
	if actorErr.Status != 401 || actorErr.Code != CodeAuthRequired ||
		actorErr.Message != "Sign-in required for this tool" || actorErr.Details != nil {
		t.Fatalf("actor sentinel mapping = %+v", actorErr)
	}

	for _, err := range []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		store.ErrValidation,
		store.ErrConflict,
		errors.New("internal failure"),
	} {
		if got, want := mapProjectApplicationError(err), mapStoreError(err); !reflect.DeepEqual(got, want) {
			t.Fatalf("mapProjectApplicationError(%v) = %+v, want shared mapping %+v", err, got, want)
		}
	}
}
