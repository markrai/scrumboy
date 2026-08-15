package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// preferenceRow reads the raw user_preferences row for (userID, key). Unlike
// GetUserPreference, which collapses "no row" and "row with empty value" both
// into "", this reports exists=false only when the row is genuinely absent, so
// tests can prove the compatibility guarantee that no row was created.
func preferenceRow(t *testing.T, st *Store, userID int64, key string) (value, provenance string, updatedAt int64, exists bool) {
	t.Helper()
	err := st.db.QueryRowContext(context.Background(),
		`SELECT value, provenance, updated_at FROM user_preferences WHERE user_id = ? AND key = ?`,
		userID, key).Scan(&value, &provenance, &updatedAt)
	if err == sql.ErrNoRows {
		return "", "", 0, false
	}
	if err != nil {
		t.Fatalf("preferenceRow(%d, %q): %v", userID, key, err)
	}
	return value, provenance, updatedAt, true
}

// TestCreateUser_UnsetOrgDefault_CreatesNoRow is the core compatibility guarantee:
// on an instance that never configures the org default, a newly created user gets
// no emailNotifications row at all (identical to pre-feature behavior), and the
// getter still reports "" via its intentional missing->"" collapse.
func TestCreateUser_UnsetOrgDefault_CreatesNoRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	user, err := st.CreateUser(ctx, "plain@test.com", "password123", "Plain")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, _, _, exists := preferenceRow(t, st, user.ID, "emailNotifications"); exists {
		t.Fatalf("expected no emailNotifications row when org default is unset")
	}
	raw, err := st.GetUserPreference(ctx, user.ID, "emailNotifications")
	if err != nil {
		t.Fatalf("GetUserPreference: %v", err)
	}
	if raw != "" {
		t.Fatalf("expected GetUserPreference to report %q, got %q", "", raw)
	}
}

// TestCreateUser_OrgDefaultSet_SeedsOrgDefaultProvenance verifies a configured
// override produces a real row tagged org_default.
func TestCreateUser_OrgDefaultSet_SeedsOrgDefaultProvenance(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"projectActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}

	user, err := st.CreateUser(ctx, "new@test.com", "password123", "New")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	_, provenance, _, exists := preferenceRow(t, st, user.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected a seeded row when org default is set")
	}
	if provenance != preferenceProvenanceOrgDefault {
		t.Fatalf("expected provenance %q, got %q", preferenceProvenanceOrgDefault, provenance)
	}
}

// TestSetUserPreference_FlipsInheritedRowToUser verifies an explicit user save
// re-tags an inherited org_default row as user-owned, even when the saved value
// is identical to what they inherited.
func TestSetUserPreference_FlipsInheritedRowToUser(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	orgDefault := `{"enabled":true,"projectActivity":true}`
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, orgDefault); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}
	user, err := st.CreateUser(ctx, "new@test.com", "password123", "New")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	seeded, _, _, exists := preferenceRow(t, st, user.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected seeded row")
	}
	// Save the exact same canonical value the user inherited.
	if err := st.SetUserPreference(ctx, user.ID, "emailNotifications", seeded); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	_, provenance, _, exists := preferenceRow(t, st, user.ID, "emailNotifications")
	if !exists {
		t.Fatalf("row unexpectedly gone after user save")
	}
	if provenance != preferenceProvenanceUser {
		t.Fatalf("expected provenance %q after user save, got %q", preferenceProvenanceUser, provenance)
	}
}

// TestClearEmailNotifyOrgDefault_ResetsAndLeavesUsersUntouched verifies the reset
// lifecycle: after clearing, GetEmailNotifyOrgDefault reports unconfigured,
// subsequently created users get no row, and previously seeded rows are unchanged.
func TestClearEmailNotifyOrgDefault_ResetsAndLeavesUsersUntouched(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"projectActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}
	seededUser, err := st.CreateUser(ctx, "seeded@test.com", "password123", "Seeded")
	if err != nil {
		t.Fatalf("CreateUser(seeded): %v", err)
	}
	seededValue, seededProv, seededUpdated, exists := preferenceRow(t, st, seededUser.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected seeded row before reset")
	}

	// Non-admin cannot clear.
	if err := st.ClearEmailNotifyOrgDefault(ctx, seededUser.ID); err == nil {
		t.Fatalf("expected error clearing org default as non-admin")
	}
	if err := st.ClearEmailNotifyOrgDefault(ctx, owner.ID); err != nil {
		t.Fatalf("ClearEmailNotifyOrgDefault: %v", err)
	}
	// Idempotent.
	if err := st.ClearEmailNotifyOrgDefault(ctx, owner.ID); err != nil {
		t.Fatalf("ClearEmailNotifyOrgDefault (second call): %v", err)
	}

	_, customized, err := st.GetEmailNotifyOrgDefault(ctx)
	if err != nil {
		t.Fatalf("GetEmailNotifyOrgDefault: %v", err)
	}
	if customized {
		t.Fatalf("expected customized=false after reset")
	}

	// A user created after reset gets no row.
	afterUser, err := st.CreateUser(ctx, "after@test.com", "password123", "After")
	if err != nil {
		t.Fatalf("CreateUser(after): %v", err)
	}
	if _, _, _, exists := preferenceRow(t, st, afterUser.ID, "emailNotifications"); exists {
		t.Fatalf("expected no row for user created after reset")
	}

	// The previously seeded user's row is completely unchanged.
	value, prov, updated, exists := preferenceRow(t, st, seededUser.ID, "emailNotifications")
	if !exists {
		t.Fatalf("seeded user's row disappeared after reset")
	}
	if value != seededValue || prov != seededProv || updated != seededUpdated {
		t.Fatalf("seeded row changed by reset: before {%q,%q,%d} after {%q,%q,%d}",
			seededValue, seededProv, seededUpdated, value, prov, updated)
	}
}

