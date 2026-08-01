package board

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type recordingLaneReadStore struct {
	calls int

	ctx            context.Context
	projectID      int64
	columnKey      string
	limit          int
	afterA         int64
	afterB         int64
	tagFilter      string
	searchFilter   string
	assigneeFilter store.AssigneeFilter
	sprintFilter   store.SprintFilter
	sortOrder      store.SortOrder

	items      []store.Todo
	nextCursor string
	hasMore    bool
	err        error
}

func (s *recordingLaneReadStore) ListTodosForBoardLane(
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
	s.calls++
	s.ctx = ctx
	s.projectID = projectID
	s.columnKey = columnKey
	s.limit = limit
	s.afterA = afterA
	s.afterB = afterB
	s.tagFilter = tagFilter
	s.searchFilter = searchFilter
	s.assigneeFilter = assigneeFilter
	s.sprintFilter = sprintFilter
	s.sortOrder = sortOrder

	return s.items, s.nextCursor, s.hasMore, s.err
}

type laneReadContextKey struct{}

func TestLaneServiceRead_DelegatesExactlyAndNamesResult(t *testing.T) {
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}

	ctx := context.WithValue(context.Background(), laneReadContextKey{}, "request")
	pc := &store.ProjectContext{
		Project: store.Project{ID: 7, Slug: "project-slug"},
		Role:    store.RoleViewer,
	}
	query := LaneQuery{
		ColumnKey:      store.DefaultColumnBacklog,
		Limit:          17,
		AfterA:         301,
		AfterB:         302,
		TagFilter:      "make space",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		SprintFilter:   store.SprintFilter{Mode: "sprint_number", SprintNumber: 3},
		SortOrder:      store.SortOrderNewest,
	}

	readStore := &recordingLaneReadStore{
		items:      []store.Todo{{ID: 101, Title: "Todo"}},
		nextCursor: "10:101",
		hasMore:    true,
	}

	result, err := NewLaneService(readStore).Read(ctx, pc, query)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if readStore.calls != 1 {
		t.Fatalf("ListTodosForBoardLane calls = %d, want 1", readStore.calls)
	}
	if readStore.ctx != ctx {
		t.Fatal("Read did not forward the same context")
	}
	if readStore.projectID != pc.Project.ID {
		t.Fatalf("projectID = %d, want %d", readStore.projectID, pc.Project.ID)
	}
	if readStore.columnKey != query.ColumnKey {
		t.Fatalf("columnKey = %q, want %q", readStore.columnKey, query.ColumnKey)
	}
	if readStore.limit != query.Limit {
		t.Fatalf("limit = %d, want %d", readStore.limit, query.Limit)
	}
	if readStore.afterA != query.AfterA || readStore.afterB != query.AfterB {
		t.Fatalf("cursor = (%d, %d), want (%d, %d)", readStore.afterA, readStore.afterB, query.AfterA, query.AfterB)
	}
	if readStore.tagFilter != query.TagFilter {
		t.Fatalf("tagFilter = %q, want %q", readStore.tagFilter, query.TagFilter)
	}
	if readStore.searchFilter != query.SearchFilter {
		t.Fatalf("searchFilter = %q, want %q", readStore.searchFilter, query.SearchFilter)
	}
	if !reflect.DeepEqual(readStore.assigneeFilter, query.AssigneeFilter) {
		t.Fatal("Read changed the assignee filter")
	}
	if !reflect.DeepEqual(readStore.sprintFilter, query.SprintFilter) {
		t.Fatal("Read changed the sprint filter")
	}
	if readStore.sortOrder != query.SortOrder {
		t.Fatalf("sortOrder = %q, want %q", readStore.sortOrder, query.SortOrder)
	}

	want := LaneResult{
		Items:      readStore.items,
		NextCursor: readStore.nextCursor,
		HasMore:    readStore.hasMore,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestLaneServiceRead_ReturnsStoreErrorUnchanged(t *testing.T) {
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("lane read failed: %w", sentinel)
	readStore := &recordingLaneReadStore{err: storeErr}

	result, err := NewLaneService(readStore).Read(context.Background(), &store.ProjectContext{}, LaneQuery{})

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if !reflect.DeepEqual(result, LaneResult{}) {
		t.Fatalf("result on error = %#v, want zero value", result)
	}
	if readStore.calls != 1 {
		t.Fatalf("ListTodosForBoardLane calls = %d, want 1", readStore.calls)
	}
}
