package httpapi

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

type priorityFilterLaneResponse struct {
	Items []struct {
		Title string `json:"title"`
	} `json:"items"`
}

type priorityFilterBoardResponse struct {
	Columns map[string][]struct {
		Title       string  `json:"title"`
		PriorityKey *string `json:"priorityKey"`
	} `json:"columns"`
}

func assertPriorityBacklogTitles(t *testing.T, board priorityFilterBoardResponse, want ...string) {
	t.Helper()
	todos := board.Columns[store.DefaultColumnBacklog]
	if len(todos) != len(want) {
		t.Fatalf("backlog has %d todos, want %d: %+v", len(todos), len(want), todos)
	}
	for i, title := range want {
		if todos[i].Title != title {
			t.Fatalf("backlog[%d].title = %q, want %q", i, todos[i].Title, title)
		}
	}
}

func TestBoardPriorityFilter_RESTContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-priority-filter-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Priority REST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	urgent := "urgent"
	high := "high"
	for _, in := range []store.CreateTodoInput{
		{Title: "Urgent card", PriorityKey: &urgent},
		{Title: "High card", PriorityKey: &high},
		{Title: "No priority"},
	} {
		if _, err := st.CreateTodo(ctxOwner, project.ID, in, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", in.Title, err)
		}
	}

	t.Run("specific priority key", func(t *testing.T) {
		var board priorityFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?priority=urgent", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board priority=urgent: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertPriorityBacklogTitles(t, board, "Urgent card")
	})

	t.Run("none", func(t *testing.T) {
		var board priorityFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?priority=none", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board priority=none: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertPriorityBacklogTitles(t, board, "No priority")
	})

	t.Run("unmatched key returns empty backlog", func(t *testing.T) {
		var board priorityFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?priority=not-a-real-key", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board priority=not-a-real-key: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertPriorityBacklogTitles(t, board)
	})

	t.Run("no filter returns all", func(t *testing.T) {
		var board priorityFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board no filter: status=%d body=%s", resp.StatusCode, string(body))
		}
		if got := len(board.Columns[store.DefaultColumnBacklog]); got != 3 {
			t.Fatalf("backlog has %d todos, want 3", got)
		}
	})

	t.Run("lane page filters by priority", func(t *testing.T) {
		var lane priorityFilterLaneResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/lanes/"+store.DefaultColumnBacklog+"?priority=high", nil, &lane)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET lane priority=high: status=%d body=%s", resp.StatusCode, string(body))
		}
		if len(lane.Items) != 1 || lane.Items[0].Title != "High card" {
			t.Fatalf("expected only 'High card', got %+v", lane.Items)
		}
	})
}
