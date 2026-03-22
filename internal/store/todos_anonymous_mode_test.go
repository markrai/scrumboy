package store

import (
	"context"
	"testing"
)

func TestCreateTodo_AnonymousTempBoard_WithAuthEnabled_AllowsCreateWithoutUser(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Enable auth by creating a user, but do NOT attach a user to ctx (anonymous request).
	if _, err := st.BootstrapUser(ctx, "user@example.com", "password", "User"); err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	p, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if p.CreatorUserID != nil {
		t.Fatalf("expected CreatorUserID to be NULL for anonymous temp board")
	}

	_, err = st.CreateTodo(ctx, p.ID, CreateTodoInput{
		Title:  "t",
		Body:   "",
		Tags:   []string{},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
}

