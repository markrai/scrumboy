package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// requiredServiceTriggers maps each protective trigger to the table it must be attached to.
func requiredServiceTriggers() map[string]string {
	required := map[string]string{
		"trg_users_is_service_immutable":                  "users",
		"trg_users_service_reject_inherited_references":   "users",
		"trg_projects_reject_service_owner_insert":        "projects",
		"trg_projects_reject_service_owner_update":        "projects",
		"trg_project_members_reject_service_owner_insert": "project_members",
		"trg_project_members_reject_service_owner_update": "project_members",
	}
	for _, table := range interactiveCredentialTables {
		required["trg_"+table+"_reject_service_insert"] = table
	}
	return required
}

// missingServiceProtections lists every part of migrations 080 and 081's protection absent from a schema.
func missingServiceProtections(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	var missing []string
	for name, table := range requiredServiceTriggers() {
		var attachedTo, body string
		err := sqlDB.QueryRow(`SELECT tbl_name, sql FROM sqlite_master WHERE type = 'trigger' AND name = ?`, name).Scan(&attachedTo, &body)
		switch {
		case err == sql.ErrNoRows:
			missing = append(missing, "trigger "+name)
		case err != nil:
			t.Fatalf("read trigger %s: %v", name, err)
		case attachedTo != table:
			missing = append(missing, fmt.Sprintf("trigger %s is on %s, want %s", name, attachedTo, table))
		case !strings.Contains(body, "RAISE(ABORT, 'service identity:"):
			missing = append(missing, "trigger "+name+" no longer aborts")
		}
	}
	var usersSQL string
	if err := sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'users'`).Scan(&usersSQL); err != nil {
		t.Fatalf("read users schema: %v", err)
	}
	for _, clause := range []string{
		"users_service_identity", "is_service = 1", "system_role = 'user'", "is_bootstrap = 0",
		"password_hash IS NULL", "two_factor_enabled = 0", "two_factor_secret_enc IS NULL", "svc-*@service.invalid",
	} {
		if !strings.Contains(usersSQL, clause) {
			missing = append(missing, "users CHECK clause "+clause)
		}
	}
	sort.Strings(missing)
	return missing
}

// Runs against the end of the whole chain, including migration 081, so a later migration that
// rebuilds a protected table and forgets its triggers, or rebuilds users and loses the CHECK,
// fails here.
func TestMigration080ProtectionsSurviveChain(t *testing.T) {
	sqlDB := openMigratedDB(t)
	if missing := missingServiceProtections(t, sqlDB); len(missing) != 0 {
		t.Fatalf("the migration chain lost service-account protection:\n  %s", strings.Join(missing, "\n  "))
	}
	if n := len(requiredServiceTriggers()); n != 16 {
		t.Fatalf("required trigger list has %d entries, want 16", n)
	}
	// Names can survive while behaviour does not, so exercise the protections on the final schema.
	insertServiceRow(t, sqlDB, 1, validServiceEmail)
	if _, err := sqlDB.Exec(`INSERT INTO projects(id, name, slug, estimation_mode, last_activity_at, created_at, updated_at) VALUES (1, 'host', 'svc-host', 'MODIFIED_FIBONACCI', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	const checkFailure, triggerFailure = "users_service_identity", "service identity:"
	for name, tc := range map[string]struct{ stmt, want string }{
		"role":       {`UPDATE users SET system_role = 'owner' WHERE id = 1`, checkFailure},
		"password":   {`UPDATE users SET password_hash = 'x' WHERE id = 1`, checkFailure},
		"type":       {`UPDATE users SET is_service = 0 WHERE id = 1`, triggerFailure},
		"session":    {`INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (1, 'h', 1, 2, 1)`, triggerFailure},
		"project":    {`INSERT INTO projects(name, slug, estimation_mode, owner_user_id, last_activity_at, created_at, updated_at) VALUES ('p', 'svc-owned', 'MODIFIED_FIBONACCI', 1, 1, 1, 1)`, triggerFailure},
		"temp board": {`INSERT INTO projects(name, slug, estimation_mode, creator_user_id, last_activity_at, expires_at, created_at, updated_at) VALUES ('p', 'svc-temp', 'MODIFIED_FIBONACCI', 1, 1, 2, 1, 1)`, triggerFailure},
		"owner role": {`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 1, 'owner', 1)`, triggerFailure},
	} {
		// Name the expected failure: an unrelated constraint rejecting the row would prove nothing.
		if _, err := sqlDB.Exec(tc.stmt); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want a failure from %q", name, err, tc.want)
		}
	}
}

