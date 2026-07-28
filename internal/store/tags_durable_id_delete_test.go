package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteTagForDurableProjectByID_RejectsCrossProjectPersonal(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	pa, _ := st.CreateProject(ctxU, "A")
	pb, _ := st.CreateProject(ctxU, "B")
	plNewTodo(t, st, ctxU, pb.ID, "only-b", "solo")
	tagID := plTagRowID(t, st, "solo", u.ID)

	_, err := st.DeleteTagForDurableProjectByID(ctxU, pa.ID, u.ID, tagID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for A-route deleting B-only tag, got %v", err)
	}
	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&remaining)
	if remaining != 1 {
		t.Fatalf("tag must still exist")
	}
}

func TestDeleteTagForDurableProjectByID_RejectsForeignBoardScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	pa, _ := st.CreateProject(ctxU, "A")
	pb, _ := st.CreateProject(ctxU, "B")
	foreignID := plInsertBoardScopedTag(t, st, pb.ID, "foreign", nil)

	_, err := st.DeleteTagForDurableProjectByID(ctxU, pa.ID, u.ID, foreignID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for foreign board-scoped tag, got %v", err)
	}
}

func TestDeleteTagForDurableProjectByID_RejectsNonMember(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	outsider, _ := st.CreateUser(ctx, "out@example.com", "password", "Out")
	ctxOwner := WithUserID(ctx, owner.ID)
	p, _ := st.CreateProject(ctxOwner, "P")
	plNewTodo(t, st, ctxOwner, p.ID, "a", "bug")
	tagID := plTagRowID(t, st, "bug", owner.ID)

	_, err := st.DeleteTagForDurableProjectByID(WithUserID(ctx, outsider.ID), p.ID, outsider.ID, tagID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-member, got %v", err)
	}
}

func TestDeleteTagForDurableProjectByID_ViewerCannotDeleteBoardScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	viewer, _ := st.CreateUser(ctx, "viewer@example.com", "password", "Viewer")
	ctxOwner := WithUserID(ctx, owner.ID)
	p, _ := st.CreateProject(ctxOwner, "P")
	plAddMember(t, st, p.ID, viewer.ID, RoleViewer)
	boardTagID := plInsertBoardScopedTag(t, st, p.ID, "shared", nil)

	_, err := st.DeleteTagForDurableProjectByID(WithUserID(ctx, viewer.ID), p.ID, viewer.ID, boardTagID)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for viewer board-scoped delete, got %v", err)
	}
}

func TestDeleteTagForDurableProjectByID_MaintainerDeletesBoardScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	ctxOwner := WithUserID(ctx, owner.ID)
	p, _ := st.CreateProject(ctxOwner, "P")
	boardTagID := plInsertBoardScopedTag(t, st, p.ID, "shared", nil)

	affected, err := st.DeleteTagForDurableProjectByID(ctxOwner, p.ID, owner.ID, boardTagID)
	if err != nil {
		t.Fatalf("maintainer delete: %v", err)
	}
	if len(affected) != 1 || affected[0] != p.ID {
		t.Fatalf("affected = %v, want [%d]", affected, p.ID)
	}
	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id=?`, boardTagID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("board-scoped tag should be deleted")
	}
}

func TestDeleteTagForDurableProjectByID_CannotDeleteOthersPersonal(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	owner, _ := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	member, _ := st.CreateUser(ctx, "member@example.com", "password", "Member")
	ctxOwner := WithUserID(ctx, owner.ID)
	p, _ := st.CreateProject(ctxOwner, "P")
	plAddMember(t, st, p.ID, member.ID, RoleMaintainer)
	plNewTodo(t, st, ctxOwner, p.ID, "a", "bug")
	tagID := plTagRowID(t, st, "bug", owner.ID)

	_, err := st.DeleteTagForDurableProjectByID(WithUserID(ctx, member.ID), p.ID, member.ID, tagID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when deleting another's personal tag, got %v", err)
	}
	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&remaining)
	if remaining != 1 {
		t.Fatalf("personal tag must still exist")
	}
}

func TestDeleteTagForDurableProjectByID_OwnCrossProjectPersonalReturnsAllAffected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	pa, _ := st.CreateProject(ctxU, "A")
	pb, _ := st.CreateProject(ctxU, "B")
	plNewTodo(t, st, ctxU, pa.ID, "a", "shared")
	plNewTodo(t, st, ctxU, pb.ID, "b", "shared")
	tagID := plTagRowID(t, st, "shared", u.ID)

	affected, err := st.DeleteTagForDurableProjectByID(ctxU, pa.ID, u.ID, tagID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("expected 2 affected projects, got %v", affected)
	}
	got := map[int64]bool{affected[0]: true, affected[1]: true}
	if !got[pa.ID] || !got[pb.ID] {
		t.Fatalf("affected = %v, want {%d, %d}", affected, pa.ID, pb.ID)
	}
	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("personal tag should be deleted")
	}
}
