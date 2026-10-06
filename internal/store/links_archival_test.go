package store

import (
	"context"
	"errors"
	"testing"
)

func TestArchivedTodoLinksRemainReadableButImmutableAndSearchDefaultsActive(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "archive links")
	if err != nil {
		t.Fatal(err)
	}
	from, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "from"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	to, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "to archived"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "other"}, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddLink(ctx, p.ID, from.LocalID, to.LocalID, "relates_to", ModeFull); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctx, p.ID, to.LocalID, ModeFull); err != nil {
		t.Fatal(err)
	}
	outbound, err := st.ListLinksForTodo(ctx, p.ID, from.LocalID, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(outbound) != 1 || outbound[0].LocalID != to.LocalID || outbound[0].ArchivedAt == nil {
		t.Fatalf("archived existing link projection=%+v", outbound)
	}
	search, err := st.SearchTodosForLinkPicker(ctx, p.ID, "to archived", 20, nil, ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if len(search) != 0 {
		t.Fatalf("default link search leaked archived target: %+v", search)
	}
	if err := st.AddLink(ctx, p.ID, other.LocalID, to.LocalID, "blocks", ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("add archived link err=%v reason=%q", err, ErrorReason(err))
	}
	if err := st.RemoveLink(ctx, p.ID, from.LocalID, to.LocalID, ModeFull); !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonTodoArchived {
		t.Fatalf("remove archived link err=%v reason=%q", err, ErrorReason(err))
	}
}
