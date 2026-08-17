package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestBackupExportsTodoCreatorAttribution(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "backup-creator-export@example.com", "password123", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Backup creator export")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "attributed"}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	exported, err := st.ExportAllProjects(ownerCtx, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(exported.Projects) != 1 || len(exported.Projects[0].Todos) != 1 {
		t.Fatalf("unexpected export shape: %+v", exported.Projects)
	}
	creatorID := exported.Projects[0].Todos[0].CreatedByUserId
	if creatorID == nil || *creatorID != owner.ID {
		t.Fatalf("exported createdByUserId = %v, want %d", creatorID, owner.ID)
	}

	encoded, err := json.Marshal(exported.Projects[0].Todos[0])
	if err != nil {
		t.Fatalf("marshal attributed todo: %v", err)
	}
	if !strings.Contains(string(encoded), `"createdByUserId":`+jsonNumber(owner.ID)) {
		t.Fatalf("attributed todo JSON omitted creator: %s", encoded)
	}
}

func TestBackupTodoCreatorNullIsOmitted(t *testing.T) {
	encoded, err := json.Marshal(TodoExport{LocalID: 1, Title: "historical null"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "createdByUserId") {
		t.Fatalf("nil creator must be omitted from backup JSON: %s", encoded)
	}
}

func TestBackupImportDoesNotTrustRawNumericCreatorID(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "backup-creator-import@example.com", "password123", "Target user")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      "untrusted-creator-source",
			Name:      "Untrusted creator source",
			CreatedAt: now,
			UpdatedAt: now,
			Todos: []TodoExport{{
				LocalID:         1,
				Title:           "imported attribution",
				Status:          "BACKLOG",
				Rank:            1000,
				CreatedByUserId: &owner.ID,
				CreatedAt:       now,
				UpdatedAt:       now,
			}},
		}},
	}

	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects: %v", err)
	}
	var importedCreator any
	if err := st.db.QueryRowContext(ctx, `SELECT created_by_user_id FROM todos WHERE title = ?`, "imported attribution").Scan(&importedCreator); err != nil {
		t.Fatalf("read imported creator: %v", err)
	}
	if importedCreator != nil {
		t.Fatalf("raw numeric creator rebound during import: %v", importedCreator)
	}
}

func TestBackupMergePreservesMatchedTodoCreator(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "backup-creator-merge@example.com", "password123", "Original creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	other, err := st.CreateUser(ctx, "backup-creator-unrelated@example.com", "password123", "Unrelated user")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Backup creator merge")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{Title: "before merge"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			Todos: []TodoExport{{
				LocalID:         created.LocalID,
				Title:           "after merge",
				Status:          created.ColumnKey,
				Rank:            created.Rank,
				CreatedByUserId: &other.ID,
				CreatedAt:       now,
				UpdatedAt:       now,
			}},
		}},
	}

	if _, err := st.ImportProjects(ownerCtx, data, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}
	merged, err := st.GetTodoByLocalID(ownerCtx, project.ID, created.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if merged.Title != "after merge" {
		t.Fatalf("merged title = %q", merged.Title)
	}
	assertTodoCreator(t, merged, &owner.ID)
}

func TestResolveImportCreatedByNeverTreatsNumericEqualityAsIdentity(t *testing.T) {
	creatorID := int64(1)
	if got := resolveImportCreatedBy(context.Background(), nil, &creatorID); got != nil {
		t.Fatalf("resolveImportCreatedBy = %v, want nil", got)
	}
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