// The detector above has to notice the failure it exists for. Rebuilding a credential table the way
// this repository's migrations do drops that table's triggers without any error.
func TestMissingServiceProtectionsDetectsTableRebuild(t *testing.T) {
	sqlDB := openMigratedDB(t)
	for _, stmt := range []string{
		`CREATE TABLE sessions_rebuilt AS SELECT * FROM sessions`,
		`DROP TABLE first_password_grants`,
		`DROP TABLE sessions`,
		`ALTER TABLE sessions_rebuilt RENAME TO sessions`,
		`CREATE TABLE project_members_rebuilt AS SELECT * FROM project_members`,
		`DROP TABLE project_members`,
		`ALTER TABLE project_members_rebuilt RENAME TO project_members`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	missing := strings.Join(missingServiceProtections(t, sqlDB), "\n")
	for _, want := range []string{
		"trg_sessions_reject_service_insert",
		"trg_first_password_grants_reject_service_insert",
		"trg_project_members_reject_service_owner_insert",
		"trg_project_members_reject_service_owner_update",
	} {
		if !strings.Contains(missing, want) {
			t.Errorf("detector did not report %s; it reported:\n%s", want, missing)
		}
	}
}

// Rebuilding users cannot lose the protection silently: its credential triggers refer to the table,
// so the standard swap either fails outright or the CHECK is reported missing.
func TestUsersRebuildCannotSilentlyDropProtection(t *testing.T) {
	sqlDB := openMigratedDB(t)
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	var failed error
	for _, stmt := range []string{
		`CREATE TABLE users_new (id INTEGER PRIMARY KEY, email TEXT NOT NULL, name TEXT, password_hash TEXT, is_bootstrap BOOLEAN NOT NULL DEFAULT FALSE, system_role TEXT NOT NULL DEFAULT 'user', two_factor_enabled INTEGER NOT NULL DEFAULT 0, two_factor_secret_enc TEXT, created_at INTEGER NOT NULL, is_service BOOLEAN NOT NULL DEFAULT FALSE)`,
		`DROP TABLE users`,
		`ALTER TABLE users_new RENAME TO users`,
	} {
		if _, err := sqlDB.Exec(stmt); err != nil {
			failed = err
			break
		}
	}
	if failed != nil {
		return // The migration would abort, which is loud enough.
	}
	if missing := missingServiceProtections(t, sqlDB); len(missing) == 0 {
		t.Fatal("rebuilding users without the CHECK went unnoticed")
	}
}

// userReferenceClasses records why each column that refers to a user does or does not need protection.
// A new table or column referring to users fails TestEveryUserReferenceIsClassified until someone has
// decided which it is, so a new credential cannot quietly bypass migration 080.
var userReferenceClasses = map[string]string{
	// Interactive credentials: must reject service accounts (see interactiveCredentialTables).
	"sessions.user_id":                   "credential",
	"user_oidc_identities.user_id":       "credential",
	"login_2fa_pending.user_id":          "credential",
	"two_factor_enrollments.user_id":     "credential",
	"user_recovery_codes.user_id":        "credential",
	"first_password_grants.user_id":      "credential",
	"oauth_auth_codes.user_id":           "credential",
	"oauth_access_tokens.user_id":        "credential",
	"oauth_refresh_tokens.user_id":       "credential",
	"mobile_oidc_handoff_grants.user_id": "credential",

	// Project authority: ownership is guarded by the project triggers, and project_members holds
	// the explicit grants that AddProjectMember authorizes.
	"projects.owner_user_id":   "project",
	"projects.creator_user_id": "project",
	"project_members.user_id":  "project",

	// The machine-authentication channel, deliberately open to service accounts.
	"api_tokens.user_id": "machine",
}

// nonAuthorityUserReferences hold data about a user, but no credential and no route into a project.
var nonAuthorityUserReferences = map[string]bool{
	// History and provenance: never read to authorize anything.
	"archived_service_api_tokens.archived_by_user_id": true,
	"archived_service_api_tokens.created_by_user_id":  true,
	"archived_service_api_tokens.origin_user_id":      true,
	"api_tokens.created_by_user_id":                   true,
	"audit_events.actor_user_id":                      true,
	"instance_audit_events.actor_user_id":             true,
	"instance_audit_events.target_user_id":            true,
	"project_assets.uploader_user_id":                 true,
	"todo_assignee_events.actor_user_id":              true,
	"todo_assignee_events.from_assignee_user_id":      true,
	"todo_assignee_events.to_assignee_user_id":        true,
	"todos.created_by_user_id":                        true,

	// Only narrows what a project member may do: GetTodoEditScope also needs a project role.
	"todos.assignee_user_id": true,

	// The user's own settings and labels.
	"tags.user_id":             true,
	"user_preferences.user_id": true,
	"user_tag_colors.user_id":  true,

	// Reachable only from a browser session, which a service account cannot hold.
	"push_subscriptions.user_id": true,

	// Delivery re-checks Maintainer membership on the project. Registering one is session-only for
	// now; webhook management over MCP must decide how it applies to service accounts.
	"webhooks.user_id": true,
}

// allUserReferences lists the columns worth classifying: foreign keys to users, plus columns named
// like a user reference that have no foreign key, such as projects.creator_user_id.
func allUserReferences(t *testing.T, sqlDB *sql.DB) []string {
	t.Helper()
	rows, err := sqlDB.Query(`
SELECT m.name, f."from"
FROM sqlite_master m, pragma_foreign_key_list(m.name) f
WHERE m.type = 'table' AND f."table" = 'users'
UNION
SELECT m.name, c.name
FROM sqlite_master m, pragma_table_info(m.name) c
WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND (c.name = 'user_id' OR c.name LIKE '%\_user\_id' ESCAPE '\')
ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var refs []string
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, table+"."+column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return refs
}

func TestEveryUserReferenceIsClassified(t *testing.T) {
	sqlDB := openMigratedDB(t)
	present := map[string]bool{}
	var unclassified []string
	for _, ref := range allUserReferences(t, sqlDB) {
		present[ref] = true
		if _, ok := userReferenceClasses[ref]; !ok && !nonAuthorityUserReferences[ref] {
			unclassified = append(unclassified, ref)
		}
	}
	if len(unclassified) != 0 {
		t.Fatalf("new references to users need a decision. If one holds an interactive credential or grants project access it needs migration 080's triggers; then add it to userReferenceClasses or nonAuthorityUserReferences:\n  %s", strings.Join(unclassified, "\n  "))
	}
	for ref := range userReferenceClasses {
		if !present[ref] {
			t.Errorf("classified reference %s no longer exists", ref)
		}
	}
	// Every credential class has its triggers, and the table list used by the other tests agrees.
	credentialTables := map[string]bool{}
	for _, table := range interactiveCredentialTables {
		credentialTables[table] = true
		if userReferenceClasses[table+".user_id"] != "credential" {
			t.Errorf("%s is in interactiveCredentialTables but not classified as a credential", table)
		}
	}
	for ref, class := range userReferenceClasses {
		if table := strings.TrimSuffix(ref, ".user_id"); class == "credential" && !credentialTables[table] {
			t.Errorf("%s is classified as a credential but has no required triggers", ref)
		}
	}
}

// A database at any point before 080 keeps its projects and its human owners through the migration.
func TestMigration080PreservesExistingProjectOwnership(t *testing.T) {
	ctx := context.Background()
	sqlDB := openRawTestDB(t)
	applyMigrationsBefore(t, sqlDB, usersIsServiceMigration)
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO users(id, email, name, created_at, system_role) VALUES (1, 'a@example.com', 'a', 1, 'owner'), (2, 'b@example.com', 'b', 1, 'user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
INSERT INTO projects(id, name, slug, estimation_mode, owner_user_id, creator_user_id, last_activity_at, expires_at, created_at, updated_at) VALUES
  (1, 'Durable', 'durable', 'MODIFIED_FIBONACCI', 1, NULL, 1, NULL, 1, 1),
  (2, 'Temp',    'temp',    'MODIFIED_FIBONACCI', NULL, 1,  1, 9999999999999, 1, 1),
  (3, 'Anon',    'anon',    'MODIFIED_FIBONACCI', NULL, NULL, 1, 9999999999999, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := applyOne(ctx, sqlDB, usersIsServiceMigration); err != nil {
		t.Fatalf("apply 080: %v", err)
	}
	for _, stmt := range []string{
		`UPDATE projects SET owner_user_id = 2 WHERE id = 1`,
		`UPDATE projects SET owner_user_id = 1, expires_at = NULL WHERE id = 2`,
		`INSERT INTO project_members(project_id, user_id, role, created_at) VALUES (1, 2, 'owner', 1)`,
		`UPDATE projects SET last_activity_at = 2 WHERE id = 3`,
	} {
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			t.Errorf("%s: %v", stmt, err)
		}
	}
	// The reference check applies only to service accounts.
	insert := `INSERT INTO users(id, email, name, created_at, system_role, is_service) VALUES (50, 'svc-r@service.invalid', 'r', 1, 'user', 1)`
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO projects(id, name, slug, estimation_mode, creator_user_id, last_activity_at, expires_at, created_at, updated_at) VALUES (4, 'Orphan', 'orphan', 'MODIFIED_FIBONACCI', 50, 1, 9999999999999, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, insert); err == nil || !strings.Contains(err.Error(), "service identity:") {
		t.Fatalf("service account inheriting a board: err = %v, want trigger rejection", err)
	}
	if n := 0; sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = 50`).Scan(&n) != nil || n != 0 {
		t.Fatalf("the rejected row was stored (%d)", n)
	}
}
