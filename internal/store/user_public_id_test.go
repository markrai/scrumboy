package store

import (
	"context"
	"regexp"
	"testing"
)

var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// A deleted user's integer id can be handed to the next user (SQLite INTEGER PRIMARY KEY), but
// the public id must not follow it: it is what an external integration keys account links on.
func TestUserPublicID_NotReusedWhenIntegerIDIs(t *testing.T) {
	ctx := context.Background()
	st, cleanup := newTestStore(t)
	defer cleanup()

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	victim, err := st.CreateUser(ctx, "victim@example.com", "password123", "Victim")
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}

	got, err := st.GetUser(ctx, victim.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !publicIDPattern.MatchString(got.PublicID) {
		t.Fatalf("victim PublicID = %q, want a UUIDv4", got.PublicID)
	}
	ownerGot, _ := st.GetUser(ctx, owner.ID)
	if ownerGot.PublicID == got.PublicID || !publicIDPattern.MatchString(ownerGot.PublicID) {
		t.Fatalf("owner PublicID = %q, victim = %q: want two distinct UUIDs", ownerGot.PublicID, got.PublicID)
	}

	if err := st.DeleteUser(ctx, owner.ID, victim.ID); err != nil {
		t.Fatalf("delete victim: %v", err)
	}
	successor, err := st.CreateUser(ctx, "successor@example.com", "password123", "Successor")
	if err != nil {
		t.Fatalf("create successor: %v", err)
	}
	if successor.ID != victim.ID {
		t.Skipf("SQLite did not reuse users.id %d here (got %d); the reuse scenario is covered at the migration level", victim.ID, successor.ID)
	}
	succGot, err := st.GetUser(ctx, successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if succGot.PublicID == got.PublicID {
		t.Fatalf("successor reusing users.id %d inherited the deleted user's PublicID", successor.ID)
	}
}
