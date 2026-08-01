package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

func encodeBoardCursor(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

type boardGetStoreCall struct {
	Operation string
	Context   context.Context

	Slug      string
	Mode      store.Mode
	ProjectID int64
	SprintID  int64
	ColumnKey string
	Limit     int
	AfterA    int64
	AfterB    int64
	Tag       string
	Search    string
	Assignee  store.AssigneeFilter
	Sprint    store.SprintFilter
	Sort      store.SortOrder
	ResultErr error
}

type boardGetListResult struct {
	Todos   []store.Todo
	Cursor  string
	HasMore bool
}

type recordingBoardGetStore struct {
	*store.Store

	Calls        []boardGetStoreCall
	Errors       map[string]error
	ListResults  map[string]boardGetListResult
	CountResults map[string]int
}

func newRecordingBoardGetStore(st *store.Store) *recordingBoardGetStore {
	return &recordingBoardGetStore{
		Store:        st,
		Errors:       make(map[string]error),
		ListResults:  make(map[string]boardGetListResult),
		CountResults: make(map[string]int),
	}
}

func (s *recordingBoardGetStore) reset() {
	s.Calls = nil
}

func (s *recordingBoardGetStore) operationNames() []string {
	out := make([]string, 0, len(s.Calls))
	for _, call := range s.Calls {
		out = append(out, call.Operation)
	}
	return out
}

func (s *recordingBoardGetStore) callsFor(operation string) []boardGetStoreCall {
	out := make([]boardGetStoreCall, 0)
	for _, call := range s.Calls {
		if call.Operation == operation {
			out = append(out, call)
		}
	}
	return out
}

func (s *recordingBoardGetStore) injectedError(operation, columnKey string) error {
	if columnKey != "" {
		if err := s.Errors[operation+":"+columnKey]; err != nil {
			return err
		}
	}
	return s.Errors[operation]
}

func (s *recordingBoardGetStore) CountUsers(ctx context.Context) (int, error) {
	n, err := s.Store.CountUsers(ctx)
	if injected := s.injectedError("countUsers", ""); injected != nil {
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "countUsers",
		Context:   ctx,
		ResultErr: err,
	})
	return n, err
}

func (s *recordingBoardGetStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	pc, err := s.Store.GetProjectContextBySlug(ctx, slug, mode)
	if injected := s.injectedError("access", ""); injected != nil {
		pc = store.ProjectContext{}
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "access",
		Context:   ctx,
		Slug:      slug,
		Mode:      mode,
		ResultErr: err,
	})
	return pc, err
}

func (s *recordingBoardGetStore) GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error) {
	sp, err := s.Store.GetSprintByID(ctx, sprintID)
	if injected := s.injectedError("sprint", ""); injected != nil {
		sp = store.Sprint{}
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "sprint",
		Context:   ctx,
		SprintID:  sprintID,
		ResultErr: err,
	})
	return sp, err
}

func (s *recordingBoardGetStore) GetProjectWorkflow(ctx context.Context, projectID int64) ([]store.WorkflowColumn, error) {
	workflow, err := s.Store.GetProjectWorkflow(ctx, projectID)
	if injected := s.injectedError("workflow", ""); injected != nil {
		workflow = nil
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "workflow",
		Context:   ctx,
		ProjectID: projectID,
		ResultErr: err,
	})
	return workflow, err
}

func (s *recordingBoardGetStore) ListTodosForBoardLane(
	ctx context.Context,
	projectID int64,
	columnKey string,
	limit int,
	afterA int64,
	afterB int64,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	sprintFilter store.SprintFilter,
	sortOrder store.SortOrder,
) ([]store.Todo, string, bool, error) {
	var (
		todos   []store.Todo
		cursor  string
		hasMore bool
		err     error
	)
	if result, ok := s.ListResults[columnKey]; ok {
		todos = append([]store.Todo(nil), result.Todos...)
		cursor = result.Cursor
		hasMore = result.HasMore
	} else {
		todos, cursor, hasMore, err = s.Store.ListTodosForBoardLane(
			ctx,
			projectID,
			columnKey,
			limit,
			afterA,
			afterB,
			tagFilter,
			searchFilter,
			assigneeFilter,
			sprintFilter,
			sortOrder,
		)
	}
	if injected := s.injectedError("list", columnKey); injected != nil {
		todos = nil
		cursor = ""
		hasMore = false
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "list",
		Context:   ctx,
		ProjectID: projectID,
		ColumnKey: columnKey,
		Limit:     limit,
		AfterA:    afterA,
		AfterB:    afterB,
		Tag:       tagFilter,
		Search:    searchFilter,
		Assignee:  assigneeFilter,
		Sprint:    sprintFilter,
		Sort:      sortOrder,
		ResultErr: err,
	})
	return todos, cursor, hasMore, err
}

