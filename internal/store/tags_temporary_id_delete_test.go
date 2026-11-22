package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTemporaryProjectIDDeleteCannotCrossAnonymousBoards(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	victim, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	attacker, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateTodo(ctx, victim.ID, CreateTodoInput{
		Title: "victim task", Tags: []string{"victimsecret"}, ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	tagID, err := st.GetBoardScopedTagIDByName(ctx, victim.ID, "victimsecret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, attacker.ID, 0, tagID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign delete error = %v, want ErrNotFound", err)
	}
	var linked int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("foreign tag associations after rejection = %d, err = %v", linked, err)
	}
	if _, err := st.GetBoardScopedTagIDByName(ctx, victim.ID, "victimsecret"); err != nil {
		t.Fatalf("victim tag disappeared: %v", err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, victim.ID, 0, tagID, true); err != nil {
		t.Fatalf("own board delete = %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&linked); err != nil || linked != 0 {
		t.Fatalf("own tag associations after deletion = %d, err = %v", linked, err)
	}
	if _, err := st.GetBoardScopedTagIDByName(ctx, victim.ID, "victimsecret"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("own tag still exists: %v", err)
	}
}

func TestTemporaryProjectIDDeletePersonalTagRequiresProjectUsageAndOwnership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temporary-tag-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	first, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateTodo(ctxOwner, first.ID, CreateTodoInput{
		Title: "owner task", Tags: []string{"owner-tag"}, ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	var tagID int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE name = 'owner-tag' AND user_id = ?`, owner.ID).Scan(&tagID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, other.ID, owner.ID, tagID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("personal tag outside board error = %v, want ErrNotFound", err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, first.ID, 0, tagID, false); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unowned personal tag error = %v, want ErrUnauthorized", err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, first.ID, owner.ID, tagID, false); err != nil {
		t.Fatalf("own linked personal tag delete = %v", err)
	}
}

func TestTemporaryProjectIDDeleteExpiredBoardAndRollback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "rollback task", Tags: []string{"rollback-tag"}, ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	tagID, err := st.GetBoardScopedTagIDByName(ctx, project.ID, "rollback-tag")
	if err != nil {
		t.Fatal(err)
	}
	trigger := fmt.Sprintf(`CREATE TRIGGER reject_temp_tag_delete BEFORE DELETE ON tags WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'rollback'); END`, tagID)
	if _, err := st.db.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, project.ID, 0, tagID, true); err == nil {
		t.Fatal("expected delete failure")
	}
	var linked int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("association after rollback = %d, err = %v", linked, err)
	}
	if _, err := st.db.ExecContext(ctx, `DROP TRIGGER reject_temp_tag_delete`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), project.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTagForTemporaryProjectByID(ctx, project.ID, 0, tagID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired board delete = %v, want ErrNotFound", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`, tagID).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("association after expiry rejection = %d, err = %v", linked, err)
	}
}
