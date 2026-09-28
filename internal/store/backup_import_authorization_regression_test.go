package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

const (
	backupAuthOriginalTitle = "owner-a-private-marker"
	backupAuthImportedTitle = "user-b-import-marker"
)

type backupImportAuthFixture struct {
	store      *Store
	cleanup    func()
	ownerID    int64
	importerID int64
	projectID  int64
}

func newBackupImportAuthFixture(t *testing.T) *backupImportAuthFixture {
	t.Helper()

	st, cleanup := newTestStore(t)
	owner, err := st.BootstrapUser(t.Context(), "backup-owner-a@example.com", "password", "Owner A")
	if err != nil {
		cleanup()
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(t.Context(), owner.ID)
	project, err := st.CreateProject(ownerCtx, "Private")
	if err != nil {
		cleanup()
		t.Fatalf("CreateProject: %v", err)
	}
	if project.Slug != "private" {
		cleanup()
		t.Fatalf("private project slug=%q want private", project.Slug)
	}
	if _, err := st.CreateTodo(ownerCtx, project.ID, CreateTodoInput{
		Title:     backupAuthOriginalTitle,
		Body:      "distinctive data belonging to User A",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull); err != nil {
		cleanup()
		t.Fatalf("CreateTodo: %v", err)
	}

	importer, err := st.CreateUser(t.Context(), "backup-importer-b@example.com", "password", "User B")
	if err != nil {
		cleanup()
		t.Fatalf("CreateUser: %v", err)
	}
	role, err := st.GetProjectRole(t.Context(), project.ID, importer.ID)
	if err != nil {
		cleanup()
		t.Fatalf("GetProjectRole: %v", err)
	}
	if role != "" {
		cleanup()
		t.Fatalf("User B unexpectedly has role %q", role)
	}

	return &backupImportAuthFixture{
		store:      st,
		cleanup:    cleanup,
		ownerID:    owner.ID,
		importerID: importer.ID,
		projectID:  project.ID,
	}
}

func backupAuthExport(slug, name string) *ExportData {
	stamp := time.Unix(1_700_000_000, 0).UTC()
	return &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: stamp,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      slug,
			Name:      name,
			CreatedAt: stamp,
			UpdatedAt: stamp,
			Todos: []TodoExport{{
				LocalID:   99,
				Title:     backupAuthImportedTitle,
				Body:      "synthetic imported data",
				Status:    "BACKLOG",
				Rank:      99_000,
				CreatedAt: stamp,
				UpdatedAt: stamp,
			}},
		}},
	}
}

func (fx *backupImportAuthFixture) addImporterRole(t *testing.T, role ProjectRole) {
	t.Helper()
	if role == "" {
		return
	}
	if err := fx.store.AddProjectMember(
		WithUserID(t.Context(), fx.ownerID),
		fx.ownerID,
		fx.projectID,
		fx.importerID,
		role,
	); err != nil {
		t.Fatalf("AddProjectMember(%s): %v", role, err)
	}
}

