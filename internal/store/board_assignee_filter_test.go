package store

import (
	"context"
	"math"
	"strconv"
	"testing"
	"time"
)

func mustAssigneeFilter(t *testing.T, raw string, actorUserID *int64) AssigneeFilter {
	t.Helper()
	filter, err := ParseAssigneeFilter(raw, actorUserID)
	if err != nil {
		t.Fatalf("ParseAssigneeFilter(%q): %v", raw, err)
	}
	return filter
}

func TestParseAssigneeFilter(t *testing.T) {
	actorUserID := int64(42)
	tests := []struct {
		name        string
		raw         string
		actorUserID *int64
		wantMode    assigneeFilterMode
		wantUserID  int64
		wantErr     bool
	}{
		{name: "empty", raw: "", wantMode: assigneeFilterNone},
		{name: "whitespace only", raw: " \t ", wantMode: assigneeFilterNone},
		{name: "unassigned", raw: "unassigned", wantMode: assigneeFilterUnassigned},
		{name: "unassigned trims whitespace", raw: " unassigned ", wantMode: assigneeFilterUnassigned},
		{name: "me", raw: "me", actorUserID: &actorUserID, wantMode: assigneeFilterUser, wantUserID: actorUserID},
		{name: "me without actor", raw: "me", wantErr: true},
		{name: "positive user id", raw: "42", wantMode: assigneeFilterUser, wantUserID: 42},
		{name: "uppercase unassigned", raw: "Unassigned", wantErr: true},
		{name: "uppercase me", raw: "Me", actorUserID: &actorUserID, wantErr: true},
		{name: "zero", raw: "0", wantErr: true},
		{name: "negative", raw: "-1", wantErr: true},
		{name: "unknown sentinel", raw: "abc", wantErr: true},
		{name: "overflow", raw: "9223372036854775808", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseAssigneeFilter(tc.raw, tc.actorUserID)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseAssigneeFilter(%q) expected error, got %+v", tc.raw, got)
				}
				if err.Error() != "invalid assignee" {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAssigneeFilter(%q): %v", tc.raw, err)
			}
			if got.mode != tc.wantMode || got.userID != tc.wantUserID {
				t.Fatalf("ParseAssigneeFilter(%q) = %+v, want mode=%d userID=%d", tc.raw, got, tc.wantMode, tc.wantUserID)
			}
		})
	}
}

func TestAssigneeFilterArgs_InvalidInternalStateFailsClosed(t *testing.T) {
	cond, args := assigneeFilterArgs(AssigneeFilter{mode: assigneeFilterUser})
	if cond != " AND 1 = 0" || len(args) != 0 {
		t.Fatalf("invalid user filter returned cond=%q args=%v, want fail-closed condition", cond, args)
	}
	cond, args = assigneeFilterArgs(AssigneeFilter{mode: assigneeFilterMode(255)})
	if cond != " AND 1 = 0" || len(args) != 0 {
		t.Fatalf("unknown filter mode returned cond=%q args=%v, want fail-closed condition", cond, args)
	}
}

func TestGetBoard_AssigneeFilter(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "assignee-filter-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	u1, err := st.CreateUser(ctx, "assignee-filter-u1@example.com", "password", "U1")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, u1.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember u1: %v", err)
	}
	u2, err := st.CreateUser(ctx, "assignee-filter-u2@example.com", "password", "U2")
	if err != nil {
		t.Fatalf("CreateUser u2: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, u2.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember u2: %v", err)
	}

	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "Assigned to u1", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(u1.ID)}, ModeFull); err != nil {
		t.Fatalf("CreateTodo assigned u1: %v", err)
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "Assigned to u2", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(u2.ID)}, ModeFull); err != nil {
		t.Fatalf("CreateTodo assigned u2: %v", err)
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "Unassigned"}, ModeFull); err != nil {
		t.Fatalf("CreateTodo unassigned: %v", err)
	}

	pc, err := st.GetProjectContextForRead(ctxOwner, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}

	t.Run("filters by specific assignee", func(t *testing.T) {
		filter := mustAssigneeFilter(t, strconv.FormatInt(u1.ID, 10), nil)
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", filter, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Assigned to u1" {
			t.Fatalf("expected only 'Assigned to u1', got %+v", todos)
		}
	})

	t.Run("filters unassigned", func(t *testing.T) {
		filter := mustAssigneeFilter(t, "unassigned", nil)
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", filter, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Unassigned" {
			t.Fatalf("expected only 'Unassigned', got %+v", todos)
		}
	})

	t.Run("no filter returns all", func(t *testing.T) {
		_, _, _, cols, err := st.GetBoard(ctxOwner, &pc, "", "", AssigneeFilter{}, SprintFilter{Mode: "none"})
		if err != nil {
			t.Fatalf("GetBoard: %v", err)
		}
		if len(cols[DefaultColumnBacklog]) != 3 {
			t.Fatalf("expected 3 todos, got %d", len(cols[DefaultColumnBacklog]))
		}
	})

	t.Run("paged path combines assignee tag and search", func(t *testing.T) {
		filter := mustAssigneeFilter(t, strconv.FormatInt(u1.ID, 10), nil)
		_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "focus", "u1", filter, SprintFilter{Mode: "none"}, 1)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Assigned to u1" {
			t.Fatalf("expected only 'Assigned to u1', got %+v", todos)
		}
		laneMeta := meta[DefaultColumnBacklog]
		if laneMeta.TotalCount != 1 || laneMeta.HasMore {
			t.Fatalf("unexpected filtered lane meta: %+v", laneMeta)
		}
	})

	t.Run("paged path filters unassigned", func(t *testing.T) {
		filter := mustAssigneeFilter(t, "unassigned", nil)
		_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "", "", filter, SprintFilter{Mode: "none"}, 1)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		todos := cols[DefaultColumnBacklog]
		if len(todos) != 1 || todos[0].Title != "Unassigned" {
			t.Fatalf("expected only 'Unassigned', got %+v", todos)
		}
		if got := meta[DefaultColumnBacklog].TotalCount; got != 1 {
			t.Fatalf("unassigned total count = %d, want 1", got)
		}
	})

	t.Run("unknown positive user id returns empty board", func(t *testing.T) {
		filter := mustAssigneeFilter(t, strconv.FormatInt(math.MaxInt64, 10), nil)
		_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "", "", filter, SprintFilter{Mode: "none"}, 1)
		if err != nil {
			t.Fatalf("GetBoardPaged: %v", err)
		}
		if len(cols[DefaultColumnBacklog]) != 0 || meta[DefaultColumnBacklog].TotalCount != 0 {
			t.Fatalf("unknown user filter should return empty board, got cols=%+v meta=%+v", cols[DefaultColumnBacklog], meta[DefaultColumnBacklog])
		}
	})
}

