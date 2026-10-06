package store

import (
	"context"
	"errors"
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
		Title:     "t",
		Body:      "",
		Tags:      []string{},
		ColumnKey: DefaultColumnBacklog,
	}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
}

func TestCreateTodo_CreatorOwnedTempBoard_WithAuthEnabled_AllowsCreateWithoutUser(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "creator@example.com", "password", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	p, err := st.CreateAnonymousBoard(ctxUser)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	loaded, err := st.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if loaded.CreatorUserID == nil || *loaded.CreatorUserID != user.ID {
		t.Fatalf("expected creator-owned temp (creator_user_id = logged-in user)")
	}
	if loaded.ExpiresAt == nil {
		t.Fatalf("expected temporary board (ExpiresAt set)")
	}

	anon := context.Background()
	_, err = st.CreateTodo(anon, p.ID, CreateTodoInput{
		Title:     "from link",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo anonymous on creator-owned temp: %v", err)
	}
}

func TestCreateTodo_DurableProject_WithAuthEnabled_DeniesCreateWithoutUser(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctxUser, "Durable Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ExpiresAt != nil {
		t.Fatalf("expected durable project (ExpiresAt nil)")
	}

	anon := context.Background()
	_, err = st.CreateTodo(anon, p.ID, CreateTodoInput{
		Title:     "no",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("CreateTodo durable without user: want ErrUnauthorized, got %v", err)
	}
}

func TestMoveTodo_CreatorOwnedTempBoard_WithAuthEnabled_AllowsAnonymous(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "mover@example.com", "password", "Mover")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	p, err := st.CreateAnonymousBoard(WithUserID(ctx, user.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	todo, err := st.CreateTodo(context.Background(), p.ID, CreateTodoInput{
		Title:     "card",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.MoveTodo(context.Background(), todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if err != nil {
		t.Fatalf("MoveTodo anonymous on creator-owned temp: %v", err)
	}
}

func TestDeleteTodo_CreatorOwnedTempBoard_WithAuthEnabled_AllowsAnonymous(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "del@example.com", "password", "Del")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	p, err := st.CreateAnonymousBoard(WithUserID(ctx, user.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	todo, err := st.CreateTodo(context.Background(), p.ID, CreateTodoInput{
		Title:     "gone",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := st.DeleteTodo(context.Background(), todo.ID, ModeFull); err != nil {
		t.Fatalf("DeleteTodo anonymous on creator-owned temp: %v", err)
	}
}

func TestMoveTodo_DurableProject_WithAuthEnabled_DeniesAnonymous(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "dur@example.com", "password", "Dur")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	p, err := st.CreateProject(ctxUser, "Durable Move")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctxUser, p.ID, CreateTodoInput{
		Title:     "t",
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	_, err = st.MoveTodo(context.Background(), todo.ID, DefaultColumnDoing, nil, nil, ModeFull)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("MoveTodo durable without user: want ErrUnauthorized, got %v", err)
	}
}
