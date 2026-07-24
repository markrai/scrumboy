package store

import (
	"context"
	"errors"
	"testing"
)

func TestGetEmailNotifyOrgDefault_UnsetFallsBackToHardcodedDefault(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	pref, customized, err := st.GetEmailNotifyOrgDefault(ctx)
	if err != nil {
		t.Fatalf("GetEmailNotifyOrgDefault: %v", err)
	}
	if customized {
		t.Fatalf("expected customized=false when no admin override set")
	}
	if pref != DefaultEmailNotifyPref() {
		t.Fatalf("got %+v, want hardcoded default %+v", pref, DefaultEmailNotifyPref())
	}
}

func TestSetEmailNotifyOrgDefault_RequiresAdminOrOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	user, err := st.CreateUser(ctx, "user@test.com", "password123", "User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.SetEmailNotifyOrgDefault(ctx, user.ID, `{"enabled":true}`); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for plain user, got %v", err)
	}

	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault(owner): %v", err)
	}

	pref, customized, err := st.GetEmailNotifyOrgDefault(ctx)
	if err != nil {
		t.Fatalf("GetEmailNotifyOrgDefault: %v", err)
	}
	if !customized {
		t.Fatalf("expected customized=true after admin override")
	}
	if !pref.Enabled {
		t.Fatalf("expected Enabled=true, got %+v", pref)
	}
}

func TestSetEmailNotifyOrgDefault_RejectsInvalidJSON(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"unknown":true}`); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
}

// TestCreateUser_SeedsCurrentOrgDefault is the core Phase 1 behavior: a user created
// after an admin sets an org override gets that override as their initial
// emailNotifications preference, not the hardcoded DefaultEmailNotifyPref().
func TestCreateUser_SeedsCurrentOrgDefault(t *testing.T) {
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

	newUser, err := st.CreateUser(ctx, "new@test.com", "password123", "New User")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := st.GetEmailNotifyPref(ctx, newUser.ID)
	if err != nil {
		t.Fatalf("GetEmailNotifyPref: %v", err)
	}
	if !got.Enabled || !got.ProjectActivity {
		t.Fatalf("new user should have inherited org default, got %+v", got)
	}
}

// TestCreateUser_ExistingUsersUnaffectedByLaterOrgDefaultChange proves the org
// default is only ever a seed at creation time, never applied retroactively.
func TestCreateUser_ExistingUsersUnaffectedByLaterOrgDefaultChange(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	earlyUser, err := st.CreateUser(ctx, "early@test.com", "password123", "Early User")
	if err != nil {
		t.Fatalf("CreateUser(early): %v", err)
	}
	// Early user seeded from the (unset) hardcoded default.
	early, err := st.GetEmailNotifyPref(ctx, earlyUser.ID)
	if err != nil {
		t.Fatalf("GetEmailNotifyPref(early): %v", err)
	}
	if early != DefaultEmailNotifyPref() {
		t.Fatalf("early user should start from hardcoded default, got %+v", early)
	}

	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"sprintActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}

	// Existing early user's preference is untouched by the later admin change.
	early, err = st.GetEmailNotifyPref(ctx, earlyUser.ID)
	if err != nil {
		t.Fatalf("GetEmailNotifyPref(early, after org default change): %v", err)
	}
	if early != DefaultEmailNotifyPref() {
		t.Fatalf("existing user's preference should be unchanged by org default update, got %+v", early)
	}

	lateUser, err := st.CreateUser(ctx, "late@test.com", "password123", "Late User")
	if err != nil {
		t.Fatalf("CreateUser(late): %v", err)
	}
	late, err := st.GetEmailNotifyPref(ctx, lateUser.ID)
	if err != nil {
		t.Fatalf("GetEmailNotifyPref(late): %v", err)
	}
	if !late.Enabled || !late.SprintActivity {
		t.Fatalf("late user should have inherited the updated org default, got %+v", late)
	}
}

// TestCreateUserOIDC_SeedsCurrentOrgDefault mirrors TestCreateUser_SeedsCurrentOrgDefault
// for the OIDC user-creation path, but only for a non-bootstrap OIDC user (an owner
// already exists here via BootstrapUser first).
func TestCreateUserOIDC_SeedsCurrentOrgDefault(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	st.configuredOIDCIssuer = "https://idp.example"

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	if err := st.SetEmailNotifyOrgDefault(ctx, owner.ID, `{"enabled":true,"cardActivity":true}`); err != nil {
		t.Fatalf("SetEmailNotifyOrgDefault: %v", err)
	}

	oidcUser, err := st.CreateUserOIDC(ctx, st.configuredOIDCIssuer, st.configuredOIDCIssuer, "sub-1", "sso@test.com", "SSO User")
	if err != nil {
		t.Fatalf("CreateUserOIDC: %v", err)
	}
	if oidcUser.IsBootstrap {
		t.Fatalf("expected non-bootstrap OIDC user since an owner already exists")
	}

	got, err := st.GetEmailNotifyPref(ctx, oidcUser.ID)
	if err != nil {
		t.Fatalf("GetEmailNotifyPref: %v", err)
	}
	if !got.Enabled || !got.CardActivity {
		t.Fatalf("OIDC user should have inherited org default, got %+v", got)
	}
	_, provenance, _, exists := preferenceRow(t, st, oidcUser.ID, "emailNotifications")
	if !exists {
		t.Fatalf("expected a seeded emailNotifications row for OIDC user when org default is set")
	}
	if provenance != preferenceProvenanceOrgDefault {
		t.Fatalf("expected provenance %q, got %q", preferenceProvenanceOrgDefault, provenance)
	}
}

// TestBootstrapUser_NotSeededFromOrgDefault documents that the first (bootstrap)
// owner predates any org default and keeps the lazy hardcoded-fallback path
// instead of a seeded user_preferences row.
func TestBootstrapUser_NotSeededFromOrgDefault(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	raw, err := st.GetUserPreference(ctx, owner.ID, "emailNotifications")
	if err != nil {
		t.Fatalf("GetUserPreference: %v", err)
	}
	if raw != "" {
		t.Fatalf("expected no seeded emailNotifications row for bootstrap owner, got %q", raw)
	}
}
