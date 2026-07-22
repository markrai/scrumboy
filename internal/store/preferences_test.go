package store

import (
	"context"
	"errors"
	"testing"
)

func TestSetUserPreference_TagColors_RejectsInvalid(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "test@example.com", "password123", "Test")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}

	// Valid tagColors should save
	err = st.SetUserPreference(ctx, user.ID, "tagColors", `{"bug":"#ff0000","feature":"#00ff00"}`)
	if err != nil {
		t.Fatalf("set valid tagColors: %v", err)
	}

	// Invalid color in tagColors should reject
	err = st.SetUserPreference(ctx, user.ID, "tagColors", `{"bug":"#ff0000","feature":"red"}`)
	if err == nil {
		t.Fatal("expected error for invalid tag color, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}

	// XSS attempt should reject
	err = st.SetUserPreference(ctx, user.ID, "tagColors", `{"bug":"#ff0000\");}</style><script>alert(1)</script>"}`)
	if err == nil {
		t.Fatal("expected error for XSS-like color, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}
}

func TestSetUserPreference_EmailNotifications_RoundTripAndRejectsInvalid(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, err := st.BootstrapUser(ctx, "email-notify@example.com", "password123", "Test")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}

	// Unset should fall back to defaults.
	pref, err := st.GetEmailNotifyPref(ctx, user.ID)
	if err != nil {
		t.Fatalf("get default pref: %v", err)
	}
	if pref.Enabled || !pref.Assigned || !pref.AddedToProject {
		t.Fatalf("unexpected defaults: %+v", pref)
	}

	// Valid preference JSON should save and round-trip.
	err = st.SetUserPreference(ctx, user.ID, "emailNotifications", `{"v":1,"enabled":true,"cardActivity":true}`)
	if err != nil {
		t.Fatalf("set valid emailNotifications: %v", err)
	}
	pref, err = st.GetEmailNotifyPref(ctx, user.ID)
	if err != nil {
		t.Fatalf("get pref after set: %v", err)
	}
	if !pref.Enabled || !pref.CardActivity {
		t.Fatalf("expected round-tripped pref, got %+v", pref)
	}
	raw, err := st.GetUserPreference(ctx, user.ID, "emailNotifications")
	if err != nil {
		t.Fatal(err)
	}
	if raw != `{"v":1,"enabled":true,"assigned":true,"cardActivity":true,"sprintActivity":false,"projectActivity":false,"addedToProject":true}` {
		t.Fatalf("expected canonical stored preference, got %s", raw)
	}

	// Malformed JSON should reject.
	err = st.SetUserPreference(ctx, user.ID, "emailNotifications", `not json`)
	if err == nil {
		t.Fatal("expected error for malformed emailNotifications JSON, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}

	// Unsupported version should reject.
	err = st.SetUserPreference(ctx, user.ID, "emailNotifications", `{"v":99,"enabled":true}`)
	if err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE user_preferences SET value = ? WHERE user_id = ? AND key = ?`, `{"v":2}`, user.ID, "emailNotifications"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetEmailNotifyPref(ctx, user.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected invalid stored preference to remain an error, got %v", err)
	}
}
