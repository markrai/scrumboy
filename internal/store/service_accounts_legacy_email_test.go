package store

import (
	"context"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// A legacy account already on the now-reserved domain is an ordinary human: account type comes only
// from users.is_service, never from the address.
func TestLegacyHumanOnReservedDomain_KeepsHumanBehaviour(t *testing.T) {
	f := newServiceProjectFixture(t)
	ctx := context.Background()
	const email = "legacy@service.invalid"
	hash, err := bcrypt.GenerateFromPassword([]byte(serviceTestPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO users(email, name, password_hash, system_role, created_at) VALUES (?, 'Legacy', ?, 'user', 1)`, email, string(hash))
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	if u, err := f.st.AuthenticateUser(ctx, email, serviceTestPassword); err != nil || u.ID != id || u.IsService {
		t.Fatalf("password login: %+v, %v", u, err)
	}
	if u, err := f.st.GetUserByEmail(ctx, email); err != nil || u.ID != id {
		t.Fatalf("recovery lookup: %+v, %v", u, err)
	}
	session, _, err := f.st.CreateSession(ctx, id, time.Hour)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if u, err := f.st.GetUserBySessionToken(ctx, session); err != nil || u.ID != id || u.IsService {
		t.Fatalf("session lookup: %+v, %v", u, err)
	}
	_, plain, _, err := f.st.CreateUserAPIToken(ctx, id, nil, true)
	if err != nil {
		t.Fatalf("automation token: %v", err)
	}
	if u, err := f.st.GetUserByAPIToken(ctx, plain); err != nil || u.IsService {
		t.Fatalf("automation token resolves as: %+v, %v", u, err)
	}
	if err := f.st.UpdateUserRole(ctx, f.owner.ID, id, SystemRoleAdmin); err != nil {
		t.Fatalf("promotion: %v", err)
	}
	if err := f.st.AddProjectMember(f.ownerCtx, f.owner.ID, f.durable.ID, id, RoleOwner); err != nil {
		t.Fatalf("legacy owner role: %v", err)
	}
	if _, err := f.st.CreateProject(WithUserID(ctx, id), "Legacy's own"); err != nil {
		t.Fatalf("project creation: %v", err)
	}
	actor := WithServiceIdentity(WithUserID(ctx, id), id, false)
	if _, err := f.st.GetProjectBySlug(actor, f.temp.Slug); err != nil {
		t.Fatalf("link board: %v", err)
	}
}
