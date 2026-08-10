package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type legacyTodoProjectionFailureStore struct {
	storeAPI
	failingProjectID atomic.Int64
	projectionReads  atomic.Int64
}

func (s *legacyTodoProjectionFailureStore) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	if projectID == s.failingProjectID.Load() {
		s.projectionReads.Add(1)
		return store.Project{}, errors.New("forced post-mutation project projection failure")
	}
	return s.storeAPI.GetProject(ctx, projectID)
}

func newLegacyTodoProjectionFailureServer(t *testing.T) (*httptest.Server, *sql.DB, *store.Store, *legacyTodoProjectionFailureStore, func()) {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	failing := &legacyTodoProjectionFailureStore{storeAPI: st}
	srv := NewServer(failing, Options{MaxRequestBody: 1 << 20, ScrumboyMode: "full"})
	st.SetTodoAssignedPublisher(srv.PublishTodoAssigned)
	ts := httptest.NewServer(srv)
	return ts, sqlDB, st, failing, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func TestLegacyNumericTodoMutationsSucceedWhenPostWriteProjectionFails(t *testing.T) {
	ts, sqlDB, st, failing, cleanup := newLegacyTodoProjectionFailureServer(t)
	defer cleanup()

	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Owner", "legacy-projection-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	project, ownerCtx := createTodoUpdateProject(t, st, ownerID, "Legacy projection failure")
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ownerCtx, project.ID, "Dormant sprint", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	patchTodo, err := st.CreateTodo(ownerCtx, project.ID, store.CreateTodoInput{
		Title: "patch before", ColumnKey: store.DefaultColumnBacklog, SprintID: &sprint.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo patch fixture: %v", err)
	}
	moveTodo, err := st.CreateTodo(ownerCtx, project.ID, store.CreateTodoInput{
		Title: "move before", ColumnKey: store.DefaultColumnBacklog, SprintID: &sprint.ID,
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo move fixture: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ownerCtx, project.ID, ownerID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}
	failing.failingProjectID.Store(project.ID)

	t.Run("patch", func(t *testing.T) {
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		payload := todoUpdateRESTPayload("patch after", patchTodo.Body, patchTodo.Tags, patchTodo.EstimationPoints, patchTodo.AssigneeUserID)
		var response map[string]any
		resp, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", ts.URL, patchTodo.ID), payload, &response)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", resp.StatusCode, body)
		}
		if _, exposed := response["sprintId"]; exposed {
			t.Fatalf("PATCH exposed dormant sprintId after projection failure: %+v", response)
		}
		var title string
		var sprintID sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT title, sprint_id FROM todos WHERE id = ?`, patchTodo.ID).Scan(&title, &sprintID); err != nil {
			t.Fatalf("read patched todo: %v", err)
		}
		if title != "patch after" || !sprintID.Valid || sprintID.Int64 != sprint.ID {
			t.Fatalf("persisted patch title=%q sprint=%+v, want committed title and dormant sprint %d", title, sprintID, sprint.ID)
		}
		assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_updated", 0)
	})

	t.Run("move", func(t *testing.T) {
		stream := subscribeTodoUpdateEvents(t, client, ts.URL+"/api/board/"+project.Slug+"/events")
		var response map[string]any
		resp, body := doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/todos/%d/move", ts.URL, moveTodo.ID), map[string]any{
			"toColumnKey": store.DefaultColumnDoing,
		}, &response)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("move status=%d body=%s", resp.StatusCode, body)
		}
		if _, exposed := response["sprintId"]; exposed {
			t.Fatalf("move exposed dormant sprintId after projection failure: %+v", response)
		}
		var columnKey string
		var sprintID sql.NullInt64
		if err := sqlDB.QueryRow(`SELECT column_key, sprint_id FROM todos WHERE id = ?`, moveTodo.ID).Scan(&columnKey, &sprintID); err != nil {
			t.Fatalf("read moved todo: %v", err)
		}
		if columnKey != store.DefaultColumnDoing || !sprintID.Valid || sprintID.Int64 != sprint.ID {
			t.Fatalf("persisted move column=%q sprint=%+v, want committed move and dormant sprint %d", columnKey, sprintID, sprint.ID)
		}
		assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_moved", 0)
	})

	if got := failing.projectionReads.Load(); got != 2 {
		t.Fatalf("post-mutation projection reads=%d want=2", got)
	}
}
