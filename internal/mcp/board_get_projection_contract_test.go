package mcp

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardGetContract_WorkflowProjectionOrderAndShape(t *testing.T) {
	h := newBoardGetContractHarness(t)
	created, err := h.Store.CreateTodo(h.Context, h.Project.ID, store.CreateTodoInput{
		Title:     "Characterized todo",
		Body:      "Body",
		ColumnKey: "building",
		Tags:      []string{"phase-seven"},
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}

	data, meta, readErr := h.call(map[string]any{"projectSlug": h.Project.Slug})
	if readErr != nil {
		t.Fatalf("board_get: %v", readErr)
	}

	project := data.(map[string]any)["project"].(boardProjectItem)
	if project != (boardProjectItem{
		ProjectSlug: h.Project.Slug,
		Name:        h.Project.Name,
		Role:        string(store.RoleMaintainer),
	}) {
		t.Fatalf("project = %#v", project)
	}

	columns := data.(map[string]any)["columns"].([]boardColumnItem)
	if len(columns) != 3 {
		t.Fatalf("columns = %#v, want 3", columns)
	}
	wantColumns := []struct {
		key    string
		name   string
		isDone bool
	}{
		{key: "triage", name: "Triage"},
		{key: "building", name: "Building"},
		{key: "shipped", name: "Shipped", isDone: true},
	}
	for i, want := range wantColumns {
		if columns[i].Key != want.key || columns[i].Name != want.name || columns[i].IsDone != want.isDone {
			t.Fatalf("column[%d] = %#v, want key=%q name=%q done=%v", i, columns[i], want.key, want.name, want.isDone)
		}
		if columns[i].Items == nil {
			t.Fatalf("column[%d].items is nil; empty lanes must serialize as arrays", i)
		}
	}
	items := columns[1].Items
	if len(items) != 1 {
		t.Fatalf("building items = %#v, want one", items)
	}
	item := items[0]
	if item.ProjectSlug != h.Project.Slug ||
		item.LocalID != created.LocalID ||
		item.Title != created.Title ||
		item.Body != created.Body ||
		item.ColumnKey != created.ColumnKey ||
		!reflect.DeepEqual(item.Tags, created.Tags) {
		t.Fatalf("todo projection = %#v, source=%#v", item, created)
	}

	for _, key := range []string{"nextCursorByColumn", "hasMoreByColumn", "totalCountByColumn"} {
		if _, ok := meta[key]; !ok {
			t.Fatalf("meta missing %q: %#v", key, meta)
		}
	}
	requireOperationNames(t, h.Recording,
		"countUsers", "access", "workflow",
		"list", "count", "list", "count", "list", "count",
	)
	requireAllBoardGetContexts(t, h.Recording, h.Context)
}

func TestBoardGetContract_RecordingStoreDelegatesWithoutChangingResult(t *testing.T) {
	h := newBoardGetContractHarness(t)
	if _, err := h.Store.CreateTodo(h.Context, h.Project.ID, store.CreateTodoInput{
		Title:     "Delegation smoke todo",
		ColumnKey: "triage",
	}, store.ModeFull); err != nil {
		t.Fatalf("create todo: %v", err)
	}
	input := map[string]any{"projectSlug": h.Project.Slug, "limit": 2}

	recordedData, recordedMeta, recordedErr := h.call(input)
	plainData, plainMeta, plainErr := New(h.Store, Options{Mode: "full"}).handleBoardGet(h.Context, input)

	if recordedErr != nil || plainErr != nil {
		t.Fatalf("recorded/plain errors = %#v/%#v", recordedErr, plainErr)
	}
	if !reflect.DeepEqual(recordedData, plainData) || !reflect.DeepEqual(recordedMeta, plainMeta) {
		t.Fatalf("recording store changed result\nrecorded=%#v %#v\nplain=%#v %#v", recordedData, recordedMeta, plainData, plainMeta)
	}
}

func TestBoardGetContract_FilterForwardingAndCardinality(t *testing.T) {
	h := newBoardGetContractHarness(t)
	start := time.Unix(1_000, 0).UTC()
	sprint, err := h.Store.CreateSprint(h.Context, h.Project.ID, "Target sprint", start, start.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}

	_, _, readErr := h.call(map[string]any{
		"projectSlug": h.Project.Slug,
		"tag":         "  Phase Seven  ",
		"search":      "  needle  ",
		"assignee":    "me",
		"sprintId":    sprint.ID,
		"sort":        "newest",
		"limit":       7,
	})
	if readErr != nil {
		t.Fatalf("board_get: %v", readErr)
	}

	wantAssignee, err := store.ParseAssigneeFilter("me", &h.Owner.ID)
	if err != nil {
		t.Fatalf("parse expected assignee: %v", err)
	}
	wantSprint := store.SprintFilter{Mode: "sprint", SprintID: sprint.ID}
	listCalls := h.Recording.callsFor("list")
	countCalls := h.Recording.callsFor("count")
	if len(listCalls) != 3 || len(countCalls) != 3 {
		t.Fatalf("list/count calls = %d/%d, want 3/3", len(listCalls), len(countCalls))
	}
	for _, call := range listCalls {
		if call.Tag != "Phase Seven" || call.Search != "needle" ||
			call.Limit != 7 || call.Sort != store.SortOrderNewest ||
			!reflect.DeepEqual(call.Assignee, wantAssignee) ||
			call.Sprint != wantSprint {
			t.Fatalf("list forwarding = %#v", call)
		}
	}
	for _, call := range countCalls {
		if call.Tag != "Phase Seven" || call.Search != "needle" ||
			!reflect.DeepEqual(call.Assignee, wantAssignee) ||
			call.Sprint != wantSprint {
			t.Fatalf("count forwarding = %#v", call)
		}
	}
}

func TestBoardGetContract_SprintSemantics(t *testing.T) {
	t.Run("absent and explicit null mean no filter and no lookup", func(t *testing.T) {
		for _, input := range []map[string]any{
			{"projectSlug": ""},
			{"projectSlug": "", "sprintId": nil},
		} {
			h := newBoardGetContractHarness(t)
			input["projectSlug"] = h.Project.Slug

			_, _, err := h.call(input)
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}
			if len(h.Recording.callsFor("sprint")) != 0 {
				t.Fatalf("sprint lookup occurred: %#v", h.Recording.callsFor("sprint"))
			}
			for _, call := range h.Recording.callsFor("list") {
				if call.Sprint != (store.SprintFilter{Mode: "none"}) {
					t.Fatalf("list sprint filter = %#v", call.Sprint)
				}
			}
		}
	})

	t.Run("zero and negative fail after access without sprint lookup", func(t *testing.T) {
		for _, sprintID := range []int64{0, -1} {
			h := newBoardGetContractHarness(t)

			_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug, "sprintId": sprintID})

			requireBoardGetError(t, err, http.StatusBadRequest, CodeValidationError, "invalid sprintId", map[string]any{"field": "sprintId"})
			requireOperationNames(t, h.Recording, "countUsers", "access")
		}
	})

	t.Run("positive input is an internal row ID rather than project-local number", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		start := time.Unix(2_000, 0).UTC()
		otherProject, err := h.Store.CreateProject(h.Context, "Sprint ID Offset")
		if err != nil {
			t.Fatalf("create offset project: %v", err)
		}
		if _, err := h.Store.CreateSprint(h.Context, otherProject.ID, "Offset sprint", start, start.Add(time.Hour)); err != nil {
			t.Fatalf("create offset sprint: %v", err)
		}
		target, err := h.Store.CreateSprint(h.Context, h.Project.ID, "Target sprint", start, start.Add(time.Hour))
		if err != nil {
			t.Fatalf("create target sprint: %v", err)
		}
		if target.ID == target.Number {
			t.Fatalf("fixture must distinguish internal id and local number: %#v", target)
		}

		_, _, readErr := h.call(map[string]any{"projectSlug": h.Project.Slug, "sprintId": target.ID})
		if readErr != nil {
			t.Fatalf("board_get: %v", readErr)
		}
		sprintCalls := h.Recording.callsFor("sprint")
		if len(sprintCalls) != 1 || sprintCalls[0].SprintID != target.ID {
			t.Fatalf("sprint calls = %#v", sprintCalls)
		}
		for _, call := range h.Recording.callsFor("list") {
			if call.Sprint != (store.SprintFilter{Mode: "sprint", SprintID: target.ID}) {
				t.Fatalf("list sprint filter = %#v", call.Sprint)
			}
		}
	})

	t.Run("missing and cross-project IDs share not-found", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		start := time.Unix(3_000, 0).UTC()
		otherProject, err := h.Store.CreateProject(h.Context, "Other sprint project")
		if err != nil {
			t.Fatalf("create other project: %v", err)
		}
		otherSprint, err := h.Store.CreateSprint(h.Context, otherProject.ID, "Other sprint", start, start.Add(time.Hour))
		if err != nil {
			t.Fatalf("create other sprint: %v", err)
		}

		for _, sprintID := range []int64{9_999_999, otherSprint.ID} {
			_, _, readErr := h.call(map[string]any{"projectSlug": h.Project.Slug, "sprintId": sprintID})
			requireBoardGetError(t, readErr, http.StatusNotFound, CodeNotFound, "not found", map[string]any{})
			requireOperationNames(t, h.Recording, "countUsers", "access", "sprint")
		}
	})

	t.Run("store failure prevents workflow and lane reads", func(t *testing.T) {
		h := newBoardGetContractHarness(t)
		h.Recording.Errors["sprint"] = injectedBoardGetError("sprint")

		_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug, "sprintId": 1})

		requireBoardGetError(t, err, http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{
			"detail": "phase 7 injected sprint failure",
		})
		requireOperationNames(t, h.Recording, "countUsers", "access", "sprint")
	})
}

