package store

import (
	"context"
	"testing"
)

// TestUpdateTagColorForDurableProjectByIDWithScopeReportsSharedWrites proves
// that only a board-scoped tags.color write reports shared scope. Public
// realtime relies on this to exclude personal user_tag_colors preferences.
func TestUpdateTagColorForDurableProjectByIDWithScopeReportsSharedWrites(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "tag-scope-owner@example.com", "password", "Owner")
	ctxOwner := WithUserID(ctx, owner.ID)
	project, _ := st.CreateProject(ctxOwner, "Tag Scope")
	plNewTodo(t, st, ctxOwner, project.ID, "personal", "personal")
	personalID := plTagRowID(t, st, "personal", owner.ID)
	boardID := plInsertBoardScopedTag(t, st, project.ID, "board", nil)

	color := "#123456"
	shared, err := st.UpdateTagColorForDurableProjectByIDWithScope(ctxOwner, project.ID, owner.ID, personalID, &color)
	if err != nil || shared {
		t.Fatalf("personal row shared/err = %v/%v, want false/nil", shared, err)
	}
	shared, err = st.UpdateTagColorForDurableProjectByIDWithScope(ctxOwner, project.ID, owner.ID, boardID, &color)
	if err != nil || !shared {
		t.Fatalf("board row shared/err = %v/%v, want true/nil", shared, err)
	}
	// The compatibility wrapper performs the identical mutation.
	clear := ""
	if err := st.UpdateTagColorForDurableProjectByID(ctxOwner, project.ID, owner.ID, boardID, &clear); err != nil {
		t.Fatalf("wrapper clear: %v", err)
	}
}