func TestListTodosForBoardLane_AssigneeFilter(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "assignee-filter-lane-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	u1, err := st.CreateUser(ctx, "assignee-filter-lane-u1@example.com", "password", "U1")
	if err != nil {
		t.Fatalf("CreateUser u1: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, u1.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember u1: %v", err)
	}

	for _, title := range []string{"Mine 1", "Mine 2", "Mine 3"} {
		if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: title, AssigneeUserID: ptrInt64(u1.ID)}, ModeFull); err != nil {
			t.Fatalf("CreateTodo %q: %v", title, err)
		}
	}
	if _, err := st.CreateTodo(ctxOwner, p.ID, CreateTodoInput{Title: "Not mine"}, ModeFull); err != nil {
		t.Fatalf("CreateTodo not mine: %v", err)
	}

	filter := mustAssigneeFilter(t, strconv.FormatInt(u1.ID, 10), nil)
	items, cursor, hasMore, err := st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, 2, math.MinInt64, 0, "", "", filter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane: %v", err)
	}
	if len(items) != 2 || !hasMore || cursor == "" {
		t.Fatalf("expected two filtered items and another page, got items=%+v hasMore=%v cursor=%q", items, hasMore, cursor)
	}

	afterRank, afterID := ParseLaneCursor(cursor)
	items, _, hasMore, err = st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, 2, afterRank, afterID, "", "", filter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane second page: %v", err)
	}
	if len(items) != 1 || hasMore || items[0].AssigneeUserID == nil || *items[0].AssigneeUserID != u1.ID {
		t.Fatalf("unexpected second filtered page: items=%+v hasMore=%v", items, hasMore)
	}

	count, err := st.CountTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, "", "", filter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("CountTodosForBoardLane assigned: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 assigned todos, got %d", count)
	}

	unassignedFilter := mustAssigneeFilter(t, "unassigned", nil)
	count, err = st.CountTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, "", "", unassignedFilter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("CountTodosForBoardLane: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 unassigned todo, got %d", count)
	}
}

func TestGetBoardPaged_AssigneeFilterSoftCapFallback(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "assignee-soft-cap-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	matchingUser, err := st.CreateUser(ctx, "assignee-soft-cap-match@example.com", "password", "Matching")
	if err != nil {
		t.Fatalf("CreateUser matching: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, matchingUser.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember matching: %v", err)
	}
	otherUser, err := st.CreateUser(ctx, "assignee-soft-cap-other@example.com", "password", "Other")
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, otherUser.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember other: %v", err)
	}

	tx, err := st.db.BeginTx(ctxOwner, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctxOwner, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, done_at)