func TestBoardGetContract_SlugLookupAndCanonicalProjection(t *testing.T) {
	for _, supplied := range []string{
		"phase-7-board",
		"PHASE-7-BOARD",
		"Phase-7-Board",
		"  PHASE-7-BOARD  ",
	} {
		t.Run(supplied, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			if _, err := h.Store.CreateTodo(h.Context, h.Project.ID, store.CreateTodoInput{
				Title:     "Slug echo todo",
				ColumnKey: "triage",
			}, store.ModeFull); err != nil {
				t.Fatalf("create todo: %v", err)
			}

			data, _, err := h.call(map[string]any{"projectSlug": supplied})
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}
			if got := h.Recording.callsFor("access")[0].Slug; got != supplied {
				t.Fatalf("lookup received %q, want exact caller input %q", got, supplied)
			}
			project := data.(map[string]any)["project"].(boardProjectItem)
			if project.ProjectSlug != h.Project.Slug {
				t.Fatalf("project slug = %q, want stored canonical slug %q", project.ProjectSlug, h.Project.Slug)
			}
			columns := data.(map[string]any)["columns"].([]boardColumnItem)
			if len(columns[0].Items) != 1 || columns[0].Items[0].ProjectSlug != h.Project.Slug {
				t.Fatalf("todo slug was not canonical: %#v", columns[0].Items)
			}
		})
	}
}