func (s *recordingBoardGetStore) CountTodosForBoardLane(
	ctx context.Context,
	projectID int64,
	columnKey string,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	sprintFilter store.SprintFilter,
) (int, error) {
	var (
		count int
		err   error
	)
	if result, ok := s.CountResults[columnKey]; ok {
		count = result
	} else {
		count, err = s.Store.CountTodosForBoardLane(
			ctx,
			projectID,
			columnKey,
			tagFilter,
			searchFilter,
			assigneeFilter,
			sprintFilter,
		)
	}
	if injected := s.injectedError("count", columnKey); injected != nil {
		count = 0
		err = injected
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "count",
		Context:   ctx,
		ProjectID: projectID,
		ColumnKey: columnKey,
		Tag:       tagFilter,
		Search:    searchFilter,
		Assignee:  assigneeFilter,
		Sprint:    sprintFilter,
		ResultErr: err,
	})
	return count, err
}

func (s *recordingBoardGetStore) UpdateBoardActivity(ctx context.Context, projectID int64) error {
	err := s.injectedError("activity", "")
	if err == nil {
		err = s.Store.UpdateBoardActivity(ctx, projectID)
	}
	s.Calls = append(s.Calls, boardGetStoreCall{
		Operation: "activity",
		Context:   ctx,
		ProjectID: projectID,
		ResultErr: err,
	})
	return err
}

type boardGetContractHarness struct {
	DB        *sql.DB
	Store     *store.Store
	Recording *recordingBoardGetStore
	Adapter   *Adapter
	Owner     store.User
	Other     store.User
	Project   store.Project
	Context   context.Context
	Logs      *bytes.Buffer
}

func newBoardGetContractHarness(t *testing.T) *boardGetContractHarness {
	t.Helper()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	st := store.New(sqlDB, nil)
	owner, err := st.CreateUser(context.Background(), "phase7-owner@example.com", "password123", "Phase 7 Owner")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	other, err := st.CreateUser(context.Background(), "phase7-other@example.com", "password123", "Phase 7 Other")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	ctx := store.WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProjectWithWorkflow(ctx, "Phase 7 Board", []store.WorkflowColumn{
		{Key: "triage", Name: "Triage", Color: "#64748B", Position: 0},
		{Key: "building", Name: "Building", Color: "#3B82F6", Position: 1},
		{Key: "shipped", Name: "Shipped", Color: "#22C55E", Position: 2, IsDone: true},
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	recording := newRecordingBoardGetStore(st)
	logs := &bytes.Buffer{}
	return &boardGetContractHarness{
		DB:        sqlDB,
		Store:     st,
		Recording: recording,
		Adapter: New(recording, Options{
			Mode:   "full",
			Logger: log.New(logs, "", 0),
		}),
		Owner:   owner,
		Other:   other,
		Project: project,
		Context: ctx,
		Logs:    logs,
	}
}

func (h *boardGetContractHarness) call(input any) (any, map[string]any, *adapterError) {
	h.Recording.reset()
	return h.Adapter.handleBoardGet(h.Context, input)
}

func requireBoardGetError(
	t *testing.T,
	err *adapterError,
	status int,
	code string,
	message string,
	details map[string]any,
) {
	t.Helper()
	if err == nil {
		t.Fatal("expected board_get error")
	}
	if err.Status != status || err.Code != code || err.Message != message {
		t.Fatalf("error = (%d, %q, %q), want (%d, %q, %q)", err.Status, err.Code, err.Message, status, code, message)
	}
	actual, ok := normalizeDetails(err.Details).(map[string]any)
	if !ok {
		t.Fatalf("error details type = %T, want map[string]any", normalizeDetails(err.Details))
	}
	if len(actual) != len(details) {
		t.Fatalf("error details = %#v, want %#v", actual, details)
	}
	for key, want := range details {
		if actual[key] != want {
			t.Fatalf("error detail %q = %#v, want %#v (all=%#v)", key, actual[key], want, actual)
		}
	}
}

func requireOperationNames(t *testing.T, recording *recordingBoardGetStore, want ...string) {
	t.Helper()
	got := recording.operationNames()
	if len(got) != len(want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	}
}

func requireAllBoardGetContexts(t *testing.T, recording *recordingBoardGetStore, want context.Context) {
	t.Helper()
	for _, call := range recording.Calls {
		if call.Context != want {
			t.Fatalf("%s context identity changed: got %p, want %p", call.Operation, call.Context, want)
		}
	}
}

func injectedBoardGetError(operation string) error {
	return errors.New("phase 7 injected " + operation + " failure")
}

func boardGetTodo(id, projectID, rank int64, columnKey, title string, createdAt time.Time) store.Todo {
	return store.Todo{
		ID:        id,
		ProjectID: projectID,
		LocalID:   id + 100,
		Title:     title,
		ColumnKey: columnKey,
		Rank:      rank,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}
