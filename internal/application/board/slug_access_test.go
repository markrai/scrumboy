package board

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type recordingSlugReadAccessStore struct {
	calls int

	ctx  context.Context
	slug string
	mode store.Mode

	projectContext store.ProjectContext
	err            error
}

func (s *recordingSlugReadAccessStore) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	s.calls++
	s.ctx = ctx
	s.slug = slug
	s.mode = mode
	return s.projectContext, s.err
}

type recordingSlugReadSprintStore struct {
	calls int

	ctx       context.Context
	projectID int64

	hasSprints     bool
	err            error
	errFromContext bool
}

func (s *recordingSlugReadSprintStore) HasSprints(
	ctx context.Context,
	projectID int64,
) (bool, error) {
	s.calls++
	s.ctx = ctx
	s.projectID = projectID
	if s.errFromContext {
		return false, ctx.Err()
	}
	return s.hasSprints, s.err
}

type cancellationReadStore struct {
	recordingReadStore
}

func (s *cancellationReadStore) GetBoardPaged(
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
	project, tags, workflow, columns, columnsMeta, _ := s.recordingReadStore.GetBoardPaged(
		ctx,
		pc,
		tagFilter,
		searchFilter,
		assigneeFilter,
		sprintFilter,
		sortOrder,
		limitPerLane,
	)
	return project, tags, workflow, columns, columnsMeta, ctx.Err()
}

type cancellationLaneReadStore struct {
	recordingLaneReadStore
}

