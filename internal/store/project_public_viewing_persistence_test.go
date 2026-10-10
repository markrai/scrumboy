package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectPublicViewingDefaultsPrivateAcrossCreationKinds(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "publication-defaults@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	userCtx := WithUserID(ctx, user.ID)

	durable, err := st.CreateProject(userCtx, "Durable Private")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	temporary, err := st.CreateAnonymousBoard(userCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard temporary: %v", err)
	}
	anonymous, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard anonymous: %v", err)
	}

	for _, project := range []Project{durable, temporary, anonymous} {
		if project.PublicViewingEnabled {
			t.Fatalf("created project %d returned public", project.ID)
		}
		persisted, err := st.GetProject(ctx, project.ID)
		if err != nil {
			t.Fatalf("GetProject(%d): %v", project.ID, err)
		}
		if persisted.PublicViewingEnabled {
			t.Fatalf("created project %d persisted public", project.ID)
		}
	}
}

func TestProjectPublicViewingScannerAndListRetainPersistedState(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "publication-scanner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	userCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(userCtx, "Publication Scanner")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("seed publication state: %v", err)
	}

	byID, err := st.GetProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	bySlug, err := st.GetProjectBySlug(ctx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	if !byID.PublicViewingEnabled || !bySlug.PublicViewingEnabled {
		t.Fatalf("canonical scanners lost publication state: byID=%v bySlug=%v", byID.PublicViewingEnabled, bySlug.PublicViewingEnabled)
	}
	listed, err := st.ListProjects(userCtx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(listed) != 1 || !listed[0].Project.PublicViewingEnabled {
		t.Fatalf("ListProjects publication state = %+v", listed)
	}
}

func TestClaimTemporaryBoardForcesPublicationPrivate(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "publication-claim@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	board, err := st.CreateAnonymousBoard(WithUserID(ctx, user.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, board.ID); err != nil {
		t.Fatalf("seed impossible temporary publication state: %v", err)
	}
	if err := st.ClaimTemporaryBoard(ctx, board.ID, user.ID); err != nil {
		t.Fatalf("ClaimTemporaryBoard: %v", err)
	}
	claimed, err := st.GetProject(ctx, board.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if claimed.PublicViewingEnabled {
		t.Fatal("claimed project retained publication state")
	}
}

func TestPortableExportOmitsPublicationAndCopyImportsPrivate(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "publication-copy@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	userCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(userCtx, "Published Source")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("seed publication state: %v", err)
	}

	exported, err := st.ExportAllProjects(userCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("Marshal export: %v", err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "publicview") || strings.Contains(strings.ToLower(string(encoded)), "public_view") {
		t.Fatalf("portable export leaked publication state: %s", encoded)
	}
	if _, err := st.ImportProjects(userCtx, exported, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}
	var copiedPublic, copiedCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(public_view_enabled), 0), COUNT(*) FROM projects WHERE id <> ?`, project.ID).Scan(&copiedPublic, &copiedCount); err != nil {
		t.Fatalf("read copied publication state: %v", err)
	}
	if copiedCount == 0 || copiedPublic != 0 {
		t.Fatalf("copied projects count=%d public max=%d, want count>0 and public=0", copiedCount, copiedPublic)
	}
}

func TestPortableImportIgnoresExplicitPublicationFields(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "publication-explicit@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	userCtx := WithUserID(ctx, user.ID)
	project, err := st.CreateProject(userCtx, "Explicit Publication Input")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	exported, err := st.ExportAllProjects(userCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("Marshal export: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("Unmarshal raw export: %v", err)
	}
	projects, ok := raw["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("raw projects = %#v", raw["projects"])
	}
	rawProject, ok := projects[0].(map[string]any)
	if !ok {
		t.Fatalf("raw project = %#v", projects[0])
	}
	rawProject["publicViewingEnabled"] = true
	rawProject["public_view_enabled"] = true
	encoded, err = json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal populated export: %v", err)
	}
	var populated ExportData
	if err := json.Unmarshal(encoded, &populated); err != nil {
		t.Fatalf("Unmarshal populated export: %v", err)
	}
	if _, err := st.ImportProjects(userCtx, &populated, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}
	var copiedPublic, copiedCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(public_view_enabled), 0), COUNT(*) FROM projects WHERE id <> ?`, project.ID).Scan(&copiedPublic, &copiedCount); err != nil {
		t.Fatalf("read copied publication state: %v", err)
	}
	if copiedCount == 0 || copiedPublic != 0 {
		t.Fatalf("copied projects count=%d public max=%d, want count>0 and public=0", copiedCount, copiedPublic)
	}
}

func TestPortableImportDoesNotTransferPublicationState(t *testing.T) {
	for _, importMode := range []string{"merge", "replace"} {
		t.Run(importMode, func(t *testing.T) {
			st, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()
			user, err := st.BootstrapUser(ctx, "publication-"+importMode+"@example.com", "password123", "Owner")
			if err != nil {
				t.Fatalf("BootstrapUser: %v", err)
			}
			userCtx := WithUserID(ctx, user.ID)
			project, err := st.CreateProject(userCtx, "Import Publication")
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			exported, err := st.ExportAllProjects(userCtx, ModeFull)
			if err != nil {
				t.Fatalf("ExportAllProjects: %v", err)
			}
			if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, project.ID); err != nil {
				t.Fatalf("seed target publication state: %v", err)
			}

			if _, err := st.ImportProjects(userCtx, exported, ModeFull, importMode); err != nil {
				t.Fatalf("ImportProjects %s: %v", importMode, err)
			}
			result, err := st.GetProjectBySlug(ctx, project.Slug)
			if err != nil {
				t.Fatalf("GetProjectBySlug: %v", err)
			}
			if importMode == "merge" && !result.PublicViewingEnabled {
				t.Fatal("merge overwrote the existing target publication preference")
			}
			if importMode == "replace" && result.PublicViewingEnabled {
				t.Fatal("replace transferred publication state into the recreated project")
			}
		})
	}
}
