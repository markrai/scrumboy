package board

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type recordingLegacyReadStore struct {
	calls int

	ctx            context.Context
	projectContext *store.ProjectContext
	tagFilter      string
	searchFilter   string
	assigneeFilter store.AssigneeFilter
	priorityFilter store.PriorityFilter
	sprintFilter   store.SprintFilter
	sortOrder      store.SortOrder

	project  store.Project
	tags     []store.TagCount
	workflow []store.WorkflowColumn
	columns  map[string][]store.Todo
	err      error

	errFromContext bool
}

type recordingLegacyReadAccessStore struct {
	calls int

	ctx       context.Context
	projectID int64
	mode      store.Mode

	projectContext store.ProjectContext
	err            error
}

func (s *recordingLegacyReadAccessStore) GetProjectContextForRead(
	ctx context.Context,
	projectID int64,
	mode store.Mode,
) (store.ProjectContext, error) {
	s.calls++
	s.ctx = ctx
	s.projectID = projectID
	s.mode = mode
	return s.projectContext, s.err
}

func (s *recordingLegacyReadStore) GetBoard(
	ctx context.Context,
	pc *store.ProjectContext,
	tagFilter string,
	searchFilter string,
	assigneeFilter store.AssigneeFilter,
	priorityFilter store.PriorityFilter,
	sprintFilter store.SprintFilter,
	sortOrder store.SortOrder,
) (
	store.Project,
	[]store.TagCount,
	[]store.WorkflowColumn,
	map[string][]store.Todo,
	error,
) {
	s.calls++
	s.ctx = ctx
	s.projectContext = pc
	s.tagFilter = tagFilter
	s.searchFilter = searchFilter
	s.assigneeFilter = assigneeFilter
	s.priorityFilter = priorityFilter
	s.sprintFilter = sprintFilter
	s.sortOrder = sortOrder

	if s.errFromContext {
		return s.project, s.tags, s.workflow, s.columns, ctx.Err()
	}
	return s.project, s.tags, s.workflow, s.columns, s.err
}

type readServiceContextKey struct{}

