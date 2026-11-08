package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

const serviceOwnerRoleMigration = "081_restrict_service_project_owner_role.sql"

// A database that applied the original migration 080 gains the owner-role
// protections through ordinary forward migration to 081. A valid human owner
// membership recorded before the upgrade stays in place.
func TestMigration081UpgradesOriginal080Schema(t *testing.T) {
	ctx := context.Background()
	const serviceID, humanID, projectID = 1, 2, 1
	const rejection = "service identity: project ownership rejected"

	upgradeFrom080 := func(t *testing.T, precreateTriggers bool) *sql.DB {
		t.Helper()
		sqlDB := openRawTestDB(t)
		applyMigrationsBefore(t, sqlDB, usersIsServiceMigration)
		if err := applyOne(ctx, sqlDB, usersIsServiceMigration); err != nil {
			t.Fatalf("apply 080: %v", err)
		}
		insertServiceRow(t, sqlDB, serviceID, validServiceEmail)
		if _, err := sqlDB.Exec(fmt.Sprintf(`INSERT INTO users(id, email, name, created_at, system_role) VALUES (%d, 'owner@example.com', 'o', 1, 'user')`, humanID)); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(fmt.Sprintf(`INSERT INTO projects(id, name, slug, estimation_mode, last_activity_at, created_at, updated_at) VALUES (%d, 'p', 'p', 'MODIFIED_FIBONACCI', 1, 1, 1)`, projectID)); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 2, 'owner', 1)`); err != nil {
			t.Fatalf("human owner membership before 081: %v", err)
		}
		if precreateTriggers {
			for _, stmt := range []string{
				`CREATE TRIGGER trg_project_members_reject_service_owner_insert
BEFORE INSERT ON project_members
WHEN NEW.role = 'owner' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END`,
				`CREATE TRIGGER trg_project_members_reject_service_owner_update
BEFORE UPDATE OF role, user_id ON project_members
WHEN NEW.role = 'owner' AND EXISTS (SELECT 1 FROM users WHERE id = NEW.user_id AND is_service <> 0)
BEGIN
  SELECT RAISE(ABORT, 'service identity: project ownership rejected');
END`,
			} {
				if _, err := sqlDB.Exec(stmt); err != nil {
					t.Fatalf("precreate trigger: %v", err)
				}
			}
		}
		if err := applyOne(ctx, sqlDB, serviceOwnerRoleMigration); err != nil {
			t.Fatalf("apply 081: %v", err)
		}
		return sqlDB
	}

	for _, precreated := range []bool{false, true} {
		name := "without pre-existing triggers"
		if precreated {
			name = "with pre-existing triggers"
		}
		t.Run(name, func(t *testing.T) {
			sqlDB := upgradeFrom080(t, precreated)
			if missing := missingServiceProtections(t, sqlDB); len(missing) != 0 {
				t.Fatalf("upgraded schema lacks protection:\n  %s", strings.Join(missing, "\n  "))
			}
			var n int
			if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM project_members WHERE user_id = ? AND role = 'owner'`, humanID).Scan(&n); err != nil || n != 1 {
				t.Fatalf("human owner rows = %d (err=%v), want 1", n, err)
			}
			for _, tc := range []struct {
				name string
				seed string
				stmt string
			}{
				{"insert", "", `INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 1, 'owner', 1)`},
				{"update", "viewer", `UPDATE project_members SET role = 'owner' WHERE user_id = 1`},
				{"upsert", "viewer", `INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 1, 'viewer', 1) ON CONFLICT(project_id, user_id) DO UPDATE SET role = 'owner'`},
			} {
				if _, err := sqlDB.Exec(`DELETE FROM project_members WHERE user_id = ?`, serviceID); err != nil {
					t.Fatal(err)
				}
				if tc.seed != "" {
					if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, ?, ?, 1)`, serviceID, tc.seed); err != nil {
						t.Fatalf("%s seed: %v", tc.name, err)
					}
				}
				if _, err := sqlDB.Exec(tc.stmt); err == nil || !strings.Contains(err.Error(), rejection) {
					t.Fatalf("%s: err = %v, want %q", tc.name, err, rejection)
				}
			}
			if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 2, 'maintainer', 1) ON CONFLICT(project_id, user_id) DO UPDATE SET role = 'owner'`); err != nil {
				t.Fatalf("human owner upsert after 081: %v", err)
			}
			versions := appliedMigrationVersions(t, sqlDB)
			if len(versions) < 2 || versions[len(versions)-2] != usersIsServiceMigration || versions[len(versions)-1] != serviceOwnerRoleMigration {
				t.Fatalf("migration history tail = %v, want [..., %q, %q]", versions, usersIsServiceMigration, serviceOwnerRoleMigration)
			}
		})
	}

	t.Run("fresh and upgraded schemas agree", func(t *testing.T) {
		upgraded := upgradeFrom080(t, false)
		fresh := openMigratedDB(t)
		for _, name := range []string{
			"trg_project_members_reject_service_owner_insert",
			"trg_project_members_reject_service_owner_update",
		} {
			var want, got string
			if err := fresh.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&want); err != nil {
				t.Fatalf("fresh schema trigger %s: %v", name, err)
			}
			if err := upgraded.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&got); err != nil {
				t.Fatalf("upgraded schema trigger %s: %v", name, err)
			}
			if got != want {
				t.Fatalf("trigger %s differs:\n got: %s\nwant: %s", name, got, want)
			}
		}
	})
}

