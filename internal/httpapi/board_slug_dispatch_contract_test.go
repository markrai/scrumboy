package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestBoardSlugReadDispatch_RESTExactRouteShapes(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		client,
		ts.URL,
		"Owner",
		"board-slug-dispatch@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Slug Dispatch Contract")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	const todoTitle = "slug dispatch contract todo"
	seedSlugBoardTodo(t, st, ctxOwner, project.ID, todoTitle, store.ModeFull)

	t.Run("uppercase slug reaches exact initial route", func(t *testing.T) {
		var board slugBoardReadResponse
		resp, body := doJSON(
			t,
			client,
			http.MethodGet,
			ts.URL+"/api/board/"+strings.ToUpper(project.Slug),
			nil,
			&board,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET uppercase initial route: status=%d body=%s", resp.StatusCode, string(body))
		}
		if board.Project.ID != project.ID || board.Project.Slug != project.Slug {
			t.Fatalf("project = %+v, want id=%d slug=%q", board.Project, project.ID, project.Slug)
		}
	})

	t.Run("uppercase slug reaches exact lane route", func(t *testing.T) {
		var page slugBoardLaneReadResponse
		resp, body := doJSON(
			t,
			client,
			http.MethodGet,
			ts.URL+"/api/board/"+strings.ToUpper(project.Slug)+"/lanes/BACKLOG",
			nil,
			&page,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET uppercase lane route: status=%d body=%s", resp.StatusCode, string(body))
		}
		if len(page.Items) != 1 || page.Items[0].Title != todoTitle {
			t.Fatalf("lane items = %+v, want %q", page.Items, todoTitle)
		}
	})

	t.Run("exact initial route performs target query validation", func(t *testing.T) {
		assertSlugBoardInvalidAssignee(t, client, ts.URL, slugBoardReadRoutes[0], project.Slug)
	})
	t.Run("exact lane route performs target query validation", func(t *testing.T) {
		assertSlugBoardInvalidAssignee(t, client, ts.URL, slugBoardReadRoutes[1], project.Slug)
	})

	t.Run("missing slug is not found", func(t *testing.T) {
		var got apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board", nil, &got)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET board without slug: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertSlugBoardNotFoundEnvelope(t, got)
	})

	t.Run("invalid slug precedes target query validation", func(t *testing.T) {
		var got apiErrorEnvelope
		resp, body := doJSON(
			t,
			client,
			http.MethodGet,
			ts.URL+"/api/board/-invalid?assignee=abc",
			nil,
			&got,
		)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET invalid slug: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertAPIError(t, got, "VALIDATION_ERROR", "slug", "invalid_slug")
		if got.Error.Message != "invalid slug" {
			t.Fatalf("error message = %q, want %q", got.Error.Message, "invalid slug")
		}
	})

	notFoundCases := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "HEAD initial route",
			method: http.MethodHead,
			path:   "/api/board/" + project.Slug + "?assignee=abc",
		},
		{
			name:   "POST initial route",
			method: http.MethodPost,
			path:   "/api/board/" + project.Slug + "?assignee=abc",
		},
		{
			name:   "POST lane route",
			method: http.MethodPost,
			path:   "/api/board/" + project.Slug + "/lanes/BACKLOG?assignee=abc",
		},
		{
			name:   "lane route with extra segment",
			method: http.MethodGet,
			path:   "/api/board/" + project.Slug + "/lanes/BACKLOG/extra?assignee=abc",
		},
		{
			name:   "unknown sibling route",
			method: http.MethodGet,
			path:   "/api/board/" + project.Slug + "/unknown?assignee=abc",
		},
	}
	for _, tc := range notFoundCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var got apiErrorEnvelope
			resp, body := doJSON(t, client, tc.method, ts.URL+tc.path, nil, &got)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("%s %s: status=%d body=%s", tc.method, tc.path, resp.StatusCode, string(body))
			}
			if tc.method == http.MethodHead {
				if len(body) != 0 {
					t.Fatalf("HEAD response body = %q, want empty", string(body))
				}
				return
			}
			assertSlugBoardNotFoundEnvelope(t, got)
		})
	}

	t.Run("unrelated tag read ignores target query", func(t *testing.T) {
		resp, body := doJSON(
			t,
			client,
			http.MethodGet,
			ts.URL+"/api/board/"+project.Slug+"/tags?assignee=abc",
			nil,
			nil,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET tag read: status=%d body=%s", resp.StatusCode, string(body))
		}
	})

	t.Run("unrelated todo mutation ignores target query", func(t *testing.T) {
		resp, body := doJSON(
			t,
			client,
			http.MethodPost,
			ts.URL+"/api/board/"+project.Slug+"/todos?assignee=abc",
			map[string]any{"title": "sibling mutation todo"},
			nil,
		)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST todo: status=%d body=%s", resp.StatusCode, string(body))
		}
	})
}