// TestBootstrapUser_CreatesNoRow proves the bootstrap owner is never seeded, even
// when checked at the raw-row level.
func TestBootstrapUser_CreatesNoRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if _, _, _, exists := preferenceRow(t, st, owner.ID, "emailNotifications"); exists {
		t.Fatalf("expected no emailNotifications row for bootstrap owner")
	}
}

// TestCreateUserOIDC_UnsetOrgDefault_CreatesNoRow mirrors the unset compatibility
// guarantee for the non-bootstrap OIDC creation path.
func TestCreateUserOIDC_UnsetOrgDefault_CreatesNoRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	st.configuredOIDCIssuer = "https://idp.example"

	if _, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	oidcUser, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub-1", "sso@test.com", "SSO User")
	if err != nil {
		t.Fatalf("CreateUserOIDC: %v", err)
	}
	if oidcUser.IsBootstrap {
		t.Fatalf("expected non-bootstrap OIDC user")
	}
	if _, _, _, exists := preferenceRow(t, st, oidcUser.ID, "emailNotifications"); exists {
		t.Fatalf("expected no row for OIDC user when org default is unset")
	}
}

// TestCreateUser_CorruptOrgDefault_SkipsSeedingWithoutFailing proves a corrupt
// org_settings value (only reachable via direct DB tampering) does not block
// account creation and does not invent a fallback row.
func TestCreateUser_CorruptOrgDefault_SkipsSeedingWithoutFailing(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	// Tamper: write invalid JSON directly, bypassing SetEmailNotifyOrgDefault validation.
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO org_settings (key, value, updated_at) VALUES (?, ?, ?)`,
		orgSettingEmailNotifyDefault, `{not json`, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatalf("insert corrupt org setting: %v", err)
	}

	user, err := st.CreateUser(ctx, "victim@test.com", "password123", "Victim")
	if err != nil {
		t.Fatalf("CreateUser should succeed despite corrupt org default: %v", err)
	}
	if _, _, _, exists := preferenceRow(t, st, user.ID, "emailNotifications"); exists {
		t.Fatalf("expected no seeded row when org default is corrupt")
	}
}

// installExplicitColumnPrefTrigger installs a representative legacy workaround:
// an AFTER INSERT ON users trigger that pre-inserts an emailNotifications row
// using an explicit column list. Explicit-column triggers survive the addition of
// the provenance column (it takes its default); positional
// `INSERT INTO user_preferences VALUES (...)` triggers do NOT and must be removed
// before upgrading -- that case is intentionally out of scope here.
func installExplicitColumnPrefTrigger(t *testing.T, st *Store, value string) {
	t.Helper()
	if _, err := st.db.ExecContext(context.Background(), `
CREATE TRIGGER test_seed_email_notify AFTER INSERT ON users
BEGIN
  INSERT INTO user_preferences (user_id, key, value, updated_at)
  VALUES (NEW.id, 'emailNotifications', '`+value+`', 0);
END;`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
}

// TestCreateUser_ExplicitColumnTrigger_NoOverrideSucceeds proves that with a
// legacy explicit-column trigger present and no org override, user creation still
// succeeds (the seed inserts nothing, so there is no uniqueness collision).
func TestCreateUser_ExplicitColumnTrigger_NoOverrideSucceeds(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	triggerValue := `{"v":1,"enabled":false,"assigned":false,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":false}`
	installExplicitColumnPrefTrigger(t, st, triggerValue)

	user, err := st.CreateUser(ctx, "trig@test.com", "password123", "Trig")
	if err != nil {
		t.Fatalf("CreateUser with legacy trigger should succeed: %v", err)
	}
	value, provenance, _, exists := preferenceRow(t, st, user.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected the trigger's row to be present")
	}
	if value != triggerValue {
		t.Fatalf("expected trigger value untouched (no override), got %q", value)
	}
	if provenance != preferenceProvenanceLegacy {
		t.Fatalf("expected trigger row provenance %q, got %q", preferenceProvenanceLegacy, provenance)
	}
}

// TestCreateUser_ExplicitColumnTrigger_OverrideWins proves that when an org
// override is configured, the seed upsert overwrites the trigger-created row with
// the official value and tags it org_default.
func TestCreateUser_ExplicitColumnTrigger_OverrideWins(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"projectActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}
	triggerValue := `{"v":1,"enabled":false,"assigned":false,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":false}`
	installExplicitColumnPrefTrigger(t, st, triggerValue)

	user, err := st.CreateUser(ctx, "trig@test.com", "password123", "Trig")
	if err != nil {
		t.Fatalf("CreateUser with legacy trigger + override should succeed: %v", err)
	}
	value, provenance, _, exists := preferenceRow(t, st, user.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected a row after creation")
	}
	want := `{"v":1,"enabled":true,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":true,"addedToProject":true}`
	if value != want {
		t.Fatalf("expected org override to win over trigger value: got %q, want %q", value, want)
	}
	if provenance != preferenceProvenanceOrgDefault {
		t.Fatalf("expected provenance %q, got %q", preferenceProvenanceOrgDefault, provenance)
	}
}

// TestCreateUser_OrgDefaultQueryErrorIsReturned proves that a real org_settings
// query failure (anything other than ErrNoRows) fails CreateUser instead of being
// swallowed as a successful "unset" no-op. The previous err==ErrNoRows||raw==""
// check was unsafe because a failed Scan leaves raw as "".
func TestCreateUser_OrgDefaultQueryErrorIsReturned(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if _, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DROP TABLE org_settings`); err != nil {
		t.Fatalf("DROP TABLE org_settings: %v", err)
	}

	_, err := st.CreateUser(ctx, "victim@test.com", "password123", "Victim")
	if err == nil {
		t.Fatal("expected CreateUser to fail when org_settings query errors, got nil")
	}
}

// TestOrgDefaultChange_LeavesUserAndLegacyRowsUntouched proves a later org-default
// change never rewrites existing preference rows tagged user or legacy.
func TestOrgDefaultChange_LeavesUserAndLegacyRowsUntouched(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"projectActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}

	// Inherited org_default row, then explicitly saved by the user (same JSON).
	userOwned, err := st.CreateUser(ctx, "userowned@test.com", "password123", "UserOwned")
	if err != nil {
		t.Fatalf("CreateUser(userOwned): %v", err)
	}
	seeded, _, _, exists := preferenceRow(t, st, userOwned.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected seeded row for userOwned")
	}
	if err := st.SetUserPreference(ctx, userOwned.ID, "emailNotifications", seeded); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	userValue, userProv, userUpdated, exists := preferenceRow(t, st, userOwned.ID, "emailNotifications")
	if !exists || userProv != preferenceProvenanceUser {
		t.Fatalf("expected user provenance before org change, exists=%v provenance=%q", exists, userProv)
	}

	// Pre-existing unknown/hand-written row classified as legacy.
	legacyUser, err := st.CreateUser(ctx, "legacy@test.com", "password123", "Legacy")
	if err != nil {
		t.Fatalf("CreateUser(legacy): %v", err)
	}
	// Clear any seed from the current override, then insert a legacy-tagged row.
	if _, err := st.db.ExecContext(ctx, `DELETE FROM user_preferences WHERE user_id = ? AND key = 'emailNotifications'`, legacyUser.ID); err != nil {
		t.Fatalf("clear seeded row: %v", err)
	}
	legacyValue := `{"v":1,"enabled":false,"assigned":true,"cardActivity":false,"sprintActivity":false,"projectActivity":false,"addedToProject":true}`
	legacyUpdated := time.Now().UTC().UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO user_preferences (user_id, key, value, updated_at, provenance)
VALUES (?, 'emailNotifications', ?, ?, ?)
`, legacyUser.ID, legacyValue, legacyUpdated, preferenceProvenanceLegacy); err != nil {
		t.Fatalf("insert legacy preference: %v", err)
	}

	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"sprintActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault(change): %v", err)
	}

	gotValue, gotProv, gotUpdated, exists := preferenceRow(t, st, userOwned.ID, "emailNotifications")
	if !exists {
		t.Fatalf("user-owned row disappeared after org default change")
	}
	if gotValue != userValue || gotProv != userProv || gotUpdated != userUpdated {
		t.Fatalf("user-owned row changed: before {%q,%q,%d} after {%q,%q,%d}",
			userValue, userProv, userUpdated, gotValue, gotProv, gotUpdated)
	}

	gotValue, gotProv, gotUpdated, exists = preferenceRow(t, st, legacyUser.ID, "emailNotifications")
	if !exists {
		t.Fatalf("legacy row disappeared after org default change")
	}
	if gotValue != legacyValue || gotProv != preferenceProvenanceLegacy || gotUpdated != legacyUpdated {
		t.Fatalf("legacy row changed: before {%q,%q,%d} after {%q,%q,%d}",
			legacyValue, preferenceProvenanceLegacy, legacyUpdated, gotValue, gotProv, gotUpdated)
	}
}