func TestPreparedSlugRead_ReadInitialDelegatesToExistingService(t *testing.T) {
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	priorityFilter, err := store.ParsePriorityFilter("urgent")
	if err != nil {
		t.Fatalf("ParsePriorityFilter: %v", err)
	}

	ctx := context.WithValue(context.Background(), readServiceContextKey{}, "initial")
	pc := &store.ProjectContext{Project: store.Project{ID: 7, Slug: "project-slug"}}
	query := Query{
		TagFilter:      "focus",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		PriorityFilter: priorityFilter,
		SprintFilter:   store.SprintFilter{Mode: "none"},
		SortOrder:      store.SortOrderNewest,
		LimitPerLane:   17,
	}
	initialStore := &recordingReadStore{
		project: pc.Project,
		tags:    []store.TagCount{{Name: "focus", Count: 1}},
		workflow: []store.WorkflowColumn{{
			Key:  store.DefaultColumnBacklog,
			Name: "Backlog",
		}},
		columns: map[string][]store.Todo{
			store.DefaultColumnBacklog: {{ID: 101}},
		},
		columnsMeta: map[string]store.LaneMeta{
			store.DefaultColumnBacklog: {TotalCount: 1},
		},
	}
	laneStore := &recordingLaneReadStore{}
	legacyAccessStore := &recordingLegacyReadAccessStore{}
	legacyStore := &recordingLegacyReadStore{}
	slugAccessStore := &recordingSlugReadAccessStore{projectContext: *pc}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareSlugRead(ctx, SlugReadTarget{Slug: pc.Project.Slug, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadInitial(query)
	if err != nil {
		t.Fatalf("ReadInitial: %v", err)
	}

	if initialStore.calls != 1 {
		t.Fatalf("GetBoardPaged calls = %d, want 1", initialStore.calls)
	}
	if laneStore.calls != 0 ||
		legacyAccessStore.calls != 0 ||
		legacyStore.calls != 0 ||
		slugAccessStore.calls != 1 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"port calls: lane=%d legacyAccess=%d legacy=%d slugAccess=%d slugSprints=%d, want slugAccess=1 and all others 0",
			laneStore.calls,
			legacyAccessStore.calls,
			legacyStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
	if initialStore.ctx != ctx || initialStore.projectContext != &prepared.projectContext {
		t.Fatal("ReadInitial did not use the bound context and owned project context")
	}
	if initialStore.projectContext == pc || !reflect.DeepEqual(*initialStore.projectContext, *pc) {
		t.Fatal("ReadInitial did not use a value-equivalent copy of the resolved project context")
	}
	if initialStore.tagFilter != query.TagFilter ||
		initialStore.searchFilter != query.SearchFilter ||
		!reflect.DeepEqual(initialStore.assigneeFilter, query.AssigneeFilter) ||
		!reflect.DeepEqual(initialStore.priorityFilter, query.PriorityFilter) ||
		!reflect.DeepEqual(initialStore.sprintFilter, query.SprintFilter) ||
		initialStore.sortOrder != query.SortOrder ||
		initialStore.limitPerLane != query.LimitPerLane {
		t.Fatalf("ReadInitial changed the normalized query: store=%+v query=%+v", initialStore, query)
	}
	want := Result{
		Project:     initialStore.project,
		Tags:        initialStore.tags,
		Workflow:    initialStore.workflow,
		Columns:     initialStore.columns,
		ColumnsMeta: initialStore.columnsMeta,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestPreparedSlugRead_ReadLaneDelegatesToExistingService(t *testing.T) {
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	priorityFilter, err := store.ParsePriorityFilter("urgent")
	if err != nil {
		t.Fatalf("ParsePriorityFilter: %v", err)
	}

	ctx := context.WithValue(context.Background(), readServiceContextKey{}, "lane")
	pc := &store.ProjectContext{Project: store.Project{ID: 7, Slug: "project-slug"}}
	query := LaneQuery{
		ColumnKey:      store.DefaultColumnBacklog,
		Limit:          17,
		AfterA:         301,
		AfterB:         302,
		TagFilter:      "focus",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		PriorityFilter: priorityFilter,
		SprintFilter:   store.SprintFilter{Mode: "none"},
		SortOrder:      store.SortOrderOldest,
	}
	initialStore := &recordingReadStore{}
	laneStore := &recordingLaneReadStore{
		items:      []store.Todo{{ID: 101}},
		nextCursor: "10:101",
		hasMore:    true,
	}
	legacyAccessStore := &recordingLegacyReadAccessStore{}
	legacyStore := &recordingLegacyReadStore{}
	slugAccessStore := &recordingSlugReadAccessStore{projectContext: *pc}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareSlugRead(ctx, SlugReadTarget{Slug: pc.Project.Slug, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadLane(query)
	if err != nil {
		t.Fatalf("ReadLane: %v", err)
	}

	if laneStore.calls != 1 {
		t.Fatalf("ListTodosForBoardLane calls = %d, want 1", laneStore.calls)
	}
	if initialStore.calls != 0 ||
		legacyAccessStore.calls != 0 ||
		legacyStore.calls != 0 ||
		slugAccessStore.calls != 1 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"port calls: initial=%d legacyAccess=%d legacy=%d slugAccess=%d slugSprints=%d, want slugAccess=1 and all others 0",
			initialStore.calls,
			legacyAccessStore.calls,
			legacyStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
	if laneStore.ctx != ctx || laneStore.projectID != pc.Project.ID {
		t.Fatal("ReadLane changed the context or project ID")
	}
	if laneStore.columnKey != query.ColumnKey ||
		laneStore.limit != query.Limit ||
		laneStore.afterA != query.AfterA ||
		laneStore.afterB != query.AfterB ||
		laneStore.tagFilter != query.TagFilter ||
		laneStore.searchFilter != query.SearchFilter ||
		!reflect.DeepEqual(laneStore.assigneeFilter, query.AssigneeFilter) ||
		!reflect.DeepEqual(laneStore.priorityFilter, query.PriorityFilter) ||
		!reflect.DeepEqual(laneStore.sprintFilter, query.SprintFilter) ||
		laneStore.sortOrder != query.SortOrder {
		t.Fatalf("ReadLane changed the normalized query: store=%+v query=%+v", laneStore, query)
	}
	want := LaneResult{
		Items:      laneStore.items,
		NextCursor: laneStore.nextCursor,
		HasMore:    laneStore.hasMore,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestReadServicePrepareLegacy_DelegatesAccessExactly(t *testing.T) {
	ctx := context.WithValue(context.Background(), readServiceContextKey{}, "legacy-access")
	target := LegacyReadTarget{
		ProjectID: 73,
		Mode:      store.ModeAnonymous,
	}
	wantProjectContext := store.ProjectContext{
		Project:     store.Project{ID: target.ProjectID, Slug: "prepared-project"},
		Role:        store.RoleViewer,
		AuthEnabled: true,
	}
	initialStore := &recordingReadStore{}
	laneStore := &recordingLaneReadStore{}
	legacyAccessStore := &recordingLegacyReadAccessStore{
		projectContext: wantProjectContext,
	}
	legacyStore := &recordingLegacyReadStore{}
	slugAccessStore := &recordingSlugReadAccessStore{}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareLegacy(ctx, target)
	if err != nil {
		t.Fatalf("PrepareLegacy: %v", err)
	}
	if prepared == nil {
		t.Fatal("PrepareLegacy returned a nil prepared read")
	}
	if legacyAccessStore.calls != 1 {
		t.Fatalf("GetProjectContextForRead calls = %d, want 1", legacyAccessStore.calls)
	}
	if legacyAccessStore.ctx != ctx {
		t.Fatal("PrepareLegacy did not forward the same context")
	}
	if legacyAccessStore.projectID != target.ProjectID {
		t.Fatalf("projectID = %d, want %d", legacyAccessStore.projectID, target.ProjectID)
	}
	if legacyAccessStore.mode != target.Mode {
		t.Fatalf("mode = %q, want %q", legacyAccessStore.mode, target.Mode)
	}
	if !reflect.DeepEqual(prepared.projectContext, wantProjectContext) {
		t.Fatalf(
			"prepared project context = %#v, want %#v",
			prepared.projectContext,
			wantProjectContext,
		)
	}
	if prepared.legacy != legacyStore {
		t.Fatal("prepared read did not retain the legacy data port")
	}
	if initialStore.calls != 0 ||
		laneStore.calls != 0 ||
		legacyStore.calls != 0 ||
		slugAccessStore.calls != 0 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"preparation called another port: initial=%d lane=%d legacy=%d slugAccess=%d slugSprints=%d",
			initialStore.calls,
			laneStore.calls,
			legacyStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
}

func TestReadServicePrepareLegacy_ReturnsAccessErrorUnchanged(t *testing.T) {
	sentinel := errors.New("sentinel")
	accessErr := fmt.Errorf("legacy board access failed: %w", sentinel)
	initialStore := &recordingReadStore{}
	laneStore := &recordingLaneReadStore{}
	legacyAccessStore := &recordingLegacyReadAccessStore{err: accessErr}
	legacyStore := &recordingLegacyReadStore{}
	slugAccessStore := &recordingSlugReadAccessStore{}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareLegacy(
		context.Background(),
		LegacyReadTarget{ProjectID: 73, Mode: store.ModeFull},
	)

	if err != accessErr {
		t.Fatalf("error = %v, want original access error %v", err, accessErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if prepared != nil {
		t.Fatalf("prepared read on error = %#v, want nil", prepared)
	}
	if legacyAccessStore.calls != 1 {
		t.Fatalf("GetProjectContextForRead calls = %d, want 1", legacyAccessStore.calls)
	}
	if initialStore.calls != 0 ||
		laneStore.calls != 0 ||
		legacyStore.calls != 0 ||
		slugAccessStore.calls != 0 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"access failure called another port: initial=%d lane=%d legacy=%d slugAccess=%d slugSprints=%d",
			initialStore.calls,
			laneStore.calls,
			legacyStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
}

func TestPreparedLegacyRead_DelegatesExactlyAndNamesResult(t *testing.T) {
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	priorityFilter, err := store.ParsePriorityFilter("urgent")
	if err != nil {
		t.Fatalf("ParsePriorityFilter: %v", err)
	}

	ctx := context.WithValue(context.Background(), readServiceContextKey{}, "legacy")
	pc := &store.ProjectContext{
		Project: store.Project{ID: 7, Slug: "project-slug", SprintsEnabled: true},
		Role:    store.RoleViewer,
	}
	query := LegacyQuery{
		TagFilter:      "make space",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		PriorityFilter: priorityFilter,
		SprintFilter:   store.SprintFilter{Mode: "sprint_number", SprintNumber: 3},
		SortOrder:      store.SortOrderNewest,
	}
	initialStore := &recordingReadStore{}
	laneStore := &recordingLaneReadStore{}
	legacyAccessStore := &recordingLegacyReadAccessStore{projectContext: *pc}
	legacyStore := &recordingLegacyReadStore{
		project:  pc.Project,
		tags:     []store.TagCount{{Name: "focus", Count: 2}},
		workflow: []store.WorkflowColumn{{Key: store.DefaultColumnBacklog, Name: "Backlog"}},
		columns: map[string][]store.Todo{
			store.DefaultColumnBacklog: {{ID: 101, Title: "Todo"}},
		},
	}
	priorityStore := &recordingPriorityReadStore{
		tiers: []store.PriorityTier{{Key: "high", Name: "High", Color: "#ff8800", Position: 0}},
	}
	slugAccessStore := &recordingSlugReadAccessStore{}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		Priorities:   priorityStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareLegacy(ctx, LegacyReadTarget{
		ProjectID: pc.Project.ID,
		Mode:      store.ModeFull,
	})
	if err != nil {
		t.Fatalf("PrepareLegacy: %v", err)
	}
	result, err := prepared.Read(query)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if legacyAccessStore.calls != 1 {
		t.Fatalf("GetProjectContextForRead calls = %d, want 1", legacyAccessStore.calls)
	}
	if legacyStore.calls != 1 {
		t.Fatalf("GetBoard calls = %d, want 1", legacyStore.calls)
	}
	if priorityStore.calls != 1 || priorityStore.ctx != ctx || priorityStore.projectID != pc.Project.ID {
		t.Fatalf("priority read calls=%d contextMatch=%v projectID=%d", priorityStore.calls, priorityStore.ctx == ctx, priorityStore.projectID)
	}
	if initialStore.calls != 0 ||
		laneStore.calls != 0 ||
		slugAccessStore.calls != 0 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"unexpected other-port calls: initial=%d lane=%d slugAccess=%d slugSprints=%d",
			initialStore.calls,
			laneStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
	if legacyAccessStore.ctx != ctx || legacyStore.ctx != ctx {
		t.Fatal("prepared legacy read did not use the same context for access and data")
	}
	if legacyStore.projectContext != &prepared.projectContext {
		t.Fatal("prepared legacy read did not use its owned project context")
	}
	if legacyStore.projectContext == pc {
		t.Fatal("prepared legacy read retained the HTTP-side project context pointer")
	}
	if !reflect.DeepEqual(*legacyStore.projectContext, *pc) {
		t.Fatal("prepared legacy read changed the resolved project context")
	}
	if legacyStore.tagFilter != query.TagFilter {
		t.Fatalf("tagFilter = %q, want %q", legacyStore.tagFilter, query.TagFilter)
	}
	if legacyStore.searchFilter != query.SearchFilter {
		t.Fatalf("searchFilter = %q, want %q", legacyStore.searchFilter, query.SearchFilter)
	}
	if !reflect.DeepEqual(legacyStore.assigneeFilter, query.AssigneeFilter) {
		t.Fatal("ReadLegacy changed the assignee filter")
	}
	if !reflect.DeepEqual(legacyStore.priorityFilter, query.PriorityFilter) {
		t.Fatal("ReadLegacy changed the priority filter")
	}
	if !reflect.DeepEqual(legacyStore.sprintFilter, query.SprintFilter) {
		t.Fatal("ReadLegacy changed the sprint filter")
	}
	if legacyStore.sortOrder != query.SortOrder {
		t.Fatalf("sortOrder = %q, want %q", legacyStore.sortOrder, query.SortOrder)
	}

	want := LegacyResult{
		Project:    legacyStore.project,
		Tags:       legacyStore.tags,
		Workflow:   legacyStore.workflow,
		Columns:    legacyStore.columns,
		Priorities: priorityStore.tiers,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestPreparedLegacyRead_RejectsSprintFilterWhenDisabledAfterAccess(t *testing.T) {
	access := &recordingLegacyReadAccessStore{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 73, SprintsEnabled: false},
			Role:    store.RoleViewer,
		},
	}
	legacy := &recordingLegacyReadStore{}
	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      &recordingReadStore{},
		Lane:         &recordingLaneReadStore{},
		LegacyAccess: access,
		Legacy:       legacy,
		SlugAccess:   &recordingSlugReadAccessStore{},
		SlugSprints:  &recordingSlugReadSprintStore{},
	}).PrepareLegacy(context.Background(), LegacyReadTarget{ProjectID: 73, Mode: store.ModeFull})
	if err != nil {
		t.Fatalf("PrepareLegacy: %v", err)
	}

	_, err = prepared.Read(LegacyQuery{SprintFilter: store.SprintFilter{Mode: "sprint_number", SprintNumber: 4}})
	if !errors.Is(err, store.ErrSprintsDisabled) {
		t.Fatalf("Read error = %v, want ErrSprintsDisabled", err)
	}
	if access.calls != 1 || legacy.calls != 0 {
		t.Fatalf("calls access=%d legacy=%d, want 1/0", access.calls, legacy.calls)
	}
}

func TestPreparedLegacyRead_ObservesCancellationAfterPreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	legacyAccessStore := &recordingLegacyReadAccessStore{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 73},
		},
	}
	legacyStore := &recordingLegacyReadStore{errFromContext: true}
	slugAccessStore := &recordingSlugReadAccessStore{}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      &recordingReadStore{},
		Lane:         &recordingLaneReadStore{},
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareLegacy(
		ctx,
		LegacyReadTarget{ProjectID: 73, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareLegacy: %v", err)
	}

	cancel()
	result, err := prepared.Read(LegacyQuery{})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if !reflect.DeepEqual(result, LegacyResult{}) {
		t.Fatalf("result on cancellation = %#v, want zero value", result)
	}
	if legacyAccessStore.ctx != ctx || legacyStore.ctx != ctx {
		t.Fatal("prepared read did not retain the exact preparation context")
	}
	if legacyStore.calls != 1 {
		t.Fatalf("GetBoard calls = %d, want 1", legacyStore.calls)
	}
	if slugAccessStore.calls != 0 || slugSprintStore.calls != 0 {
		t.Fatalf(
			"legacy read called slug ports: access=%d sprints=%d",
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
}

func TestPreparedLegacyRead_ReturnsStoreErrorUnchanged(t *testing.T) {
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("legacy board read failed: %w", sentinel)
	ctx := context.Background()
	initialStore := &recordingReadStore{}
	laneStore := &recordingLaneReadStore{}
	legacyAccessStore := &recordingLegacyReadAccessStore{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 73},
		},
	}
	legacyStore := &recordingLegacyReadStore{err: storeErr}
	slugAccessStore := &recordingSlugReadAccessStore{}
	slugSprintStore := &recordingSlugReadSprintStore{}

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initialStore,
		Lane:         laneStore,
		LegacyAccess: legacyAccessStore,
		Legacy:       legacyStore,
		SlugAccess:   slugAccessStore,
		SlugSprints:  slugSprintStore,
	}).PrepareLegacy(
		ctx,
		LegacyReadTarget{ProjectID: 73, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareLegacy: %v", err)
	}
	result, err := prepared.Read(LegacyQuery{})

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if !reflect.DeepEqual(result, LegacyResult{}) {
		t.Fatalf("result on error = %#v, want zero value", result)
	}
	if legacyStore.calls != 1 {
		t.Fatalf("GetBoard calls = %d, want 1", legacyStore.calls)
	}
	if legacyAccessStore.calls != 1 {
		t.Fatalf("GetProjectContextForRead calls = %d, want 1", legacyAccessStore.calls)
	}
	if legacyAccessStore.ctx != ctx || legacyStore.ctx != ctx {
		t.Fatal("prepared read did not retain the exact preparation context")
	}
	if initialStore.calls != 0 ||
		laneStore.calls != 0 ||
		slugAccessStore.calls != 0 ||
		slugSprintStore.calls != 0 {
		t.Fatalf(
			"unexpected other-port calls: initial=%d lane=%d slugAccess=%d slugSprints=%d",
			initialStore.calls,
			laneStore.calls,
			slugAccessStore.calls,
			slugSprintStore.calls,
		)
	}
}
