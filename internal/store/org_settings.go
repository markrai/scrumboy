package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// orgSettingEmailNotifyDefault is the org_settings key holding the admin-configured
// default emailNotifications preference new users are seeded with (#169 Phase 1).
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

// seedEmailNotifyPrefTx writes the current org-wide default email-notification
// preference as a brand-new user's initial user_preferences row, within the same
// transaction as the user's creation. This is what makes an admin-configured
// default apply only to users created from this point forward: existing users'
// rows (or lack thereof, which falls back to the hardcoded DefaultEmailNotifyPref
// via ParseEmailNotifyPref) are never touched.
func seedEmailNotifyPrefTx(ctx context.Context, tx *sql.Tx, userID int64) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM org_settings WHERE key = ?`, orgSettingEmailNotifyDefault).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("get org email notify default: %w", err)
	}
	// ParseEmailNotifyPref("") already returns DefaultEmailNotifyPref(), covering
	// both sql.ErrNoRows (raw == "") and the never-configured case uniformly.
	pref, err := ParseEmailNotifyPref(raw)
	if err != nil {
		// Unreachable in practice: SetEmailNotifyOrgDefault only ever stores
		// canonicalized, already-validated JSON. Fall back rather than fail
		// user creation over a corrupted org setting.
		pref = DefaultEmailNotifyPref()
	}
	canonical, err := json.Marshal(pref)
	if err != nil {
		return fmt.Errorf("marshal seeded email notification preference: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_preferences (user_id, key, value, updated_at)
VALUES (?, 'emailNotifications', ?, ?)
`, userID, string(canonical), nowMs); err != nil {
		return fmt.Errorf("seed email notification preference: %w", err)
	}
	return nil
}