VALUES (?, ?, ?, '', ?, ?, NULL, ?, NULL, ?, ?, NULL)`)
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	defer stmt.Close()

	nowMs := time.Now().UTC().UnixMilli()
	nextLocalID := int64(1)
	insertTodo := func(title string, rank int64, assigneeUserID int64) {
		t.Helper()
		if _, err := stmt.ExecContext(ctxOwner, p.ID, nextLocalID, title, DefaultColumnBacklog, rank, assigneeUserID, nowMs, nowMs); err != nil {
			t.Fatalf("insert todo local_id=%d: %v", nextLocalID, err)
		}
		nextLocalID++
	}
	for i := 0; i < boardTodoSoftCap+1; i++ {
		if i < 3 {
			insertTodo("Other assignee", int64(i)*10+5, otherUser.ID)
		}
		insertTodo("Matching assignee", int64(i)*10+10, matchingUser.ID)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("close insert statement: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit todos: %v", err)
	}

	pc, err := st.GetProjectContextForRead(ctxOwner, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	filter := mustAssigneeFilter(t, strconv.FormatInt(matchingUser.ID, 10), nil)
	_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "", "", filter, SprintFilter{Mode: "none"}, 2)
	if err != nil {
		t.Fatalf("GetBoardPaged: %v", err)
	}
	items := cols[DefaultColumnBacklog]
	if len(items) != 2 {
		t.Fatalf("fallback first page has %d items, want 2: %+v", len(items), items)
	}
	for _, item := range items {
		if item.AssigneeUserID == nil || *item.AssigneeUserID != matchingUser.ID {
			t.Fatalf("fallback returned a todo for another assignee: %+v", item)
		}
	}
	laneMeta := meta[DefaultColumnBacklog]
	if laneMeta.TotalCount != boardTodoSoftCap+1 {
		t.Fatalf("fallback filtered TotalCount = %d, want %d", laneMeta.TotalCount, boardTodoSoftCap+1)
	}
	if !laneMeta.HasMore || laneMeta.NextCursor == "" {
		t.Fatalf("fallback should return a next page: %+v", laneMeta)
	}

	afterRank, afterID := ParseLaneCursor(laneMeta.NextCursor)
	nextItems, _, _, err := st.ListTodosForBoardLane(ctxOwner, p.ID, DefaultColumnBacklog, 3, afterRank, afterID, "", "", filter, SprintFilter{Mode: "none"})
	if err != nil {
		t.Fatalf("ListTodosForBoardLane next page: %v", err)
	}
	if len(nextItems) != 3 {
		t.Fatalf("fallback next page has %d items, want 3: %+v", len(nextItems), nextItems)
	}
	for _, item := range nextItems {
		if item.AssigneeUserID == nil || *item.AssigneeUserID != matchingUser.ID {
			t.Fatalf("fallback pagination returned a todo for another assignee: %+v", item)
		}
	}
}

func TestGetBoardPaged_AssigneeSprintTagSearchComposition(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "assignee-composition-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	p, err := st.CreateProject(ctxOwner, "p")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	matchingUser, err := st.CreateUser(ctx, "assignee-composition-match@example.com", "password", "Matching")
	if err != nil {
		t.Fatalf("CreateUser matching: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, matchingUser.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember matching: %v", err)
	}
	otherUser, err := st.CreateUser(ctx, "assignee-composition-other@example.com", "password", "Other")
	if err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, owner.ID, p.ID, otherUser.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember other: %v", err)
	}

	targetSprint, err := st.CreateSprint(ctxOwner, p.ID, "Target", time.UnixMilli(1000), time.UnixMilli(2000))
	if err != nil {
		t.Fatalf("CreateSprint target: %v", err)
	}
	otherSprint, err := st.CreateSprint(ctxOwner, p.ID, "Other", time.UnixMilli(3000), time.UnixMilli(4000))
	if err != nil {
		t.Fatalf("CreateSprint other: %v", err)
	}

	for _, in := range []CreateTodoInput{
		{Title: "Needle matching todo", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(matchingUser.ID), SprintID: ptrInt64(targetSprint.ID)},
		{Title: "Needle wrong assignee", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(otherUser.ID), SprintID: ptrInt64(targetSprint.ID)},
		{Title: "Needle wrong sprint", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(matchingUser.ID), SprintID: ptrInt64(otherSprint.ID)},
		{Title: "Needle wrong tag", Tags: []string{"other"}, AssigneeUserID: ptrInt64(matchingUser.ID), SprintID: ptrInt64(targetSprint.ID)},
		{Title: "Wrong search term", Tags: []string{"focus"}, AssigneeUserID: ptrInt64(matchingUser.ID), SprintID: ptrInt64(targetSprint.ID)},
	} {
		if _, err := st.CreateTodo(ctxOwner, p.ID, in, ModeFull); err != nil {
			t.Fatalf("CreateTodo(%q): %v", in.Title, err)
		}
	}

	pc, err := st.GetProjectContextForRead(ctxOwner, p.ID, ModeFull)
	if err != nil {
		t.Fatalf("GetProjectContextForRead: %v", err)
	}
	filter := mustAssigneeFilter(t, strconv.FormatInt(matchingUser.ID, 10), nil)
	sprintFilter := SprintFilter{Mode: "sprint", SprintID: targetSprint.ID}
	_, _, _, cols, meta, err := st.GetBoardPaged(ctxOwner, &pc, "focus", "needle", filter, sprintFilter, 10)
	if err != nil {
		t.Fatalf("GetBoardPaged: %v", err)
	}
	items := cols[DefaultColumnBacklog]
	if len(items) != 1 || items[0].Title != "Needle matching todo" {
		t.Fatalf("expected only the fully matching todo, got %+v", items)
	}
	if got := meta[DefaultColumnBacklog].TotalCount; got != 1 {
		t.Fatalf("composed filter TotalCount = %d, want 1", got)
	}
}
