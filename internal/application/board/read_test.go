package board

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type recordingReadStore struct {
	calls int

	ctx            context.Context
	projectContext *store.ProjectContext
	tagFilter      string
	searchFilter   string
	assigneeFilter store.AssigneeFilter
	sprintFilter   store.SprintFilter
	sortOrder      store.SortOrder
	limitPerLane   int

	project     store.Project
	tags        []store.TagCount
	workflow    []store.WorkflowColumn
	columns     map[string][]store.Todo
	columnsMeta map[string]store.LaneMeta
	err         error
}

type recordingPriorityReadStore struct {
	calls     int
	ctx       context.Context
	projectID int64
	tiers     []store.PriorityTier
	err       error
}

func (s *recordingPriorityReadStore) GetProjectPriorities(ctx context.Context, projectID int64) ([]store.PriorityTier, error) {
	s.calls++
	s.ctx = ctx
	s.projectID = projectID
	return s.tiers, s.err
}

func (s *recordingReadStore) GetBoardPaged(
	ctx context.Context,
	pc *store.ProjectContext,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	sprintFilter store.SprintFilter,
	sortOrder store.SortOrder,
	limitPerLane int,
) (
	store.Project,
	[]store.TagCount,
	[]store.WorkflowColumn,
	map[string][]store.Todo,
	map[string]store.LaneMeta,
	error,
) {
	s.calls++
	s.ctx = ctx
	s.projectContext = pc
	s.tagFilter = tagFilter
	s.searchFilter = searchFilter
	s.assigneeFilter = assigneeFilter
	s.sprintFilter = sprintFilter
	s.sortOrder = sortOrder
	s.limitPerLane = limitPerLane

	return s.project, s.tags, s.workflow, s.columns, s.columnsMeta, s.err
}

type readContextKey struct{}

func TestServiceReadInitial_DelegatesExactlyAndNamesResult(t *testing.T) {
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}

	ctx := context.WithValue(context.Background(), readContextKey{}, "request")
	pc := &store.ProjectContext{
		Project: store.Project{ID: 7, Slug: "project-slug"},
		Role:    store.RoleViewer,
	}
	query := Query{
		TagFilter:      "make space",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		SprintFilter:   store.SprintFilter{Mode: "sprint_number", SprintNumber: 3},
		SortOrder:      store.SortOrderNewest,
		LimitPerLane:   17,
	}

	readStore := &recordingReadStore{
		project:  store.Project{ID: 7, Slug: "project-slug"},
		tags:     []store.TagCount{{Name: "focus", Count: 2}},
		workflow: []store.WorkflowColumn{{Key: store.DefaultColumnBacklog, Name: "Backlog"}},
		columns: map[string][]store.Todo{
			store.DefaultColumnBacklog: {{ID: 101, Title: "Todo"}},
		},
		columnsMeta: map[string]store.LaneMeta{
			store.DefaultColumnBacklog: {HasMore: true, NextCursor: "10:101", TotalCount: 2},
		},
	}
	priorityStore := &recordingPriorityReadStore{
		tiers: []store.PriorityTier{{Key: "urgent", Name: "Urgent", Color: "#ff0000", Position: 0}},
	}

	result, err := NewService(readStore, priorityStore).ReadInitial(ctx, pc, query)
	if err != nil {
		t.Fatalf("ReadInitial: %v", err)
	}

	if readStore.calls != 1 {
		t.Fatalf("GetBoardPaged calls = %d, want 1", readStore.calls)
	}
	if readStore.ctx != ctx {
		t.Fatal("ReadInitial did not forward the same context")
	}
	if readStore.projectContext != pc {
		t.Fatal("ReadInitial did not forward the same project context pointer")
	}
	if readStore.tagFilter != query.TagFilter {
		t.Fatalf("tagFilter = %q, want %q", readStore.tagFilter, query.TagFilter)
	}
	if readStore.searchFilter != query.SearchFilter {
		t.Fatalf("searchFilter = %q, want %q", readStore.searchFilter, query.SearchFilter)
	}
	if !reflect.DeepEqual(readStore.assigneeFilter, query.AssigneeFilter) {
		t.Fatal("ReadInitial changed the assignee filter")
	}
	if !reflect.DeepEqual(readStore.sprintFilter, query.SprintFilter) {
		t.Fatal("ReadInitial changed the sprint filter")
	}
	if readStore.sortOrder != query.SortOrder {
		t.Fatalf("sortOrder = %q, want %q", readStore.sortOrder, query.SortOrder)
	}
	if readStore.limitPerLane != query.LimitPerLane {
		t.Fatalf("limitPerLane = %d, want %d", readStore.limitPerLane, query.LimitPerLane)
	}
	if priorityStore.calls != 1 || priorityStore.ctx != ctx || priorityStore.projectID != pc.Project.ID {
		t.Fatalf("priority read calls=%d contextMatch=%v projectID=%d", priorityStore.calls, priorityStore.ctx == ctx, priorityStore.projectID)
	}

	want := Result{
		Project:     readStore.project,
		Tags:        readStore.tags,
		Workflow:    readStore.workflow,
		Columns:     readStore.columns,
		ColumnsMeta: readStore.columnsMeta,
		Priorities:  priorityStore.tiers,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestServiceReadInitial_ReturnsPriorityReadErrorUnchanged(t *testing.T) {
	sentinel := errors.New("priority sentinel")
	priorityErr := fmt.Errorf("priority read failed: %w", sentinel)
	readStore := &recordingReadStore{project: store.Project{ID: 999}}
	priorityStore := &recordingPriorityReadStore{err: priorityErr}
	pc := &store.ProjectContext{Project: store.Project{ID: 7}}

	result, err := NewService(readStore, priorityStore).ReadInitial(context.Background(), pc, Query{})
	if err != priorityErr || !errors.Is(err, sentinel) {
		t.Fatalf("error=%v want original %v", err, priorityErr)
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("result=%#v want zero", result)
	}
	if priorityStore.calls != 1 || priorityStore.projectID != 7 {
		t.Fatalf("priority calls=%d projectID=%d", priorityStore.calls, priorityStore.projectID)
	}
}

func TestServiceReadInitial_ReturnsStoreErrorUnchanged(t *testing.T) {
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("board read failed: %w", sentinel)
	readStore := &recordingReadStore{err: storeErr}

	result, err := NewService(readStore).ReadInitial(context.Background(), &store.ProjectContext{}, Query{})

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("result on error = %#v, want zero value", result)
	}
	if readStore.calls != 1 {
		t.Fatalf("GetBoardPaged calls = %d, want 1", readStore.calls)
	}
}
