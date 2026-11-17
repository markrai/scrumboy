package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestTodoListAllStopsOnCursorCycle: --all only rejected a cursor equal to the
// previous one, so a server cycling A -> B -> A made it page forever.
func TestTodoListAllStopsOnCursorCycle(t *testing.T) {
	var pages atomic.Int32
	next := map[string]string{"A": "B", "B": "A"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		cur := ""
		if m, ok := c.Input["cursorByColumn"].(map[string]any); ok {
			cur, _ = m["todo"].(string)
		}
		n := pages.Add(1)
		nc := next[cur]
		if cur == "" {
			nc = "A"
		}
		fmt.Fprintf(w, `{"ok":true,"data":{"columns":[{"key":"todo","name":"Todo","items":[{"projectSlug":"demo","localId":%d,"title":"t","columnKey":"todo"}]}]},"meta":{"nextCursorByColumn":{"todo":%q}}}`, n, nc)
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	done := make(chan int, 1)
	go func() { done <- h.run("todo", "list", "--all", "--json") }()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(h.err.String(), "repeating cursor") {
			t.Fatalf("exit %d, stderr %q", code, h.err)
		}
		if h.out.Len() != 0 {
			t.Fatalf("partial results printed on a pagination failure: %s", h.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("todo list --all did not terminate (%d pages fetched)", pages.Load())
	}
}

// TestTodoListAllRejectsMalformedCursor: a cursor that is not a string or null
// is a protocol error, not the end of the column.
func TestTodoListAllRejectsMalformedCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"data":{"columns":[{"key":"todo","name":"Todo","items":[]}]},"meta":{"nextCursorByColumn":{"todo":17}}}`)
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "list", "--all", "--json"); code != 1 || h.out.Len() != 0 {
		t.Fatalf("exit %d out %q err %q", code, h.out, h.err)
	}
}

// TestTodoArchivedAllFollowsCursors: --all walks every page of the archive
// and the table lists them all; without --all one page and a cursor hint.
func TestTodoArchivedAllFollowsCursors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		if c.Tool != "todos_listArchived" || c.Input["projectSlug"] != "demo" {
			t.Errorf("unexpected call %+v", c)
		}
		switch c.Input["cursor"] {
		case nil:
			io.WriteString(w, `{"ok":true,"data":{"items":[{"projectSlug":"demo","localId":3,"title":"third","columnKey":"done","archivedAt":"2026-01-03T00:00:00Z"}],"nextCursor":"P2","hasMore":true}}`)
		case "P2":
			io.WriteString(w, `{"ok":true,"data":{"items":[{"projectSlug":"demo","localId":1,"title":"first","columnKey":"todo","archivedAt":"2026-01-01T00:00:00Z"}],"nextCursor":"","hasMore":false}}`)
		default:
			t.Errorf("unexpected cursor %v", c.Input["cursor"])
		}
	}))
	defer srv.Close()

	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "archived", "--all"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.err)
	}
	if !strings.Contains(h.out.String(), "third") || !strings.Contains(h.out.String(), "first") {
		t.Fatalf("missing rows: %s", h.out)
	}

	h = newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "archived"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, h.err)
	}
	if strings.Contains(h.out.String(), "first") || !strings.Contains(h.err.String(), "--cursor P2") {
		t.Fatalf("single page: out %q err %q", h.out, h.err)
	}
}

// TestTodoArchivedAllRejectsMissingCursor: hasMore without nextCursor must not
// look like a successful --all drain.
func TestTodoArchivedAllRejectsMissingCursor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cursor string
	}{
		{name: "empty", cursor: ""},
		{name: "whitespace", cursor: "   \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"ok": true,
				"data": map[string]any{
					"items": []map[string]any{{
						"projectSlug": "demo",
						"localId":     1,
						"title":       "only",
						"columnKey":   "todo",
						"archivedAt":  "2026-01-01T00:00:00Z",
					}},
					"nextCursor": tc.cursor,
					"hasMore":    true,
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(payload)
			}))
			defer srv.Close()
			h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
			if code := h.run("todo", "archived", "--all", "--json"); code != 1 {
				t.Fatalf("exit %d, want 1; stderr %q", code, h.err)
			}
			if !strings.Contains(h.err.String(), "without a usable nextCursor") {
				t.Fatalf("stderr %q", h.err)
			}
			if h.out.Len() != 0 {
				t.Fatalf("partial JSON printed on cursor anomaly: %s", h.out)
			}
		})
	}
}

// TestTodoArchivedAllRejectsRepeatingCursor preserves the cycle guard used by board --all.
func TestTodoArchivedAllRejectsRepeatingCursor(t *testing.T) {
	var pages atomic.Int32
	next := map[string]string{"A": "B", "B": "A"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		cur, _ := c.Input["cursor"].(string)
		n := pages.Add(1)
		nc := next[cur]
		if cur == "" {
			nc = "A"
		}
		fmt.Fprintf(w, `{"ok":true,"data":{"items":[{"projectSlug":"demo","localId":%d,"title":"t","columnKey":"todo","archivedAt":"2026-01-01T00:00:00Z"}],"nextCursor":%q,"hasMore":true}}`, n, nc)
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	done := make(chan int, 1)
	go func() { done <- h.run("todo", "archived", "--all", "--json") }()
	select {
	case code := <-done:
		if code != 1 || !strings.Contains(h.err.String(), "repeating cursor") {
			t.Fatalf("exit %d, stderr %q", code, h.err)
		}
		if h.out.Len() != 0 {
			t.Fatalf("partial JSON printed on repeating cursor: %s", h.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("todo archived --all did not terminate (%d pages fetched)", pages.Load())
	}
}

// TestTodoArchivedJSONSingleAndAllPages covers --json envelope for one page and --all.
func TestTodoArchivedJSONSingleAndAllPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		switch c.Input["cursor"] {
		case nil:
			io.WriteString(w, `{"ok":true,"data":{"items":[{"projectSlug":"demo","localId":3,"title":"third","columnKey":"done","archivedAt":"2026-01-03T00:00:00Z"}],"nextCursor":"P2","hasMore":true}}`)
		case "P2":
			io.WriteString(w, `{"ok":true,"data":{"items":[{"projectSlug":"demo","localId":1,"title":"first","columnKey":"todo","archivedAt":"2026-01-01T00:00:00Z"}],"nextCursor":"","hasMore":false}}`)
		default:
			t.Errorf("unexpected cursor %v", c.Input["cursor"])
		}
	}))
	defer srv.Close()

	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "archived", "--json"); code != 0 {
		t.Fatalf("single-page exit %d stderr %q", code, h.err)
	}
	var single struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
		HasMore    bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &single); err != nil {
		t.Fatalf("single-page JSON: %v (%s)", err, h.out)
	}
	if len(single.Items) != 1 || single.NextCursor != "P2" || !single.HasMore || single.Items[0]["localId"] != float64(3) {
		t.Fatalf("single-page payload %+v", single)
	}

	h = newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "archived", "--all", "--json"); code != 0 {
		t.Fatalf("all-page exit %d stderr %q", code, h.err)
	}
	var all struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"nextCursor"`
		HasMore    bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &all); err != nil {
		t.Fatalf("all-page JSON: %v (%s)", err, h.out)
	}
	if len(all.Items) != 2 || all.HasMore || all.NextCursor != "" ||
		all.Items[0]["localId"] != float64(3) || all.Items[1]["localId"] != float64(1) {
		t.Fatalf("all-page payload %+v", all)
	}
}
