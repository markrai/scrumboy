package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// orgSettingEmailNotifyDefault is the org_settings key holding the admin-configured
// default emailNotifications preference new users are seeded with.
const orgSettingEmailNotifyDefault = "emailNotifyDefault"

// GetOrgSetting retrieves an org-wide setting value by key.
// Returns empty string if not found (not an error).
func (s *Store) GetOrgSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM org_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("get org setting: %w", err)
	}
	return value, nil
}

func setOrgSettingTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	nowMs := time.Now().UTC().UnixMilli()
	_, err := tx.ExecContext(ctx, `
INSERT INTO org_settings (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
`, key, value, nowMs)
	if err != nil {
		return fmt.Errorf("set org setting: %w", err)
	}
	return nil
}

// GetEmailNotifyOrgDefault returns the org-wide default email-notification
// preference newly created users are seeded with, falling back to
// DefaultEmailNotifyPref() when no admin override has been configured.
// customized reports whether an admin has actually set an override (as opposed
// to the caller just seeing the hardcoded fallback).
func (s *Store) GetEmailNotifyOrgDefault(ctx context.Context) (pref EmailNotifyPref, customized bool, err error) {
	raw, err := s.GetOrgSetting(ctx, orgSettingEmailNotifyDefault)
	if err != nil {
		return EmailNotifyPref{}, false, err
	}
	pref, err = ParseEmailNotifyPref(raw)
	if err != nil {
		return EmailNotifyPref{}, false, err
	}
	return pref, raw != "", nil
}

// SetEmailNotifyOrgDefault sets the org-wide default email-notification preference
// newly created users are seeded with. Requires admin or owner role. Existing
// users' own preferences are never modified by this call -- the new default only
// takes effect for users created after it's set (see seedEmailNotifyPrefTx).
func (s *Store) SetEmailNotifyOrgDefault(ctx context.Context, requesterID int64, raw string) error {
	if err := s.requireAdmin(ctx, requesterID); err != nil {
		return err
	}
	pref, err := ParseEmailNotifyPref(raw)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(pref)
	if err != nil {
		return fmt.Errorf("marshal email notification org default: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin set org default tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := setOrgSettingTx(ctx, tx, orgSettingEmailNotifyDefault, string(canonical)); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearEmailNotifyOrgDefault removes the org-wide default email-notification
// override, returning GetEmailNotifyOrgDefault to its unconfigured state
// (customized=false, hardcoded fallback). Requires admin or owner role. Existing
// users' own preferences are never modified; subsequently created users get no
// seeded row (the rowless lazy-default path). Deleting a missing override is a
// no-op success.
func (s *Store) ClearEmailNotifyOrgDefault(ctx context.Context, requesterID int64) error {
	if err := s.requireAdmin(ctx, requesterID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM org_settings WHERE key = ?`, orgSettingEmailNotifyDefault); err != nil {
		return fmt.Errorf("clear email notification org default: %w", err)
	}
	return nil
}

// seedEmailNotifyPrefTx seeds a brand-new user's initial emailNotifications
// preference row from the current org-wide default, within the same transaction
// as the user's creation.
//
// Compatibility contract:
//   - No admin override configured -> insert nothing, so the user keeps the exact
//     rowless behavior of a stock instance (lazy hardcoded DefaultEmailNotifyPref
//     via GetEmailNotifyPref). This is what makes an untouched install behave
//     identically to before the org-default feature existed.
//   - Override configured -> upsert the canonical value tagged as org_default.
//     The upsert is conflict-safe so a legacy hand-rolled AFTER INSERT ON users
//     trigger that pre-inserts the row does not collide, and the official value
//     wins over the trigger's.
//   - Corrupt (non-empty, unparseable) override -> skip seeding rather than
//     failing account creation; the user falls back to the lazy hardcoded default.
//     This is deliberately not equivalent to "unset": admin GET still surfaces the
//     corruption as an error.
func seedEmailNotifyPrefTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM org_settings WHERE key = ?`, orgSettingEmailNotifyDefault).Scan(&raw)
	if err == sql.ErrNoRows {
		// No override configured: preserve the rowless fallback behavior.
		return nil
	}
	if err != nil {
		// Must check before treating raw == "" as unset: a failed Scan leaves raw
		// as the empty string, so combining those conditions would swallow real
		// query errors (e.g. missing table) as a successful no-op.
		return fmt.Errorf("get org email notify default: %w", err)
	}
	if raw == "" {
		// Row exists but value is empty: treat like unset (no seed).
		return nil
	}
	pref, err := ParseEmailNotifyPref(raw)
	if err != nil {
		// Corrupt org setting (only reachable via direct DB tampering, since
		// SetEmailNotifyOrgDefault stores canonicalized, validated JSON). Skip
		// seeding so account creation still succeeds; do not invent a fallback row.
		return nil
	}
	canonical, err := json.Marshal(pref)
	if err != nil {
		return fmt.Errorf("marshal seeded email notification preference: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_preferences (user_id, key, value, updated_at, provenance)
VALUES (?, 'emailNotifications', ?, ?, ?)
ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at, provenance = excluded.provenance
`, userID, string(canonical), nowMs, preferenceProvenanceOrgDefault); err != nil {
		return fmt.Errorf("seed email notification preference: %w", err)
	}
	return nil
}
