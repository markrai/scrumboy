package project

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type mcpManagedProjectFake struct {
	trace projectServiceTrace

	projectContext store.ProjectContext
	accessErr      error
	manageErr      error

	accessCalls int
	accessCtx   context.Context
	accessSlug  string
	accessMode  store.Mode

	manageCalls     int
	manageCtx       context.Context
	manageProjectID int64
	manageActorID   int64
}

func (f *mcpManagedProjectFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace.add("access")
	f.accessCalls++
	f.accessCtx = ctx
	f.accessSlug = slug
	f.accessMode = mode
	return f.projectContext, f.accessErr
}

func (f *mcpManagedProjectFake) CheckCanManageProject(
	ctx context.Context,
	projectID int64,
	actorID int64,
) error {
	f.trace.add("manage")
	f.manageCalls++
	f.manageCtx = ctx
	f.manageProjectID = projectID
	f.manageActorID = actorID
	return f.manageErr
}

func TestMCPManagedProjectPreparationPreservesSequencing(t *testing.T) {
	t.Run("access failure stops before actor and manage", func(t *testing.T) {
		wantErr := errors.New("access failed")
		fake := &mcpManagedProjectFake{accessErr: wantErr}
		prepared, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			context.Background(),
			ProjectSlugTarget{ProjectSlug: "raw-slug", Mode: store.ModeFull},
		)
		if err != wantErr || prepared.actorUserID != 0 {
			t.Fatalf("prepare() = %+v, %v, want exact access error", prepared, err)
		}
		assertProjectServiceTrace(t, &fake.trace, "access")
	})

	t.Run("missing actor follows access and stops manage", func(t *testing.T) {
		fake := &mcpManagedProjectFake{projectContext: store.ProjectContext{Project: store.Project{ID: 22}}}
		_, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			context.Background(),
			ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeFull},
		)
		if !errors.Is(err, ErrActorRequired) {
			t.Fatalf("prepare() error = %v, want actor required", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "access")
	})

	t.Run("manage failure is returned unchanged", func(t *testing.T) {
		wantErr := errors.New("manage failed")
		ctx := store.WithUserID(context.Background(), 31)
		fake := &mcpManagedProjectFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 23}},
			manageErr:      wantErr,
		}
		_, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			ctx,
			ProjectSlugTarget{ProjectSlug: "project", Mode: store.ModeAnonymous},
		)
		if err != wantErr {
			t.Fatalf("prepare() error = %v, want exact manage error", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "access", "manage")
		assertProjectServiceContext(t, fake.accessCtx, ctx)
		assertProjectServiceContext(t, fake.manageCtx, ctx)
		if fake.accessSlug != "project" || fake.accessMode != store.ModeAnonymous ||
			fake.manageProjectID != 23 || fake.manageActorID != 31 {
			t.Fatalf("captured access/manage values = %+v", fake)
		}
	})
}

func TestMCPManagedProjectPreparationClonesContextAndPreservesRoleRules(t *testing.T) {
	t.Run("durable role is preserved", func(t *testing.T) {
		ctx := store.WithUserID(context.Background(), 41)
		fake := &mcpManagedProjectFake{projectContext: store.ProjectContext{
			Project: store.Project{ID: 24, Slug: "canonical"},
			Role:    store.RoleOwner,
		}}
		prepared, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			ctx,
			ProjectSlugTarget{ProjectSlug: "requested", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("prepare() error = %v", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "access", "manage")
		if prepared.projectContext.Role != store.RoleOwner || prepared.actorUserID != 41 {
			t.Fatalf("prepared managed project = %+v", prepared)
		}
	})

	t.Run("temporary creator receives characterized maintainer projection", func(t *testing.T) {
		actorID := int64(42)
		expires := time.Now().UTC().Add(time.Hour)
		fake := &mcpManagedProjectFake{projectContext: store.ProjectContext{
			Project: store.Project{
				ID:            25,
				Slug:          "temporary",
				CreatorUserID: &actorID,
				ExpiresAt:     &expires,
			},
			Role: store.RoleViewer,
		}}
		prepared, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			store.WithUserID(context.Background(), actorID),
			ProjectSlugTarget{ProjectSlug: "temporary", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("prepare() error = %v", err)
		}
		if prepared.projectContext.Role != store.RoleMaintainer {
			t.Fatalf("prepared role = %q, want Maintainer", prepared.projectContext.Role)
		}

		// The prepared value owns the project classification and identity even if
		// the source projection is later changed.
		fake.projectContext.Project.ExpiresAt = nil
		actorID = 1000
		if prepared.projectContext.Project.ExpiresAt == nil ||
			prepared.projectContext.Project.CreatorUserID == nil ||
			*prepared.projectContext.Project.CreatorUserID != 42 {
			t.Fatalf("prepared project aliases source context: %+v", prepared.projectContext.Project)
		}
	})

	t.Run("temporary non-creator does not gain maintainer projection", func(t *testing.T) {
		creatorID := int64(43)
		expires := time.Now().UTC().Add(time.Hour)
		fake := &mcpManagedProjectFake{projectContext: store.ProjectContext{
			Project: store.Project{
				ID:            26,
				CreatorUserID: &creatorID,
				ExpiresAt:     &expires,
			},
			Role: store.RoleViewer,
		}}
		prepared, err := (mcpManagedProjectPreparer{access: fake, manage: fake}).prepare(
			store.WithUserID(context.Background(), 44),
			ProjectSlugTarget{ProjectSlug: "temporary", Mode: store.ModeFull},
		)
		if err != nil {
			t.Fatalf("prepare() error = %v", err)
		}
		if prepared.projectContext.Role != store.RoleViewer {
			t.Fatalf("prepared role = %q, want Viewer", prepared.projectContext.Role)
		}
	})
}
