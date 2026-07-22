package migrate

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"
)

const recoveryMinimumMigration = "049_add_oidc_identities.sql"

func knownVersions() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	sort.Strings(versions)
	return versions, nil
}

// CheckRecoverySchema performs only read-only compatibility checks. It never
// creates schema_migrations and never applies migrations.
func CheckRecoverySchema(ctx context.Context, db *sql.DB) error {
	known, err := knownVersions()
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	knownSet := make(map[string]bool, len(known))
	for _, version := range known {
		knownSet[version] = true
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("schema is too old or incompatible (schema_migrations unavailable): back up the database and run the normal Scrumboy upgrade first: %w", err)
	}
	defer rows.Close()
	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("read schema version: %w", err)
		}
		if !knownSet[version] {
			return fmt.Errorf("database schema contains migration %q unknown to this binary; use the same or a newer compatible Scrumboy binary", version)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read schema versions: %w", err)
	}
	if !applied[recoveryMinimumMigration] {
		return fmt.Errorf("database schema is too old for owner recovery: back up the database, run the normal Scrumboy upgrade path, stop the service again, and rerun recover-owner")
	}
	required := map[string][]string{
		"users":             {"id", "email", "password_hash", "system_role"},
		"sessions":          {"user_id"},
		"login_2fa_pending": {"user_id"},
	}
	for table, columns := range required {
		present := make(map[string]bool)
		columnRows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
		if err != nil {
			return fmt.Errorf("inspect required table %s: %w", table, err)
		}
		for columnRows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue any
			if err := columnRows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				_ = columnRows.Close()
				return fmt.Errorf("inspect %s columns: %w", table, err)
			}
			present[strings.ToLower(name)] = true
		}
		_ = columnRows.Close()
		for _, column := range columns {
			if !present[column] {
				return fmt.Errorf("database schema is incompatible: required column %s.%s is missing; restore a backup or run the normal Scrumboy upgrade", table, column)
			}
		}
	}
	return nil
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

func Apply(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version    TEXT PRIMARY KEY,
  applied_at INTEGER NOT NULL
);`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	versions, err := knownVersions()
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	applied, err := alreadyApplied(ctx, db)
	if err != nil {
		return err
	}

	for _, v := range versions {
		if applied[v] {
			continue
		}
		if err := applyOne(ctx, db, v); err != nil {
			return err
		}
	}

	return nil
}

func alreadyApplied(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows applied migrations: %w", err)
	}
	return applied, nil
}

func applyOne(ctx context.Context, db *sql.DB, version string) error {
	b, err := migrationsFS.ReadFile("migrations/" + version)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", version, err)
	}

	// IMPORTANT (SQLite): PRAGMA foreign_keys cannot be reliably toggled inside a transaction.
	// For table-swap style migrations we must:
	// - execute PRAGMA foreign_keys=OFF on the *same connection* before BEGIN
	// - run the migration in a transaction
	// - execute PRAGMA foreign_keys=ON on the *same connection* after COMMIT
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get conn for migration %s: %w", version, err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF;`); err != nil {
		return fmt.Errorf("disable foreign_keys for migration %s: %w", version, err)
	}
	// Best-effort re-enable if anything below fails.
	defer func() { _, _ = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`) }()

	tx, err := conn.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migration tx %s: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, string(b)); err != nil {
		return fmt.Errorf("exec migration %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, version, time.Now().UTC().UnixMilli()); err != nil {
		return fmt.Errorf("record migration %s: %w", version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", version, err)
	}

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable foreign_keys for migration %s: %w", version, err)
	}
	return nil
}
