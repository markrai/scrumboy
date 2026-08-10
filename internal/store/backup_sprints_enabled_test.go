package store

import (
	"context"
	"testing"
)

func TestBackupPreservesSprintsEnabledAndOlderPayloadDefaultsEnabled(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "backup-sprints@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ownerCtx, "Backup sprint capability")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, user.ID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}

	data, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 || data.Projects[0].SprintsEnabled == nil || *data.Projects[0].SprintsEnabled {
		t.Fatalf("exported sprintsEnabled=%v, want explicit false", data.Projects[0].SprintsEnabled)
	}

	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "replace"); err != nil {
		t.Fatalf("replace import: %v", err)
	}
	imported, err := st.GetProjectBySlug(ownerCtx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug after replace: %v", err)
	}
	if imported.SprintsEnabled {
		t.Fatal("replace import did not preserve disabled capability")
	}

	data.Projects[0].SprintsEnabled = nil
	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "merge"); err != nil {
		t.Fatalf("legacy merge import: %v", err)
	}
	imported, err = st.GetProjectBySlug(ownerCtx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug after legacy merge: %v", err)
	}
	if !imported.SprintsEnabled {
		t.Fatal("older backup without sprintsEnabled must default to enabled")
	}
}
