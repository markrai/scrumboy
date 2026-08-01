package httpapi

import (
	"context"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

type slugBoardReadRoute struct {
	name   string
	suffix string
	lane   bool
}

var slugBoardReadRoutes = []slugBoardReadRoute{
	{name: "initial", suffix: "", lane: false},
	{name: "lane", suffix: "/lanes/BACKLOG", lane: true},
}

type slugBoardReadResponse struct {
	Project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	} `json:"project"`
	Columns map[string][]struct {
		Title string `json:"title"`
	} `json:"columns"`
	ColumnsMeta map[string]struct {
		HasMore    bool `json:"hasMore"`
		TotalCount int  `json:"totalCount"`
	} `json:"columnsMeta"`
}

type slugBoardLaneReadResponse struct {
	Items []struct {
		Title string `json:"title"`
	} `json:"items"`
	HasMore bool `json:"hasMore"`
}

func slugBoardReadURL(baseURL string, projectSlug string, route slugBoardReadRoute, query string) string {
	return baseURL + "/api/board/" + projectSlug + route.suffix + query
}

func seedSlugBoardTodo(
	t *testing.T,
	st *store.Store,
	ctx context.Context,
	projectID int64,
	title string,
	mode store.Mode,
) {
	t.Helper()

	if _, err := st.CreateTodo(ctx, projectID, store.CreateTodoInput{Title: title}, mode); err != nil {
		t.Fatalf("CreateTodo(%q): %v", title, err)
	}
}

func assertSlugBoardReadSuccess(
	t *testing.T,
	client *http.Client,
	baseURL string,
	route slugBoardReadRoute,
	project store.Project,
	laneTitle string,
) {
	t.Helper()

	if route.lane {
		var page slugBoardLaneReadResponse
		resp, body := doJSON(
			t,
			client,
			http.MethodGet,
			slugBoardReadURL(baseURL, project.Slug, route, ""),
			nil,
			&page,
		)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET slug lane read: status=%d body=%s", resp.StatusCode, string(body))
		}
		if len(page.Items) != 1 || page.Items[0].Title != laneTitle {
			t.Fatalf("lane items = %+v, want the board-specific todo %q", page.Items, laneTitle)
		}
		return
	}

	var board slugBoardReadResponse
	resp, body := doJSON(
		t,
		client,
		http.MethodGet,
		slugBoardReadURL(baseURL, project.Slug, route, ""),
		nil,
		&board,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET slug initial read: status=%d body=%s", resp.StatusCode, string(body))
	}
	if board.Project.ID != project.ID || board.Project.Slug != project.Slug {
		t.Fatalf("project = %+v, want id=%d slug=%q", board.Project, project.ID, project.Slug)
	}
	backlog := board.Columns[store.DefaultColumnBacklog]
	if len(backlog) != 1 || backlog[0].Title != laneTitle {
		t.Fatalf("backlog = %+v, want the board-specific todo %q", backlog, laneTitle)
	}
}

func assertSlugBoardReadNotFound(
	t *testing.T,
	client *http.Client,
	baseURL string,
	route slugBoardReadRoute,
	projectSlug string,
	query string,
) {
	t.Helper()

	var got apiErrorEnvelope
	resp, body := doJSON(
		t,
		client,
		http.MethodGet,
		slugBoardReadURL(baseURL, projectSlug, route, query),
		nil,
		&got,
	)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET slug board read: status=%d body=%s", resp.StatusCode, string(body))
	}
	assertSlugBoardNotFoundEnvelope(t, got)
}

func assertSlugBoardNotFoundEnvelope(t *testing.T, got apiErrorEnvelope) {
	t.Helper()

	assertAPIError(t, got, "NOT_FOUND", "")
	if got.Error.Message != "not found" {
		t.Fatalf("error message = %q, want %q", got.Error.Message, "not found")
	}
	if reason, ok := got.Error.Details["reason"]; ok {
		t.Fatalf("not-found response exposed validation reason %v: %+v", reason, got.Error)
	}
	if field, ok := got.Error.Details["field"]; ok {
		t.Fatalf("not-found response exposed validation field %v: %+v", field, got.Error)
	}
}

func assertSlugBoardValidationError(
	t *testing.T,
	client *http.Client,
	requestURL string,
	wantField string,
	wantReason string,
	wantMessage string,
) {
	t.Helper()

	var got apiErrorEnvelope
	resp, body := doJSON(t, client, http.MethodGet, requestURL, nil, &got)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET slug board read: status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, got, "VALIDATION_ERROR", wantField, wantReason)
	if got.Error.Message != wantMessage {
		t.Fatalf("error message = %q, want %q", got.Error.Message, wantMessage)
	}
}

func assertSlugBoardInvalidAssignee(
	t *testing.T,
	client *http.Client,
	baseURL string,
	route slugBoardReadRoute,
	projectSlug string,
) {
	t.Helper()

	assertSlugBoardValidationError(
		t,
		client,
		slugBoardReadURL(baseURL, projectSlug, route, "?assignee=abc"),
		"assignee",
		"invalid_assignee",
		"invalid assignee",
	)
}