func (fx *backupImportAuthFixture) assertOriginalPrivateIntact(t *testing.T, wantImporterRole ProjectRole) {
	t.Helper()

	var id int64
	var ownerID sql.NullInt64
	if err := fx.store.db.QueryRowContext(t.Context(), `
		SELECT id, owner_user_id FROM projects
		WHERE slug = 'private' AND import_batch_id IS NULL`,
	).Scan(&id, &ownerID); err != nil {
		t.Fatalf("query original private project: %v", err)
	}
	if id != fx.projectID {
		t.Fatalf("private project id=%d want original id=%d", id, fx.projectID)
	}
	if !ownerID.Valid || ownerID.Int64 != fx.ownerID {
		t.Fatalf("private owner=%v want User A id=%d", ownerID, fx.ownerID)
	}

	var originalCount, importedCount int
	if err := fx.store.db.QueryRowContext(t.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE title = ?),
			COUNT(*) FILTER (WHERE title = ?)
		FROM todos WHERE project_id = ?`,
		backupAuthOriginalTitle, backupAuthImportedTitle, fx.projectID,
	).Scan(&originalCount, &importedCount); err != nil {
		t.Fatalf("query original project todos: %v", err)
	}
	if originalCount != 1 || importedCount != 0 {
		t.Fatalf("private todo counts original=%d imported=%d want 1,0", originalCount, importedCount)
	}

	var importerRoles []string
	rows, err := fx.store.db.QueryContext(t.Context(), `
		SELECT role FROM project_members WHERE project_id = ? AND user_id = ? ORDER BY role`,
		fx.projectID, fx.importerID)
	if err != nil {
		t.Fatalf("query User B membership: %v", err)
	}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			t.Fatalf("scan User B membership: %v", err)
		}
		importerRoles = append(importerRoles, role)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close User B membership rows: %v", err)
	}
	wantRoles := []string(nil)
	if wantImporterRole != "" {
		wantRoles = []string{string(wantImporterRole)}
	}
	if fmt.Sprint(importerRoles) != fmt.Sprint(wantRoles) {
		t.Fatalf("User B roles on private=%v want=%v", importerRoles, wantRoles)
	}
}

func quoteBackupSnapshotIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// snapshotBackupImportDB captures every application table plus sqlite_sequence.
// Rejected imports must leave this byte-for-byte logical snapshot unchanged.
func snapshotBackupImportDB(t *testing.T, st *Store) string {
	t.Helper()

	rows, err := st.db.QueryContext(t.Context(), `
		SELECT name FROM sqlite_master
		WHERE type = 'table' AND (name NOT LIKE 'sqlite_%' OR name = 'sqlite_sequence')
		ORDER BY name`)
	if err != nil {
		t.Fatalf("list snapshot tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatalf("scan snapshot table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close snapshot table rows: %v", err)
	}

	var snapshot strings.Builder
	for _, table := range tables {
		pragmaRows, err := st.db.QueryContext(t.Context(), "PRAGMA table_info("+quoteBackupSnapshotIdent(table)+")")
		if err != nil {
			t.Fatalf("table_info(%s): %v", table, err)
		}
		var columns []string
		for pragmaRows.Next() {
			var cid, notNull, pk int
			var name, dataType string
			var defaultValue any
			if err := pragmaRows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
				pragmaRows.Close()
				t.Fatalf("scan table_info(%s): %v", table, err)
			}
			columns = append(columns, name)
		}
		if err := pragmaRows.Close(); err != nil {
			t.Fatalf("close table_info(%s): %v", table, err)
		}

		quotedColumns := make([]string, len(columns))
		for i, column := range columns {
			quotedColumns[i] = quoteBackupSnapshotIdent(column)
		}
		query := "SELECT * FROM " + quoteBackupSnapshotIdent(table)
		if len(quotedColumns) > 0 {
			query += " ORDER BY " + strings.Join(quotedColumns, ", ")
		}
		dataRows, err := st.db.QueryContext(t.Context(), query)
		if err != nil {
			t.Fatalf("snapshot query %s: %v", table, err)
		}
		snapshot.WriteString("TABLE ")
		snapshot.WriteString(table)
		snapshot.WriteByte('\n')
		for dataRows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := dataRows.Scan(dest...); err != nil {
				dataRows.Close()
				t.Fatalf("snapshot scan %s: %v", table, err)
			}
			for i, value := range values {
				if i > 0 {
					snapshot.WriteByte('|')
				}
				switch value := value.(type) {
				case nil:
					snapshot.WriteString("NULL")
				case []byte:
					snapshot.WriteString("bytes:")
					snapshot.WriteString(fmt.Sprintf("%x", value))
				case string:
					snapshot.WriteString("string:")
					snapshot.WriteString(strconv.Quote(value))
				default:
					snapshot.WriteString(fmt.Sprintf("%T:%v", value, value))
				}
			}
			snapshot.WriteByte('\n')
		}
		if err := dataRows.Close(); err != nil {
			t.Fatalf("close snapshot rows %s: %v", table, err)
		}
	}
	return snapshot.String()
}

func TestBackupImportAuthorizationRegression(t *testing.T) {
	roles := []struct {
		name    string
		role    ProjectRole
		allowed bool
	}{
		{name: "no membership"},
		{name: "viewer", role: RoleViewer},
		{name: "contributor", role: RoleContributor},
		{name: "maintainer", role: RoleMaintainer, allowed: true},
	}

	for _, importMode := range []string{"replace", "merge"} {
		for _, roleCase := range roles {
			t.Run(importMode+"/"+roleCase.name, func(t *testing.T) {
				fx := newBackupImportAuthFixture(t)
				defer fx.cleanup()
				fx.addImporterRole(t, roleCase.role)

				before := snapshotBackupImportDB(t, fx.store)
				result, err := fx.store.ImportProjectsWithTarget(
					WithUserID(t.Context(), fx.importerID),
					backupAuthExport("private", "Imported Private"),
					ModeFull,
					importMode,
					"",
				)
				t.Logf("ImportProjectsWithTarget result=%+v err=%v", result, err)

				if !roleCase.allowed {
					if !errors.Is(err, ErrUnauthorized) {
						t.Fatalf("error=%v want ErrUnauthorized", err)
					}
					if result != nil {
						t.Fatalf("result=%+v want nil", result)
					}
					after := snapshotBackupImportDB(t, fx.store)
					if after != before {
						t.Fatalf("rejected %s import mutated database\nBEFORE:\n%s\nAFTER:\n%s", importMode, before, after)
					}
					fx.assertOriginalPrivateIntact(t, roleCase.role)
					return
				}

				if err != nil {
					t.Fatalf("maintainer import: %v", err)
				}
				if result == nil {
					t.Fatal("maintainer import returned nil result")
				}

				var projectID int64
				var ownerID sql.NullInt64
				if err := fx.store.db.QueryRowContext(t.Context(), `
					SELECT id, owner_user_id FROM projects
					WHERE slug = 'private' AND import_batch_id IS NULL`,
				).Scan(&projectID, &ownerID); err != nil {
					t.Fatalf("query imported private project: %v", err)
				}
				if importMode == "replace" {
					if projectID == fx.projectID {
						t.Fatalf("replace retained old project id=%d", projectID)
					}
					if !ownerID.Valid || ownerID.Int64 != fx.importerID {
						t.Fatalf("replacement owner=%v want User B id=%d", ownerID, fx.importerID)
					}
				} else {
					if projectID != fx.projectID {
						t.Fatalf("merge project id=%d want original id=%d", projectID, fx.projectID)
					}
					if !ownerID.Valid || ownerID.Int64 != fx.ownerID {
						t.Fatalf("merged project owner=%v want User A id=%d", ownerID, fx.ownerID)
					}
				}

				var originalCount, importedCount int
				if err := fx.store.db.QueryRowContext(t.Context(), `
					SELECT
						COUNT(*) FILTER (WHERE title = ?),
						COUNT(*) FILTER (WHERE title = ?)
					FROM todos WHERE project_id = ?`,
					backupAuthOriginalTitle, backupAuthImportedTitle, projectID,
				).Scan(&originalCount, &importedCount); err != nil {
					t.Fatalf("query post-import todos: %v", err)
				}
				if importedCount != 1 {
					t.Fatalf("imported todo count=%d want 1", importedCount)
				}
				if importMode == "replace" && originalCount != 0 {
					t.Fatalf("replace retained %d original todos", originalCount)
				}
				if importMode == "merge" && originalCount != 1 {
					t.Fatalf("merge lost original todo; count=%d", originalCount)
				}

				var role string
				if err := fx.store.db.QueryRowContext(t.Context(), `
					SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`,
					projectID, fx.importerID,
				).Scan(&role); err != nil {
					t.Fatalf("query importer membership: %v", err)
				}
				if role != string(RoleMaintainer) {
					t.Fatalf("importer role=%q want maintainer", role)
				}
			})
		}
	}
}

func TestBackupImportAuthorizationSlugBoundaries(t *testing.T) {
	t.Run("case-only slug is isolated as a distinct project", func(t *testing.T) {
		for _, importMode := range []string{"replace", "merge"} {
			t.Run(importMode, func(t *testing.T) {
				fx := newBackupImportAuthFixture(t)
				defer fx.cleanup()
				result, err := fx.store.ImportProjectsWithTarget(
					WithUserID(t.Context(), fx.importerID),
					backupAuthExport("PRIVATE", "Case Import"), ModeFull, importMode, "",
				)
				t.Logf("case-only result=%+v err=%v", result, err)
				if err != nil {
					t.Fatalf("case-only %s import: %v", importMode, err)
				}
				fx.assertOriginalPrivateIntact(t, "")
				wantSlug := "PRIVATE"
				if importMode == "merge" {
					wantSlug = "case-import"
				}
				assertBackupProjectOwnedBy(t, fx, wantSlug, fx.importerID)
			})
		}
	})

	t.Run("surrounding whitespace slug is isolated as a distinct project", func(t *testing.T) {
		for _, importMode := range []string{"replace", "merge"} {
			t.Run(importMode, func(t *testing.T) {
				fx := newBackupImportAuthFixture(t)
				defer fx.cleanup()
				result, err := fx.store.ImportProjectsWithTarget(
					WithUserID(t.Context(), fx.importerID),
					backupAuthExport(" private ", "Whitespace Import"), ModeFull, importMode, "",
				)
				t.Logf("whitespace result=%+v err=%v", result, err)
				if err != nil {
					t.Fatalf("whitespace %s import: %v", importMode, err)
				}
				fx.assertOriginalPrivateIntact(t, "")
				wantSlug := " private "
				if importMode == "merge" {
					wantSlug = "whitespace-import"
				}
				assertBackupProjectOwnedBy(t, fx, wantSlug, fx.importerID)
			})
		}
	})

	t.Run("empty slug generated from non-conflicting name", func(t *testing.T) {
		fx := newBackupImportAuthFixture(t)
		defer fx.cleanup()
		result, err := fx.store.ImportProjectsWithTarget(
			WithUserID(t.Context(), fx.importerID),
			backupAuthExport("", "Fresh Board"), ModeFull, "replace", "",
		)
		t.Logf("empty generated result=%+v err=%v", result, err)
		if err != nil {
			t.Fatalf("replace empty slug: %v", err)
		}
		fx.assertOriginalPrivateIntact(t, "")
		assertBackupProjectOwnedBy(t, fx, "fresh-board", fx.importerID)
	})

	t.Run("merge rejects empty slug without mutation", func(t *testing.T) {
		fx := newBackupImportAuthFixture(t)
		defer fx.cleanup()
		before := snapshotBackupImportDB(t, fx.store)
		result, err := fx.store.ImportProjectsWithTarget(
			WithUserID(t.Context(), fx.importerID),
			backupAuthExport("", "Fresh Board"), ModeFull, "merge", "",
		)
		t.Logf("merge empty result=%+v err=%v", result, err)
		if !errors.Is(err, ErrValidation) || result != nil {
			t.Fatalf("merge empty result=%+v err=%v want validation error", result, err)
		}
		if after := snapshotBackupImportDB(t, fx.store); after != before {
			t.Fatal("merge empty slug mutated database")
		}
		fx.assertOriginalPrivateIntact(t, "")
	})

	t.Run("replace generated slug collision rolls back atomically", func(t *testing.T) {
		fx := newBackupImportAuthFixture(t)
		defer fx.cleanup()
		before := snapshotBackupImportDB(t, fx.store)
		result, err := fx.store.ImportProjectsWithTarget(
			WithUserID(t.Context(), fx.importerID),
			backupAuthExport("", "Private"), ModeFull, "replace", "",
		)
		t.Logf("replace generated collision result=%+v err=%v", result, err)
		if err == nil || result != nil {
			t.Fatalf("generated collision result=%+v err=%v want failure", result, err)
		}
		if after := snapshotBackupImportDB(t, fx.store); after != before {
			t.Fatal("failed replace generated-slug collision mutated database")
		}
		fx.assertOriginalPrivateIntact(t, "")
	})

	t.Run("merge generated slug collision receives suffix", func(t *testing.T) {
		fx := newBackupImportAuthFixture(t)
		defer fx.cleanup()
		result, err := fx.store.ImportProjectsWithTarget(
			WithUserID(t.Context(), fx.importerID),
			backupAuthExport("unmatched-import-slug", "Private"), ModeFull, "merge", "",
		)
		t.Logf("merge generated collision result=%+v err=%v", result, err)
		if err != nil {
			t.Fatalf("merge generated collision: %v", err)
		}
		fx.assertOriginalPrivateIntact(t, "")
		assertBackupProjectOwnedBy(t, fx, "private-2", fx.importerID)
	})

	t.Run("targetSlug in full mode cannot redirect import", func(t *testing.T) {
		for _, importMode := range []string{"replace", "merge"} {
			t.Run(importMode+"/different data slug", func(t *testing.T) {
				fx := newBackupImportAuthFixture(t)
				defer fx.cleanup()
				result, err := fx.store.ImportProjectsWithTarget(
					WithUserID(t.Context(), fx.importerID),
					backupAuthExport("target-import", "Target Import"), ModeFull, importMode, "private",
				)
				t.Logf("full targetSlug result=%+v err=%v", result, err)
				if err != nil {
					t.Fatalf("full targetSlug %s import: %v", importMode, err)
				}
				fx.assertOriginalPrivateIntact(t, "")
				assertBackupProjectOwnedBy(t, fx, "target-import", fx.importerID)
			})

			t.Run(importMode+"/protected data slug", func(t *testing.T) {
				fx := newBackupImportAuthFixture(t)
				defer fx.cleanup()
				before := snapshotBackupImportDB(t, fx.store)
				result, err := fx.store.ImportProjectsWithTarget(
					WithUserID(t.Context(), fx.importerID),
					backupAuthExport("private", "Target Import"), ModeFull, importMode, "anything-else",
				)
				t.Logf("full protected data slug result=%+v err=%v", result, err)
				if !errors.Is(err, ErrUnauthorized) || result != nil {
					t.Fatalf("full protected data slug result=%+v err=%v want unauthorized", result, err)
				}
				if after := snapshotBackupImportDB(t, fx.store); after != before {
					t.Fatal("rejected full-mode targetSlug import mutated database")
				}
				fx.assertOriginalPrivateIntact(t, "")
			})
		}
	})
}

func assertBackupProjectOwnedBy(t *testing.T, fx *backupImportAuthFixture, slug string, ownerID int64) {
	t.Helper()

	var projectID int64
	var gotOwner sql.NullInt64
	if err := fx.store.db.QueryRowContext(t.Context(), `
		SELECT id, owner_user_id FROM projects
		WHERE slug = ? AND import_batch_id IS NULL`, slug,
	).Scan(&projectID, &gotOwner); err != nil {
		t.Fatalf("query project slug %q: %v", slug, err)
	}
	if !gotOwner.Valid || gotOwner.Int64 != ownerID {
		t.Fatalf("project %q owner=%v want=%d", slug, gotOwner, ownerID)
	}
	var roles []string
	rows, err := fx.store.db.QueryContext(t.Context(), `
		SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, ownerID)
	if err != nil {
		t.Fatalf("query project %q memberships: %v", slug, err)
	}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			t.Fatalf("scan project %q membership: %v", slug, err)
		}
		roles = append(roles, role)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close project %q memberships: %v", slug, err)
	}
	sort.Strings(roles)
	if len(roles) != 1 || roles[0] != string(RoleMaintainer) {
		t.Fatalf("project %q owner memberships=%v want [maintainer]", slug, roles)
	}
}