func (s *cancellationLaneReadStore) ListTodosForBoardLane(
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
	items, nextCursor, hasMore, _ := s.recordingLaneReadStore.ListTodosForBoardLane(
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
	return items, nextCursor, hasMore, ctx.Err()
}

type slugReadTestHarness struct {
	initial      *recordingReadStore
	lane         *recordingLaneReadStore
	legacyAccess *recordingLegacyReadAccessStore
	legacy       *recordingLegacyReadStore
	access       *recordingSlugReadAccessStore
	sprints      *recordingSlugReadSprintStore
}

func newSlugReadTestHarness() *slugReadTestHarness {
	return &slugReadTestHarness{
		initial:      &recordingReadStore{},
		lane:         &recordingLaneReadStore{},
		legacyAccess: &recordingLegacyReadAccessStore{},
		legacy:       &recordingLegacyReadStore{},
		access:       &recordingSlugReadAccessStore{},
		sprints:      &recordingSlugReadSprintStore{},
	}
}

func (h *slugReadTestHarness) service() *ReadService {
	return NewReadService(ReadServiceDependencies{
		Initial:      h.initial,
		Lane:         h.lane,
		LegacyAccess: h.legacyAccess,
		Legacy:       h.legacy,
		SlugAccess:   h.access,
		SlugSprints:  h.sprints,
	})
}

func (h *slugReadTestHarness) assertLegacyUnused(t *testing.T) {
	t.Helper()
	if h.legacyAccess.calls != 0 || h.legacy.calls != 0 {
		t.Fatalf(
			"slug read called legacy ports: access=%d data=%d",
			h.legacyAccess.calls,
			h.legacy.calls,
		)
	}
}

type slugReadContextKey struct{}

func TestReadServicePrepareSlugRead_DelegatesAccessExactly(t *testing.T) {
	h := newSlugReadTestHarness()
	ctx := context.WithValue(context.Background(), slugReadContextKey{}, "access")
	target := SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeAnonymous}
	wantProjectContext := store.ProjectContext{
		Project:     store.Project{ID: 73, Slug: target.Slug},
		Role:        store.RoleViewer,
		AuthEnabled: true,
	}
	h.access.projectContext = wantProjectContext

	prepared, err := h.service().PrepareSlugRead(ctx, target)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	if prepared == nil {
		t.Fatal("PrepareSlugRead returned a nil prepared read")
	}
	if h.access.calls != 1 {
		t.Fatalf("GetProjectContextBySlug calls = %d, want 1", h.access.calls)
	}
	if h.access.ctx != ctx {
		t.Fatal("PrepareSlugRead did not forward the exact context")
	}
	if h.access.slug != target.Slug {
		t.Fatalf("slug = %q, want %q", h.access.slug, target.Slug)
	}
	if h.access.mode != target.Mode {
		t.Fatalf("mode = %q, want %q", h.access.mode, target.Mode)
	}
	if prepared.ctx != ctx {
		t.Fatal("prepared read did not bind the preparation context")
	}
	if !reflect.DeepEqual(prepared.projectContext, wantProjectContext) {
		t.Fatalf(
			"prepared project context = %#v, want %#v",
			prepared.projectContext,
			wantProjectContext,
		)
	}
	if &prepared.projectContext == &h.access.projectContext {
		t.Fatal("prepared read did not own a value copy of the project context")
	}
	if prepared.initial.store != h.initial ||
		prepared.lane.store != h.lane ||
		prepared.sprints != h.sprints {
		t.Fatal("prepared read retained the wrong narrow dependencies")
	}
	if h.sprints.calls != 0 || h.initial.calls != 0 || h.lane.calls != 0 {
		t.Fatalf(
			"preparation called a data port: sprints=%d initial=%d lane=%d",
			h.sprints.calls,
			h.initial.calls,
			h.lane.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestReadServicePrepareSlugRead_ReturnsAccessErrorUnchanged(t *testing.T) {
	h := newSlugReadTestHarness()
	sentinel := errors.New("sentinel")
	accessErr := fmt.Errorf("slug read access failed: %w", sentinel)
	h.access.err = accessErr

	prepared, err := h.service().PrepareSlugRead(
		context.Background(),
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
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
	if h.access.calls != 1 {
		t.Fatalf("GetProjectContextBySlug calls = %d, want 1", h.access.calls)
	}
	if h.sprints.calls != 0 || h.initial.calls != 0 || h.lane.calls != 0 {
		t.Fatalf(
			"access failure called a data port: sprints=%d initial=%d lane=%d",
			h.sprints.calls,
			h.initial.calls,
			h.lane.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_HasSprintsUsesBoundContextAndProject(t *testing.T) {
	for _, want := range []bool{false, true} {
		want := want
		t.Run(fmt.Sprintf("result=%t", want), func(t *testing.T) {
			h := newSlugReadTestHarness()
			ctx := context.WithValue(context.Background(), slugReadContextKey{}, "sprints")
			h.access.projectContext = store.ProjectContext{
				Project: store.Project{ID: 73, Slug: "normalized-slug"},
			}
			h.sprints.hasSprints = want

			prepared, err := h.service().PrepareSlugRead(
				ctx,
				SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
			)
			if err != nil {
				t.Fatalf("PrepareSlugRead: %v", err)
			}
			got, err := prepared.HasSprints()
			if err != nil {
				t.Fatalf("HasSprints: %v", err)
			}

			if got != want {
				t.Fatalf("HasSprints = %t, want %t", got, want)
			}
			if h.sprints.calls != 1 {
				t.Fatalf("HasSprints store calls = %d, want 1", h.sprints.calls)
			}
			if h.sprints.ctx != ctx {
				t.Fatal("HasSprints did not use the bound context")
			}
			if h.sprints.projectID != h.access.projectContext.Project.ID {
				t.Fatalf(
					"projectID = %d, want %d",
					h.sprints.projectID,
					h.access.projectContext.Project.ID,
				)
			}
			if h.initial.calls != 0 || h.lane.calls != 0 {
				t.Fatalf(
					"HasSprints called a data read: initial=%d lane=%d",
					h.initial.calls,
					h.lane.calls,
				)
			}
			h.assertLegacyUnused(t)
		})
	}
}

func TestPreparedSlugRead_HasSprintsReturnsStoreErrorUnchanged(t *testing.T) {
	h := newSlugReadTestHarness()
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("sprint state failed: %w", sentinel)
	h.access.projectContext = store.ProjectContext{Project: store.Project{ID: 73}}
	h.sprints.err = storeErr

	prepared, err := h.service().PrepareSlugRead(
		context.Background(),
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	got, err := prepared.HasSprints()

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if got {
		t.Fatal("HasSprints returned true on error")
	}
	if h.sprints.calls != 1 || h.initial.calls != 0 || h.lane.calls != 0 {
		t.Fatalf(
			"unexpected calls: sprints=%d initial=%d lane=%d",
			h.sprints.calls,
			h.initial.calls,
			h.lane.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_ReadInitialDelegatesExactly(t *testing.T) {
	h := newSlugReadTestHarness()
	ctx := context.WithValue(context.Background(), slugReadContextKey{}, "initial")
	h.access.projectContext = store.ProjectContext{
		Project: store.Project{ID: 73, Slug: "normalized-slug"},
		Role:    store.RoleViewer,
	}
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	query := Query{
		TagFilter:      "focus",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		SprintFilter:   store.SprintFilter{Mode: "sprint_number", SprintNumber: 3},
		SortOrder:      store.SortOrderNewest,
		LimitPerLane:   17,
	}
	h.initial.project = h.access.projectContext.Project
	h.initial.tags = []store.TagCount{{Name: "focus", Count: 2}}
	h.initial.workflow = []store.WorkflowColumn{{
		Key:  store.DefaultColumnBacklog,
		Name: "Backlog",
	}}
	h.initial.columns = map[string][]store.Todo{
		store.DefaultColumnBacklog: {{ID: 101, Title: "Todo"}},
	}
	h.initial.columnsMeta = map[string]store.LaneMeta{
		store.DefaultColumnBacklog: {HasMore: true, NextCursor: "10:101", TotalCount: 2},
	}

	prepared, err := h.service().PrepareSlugRead(
		ctx,
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadInitial(query)
	if err != nil {
		t.Fatalf("ReadInitial: %v", err)
	}

	if h.initial.calls != 1 {
		t.Fatalf("GetBoardPaged calls = %d, want 1", h.initial.calls)
	}
	if h.initial.ctx != ctx {
		t.Fatal("ReadInitial did not use the bound context")
	}
	if h.initial.projectContext != &prepared.projectContext {
		t.Fatal("ReadInitial did not use the prepared read's owned project context")
	}
	if h.initial.projectContext == &h.access.projectContext {
		t.Fatal("ReadInitial retained the access store's project-context pointer")
	}
	if h.initial.tagFilter != query.TagFilter ||
		h.initial.searchFilter != query.SearchFilter ||
		!reflect.DeepEqual(h.initial.assigneeFilter, query.AssigneeFilter) ||
		!reflect.DeepEqual(h.initial.sprintFilter, query.SprintFilter) ||
		h.initial.sortOrder != query.SortOrder ||
		h.initial.limitPerLane != query.LimitPerLane {
		t.Fatalf("ReadInitial changed the normalized query: store=%+v query=%+v", h.initial, query)
	}
	want := Result{
		Project:     h.initial.project,
		Tags:        h.initial.tags,
		Workflow:    h.initial.workflow,
		Columns:     h.initial.columns,
		ColumnsMeta: h.initial.columnsMeta,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if h.sprints.calls != 0 || h.lane.calls != 0 {
		t.Fatalf(
			"ReadInitial called another slug port: sprints=%d lane=%d",
			h.sprints.calls,
			h.lane.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_ReadInitialReturnsStoreErrorUnchanged(t *testing.T) {
	h := newSlugReadTestHarness()
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("initial read failed: %w", sentinel)
	h.access.projectContext = store.ProjectContext{Project: store.Project{ID: 73}}
	h.initial.err = storeErr

	prepared, err := h.service().PrepareSlugRead(
		context.Background(),
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadInitial(Query{})

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("result on error = %#v, want zero value", result)
	}
	if h.initial.calls != 1 || h.sprints.calls != 0 || h.lane.calls != 0 {
		t.Fatalf(
			"unexpected calls: initial=%d sprints=%d lane=%d",
			h.initial.calls,
			h.sprints.calls,
			h.lane.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_ReadLaneDelegatesExactly(t *testing.T) {
	h := newSlugReadTestHarness()
	ctx := context.WithValue(context.Background(), slugReadContextKey{}, "lane")
	h.access.projectContext = store.ProjectContext{
		Project: store.Project{ID: 73, Slug: "normalized-slug"},
		Role:    store.RoleViewer,
	}
	assigneeFilter, err := store.ParseAssigneeFilter("42", nil)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter: %v", err)
	}
	query := LaneQuery{
		ColumnKey:      store.DefaultColumnBacklog,
		Limit:          17,
		AfterA:         301,
		AfterB:         302,
		TagFilter:      "focus",
		SearchFilter:   "needle",
		AssigneeFilter: assigneeFilter,
		SprintFilter:   store.SprintFilter{Mode: "sprint_number", SprintNumber: 3},
		SortOrder:      store.SortOrderOldest,
	}
	h.lane.items = []store.Todo{{ID: 101, Title: "Todo"}}
	h.lane.nextCursor = "10:101"
	h.lane.hasMore = true

	prepared, err := h.service().PrepareSlugRead(
		ctx,
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadLane(query)
	if err != nil {
		t.Fatalf("ReadLane: %v", err)
	}

	if h.lane.calls != 1 {
		t.Fatalf("ListTodosForBoardLane calls = %d, want 1", h.lane.calls)
	}
	if h.lane.ctx != ctx {
		t.Fatal("ReadLane did not use the bound context")
	}
	if h.lane.projectID != prepared.projectContext.Project.ID {
		t.Fatalf(
			"projectID = %d, want %d",
			h.lane.projectID,
			prepared.projectContext.Project.ID,
		)
	}
	if h.lane.columnKey != query.ColumnKey ||
		h.lane.limit != query.Limit ||
		h.lane.afterA != query.AfterA ||
		h.lane.afterB != query.AfterB ||
		h.lane.tagFilter != query.TagFilter ||
		h.lane.searchFilter != query.SearchFilter ||
		!reflect.DeepEqual(h.lane.assigneeFilter, query.AssigneeFilter) ||
		!reflect.DeepEqual(h.lane.sprintFilter, query.SprintFilter) ||
		h.lane.sortOrder != query.SortOrder {
		t.Fatalf("ReadLane changed the normalized query: store=%+v query=%+v", h.lane, query)
	}
	want := LaneResult{
		Items:      h.lane.items,
		NextCursor: h.lane.nextCursor,
		HasMore:    h.lane.hasMore,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if h.sprints.calls != 0 || h.initial.calls != 0 {
		t.Fatalf(
			"ReadLane called another slug port: sprints=%d initial=%d",
			h.sprints.calls,
			h.initial.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_ReadLaneReturnsStoreErrorUnchanged(t *testing.T) {
	h := newSlugReadTestHarness()
	sentinel := errors.New("sentinel")
	storeErr := fmt.Errorf("lane read failed: %w", sentinel)
	h.access.projectContext = store.ProjectContext{Project: store.Project{ID: 73}}
	h.lane.err = storeErr

	prepared, err := h.service().PrepareSlugRead(
		context.Background(),
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	result, err := prepared.ReadLane(LaneQuery{})

	if err != storeErr {
		t.Fatalf("error = %v, want original store error %v", err, storeErr)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("error %v no longer matches sentinel", err)
	}
	if !reflect.DeepEqual(result, LaneResult{}) {
		t.Fatalf("result on error = %#v, want zero value", result)
	}
	if h.lane.calls != 1 || h.sprints.calls != 0 || h.initial.calls != 0 {
		t.Fatalf(
			"unexpected calls: lane=%d sprints=%d initial=%d",
			h.lane.calls,
			h.sprints.calls,
			h.initial.calls,
		)
	}
	h.assertLegacyUnused(t)
}

func TestPreparedSlugRead_ObservesCancellationAfterPreparation(t *testing.T) {
	initial := &cancellationReadStore{}
	lane := &cancellationLaneReadStore{}
	legacyAccess := &recordingLegacyReadAccessStore{}
	legacy := &recordingLegacyReadStore{}
	access := &recordingSlugReadAccessStore{
		projectContext: store.ProjectContext{Project: store.Project{ID: 73}},
	}
	sprints := &recordingSlugReadSprintStore{errFromContext: true}
	ctx, cancel := context.WithCancel(context.Background())

	prepared, err := NewReadService(ReadServiceDependencies{
		Initial:      initial,
		Lane:         lane,
		LegacyAccess: legacyAccess,
		Legacy:       legacy,
		SlugAccess:   access,
		SlugSprints:  sprints,
	}).PrepareSlugRead(
		ctx,
		SlugReadTarget{Slug: "normalized-slug", Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareSlugRead: %v", err)
	}
	cancel()

	hasSprints, sprintErr := prepared.HasSprints()
	initialResult, initialErr := prepared.ReadInitial(Query{})
	laneResult, laneErr := prepared.ReadLane(LaneQuery{})

	if hasSprints {
		t.Fatal("HasSprints returned true after cancellation")
	}
	if !errors.Is(sprintErr, context.Canceled) {
		t.Fatalf("HasSprints error = %v, want context cancellation", sprintErr)
	}
	if !errors.Is(initialErr, context.Canceled) {
		t.Fatalf("ReadInitial error = %v, want context cancellation", initialErr)
	}
	if !reflect.DeepEqual(initialResult, Result{}) {
		t.Fatalf("initial result = %#v, want zero value", initialResult)
	}
	if !errors.Is(laneErr, context.Canceled) {
		t.Fatalf("ReadLane error = %v, want context cancellation", laneErr)
	}
	if !reflect.DeepEqual(laneResult, LaneResult{}) {
		t.Fatalf("lane result = %#v, want zero value", laneResult)
	}
	if access.ctx != ctx ||
		sprints.ctx != ctx ||
		initial.ctx != ctx ||
		lane.ctx != ctx {
		t.Fatal("prepared operations did not retain the exact preparation context")
	}
	if sprints.calls != 1 || initial.calls != 1 || lane.calls != 1 {
		t.Fatalf(
			"cancellation calls: sprints=%d initial=%d lane=%d, want 1 each",
			sprints.calls,
			initial.calls,
			lane.calls,
		)
	}
	if legacyAccess.calls != 0 || legacy.calls != 0 {
		t.Fatalf(
			"slug read called legacy ports: access=%d data=%d",
			legacyAccess.calls,
			legacy.calls,
		)
	}
}
