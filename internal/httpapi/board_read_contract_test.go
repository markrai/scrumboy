package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type boardReadContractResponse struct {
	Project struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	} `json:"project"`
	ColumnOrder []struct {
		Key string `json:"key"`
	} `json:"columnOrder"`
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Columns map[string][]struct {
		ID              int64  `json:"id"`
		Title           string `json:"title"`
		CreatedByUserID *int64 `json:"createdByUserId"`
	} `json:"columns"`
	ColumnsMeta map[string]struct {
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
		TotalCount int     `json:"totalCount"`
	} `json:"columnsMeta"`
}

type boardLaneReadContractResponse struct {
	Items []struct {
		ID              int64  `json:"id"`
		Title           string `json:"title"`
		CreatedByUserID *int64 `json:"createdByUserId"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

func newBoardLaneCursorFixture(t *testing.T, email, projectName string) (string, string, *http.Client) {
	t.Helper()

	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", email, "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, projectName)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, title := range []string{"First", "Second", "Third"} {
		if _, err := st.CreateTodo(ctxOwner, project.ID, store.CreateTodoInput{Title: title}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	return ts.URL, project.Slug, client
}

func TestBoardRead_RESTCombinedFiltersAndPaginationContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-read-contract@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Read Contract")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	start := time.Now().UTC()
	sprint, err := st.CreateSprint(ctxOwner, project.ID, "Board Read Sprint", start, start.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	createTodo := func(title, body string, tags []string, assigneeUserID, sprintID *int64) {
		t.Helper()
		if _, err := st.CreateTodo(ctxOwner, project.ID, store.CreateTodoInput{
			Title:          title,
			Body:           body,
			Tags:           tags,
			AssigneeUserID: assigneeUserID,
			SprintID:       sprintID,
		}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	createTodo("Older matching", "contains the needle", []string{"focus"}, &ownerID, &sprint.ID)
	createTodo("Wrong tag", "contains the needle", []string{"other"}, &ownerID, &sprint.ID)
	createTodo("Wrong search", "contains only hay", []string{"focus"}, &ownerID, &sprint.ID)
	createTodo("Wrong assignee", "contains the needle", []string{"focus"}, nil, &sprint.ID)
	createTodo("Wrong sprint", "contains the needle", []string{"focus"}, &ownerID, nil)
	createTodo("Newer matching", "also contains the needle", []string{"focus"}, &ownerID, &sprint.ID)

	query := url.Values{}
	query.Set("tag", "focus")
	query.Set("search", "  needle  ")
	query.Set("assignee", "me")
	query.Set("sprintId", strconv.FormatInt(sprint.Number, 10))
	query.Set("sort", "newest")
	query.Set("limitPerLane", "1")

	var board boardReadContractResponse
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?"+query.Encode(), nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET board: status=%d body=%s", resp.StatusCode, string(body))
	}

	if board.Project.ID != project.ID || board.Project.Slug != project.Slug {
		t.Fatalf("unexpected project: %+v", board.Project)
	}
	if len(board.ColumnOrder) == 0 {
		t.Fatal("expected columnOrder")
	}
	if len(board.Tags) == 0 {
		t.Fatal("expected tags")
	}

	workflow, err := st.GetProjectWorkflow(ctxOwner, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	if len(board.ColumnOrder) != len(workflow) {
		t.Fatalf("columnOrder len = %d, want workflow len %d", len(board.ColumnOrder), len(workflow))
	}
	workflowKeys := make(map[string]struct{}, len(workflow))
	for i, col := range workflow {
		workflowKeys[col.Key] = struct{}{}
		if board.ColumnOrder[i].Key != col.Key {
			t.Fatalf("columnOrder[%d] = %q, want workflow key %q", i, board.ColumnOrder[i].Key, col.Key)
		}
	}

	for _, column := range board.ColumnOrder {
		if _, ok := board.Columns[column.Key]; !ok {
			t.Fatalf("columns missing workflow lane %q", column.Key)
		}
		if _, ok := board.ColumnsMeta[column.Key]; !ok {
			t.Fatalf("columnsMeta missing workflow lane %q", column.Key)
		}
	}
	for key := range board.Columns {
		if _, ok := workflowKeys[key]; !ok {
			t.Fatalf("columns contains non-workflow key %q", key)
		}
	}
	for key := range board.ColumnsMeta {
		if _, ok := workflowKeys[key]; !ok {
			t.Fatalf("columnsMeta contains non-workflow key %q", key)
		}
	}

	backlog := board.Columns[store.DefaultColumnBacklog]
	if len(backlog) != 1 || backlog[0].Title != "Newer matching" {
		t.Fatalf("unexpected filtered backlog: %+v", backlog)
	}
	if backlog[0].CreatedByUserID == nil || *backlog[0].CreatedByUserID != ownerID {
		t.Fatalf("createdByUserId=%v want=%d", backlog[0].CreatedByUserID, ownerID)
	}

	meta := board.ColumnsMeta[store.DefaultColumnBacklog]
	if meta.TotalCount != 2 {
		t.Fatalf("backlog totalCount = %d, want 2", meta.TotalCount)
	}
	if !meta.HasMore {
		t.Fatal("expected backlog hasMore=true")
	}
	if meta.NextCursor == nil || *meta.NextCursor == "" {
		t.Fatal("expected non-empty backlog nextCursor")
	}
}

func TestBoardLaneRead_RESTCombinedFiltersAndCursorContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "board-lane-read-contract@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Board Lane Read Contract")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	start := time.Now().UTC()
	sprint, err := st.CreateSprint(ctxOwner, project.ID, "Board Lane Read Sprint", start, start.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	createTodo := func(title, body string, tags []string, assigneeUserID, sprintID *int64) {
		t.Helper()
		if _, err := st.CreateTodo(ctxOwner, project.ID, store.CreateTodoInput{
			Title:          title,
			Body:           body,
			Tags:           tags,
			AssigneeUserID: assigneeUserID,
			SprintID:       sprintID,
		}, store.ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", title, err)
		}
	}

	createTodo("Oldest matching", "contains the needle", []string{"focus"}, &ownerID, &sprint.ID)
	createTodo("Wrong tag", "contains the needle", []string{"other"}, &ownerID, &sprint.ID)
	createTodo("Middle matching", "also contains the needle", []string{"focus"}, &ownerID, &sprint.ID)
	createTodo("Wrong search", "contains only hay", []string{"focus"}, &ownerID, &sprint.ID)
	createTodo("Wrong assignee", "contains the needle", []string{"focus"}, nil, &sprint.ID)
	createTodo("Wrong sprint", "contains the needle", []string{"focus"}, &ownerID, nil)
	createTodo("Newest matching", "still contains the needle", []string{"focus"}, &ownerID, &sprint.ID)

	query := url.Values{}
	query.Set("tag", "focus")
	query.Set("search", "  needle  ")
	query.Set("assignee", "me")
	query.Set("sprintId", strconv.FormatInt(sprint.Number, 10))
	query.Set("sort", "newest")
	query.Set("limitPerLane", "1")

	var board boardReadContractResponse
	resp, body := doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"?"+query.Encode(), nil, &board)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET board: status=%d body=%s", resp.StatusCode, string(body))
	}

	backlog := board.Columns[store.DefaultColumnBacklog]
	if len(backlog) != 1 || backlog[0].Title != "Newest matching" {
		t.Fatalf("unexpected initial filtered backlog: %+v", backlog)
	}
	meta := board.ColumnsMeta[store.DefaultColumnBacklog]
	if meta.TotalCount != 3 || !meta.HasMore || meta.NextCursor == nil || *meta.NextCursor == "" {
		t.Fatalf("unexpected initial backlog metadata: %+v", meta)
	}

	query.Del("limitPerLane")
	query.Set("limit", "1")
	query.Set("afterCursor", *meta.NextCursor)

	var page2 boardLaneReadContractResponse
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/lanes/BACKLOG?"+query.Encode(), nil, &page2)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET lane page 2: status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(page2.Items) != 1 || page2.Items[0].Title != "Middle matching" {
		t.Fatalf("unexpected lane page 2: %+v", page2)
	}
	if !page2.HasMore || page2.NextCursor == nil || *page2.NextCursor == "" {
		t.Fatalf("unexpected lane page 2 metadata: %+v", page2)
	}

	query.Set("afterCursor", *page2.NextCursor)

	var page3 boardLaneReadContractResponse
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/lanes/BACKLOG?"+query.Encode(), nil, &page3)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET lane page 3: status=%d body=%s", resp.StatusCode, string(body))
	}
	if len(page3.Items) != 1 || page3.Items[0].Title != "Oldest matching" {
		t.Fatalf("unexpected lane page 3: %+v", page3)
	}
	if page3.HasMore || page3.NextCursor != nil {
		t.Fatalf("unexpected lane page 3 metadata: %+v", page3)
	}

	gotTitles := []string{backlog[0].Title, page2.Items[0].Title, page3.Items[0].Title}
	wantTitles := []string{"Newest matching", "Middle matching", "Oldest matching"}
	for i := range wantTitles {
		if gotTitles[i] != wantTitles[i] {
			t.Fatalf("filtered pagination order = %v, want %v", gotTitles, wantTitles)
		}
	}
}

func TestBoardLaneRead_RESTNoCursorUsesCorrectSortBoundary(t *testing.T) {
	baseURL, slug, client := newBoardLaneCursorFixture(
		t,
		"board-lane-no-cursor@example.com",
		"Board Lane No Cursor",
	)

	tests := []struct {
		name       string
		sort       string
		wantTitles []string
	}{
		{name: "default ascending", wantTitles: []string{"First", "Second"}},
		{name: "oldest ascending", sort: "oldest", wantTitles: []string{"First", "Second"}},
		{name: "newest descending", sort: "newest", wantTitles: []string{"Third", "Second"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{"limit": {"2"}}
			if tc.sort != "" {
				query.Set("sort", tc.sort)
			}

			var page boardLaneReadContractResponse
			resp, body := doJSON(
				t,
				client,
				http.MethodGet,
				baseURL+"/api/board/"+slug+"/lanes/BACKLOG?"+query.Encode(),
				nil,
				&page,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET lane without cursor: status=%d body=%s", resp.StatusCode, string(body))
			}
			if len(page.Items) != len(tc.wantTitles) {
				t.Fatalf("items = %+v, want titles %v", page.Items, tc.wantTitles)
			}
			for i, want := range tc.wantTitles {
				if page.Items[i].Title != want {
					t.Fatalf("items = %+v, want titles %v", page.Items, tc.wantTitles)
				}
			}
			if !page.HasMore || page.NextCursor == nil || *page.NextCursor == "" {
				t.Fatalf("unexpected first-page metadata: %+v", page)
			}
		})
	}
}

func TestBoardLaneRead_RESTMalformedCursorPreservesCurrentContract(t *testing.T) {
	baseURL, slug, client := newBoardLaneCursorFixture(
		t,
		"board-lane-malformed-cursor@example.com",
		"Board Lane Malformed Cursor",
	)

	tests := []struct {
		name       string
		sort       string
		wantTitles []string
		wantMore   bool
		wantCursor bool
	}{
		{
			name:       "default treats parsed zero bound as first page",
			wantTitles: []string{"First", "Second"},
			wantMore:   true,
			wantCursor: true,
		},
		{
			name:       "oldest treats parsed zero bound as first page",
			sort:       "oldest",
			wantTitles: []string{"First", "Second"},
			wantMore:   true,
			wantCursor: true,
		},
		{
			name:       "newest treats parsed zero bound as exhausted",
			sort:       "newest",
			wantTitles: nil,
			wantMore:   false,
			wantCursor: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{
				"afterCursor": {"not-a-cursor"},
				"limit":       {"2"},
			}
			if tc.sort != "" {
				query.Set("sort", tc.sort)
			}

			var page boardLaneReadContractResponse
			resp, body := doJSON(
				t,
				client,
				http.MethodGet,
				baseURL+"/api/board/"+slug+"/lanes/BACKLOG?"+query.Encode(),
				nil,
				&page,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET lane with malformed cursor: status=%d body=%s", resp.StatusCode, string(body))
			}
			if len(page.Items) != len(tc.wantTitles) {
				t.Fatalf("items = %+v, want titles %v", page.Items, tc.wantTitles)
			}
			for i, want := range tc.wantTitles {
				if page.Items[i].Title != want {
					t.Fatalf("items = %+v, want titles %v", page.Items, tc.wantTitles)
				}
			}
			if page.HasMore != tc.wantMore {
				t.Fatalf("hasMore = %v, want %v", page.HasMore, tc.wantMore)
			}
			if (page.NextCursor != nil) != tc.wantCursor {
				t.Fatalf("nextCursor = %v, want present=%v", page.NextCursor, tc.wantCursor)
			}
			if page.NextCursor != nil && *page.NextCursor == "" {
				t.Fatal("nextCursor is present but empty")
			}
		})
	}
}
