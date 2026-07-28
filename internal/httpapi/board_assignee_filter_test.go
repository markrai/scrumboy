package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

type assigneeFilterBoardResponse struct {
	Columns map[string][]struct {
		Title          string `json:"title"`
		AssigneeUserID *int64 `json:"assigneeUserId"`
	} `json:"columns"`
}

func assertBacklogTitles(t *testing.T, board assigneeFilterBoardResponse, want ...string) {
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

func TestBoardAssigneeFilter_RESTContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-filter-owner@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Assignee REST")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	member, err := st.CreateUser(context.Background(), "board-filter-member@example.com", "password123", "Member")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, member.ID, store.RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	for _, in := range []store.CreateTodoInput{
		{Title: "Mine", AssigneeUserID: &ownerID},
		{Title: "Member card", AssigneeUserID: &member.ID},
		{Title: "Unassigned"},
	} {
		if _, err := st.CreateTodo(ctxOwner, project.ID, in, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", in.Title, err)
		}
	}

	t.Run("me", func(t *testing.T) {
		var board assigneeFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?assignee=me", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board assignee=me: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitles(t, board, "Mine")
	})

	t.Run("positive user id string", func(t *testing.T) {
		var board assigneeFilterBoardResponse
		filter := url.QueryEscape(strconv.FormatInt(member.ID, 10))
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?assignee="+filter, nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board member assignee: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitles(t, board, "Member card")
	})

	t.Run("unassigned", func(t *testing.T) {
		var board assigneeFilterBoardResponse
		resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?assignee=unassigned", nil, &board)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET board assignee=unassigned: status=%d body=%s", resp.StatusCode, string(body))
		}
		assertBacklogTitles(t, board, "Unassigned")
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
			resp, body := doJSON(t, client, http.MethodGet, ts.URL+tc.path+"?assignee=abc", nil, &apiErr)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("GET %s invalid assignee: status=%d body=%s", tc.path, resp.StatusCode, string(body))
			}
			assertAPIError(t, apiErr, "VALIDATION_ERROR", "assignee", "invalid_assignee")
			if apiErr.Error.Message != "invalid assignee" {
				t.Fatalf("unexpected error message: %+v", apiErr.Error)
			}
		})
	}
}

func TestBoardAssigneeFilter_UnauthenticatedMeFailsWithoutBroadening(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	authenticated := newCookieClient(t)
	bootstrapUserClient(t, authenticated, ts.URL, "Owner", "board-filter-auth@example.com", "password123")

	anonymous := newCookieClient(t)
	slug := createAnonBoardViaHTTP(t, anonymous, ts.URL)

	var apiErr apiErrorEnvelope
	resp, body := doJSON(t, anonymous, http.MethodGet, ts.URL+"/api/board/"+slug+"?assignee=me", nil, &apiErr)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET anonymous board assignee=me: status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, apiErr, "VALIDATION_ERROR", "assignee", "invalid_assignee")
}
