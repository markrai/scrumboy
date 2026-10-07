package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

func TestWallStoryRoutesPinProjectAndUnpinWithoutDeletingTodo(t *testing.T) {
	fx := newWallCharacterizationFixture(t, true)
	ctx := store.WithUserID(context.Background(), fx.owner.ID)
	todo, err := fx.store.CreateTodo(ctx, fx.project.ID, store.CreateTodoInput{
		Title: "Canonical wall story", Tags: []string{"shared"}, ColumnKey: store.DefaultColumnBacklog,
	}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}

	var placement map[string]any
	resp, body := doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/stories"), map[string]any{
		"localId": todo.LocalID, "x": 12, "y": 34,
	}, &placement)
	assertWallStatus(t, resp, body, http.StatusCreated)
	assertExactJSONKeys(t, placement, "localId", "x", "y", "version")

	var duplicate map[string]any
	resp, body = doJSON(t, fx.client, http.MethodPost, wallMutationURL(fx, "/stories"), map[string]any{
		"localId": todo.LocalID, "x": 999, "y": 999,
	}, &duplicate)
	assertWallStatus(t, resp, body, http.StatusOK)
	if duplicate["x"] != float64(12) || duplicate["y"] != float64(34) {
		t.Fatalf("duplicate pin moved placement: %+v", duplicate)
	}

	var wall map[string]any
	resp, body = doJSON(t, fx.client, http.MethodGet, wallMutationURL(fx, ""), nil, &wall)
	assertWallStatus(t, resp, body, http.StatusOK)
	stories, ok := wall["stories"].([]any)
	if !ok || len(stories) != 1 {
		t.Fatalf("stories=%+v", wall["stories"])
	}
	story := stories[0].(map[string]any)
	projected := story["todo"].(map[string]any)
	if projected["id"] != float64(todo.ID) || projected["localId"] != float64(todo.LocalID) || projected["title"] != todo.Title {
		t.Fatalf("canonical projection=%+v", projected)
	}

	var moved map[string]any
	storyPath := "/stories/" + strconv.FormatInt(todo.LocalID, 10)
	resp, body = doJSON(t, fx.client, http.MethodPatch, wallMutationURL(fx, storyPath), map[string]any{
		"ifVersion": placement["version"], "x": -22, "y": 51,
	}, &moved)
	assertWallStatus(t, resp, body, http.StatusOK)
	if moved["x"] != float64(-22) || moved["y"] != float64(51) {
		t.Fatalf("moved=%+v", moved)
	}

	resp, body = doJSON(t, fx.client, http.MethodDelete, wallMutationURL(fx, storyPath), nil, nil)
	assertWallStatus(t, resp, body, http.StatusNoContent)
	if _, err := fx.store.GetTodoByLocalID(ctx, fx.project.ID, todo.LocalID, store.ModeFull); err != nil {
		t.Fatalf("unpin deleted canonical todo: %v", err)
	}
}
