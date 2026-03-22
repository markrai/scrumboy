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
