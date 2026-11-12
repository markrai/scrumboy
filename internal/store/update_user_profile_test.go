package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func strp(s string) *string { return &s }

func TestUpdateUserProfile(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, err := st.BootstrapUser(ctx, "owner@test.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin@test.com", "password123", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateUserRole(ctx, owner.ID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatal(err)
	}
	target, err := st.CreateUser(ctx, "mars@test.com", "password123", "Mars Yeen")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "other@test.com", "password123", "Other")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, owner.ID, "ci-bot")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("owner changes name and email, normalized", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, target.ID, strp("  Lynix Wolf "), strp("  Lynix@Test.COM ")); err != nil {
			t.Fatalf("UpdateUserProfile: %v", err)
		}
		got, err := st.GetUser(ctx, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "Lynix Wolf" || got.Email != "lynix@test.com" {
			t.Fatalf("got name=%q email=%q", got.Name, got.Email)
		}
		if got.SystemRole != SystemRoleUser {
			t.Fatalf("role changed to %q", got.SystemRole)
		}
	})

	t.Run("name only leaves email alone", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, other.ID, strp("Other Name"), nil); err != nil {
			t.Fatal(err)
		}
		got, _ := st.GetUser(ctx, other.ID)
		if got.Name != "Other Name" || got.Email != "other@test.com" {
			t.Fatalf("got name=%q email=%q", got.Name, got.Email)
		}
	})

	t.Run("old email is free again and the user can still log in", func(t *testing.T) {
		if _, err := st.CreateUser(ctx, "mars@test.com", "password123", "New Mars"); err != nil {
			t.Fatalf("old address should be reusable: %v", err)
		}
		if _, err := st.AuthenticateUser(ctx, "lynix@test.com", "password123"); err != nil {
			t.Fatalf("login with new email: %v", err)
		}
	})

	t.Run("duplicate email is a conflict and changes nothing", func(t *testing.T) {
		err := st.UpdateUserProfile(ctx, owner.ID, other.ID, strp("Should Not Apply"), strp("LYNIX@test.com"))
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		got, _ := st.GetUser(ctx, other.ID)
		if got.Name != "Other Name" || got.Email != "other@test.com" {
			t.Fatalf("partial write: name=%q email=%q", got.Name, got.Email)
		}
	})

	t.Run("non-owners are refused", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, admin.ID, other.ID, strp("X"), nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("admin: err = %v, want ErrUnauthorized", err)
		}
		if err := st.UpdateUserProfile(ctx, other.ID, other.ID, strp("X"), nil); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("self-service user: err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("service accounts and missing users are refused", func(t *testing.T) {
		if err := st.UpdateUserProfile(ctx, owner.ID, svc.ID, strp("renamed"), nil); !errors.Is(err, ErrServiceAccountRestricted) {
			t.Fatalf("service account: err = %v, want ErrServiceAccountRestricted", err)
		}
		if err := st.UpdateUserProfile(ctx, owner.ID, 999999, strp("x"), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing user: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("validation", func(t *testing.T) {
		for name, tc := range map[string]struct{ name, email *string }{
			"nothing":           {nil, nil},
			"blank name":        {strp("   "), nil},
			"long name":         {strp(strings.Repeat("a", maxUserNameRunes+1)), nil},
			"blank email":       {nil, strp("  ")},
			"no at sign":        {nil, strp("nope")},
			"service-account":   {nil, strp("bot@" + serviceAccountEmailDomain)},
			"service subdomain": {nil, strp("bot@x." + serviceAccountEmailDomain)},
		} {
			if err := st.UpdateUserProfile(ctx, owner.ID, other.ID, tc.name, tc.email); !errors.Is(err, ErrValidation) {
				t.Errorf("%s: err = %v, want ErrValidation", name, err)
			}
		}
	})
}
