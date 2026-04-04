package store

import (
	"context"
	"testing"
)

// Regression: invited members of an authenticated temporary board (expires_at set, creator_user_id set)
// must see the project in GET /api/projects (ListProjects). Previously only the creator matched the listing query.
func TestListProjects_IncludesAuthenticatedTemporaryBoardForInvitedMember(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	creator, err := st.BootstrapUser(ctx, "creator@example.com", "password", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "invitee@example.com", "password", "Invitee")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ctxCreator := WithUserID(ctx, creator.ID)
	tempBoard, err := st.CreateAnonymousBoard(ctxCreator)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if tempBoard.ExpiresAt == nil {
		t.Fatal("expected temporary board with expires_at")
	}
	// Creator needs a project_members row so AddProjectMember authorization (GetProjectRole) succeeds.
	if err := st.EnsureMaintainerMembership(ctx, tempBoard.ID, creator.ID); err != nil {
		t.Fatalf("EnsureMaintainerMembership creator: %v", err)
	}
	if err := st.AddProjectMember(ctx, creator.ID, tempBoard.ID, invitee.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	ctxInvitee := WithUserID(ctx, invitee.ID)
	list, err := st.ListProjects(ctxInvitee)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Project.ID == tempBoard.ID {
			found = true
			if e.Role != RoleMaintainer {
				t.Fatalf("expected role maintainer, got %q", e.Role)
			}
			break
		}
	}
	if !found {
		t.Fatal("invited member should see authenticated temporary board in ListProjects")
	}
}

// Orphan project_members on a true anonymous temp board (creator_user_id NULL) must not list for invitees.
func TestListProjects_ExcludesAnonymousTemporaryBoardEvenWithOrphanMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	_, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "invitee@example.com", "password", "Invitee")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if anonBoard.CreatorUserID != nil {
		t.Fatal("expected anonymous board without creator_user_id")
	}
	nowMs := anonBoard.CreatedAt.UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		anonBoard.ID, invitee.ID, RoleMaintainer, nowMs); err != nil {
		t.Fatalf("seed orphan membership: %v", err)
	}

	ctxInvitee := WithUserID(ctx, invitee.ID)
	list, err := st.ListProjects(ctxInvitee)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, e := range list {
		if e.Project.ID == anonBoard.ID {
			t.Fatal("anonymous temp board must not appear in ListProjects via membership alone")
		}
	}
}
