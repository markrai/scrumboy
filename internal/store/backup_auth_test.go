package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func setupBackupAuthProject(t *testing.T) (*Store, func(), context.Context, Project, int64, int64, int64, int64) {
	st, cleanup := newTestStore(t)
	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "owner-auth@example.com", "password", "Owner")
	maintainer, _ := st.CreateUser(ctx, "maintainer-auth@example.com", "password", "Maintainer")
	contributor, _ := st.CreateUser(ctx, "contrib-auth@example.com", "password", "Contributor")
	viewer, _ := st.CreateUser(ctx, "viewer-auth@example.com", "password", "Viewer")
	ctxOwner := WithUserID(ctx, owner.ID)
	project, _ := st.CreateProject(ctxOwner, "Auth Test Project")
	_ = st.AddProjectMember(ctxOwner, owner.ID, project.ID, maintainer.ID, RoleMaintainer)
	_ = st.AddProjectMember(ctxOwner, owner.ID, project.ID, contributor.ID, RoleContributor)
	_ = st.AddProjectMember(ctxOwner, owner.ID, project.ID, viewer.ID, RoleViewer)
	return st, cleanup, ctx, project, owner.ID, maintainer.ID, contributor.ID, viewer.ID
}

func TestBackupAuth_ViewerCannotExport(t *testing.T) {
	st, cleanup, ctx, project, _, _, _, viewerID := setupBackupAuthProject(t)
	defer cleanup()
	ctxViewer := WithUserID(ctx, viewerID)
	data, _ := st.ExportAllProjects(ctxViewer, ModeFull)
	for _, p := range data.Projects {
		if p.Slug == project.Slug {
			t.Error("viewer should not export project")
		}
	}
}

func TestBackupAuth_ContributorCannotExport(t *testing.T) {
	st, cleanup, ctx, project, _, _, contribID, _ := setupBackupAuthProject(t)
	defer cleanup()
	ctxContrib := WithUserID(ctx, contribID)
	data, _ := st.ExportAllProjects(ctxContrib, ModeFull)
	for _, p := range data.Projects {
		if p.Slug == project.Slug {
			t.Error("contributor should not export project")
		}
	}
}

func TestBackupAuth_MaintainerCanExport(t *testing.T) {
	st, cleanup, ctx, project, _, maintID, _, _ := setupBackupAuthProject(t)
	defer cleanup()
	ctxMaint := WithUserID(ctx, maintID)
	data, _ := st.ExportAllProjects(ctxMaint, ModeFull)
	var found bool
	for _, p := range data.Projects {
		if p.Slug == project.Slug {
			found = true
		}
	}
	if !found {
		t.Error("maintainer should export project")
	}
}

func TestBackupAuth_ViewerCannotMerge(t *testing.T) {
	st, cleanup, ctx, project, _, _, _, viewerID := setupBackupAuthProject(t)
	defer cleanup()
	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now,
			Todos: []TodoExport{{LocalID: 1, Title: "x", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}},
		}},
	}
	_, err := st.ImportProjects(WithUserID(ctx, viewerID), data, ModeFull, "merge")
	if err == nil {
		t.Fatal("viewer should not merge")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestBackupAuth_MaintainerCanMerge(t *testing.T) {
	st, cleanup, ctx, project, _, maintID, _, _ := setupBackupAuthProject(t)
	defer cleanup()
	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now,
			Todos: []TodoExport{{LocalID: 1, Title: "Merged", Status: "DONE", Rank: 1000, CreatedAt: now, UpdatedAt: now}},
		}},
	}
	_, err := st.ImportProjects(WithUserID(ctx, maintID), data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("maintainer should merge: %v", err)
	}
}
