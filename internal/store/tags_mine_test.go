package store

import (
	"context"
	"errors"
	"testing"
)

func TestDeleteMyTagByID_ReturnsAllAffectedProjects(t *testing.T) {
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
	affected, err := st.DeleteMyTagByID(ctxU, u.ID, tagID)
	if err != nil {
		t.Fatalf("DeleteMyTagByID: %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("expected 2 affected projects, got %v", affected)
	}
	got := map[int64]bool{affected[0]: true, affected[1]: true}
	if !got[pa.ID] || !got[pb.ID] {
		t.Fatalf("affected projects = %v, want {%d, %d}", affected, pa.ID, pb.ID)
	}

	var remaining int
	_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE id=?`, tagID).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("tag should be deleted")
	}
}

func TestDeleteMyTagByID_RejectsNonOwner(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	tagID := plTagRowID(t, st, "bug", u1.ID)

	_, err := st.DeleteMyTagByID(WithUserID(ctx, u2.ID), u2.ID, tagID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner, got %v", err)
	}
}

func TestUpdateMyTagColor_OwnerOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	tagID := plTagRowID(t, st, "bug", u1.ID)

	color := "#abcdef"
	if err := st.UpdateMyTagColor(ctx1, u2.ID, tagID, &color); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner color, got %v", err)
	}
	if err := st.UpdateMyTagColor(ctx1, u1.ID, tagID, &color); err != nil {
		t.Fatalf("owner UpdateMyTagColor: %v", err)
	}
	// Clearing with no residual preference after a clear is idempotent.
	if err := st.UpdateMyTagColor(ctx1, u1.ID, tagID, nil); err != nil {
		t.Fatalf("clear color: %v", err)
	}
	if err := st.UpdateMyTagColor(ctx1, u1.ID, tagID, nil); err != nil {
		t.Fatalf("idempotent clear: %v", err)
	}
}
