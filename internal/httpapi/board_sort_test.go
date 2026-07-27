package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"scrumboy/internal/store"
)

type sortBoardResponse struct {
	Columns map[string][]struct {
		Title string `json:"title"`
	} `json:"columns"`
}

func assertBacklogTitlesInOrder(t *testing.T, board sortBoardResponse, want ...string) {
	t.Helper()
	todos := board.Columns[store.DefaultColumnBacklog]
	if len(todos) != len(want) {
		t.Fatalf("backlog has %d todos, want %d: %+v", len(todos), len(want), todos)
	}
	for i, title := range want {
		if todos[i].Title != title {
			t.Fatalf("backlog order = %+v, want %v", todos, want)
		}
	}
}

func TestBoardSortOrder_RESTContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-sort-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Sort REST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create in an order that disagrees with manual rank order (each new todo
	// is appended after the others), so newest/oldest/default are distinguishable.
	for _, title := range []string{"First", "Second", "Third"} {
		if _, err := st.CreateTodo(ctxOwner, project.ID, store.CreateTodoInput{Title: title}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	t.Run("default order is manual rank (creation order here)", func(t *testing.T) {
		var board sortBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitlesInOrder(t, board, "First", "Second", "Third")
	})

	t.Run("sort=newest", func(t *testing.T) {
		var board sortBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?sort=newest", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board sort=newest: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitlesInOrder(t, board, "Third", "Second", "First")
	})

	t.Run("sort=oldest", func(t *testing.T) {
		var board sortBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?sort=oldest", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board sort=oldest: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitlesInOrder(t, board, "First", "Second", "Third")
	})

	invalidRoutes := []struct {
		name string
		path string
	}{
		{name: "primary board", path: "/api/board/" + project.Slug},
		{name: "lane page", path: "/api/board/" + project.Slug + "/lanes/" + store.DefaultColumnBacklog},
		{name: "legacy project board", path: fmt.Sprintf("/api/projects/%d/board", project.ID)},
	}
	for _, tc := range invalidRoutes {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			var apiErr apiErrorEnvelope
			resp, body := doJSON(t, client, http.MethodGet, ts.URL+tc.path+"?sort=manual", nil, &apiErr)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("GET %s invalid sort: status=%d body=%s", tc.path, resp.StatusCode, string(body))
			}
			assertAPIError(t, apiErr, "VALIDATION_ERROR", "sort", "invalid_sort")
			if apiErr.Error.Message != "invalid sort" {
				t.Fatalf("unexpected error message: %+v", apiErr.Error)
			}
		})
	}
}

func TestBoardSortOrder_LanePagePaginationContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-sort-lane-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Sort Lane REST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, title := range []string{"One", "Two", "Three", "Four"} {
		if _, err := st.CreateTodo(ctxOwner, project.ID, store.CreateTodoInput{Title: title}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	var page1 struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
		NextCursor *string `json:"nextCursor"`
		HasMore    bool    `json:"hasMore"`
	}
	path := "/api/board/" + project.Slug + "/lanes/" + store.DefaultColumnBacklog + "?sort=newest&limit=2"
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+path, nil, &page1)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET lane page1: status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(page1.Items) != 2 || page1.Items[0].Title != "Four" || page1.Items[1].Title != "Three" {
		t.Fatalf("unexpected page1: %+v", page1)
	}
	if !page1.HasMore || page1.NextCursor == nil || *page1.NextCursor == "" {
		t.Fatalf("expected hasMore page1: %+v", page1)
	}

	var page2 struct {
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
		HasMore bool `json:"hasMore"`
	}
	path2 := "/api/board/" + project.Slug + "/lanes/" + store.DefaultColumnBacklog + "?sort=newest&limit=2&afterCursor=" + url.QueryEscape(*page1.NextCursor)
	resp2, body2 := doJSON(t, client, http.MethodGet, ts.URL+path2, nil, &page2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("GET lane page2: status=%d body=%s", resp2.StatusCode, string(body2))
	}
	if len(page2.Items) != 2 || page2.Items[0].Title != "Two" || page2.Items[1].Title != "One" {
		t.Fatalf("unexpected page2: %+v", page2)
	}
	if page2.HasMore {
		t.Fatalf("expected no more pages: %+v", page2)
	}
}
