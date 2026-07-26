package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// orgSettingDefaultBoardProjectID is the org_settings key holding the
// admin-configured default board project ID that new users are auto-enrolled
// into at creation time. Mirrors the emailNotifyDefault org_settings pattern
// from #169/#171 (see internal/store/org_settings.go).
const orgSettingDefaultBoardProjectID = "defaultBoardProjectId"

// GetDefaultBoardOrgSetting returns the org-wide default board project ID new
// users are seeded into, and whether an admin has actually configured one.
// projectID is 0 when unconfigured (customized=false).
func (s *Store) GetDefaultBoardOrgSetting(ctx context.Context) (projectID int64, customized bool, err error) {
	raw, err := s.GetOrgSetting(ctx, orgSettingDefaultBoardProjectID)
	if err != nil {
		return 0, false, err
	}
	if raw == "" {
		return 0, false, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%w: corrupt default board org setting", ErrValidation)
	}
	return id, true, nil
}

// SetDefaultBoardOrgSetting sets the org-wide default board project new users
// are auto-enrolled into (as RoleViewer, the lowest-appropriate project role)
// at creation time. Requires system Admin/Owner and Maintainer membership on
// the selected durable project (expires_at IS NULL). Existence, durability,
// and maintainer checks run in the same transaction as the org_settings write.
// Missing or inaccessible projects return ErrNotFound (404 / no existence leak).
// Temporary and anonymous boards (expires_at set) return ErrValidation.
// Existing users are never retroactively enrolled -- the new default only takes
// effect for users created after it's set (see seedDefaultBoardMembershipTx).
func (s *Store) SetDefaultBoardOrgSetting(ctx context.Context, requesterID, projectID int64) error {
	if err := s.requireAdmin(ctx, requesterID); err != nil {
		return err
	}
	if projectID <= 0 {
		return fmt.Errorf("%w: invalid project id", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin set default board tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var expiresAt sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT expires_at FROM projects WHERE id = ? AND import_batch_id IS NULL
`, projectID).Scan(&expiresAt)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get default board project: %w", err)
	}

	// Membership is checked via project_members directly (not userHasProjectRoleTx),
	// because that helper treats temporary boards as universally accessible.
	role, err := s.getProjectRoleTx(ctx, tx, projectID, requesterID)
	if err != nil {
		return err
	}
	if !role.HasMinimumRole(RoleMaintainer) {
		return ErrNotFound
	}

	if expiresAt.Valid {
		return fmt.Errorf("%w: default board must be a durable project", ErrValidation)
	}

	if err := setOrgSettingTx(ctx, tx, orgSettingDefaultBoardProjectID, strconv.FormatInt(projectID, 10)); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearDefaultBoardOrgSetting removes the org-wide default board override,
// returning GetDefaultBoardOrgSetting to its unconfigured state
// (customized=false, no auto-enrollment). Requires admin or owner role.
// Existing users' memberships are never modified; subsequently created users
// get no seeded project_members row. Deleting a missing override is a no-op
// success.
func (s *Store) ClearDefaultBoardOrgSetting(ctx context.Context, requesterID int64) error {
	if err := s.requireAdmin(ctx, requesterID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM org_settings WHERE key = ?`, orgSettingDefaultBoardProjectID); err != nil {
		return fmt.Errorf("clear default board org setting: %w", err)
	}
	return nil
}

// seedDefaultBoardMembershipTx seeds a brand-new user's project_members row
// for the currently configured default board project, within the same
// transaction as the user's creation.
//
// Compatibility contract:
//   - No admin override configured -> insert nothing, so an untouched instance
//     behaves identically to before this feature existed.
//   - Override configured -> insert a project_members row at RoleViewer (the
//     lowest-appropriate project role). INSERT OR IGNORE so this can never
//     conflict with (and never overrides) an already-existing membership.
//   - Corrupt (non-empty, unparseable) override -> skip seeding rather than
//     failing account creation.
//   - Override points at a project that no longer exists, or is no longer a
//     durable project (expires_at set; e.g. stale setting after a board type
//     change) -> skip seeding rather than failing account creation; the admin
//     GET path still surfaces the current org_settings value as-is since
//     validation only happens on write.
func seedDefaultBoardMembershipTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM org_settings WHERE key = ?`, orgSettingDefaultBoardProjectID).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get default board org setting: %w", err)
	}
	if raw == "" {
		return nil
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		// Corrupt org setting; skip seeding so account creation still succeeds.
		return nil
	}

	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM projects
  WHERE id = ? AND import_batch_id IS NULL AND expires_at IS NULL
)`, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("check default board project exists: %w", err)
	}
	if !exists {
		// Configured project was deleted, or is no longer durable, since the
		// setting was set; skip seeding rather than failing account creation
		// or reviving a stub / temporary row.
		return nil
	}

	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
VALUES (?, ?, ?, ?)
`, projectID, userID, RoleViewer, nowMs); err != nil {
		return fmt.Errorf("seed default board membership: %w", err)
	}
	return nil
}
