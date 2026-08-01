package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardSlugRead_RESTValidationOrderContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		client,
		ts.URL,
		"Owner",
		"board-slug-validation-order@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	project, err := st.CreateProject(ctxOwner, "Slug Validation Order")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for i := 0; i < 21; i++ {
		seedSlugBoardTodo(
			t,
			st,
			ctxOwner,
			project.ID,
			"validation-order todo "+time.Unix(int64(i), 0).UTC().Format("150405"),
			store.ModeFull,
		)
	}

	initialURL := ts.URL + "/api/board/" + project.Slug
	laneURL := initialURL + "/lanes/BACKLOG"

	t.Run("initial", func(t *testing.T) {
		t.Run("assignee precedes sort and sprint", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				initialURL+"?assignee=abc&sort=sideways&sprintId=bad",
				"assignee",
				"invalid_assignee",
				"invalid assignee",
			)
		})
		t.Run("sort precedes sprint", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				initialURL+"?sort=sideways&sprintId=bad",
				"sort",
				"invalid_sort",
				"invalid sort",
			)
		})
		t.Run("malformed sprint is ignored when project has no sprints", func(t *testing.T) {
			var board slugBoardReadResponse
			resp, body := doJSON(
				t,
				client,
				http.MethodGet,
				initialURL+"?sprintId=bad",
				nil,
				&board,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET initial board without sprints: status=%d body=%s", resp.StatusCode, string(body))
			}
			if board.Project.ID != project.ID {
				t.Fatalf("project ID = %d, want %d", board.Project.ID, project.ID)
			}
		})
		t.Run("non-positive limit uses the existing default", func(t *testing.T) {
			var board slugBoardReadResponse
			resp, body := doJSON(
				t,
				client,
				http.MethodGet,
				initialURL+"?limitPerLane=0",
				nil,
				&board,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET initial board with defaulted limit: status=%d body=%s", resp.StatusCode, string(body))
			}
			backlog := board.Columns[store.DefaultColumnBacklog]
			meta := board.ColumnsMeta[store.DefaultColumnBacklog]
			if len(backlog) != 20 || meta.TotalCount != 21 || !meta.HasMore {
				t.Fatalf("defaulted initial page: items=%d meta=%+v, want 20 of 21 with hasMore", len(backlog), meta)
			}
		})

		start := time.Now().UTC()
		if _, err := st.CreateSprint(
			ctxOwner,
			project.ID,
			"Validation Gate Sprint",
			start,
			start.Add(14*24*time.Hour),
		); err != nil {
			t.Fatalf("CreateSprint: %v", err)
		}

		t.Run("malformed sprint is rejected once project has a sprint", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				initialURL+"?sprintId=bad",
				"sprintId",
				"invalid_sprint_id",
				`invalid sprintId: "bad"`,
			)
		})
	})

	t.Run("lane", func(t *testing.T) {
		t.Run("assignee precedes sprint and sort", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				laneURL+"?assignee=abc&sprintId=bad&sort=sideways",
				"assignee",
				"invalid_assignee",
				"invalid assignee",
			)
		})
		t.Run("sprint precedes sort", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				laneURL+"?sprintId=bad&sort=sideways",
				"sprintId",
				"invalid_sprint_id",
				`invalid sprintId: "bad"`,
			)
		})
		t.Run("sort precedes cursor handling", func(t *testing.T) {
			assertSlugBoardValidationError(
				t,
				client,
				laneURL+"?sort=sideways&afterCursor=malformed",
				"sort",
				"invalid_sort",
				"invalid sort",
			)
		})
		t.Run("out-of-range limit uses the existing default", func(t *testing.T) {
			var page slugBoardLaneReadResponse
			resp, body := doJSON(
				t,
				client,
				http.MethodGet,
				laneURL+"?limit=101",
				nil,
				&page,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET lane with defaulted limit: status=%d body=%s", resp.StatusCode, string(body))
			}
			if len(page.Items) != 20 || !page.HasMore {
				t.Fatalf("defaulted lane page: items=%d hasMore=%v, want 20 with hasMore", len(page.Items), page.HasMore)
			}
		})
	})
}