func TestProjectMemberOwnerRole_RejectedOnlyForServiceAccounts(t *testing.T) {
	const serviceID, humanID, otherHumanID, projectID = 1, 2, 3, 1
	const rejection = "service identity: project ownership rejected"
	statements := map[string]struct {
		seedRole string
		seedUser string
		stmt     string
	}{
		"insert":           {stmt: `INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, %[1]d, 'owner', 1)`},
		"insert or ignore": {stmt: `INSERT OR IGNORE INTO project_members(project_id, user_id, role, created_at) VALUES (1, %[1]d, 'owner', 1)`},
		"insert or replace": {
			seedRole: "viewer",
			stmt:     `INSERT OR REPLACE INTO project_members(project_id, user_id, role, created_at) VALUES (1, %[1]d, 'owner', 1)`,
		},
		"upsert": {
			seedRole: "viewer",
			stmt:     `INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, %[1]d, 'viewer', 1) ON CONFLICT(project_id, user_id) DO UPDATE SET role = 'owner'`,
		},
		"update": {
			seedRole: "viewer",
			stmt:     `UPDATE project_members SET role = 'owner' WHERE user_id = %[1]d`,
		},
		"update or ignore": {
			seedRole: "viewer",
			stmt:     `UPDATE OR IGNORE project_members SET role = 'owner' WHERE user_id = %[1]d`,
		},
		"re-parent an owner row": {
			seedRole: "owner",
			seedUser: "other",
			stmt:     `UPDATE project_members SET user_id = %[1]d WHERE user_id = 3`,
		},
	}
	sqlDB := openMigratedDB(t)
	insertServiceRow(t, sqlDB, serviceID, validServiceEmail)
	for _, stmt := range []string{
		fmt.Sprintf(`INSERT INTO users(id, email, name, created_at, system_role) VALUES (%d, 'a@example.com', 'a', 1, 'user'), (%d, 'b@example.com', 'b', 1, 'user')`, humanID, otherHumanID),
		fmt.Sprintf(`INSERT INTO projects(id, name, slug, estimation_mode, last_activity_at, created_at, updated_at) VALUES (%d, 'p', 'p', 'MODIFIED_FIBONACCI', 1, 1, 1)`, projectID),
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	for name, tc := range statements {
		for _, account := range []struct {
			label   string
			id      int
			refused bool
		}{{"service", serviceID, true}, {"human", humanID, false}} {
			t.Run(name+"/"+account.label, func(t *testing.T) {
				if _, err := sqlDB.Exec(`DELETE FROM project_members`); err != nil {
					t.Fatal(err)
				}
				if tc.seedRole != "" {
					seedUser := account.id
					if tc.seedUser == "other" {
						seedUser = otherHumanID
					}
					if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, ?, ?, 1)`, seedUser, tc.seedRole); err != nil {
						t.Fatalf("seed: %v", err)
					}
				}

				_, err := sqlDB.Exec(fmt.Sprintf(tc.stmt, account.id))

				if account.refused {
					if err == nil || !strings.Contains(err.Error(), rejection) {
						t.Fatalf("err = %v, want %q", err, rejection)
					}
					var n int
					if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM project_members WHERE user_id = ? AND role = 'owner'`, account.id).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != 0 {
						t.Fatal("the rejected write left an owner row")
					}
					return
				}
				if err != nil {
					t.Fatalf("the same write for a human: %v", err)
				}
				var n int
				if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM project_members WHERE user_id = ? AND role = 'owner'`, account.id).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("%d owner rows for the human, want 1", n)
				}
			})
		}
	}

	t.Run("other roles stay available to a service account", func(t *testing.T) {
		for _, role := range []string{"maintainer", "contributor", "viewer"} {
			if _, err := sqlDB.Exec(`DELETE FROM project_members`); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, ?, ?, 1)`, serviceID, role); err != nil {
				t.Errorf("role %s: %v", role, err)
			}
		}
	})
	t.Run("no other spelling reaches the owner role", func(t *testing.T) {
		if _, err := sqlDB.Exec(`DELETE FROM project_members`); err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"OWNER", "Owner", " owner", "owner "} {
			if _, err := sqlDB.Exec(`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, ?, ?, 1)`, serviceID, role); err == nil {
				t.Errorf("role %q was accepted", role)
			}
		}
	})
}
