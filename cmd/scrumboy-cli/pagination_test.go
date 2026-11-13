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