func TestBoardGetContract_PortFailuresShortCircuit(t *testing.T) {
	tests := []struct {
		name       string
		errorKey   string
		wantOps    []string
		wantStatus int
	}{
		{
			name:       "workflow",
			errorKey:   "workflow",
			wantOps:    []string{"countUsers", "access", "workflow"},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "first lane list",
			errorKey:   "list:triage",
			wantOps:    []string{"countUsers", "access", "workflow", "list"},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "first lane count",
			errorKey:   "count:triage",
			wantOps:    []string{"countUsers", "access", "workflow", "list", "count"},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "later lane list",
			errorKey:   "list:building",
			wantOps:    []string{"countUsers", "access", "workflow", "list", "count", "list"},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "later lane count",
			errorKey:   "count:building",
			wantOps:    []string{"countUsers", "access", "workflow", "list", "count", "list", "count"},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			h.Recording.Errors[tt.errorKey] = injectedBoardGetError(tt.name)

			_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})

			requireBoardGetError(t, err, tt.wantStatus, CodeInternal, "internal error", map[string]any{
				"detail": "phase 7 injected " + tt.name + " failure",
			})
			requireOperationNames(t, h.Recording, tt.wantOps...)
		})
	}
}

func TestBoardGetContract_ContextIdentityAcrossAccessAndDataPorts(t *testing.T) {
	h := newBoardGetContractHarness(t)
	markerKey := struct{ name string }{name: "phase-7-context-marker"}
	h.Context = context.WithValue(store.WithUserID(context.Background(), h.Owner.ID), markerKey, "same-request")

	_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
	if err != nil {
		t.Fatalf("board_get: %v", err)
	}

	requireAllBoardGetContexts(t, h.Recording, h.Context)
}
