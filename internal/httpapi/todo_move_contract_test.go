package httpapi

import (
	"net/http"
	"strconv"
	"testing"
)

func TestTodoMove_RESTAllowsNonBoundaryOneSidedAnchorContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := ts.Client()
	var project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	resp, body := doJSON(
		t,
		client,
		http.MethodPost,
		ts.URL+"/api/projects",
		map[string]any{"name": "REST Move Anchor Contract"},
		&project,
	)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project: status=%d body=%s", resp.StatusCode, string(body))
	}

	todos := make([]struct {
		ID      int64 `json:"id"`
		LocalID int64 `json:"localId"`
	}, 3)
	for i := range todos {
		resp, body = doJSON(
			t,
			client,
			http.MethodPost,
			ts.URL+"/api/board/"+project.Slug+"/todos",
			map[string]any{"title": "Todo " + strconv.Itoa(i+1)},
			&todos[i],
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create todo %d: status=%d body=%s", i+1, resp.StatusCode, string(body))
		}
	}

	moveURL := func(localID int64) string {
		return ts.URL + "/api/board/" + project.Slug + "/todos/" + strconv.FormatInt(localID, 10) + "/move"
	}
	for _, todo := range todos[:2] {
		resp, body = doJSON(
			t,
			client,
			http.MethodPost,
			moveURL(todo.LocalID),
			map[string]any{"toColumnKey": "doing"},
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("seed target lane: status=%d body=%s", resp.StatusCode, string(body))
		}
	}

	var before struct {
		Columns map[string][]struct {
			LocalID int64 `json:"localId"`
		} `json:"columns"`
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug, nil, &before)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read target lane: status=%d body=%s", resp.StatusCode, string(body))
	}
	doing := before.Columns["doing"]
	if len(doing) != 2 || doing[0].LocalID != todos[0].LocalID || doing[1].LocalID != todos[1].LocalID {
		t.Fatalf("target lane before move = %+v, want localIds [%d %d]", doing, todos[0].LocalID, todos[1].LocalID)
	}

	var moved struct {
		LocalID   int64  `json:"localId"`
		ColumnKey string `json:"columnKey"`
	}
	resp, body = doJSON(
		t,
		client,
		http.MethodPost,
		moveURL(todos[2].LocalID),
		map[string]any{
			"toColumnKey": "doing",
			"afterId":     todos[0].LocalID,
		},
		&moved,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("non-boundary one-sided move: status=%d body=%s", resp.StatusCode, string(body))
	}
	if moved.LocalID != todos[2].LocalID || moved.ColumnKey != "doing" {
		t.Fatalf("moved todo = %+v, want localId=%d columnKey=doing", moved, todos[2].LocalID)
	}

	var auditCount int
	if err := sqlDB.QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = 'todo_moved' AND target_id = ?`,
		todos[2].ID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count todo_moved audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("todo_moved audit rows = %d, want 1", auditCount)
	}
}
