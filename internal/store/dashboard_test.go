package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func dashboardTestContext(t *testing.T, st *Store) (context.Context, User) {
	t.Helper()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "dashboard@example.com", "password123", "Dashboard User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	return WithUserID(ctx, user.ID), user
}

func setTodoTimes(t *testing.T, st *Store, todoID int64, createdAt, updatedAt time.Time) {
	t.Helper()
	if _, err := st.db.Exec(`
UPDATE todos
SET created_at = ?, updated_at = ?
WHERE id = ?`, createdAt.UnixMilli(), updatedAt.UnixMilli(), todoID); err != nil {
		t.Fatalf("set todo times: %v", err)
	}
}

func TestDashboardSummary_CustomDoneKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	customProject, err := st.CreateProjectWithWorkflow(ctx, "Custom Done", []WorkflowColumn{
		{Key: "backlog_custom", Name: "Backlog", Color: "#9CA3AF", Position: 0},
		{Key: "build_custom", Name: "Build", Color: "#10B981", Position: 1},
		{Key: "verify_custom", Name: "Verify", Color: "#3B82F6", Position: 2},
		{Key: "shipped_custom", Name: "Shipped", Color: "#EF4444", Position: 3, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow custom: %v", err)
	}
	defaultProject, err := st.CreateProject(ctx, "Default Done")
	if err != nil {
		t.Fatalf("CreateProject default: %v", err)
	}

	pointsCustom := int64(3)
	customTodo, err := st.CreateTodo(ctx, customProject.ID, CreateTodoInput{
		Title:            "Custom done todo",
		ColumnKey:        "build_custom",
		AssigneeUserID:   &user.ID,
		EstimationPoints: &pointsCustom,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo custom: %v", err)
	}
	setTodoTimes(t, st, customTodo.ID, time.Now().UTC().Add(-48*time.Hour), time.Now().UTC().Add(-48*time.Hour))
	if _, err := st.MoveTodo(ctx, customTodo.ID, "shipped_custom", nil, nil, ModeFull); err != nil {
		t.Fatalf("MoveTodo custom done: %v", err)
	}

	pointsDefault := int64(5)
	defaultTodo, err := st.CreateTodo(ctx, defaultProject.ID, CreateTodoInput{
		Title:            "Default done todo",
		ColumnKey:        DefaultColumnDoing,
		AssigneeUserID:   &user.ID,
		EstimationPoints: &pointsDefault,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo default: %v", err)
	}
	setTodoTimes(t, st, defaultTodo.ID, time.Now().UTC().Add(-96*time.Hour), time.Now().UTC().Add(-96*time.Hour))
	if _, err := st.MoveTodo(ctx, defaultTodo.ID, DefaultColumnDone, nil, nil, ModeFull); err != nil {
		t.Fatalf("MoveTodo default done: %v", err)
	}

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}

	if summary.StoriesCompletedThisWeek != 2 {
		t.Fatalf("expected 2 stories completed this week, got %d", summary.StoriesCompletedThisWeek)
	}
	if summary.PointsCompletedThisWeek != 8 {
		t.Fatalf("expected 8 points completed this week, got %d", summary.PointsCompletedThisWeek)
	}
	if len(summary.WeeklyThroughput) != 5 {
		t.Fatalf("expected 5 weekly throughput buckets, got %d", len(summary.WeeklyThroughput))
	}
	lastWeek := summary.WeeklyThroughput[len(summary.WeeklyThroughput)-1]
	if lastWeek.Stories != 2 || lastWeek.Points != 8 {
		t.Fatalf("expected current throughput bucket stories=2 points=8, got %+v", lastWeek)
	}
	if summary.AvgLeadTimeDays == nil {
		t.Fatal("expected avg lead time for completed work")
	}
	if *summary.AvgLeadTimeDays < 2.5 || *summary.AvgLeadTimeDays > 3.5 {
		t.Fatalf("expected avg lead time near 3 days, got %.2f", *summary.AvgLeadTimeDays)
	}
}

func TestDashboardSummary_CustomWIPKeys_AllNonDoneCountAsWipWithoutLegacySplit(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProjectWithWorkflow(ctx, "Custom WIP", []WorkflowColumn{
		{Key: "intake_custom", Name: "Intake", Color: "#9CA3AF", Position: 0},
		{Key: "build_custom", Name: "Build", Color: "#10B981", Position: 1},
		{Key: "verify_custom", Name: "Verify", Color: "#3B82F6", Position: 2},
		{Key: "shipped_custom", Name: "Shipped", Color: "#EF4444", Position: 3, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow: %v", err)
	}

	intakeTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Intake todo",
		ColumnKey:      "intake_custom",
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo intake: %v", err)
	}
	buildTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Build todo",
		ColumnKey:      "build_custom",
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo build: %v", err)
	}
	verifyTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Verify todo",
		ColumnKey:      "verify_custom",
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo verify: %v", err)
	}

	now := time.Now().UTC()
	setTodoTimes(t, st, intakeTodo.ID, now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))
	setTodoTimes(t, st, buildTodo.ID, now.Add(-5*24*time.Hour), now.Add(-3*24*time.Hour))
	setTodoTimes(t, st, verifyTodo.ID, now.Add(-4*24*time.Hour), now.Add(-2*24*time.Hour))

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}

	if summary.WipCount != 3 {
		t.Fatalf("expected WIP count 3 for all non-done assigned work, got %d", summary.WipCount)
	}
	if summary.WipInProgressCount != 0 {
		t.Fatalf("expected in-progress WIP split 0 without explicit %q lane, got %d", DefaultColumnDoing, summary.WipInProgressCount)
	}
	if summary.WipTestingCount != 0 {
		t.Fatalf("expected testing WIP split 0 without explicit %q lane, got %d", DefaultColumnTesting, summary.WipTestingCount)
	}
	if summary.OldestWip == nil {
		t.Fatal("expected oldest WIP")
	}
	if summary.OldestWip.LocalID != intakeTodo.LocalID {
		t.Fatalf("expected oldest WIP local ID %d, got %+v", intakeTodo.LocalID, summary.OldestWip)
	}
	if summary.OldestWip.Title != intakeTodo.Title {
		t.Fatalf("expected oldest WIP title %q, got %+v", intakeTodo.Title, summary.OldestWip)
	}
}

func TestDashboardSummary_DefaultWIPKeys_PreserveLegacySplit(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProject(ctx, "Default WIP")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	doingTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Doing todo",
		ColumnKey:      DefaultColumnDoing,
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo doing: %v", err)
	}
	testingTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Testing todo",
		ColumnKey:      DefaultColumnTesting,
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo testing: %v", err)
	}

	now := time.Now().UTC()
	setTodoTimes(t, st, doingTodo.ID, now.Add(-5*24*time.Hour), now.Add(-5*24*time.Hour))
	setTodoTimes(t, st, testingTodo.ID, now.Add(-2*24*time.Hour), now.Add(-2*24*time.Hour))

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}

	if summary.WipCount != 2 {
		t.Fatalf("expected WIP count 2, got %d", summary.WipCount)
	}
	if summary.WipInProgressCount != 1 {
		t.Fatalf("expected in-progress WIP count 1, got %d", summary.WipInProgressCount)
	}
	if summary.WipTestingCount != 1 {
		t.Fatalf("expected testing WIP count 1, got %d", summary.WipTestingCount)
	}
	if summary.OldestWip == nil {
		t.Fatal("expected oldest WIP")
	}
	if summary.OldestWip.LocalID != doingTodo.LocalID {
		t.Fatalf("expected oldest WIP local ID %d, got %+v", doingTodo.LocalID, summary.OldestWip)
	}
}

func TestDashboardSummary_UnassignedTodoExcludedEvenInActiveSprint(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProject(ctx, "Unassigned Active Sprint")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ctx, project.ID, "Sprint 1", now.Add(-time.Hour), now.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if err := st.ActivateSprint(ctx, project.ID, sprint.ID); err != nil {
		t.Fatalf("ActivateSprint: %v", err)
	}

	points := int64(3)
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:            "Unassigned sprint work",
		ColumnKey:        DefaultColumnDoing,
		EstimationPoints: &points,
		SprintID:         &sprint.ID,
	}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}
	if summary.AssignedCount != 0 || summary.TotalAssignedStoryPoints != 0 {
		t.Fatalf("expected no assigned work, got count=%d points=%d", summary.AssignedCount, summary.TotalAssignedStoryPoints)
	}
	if summary.AssignedSplit == nil {
		t.Fatal("expected assigned split")
	}
	if *summary.AssignedSplit != (AssignedSplit{}) {
		t.Fatalf("expected empty assigned split, got %+v", *summary.AssignedSplit)
	}

	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "activity")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no dashboard todos, got %+v", items)
	}
}

func TestDashboardSummary_AssignedTodoInActiveSprintCountsAsSprint(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProject(ctx, "Assigned Active Sprint")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	if _, err := st.CreateSprint(ctx, project.ID, "Sprint 0", now.Add(time.Hour), now.Add(7*24*time.Hour)); err != nil {
		t.Fatalf("CreateSprint setup: %v", err)
	}
	sprint, err := st.CreateSprint(ctx, project.ID, "Sprint 1", now.Add(-time.Hour), now.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if sprint.ID == user.ID {
		t.Fatalf("test setup requires sprint ID to differ from user ID; both were %d", user.ID)
	}
	if err := st.ActivateSprint(ctx, project.ID, sprint.ID); err != nil {
		t.Fatalf("ActivateSprint: %v", err)
	}

	points := int64(3)
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:            "Assigned sprint work",
		ColumnKey:        DefaultColumnDoing,
		AssigneeUserID:   &user.ID,
		EstimationPoints: &points,
		SprintID:         &sprint.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}
	if summary.AssignedCount != 1 || summary.TotalAssignedStoryPoints != points {
		t.Fatalf("expected assigned count=1 points=%d, got count=%d points=%d", points, summary.AssignedCount, summary.TotalAssignedStoryPoints)
	}
	if summary.AssignedSplit == nil {
		t.Fatal("expected assigned split")
	}
	if summary.AssignedSplit.SprintCount != 1 || summary.AssignedSplit.SprintPoints != points {
		t.Fatalf("expected active sprint work in sprint bucket, got %+v", *summary.AssignedSplit)
	}
	if summary.AssignedSplit.BacklogCount != 0 || summary.AssignedSplit.BacklogPoints != 0 {
		t.Fatalf("expected empty backlog bucket, got %+v", *summary.AssignedSplit)
	}

	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "activity")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	if len(items) != 1 || items[0].ID != todo.ID {
		t.Fatalf("expected active sprint todo in dashboard list, got %+v", items)
	}
}

func TestDashboardSummary_AssignedTodoInPlannedSprintCountsAsBacklog(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProject(ctx, "Planned Sprint Workload")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	sprint, err := st.CreateSprint(ctx, project.ID, "Sprint 1", now.Add(time.Hour), now.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	points := int64(3)
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:            "Assigned planned sprint work",
		ColumnKey:        DefaultColumnDoing,
		AssigneeUserID:   &user.ID,
		EstimationPoints: &points,
		SprintID:         &sprint.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("GetDashboardSummary: %v", err)
	}
	if summary.AssignedCount != 1 || summary.TotalAssignedStoryPoints != points {
		t.Fatalf("expected assigned count=1 points=%d, got count=%d points=%d", points, summary.AssignedCount, summary.TotalAssignedStoryPoints)
	}
	if summary.AssignedSplit == nil {
		t.Fatal("expected assigned split")
	}
	if summary.AssignedSplit.SprintCount != 0 || summary.AssignedSplit.SprintPoints != 0 {
		t.Fatalf("expected planned sprint work outside current sprint bucket, got %+v", *summary.AssignedSplit)
	}
	if summary.AssignedSplit.BacklogCount != 1 || summary.AssignedSplit.BacklogPoints != points {
		t.Fatalf("expected planned sprint work in backlog bucket, got %+v", *summary.AssignedSplit)
	}

	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "activity")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	if len(items) != 1 || items[0].ID != todo.ID {
		t.Fatalf("expected planned sprint todo in dashboard list, got %+v", items)
	}
}

func TestListDashboardTodos_CustomDoneKeyExcludesDone(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProjectWithWorkflow(ctx, "Dashboard Todos", []WorkflowColumn{
		{Key: "backlog_custom", Name: "Backlog", Color: "#9CA3AF", Position: 0},
		{Key: "build_custom", Name: "Build", Color: "#10B981", Position: 1},
		{Key: "shipped_custom", Name: "Shipped", Color: "#EF4444", Position: 2, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow: %v", err)
	}

	openTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Open todo",
		ColumnKey:      "build_custom",
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo open: %v", err)
	}
	doneTodo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title:          "Done todo",
		ColumnKey:      "build_custom",
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo done: %v", err)
	}
	if _, err := st.MoveTodo(ctx, doneTodo.ID, "shipped_custom", nil, nil, ModeFull); err != nil {
		t.Fatalf("MoveTodo done: %v", err)
	}

	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 dashboard todo, got %d", len(items))
	}
	if items[0].ID != openTodo.ID {
		t.Fatalf("expected open todo ID %d, got %+v", openTodo.ID, items)
	}
	if items[0].StatusName != "Build" {
		t.Fatalf("expected workflow status name Build, got %+v", items[0])
	}
}

func TestListDashboardTodos_BoardOrderByProjectID(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	p1, err := st.CreateProject(ctx, "First")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p2, err := st.CreateProject(ctx, "Second")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	t1, err := st.CreateTodo(ctx, p1.ID, CreateTodoInput{
		Title:          "Older project todo",
		ColumnKey:      DefaultColumnDoing,
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo t1: %v", err)
	}
	t2, err := st.CreateTodo(ctx, p2.ID, CreateTodoInput{
		Title:          "Newer project todo",
		ColumnKey:      DefaultColumnDoing,
		AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo t2: %v", err)
	}
	now := time.Now().UTC()
	setTodoTimes(t, st, t1.ID, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	setTodoTimes(t, st, t2.ID, now, now)

	byActivity, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "activity")
	if err != nil {
		t.Fatalf("ListDashboardTodos activity: %v", err)
	}
	if len(byActivity) != 2 || byActivity[0].ID != t2.ID {
		t.Fatalf("activity order: want newer project first, got %+v", byActivity)
	}

	byBoard, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "board")
	if err != nil {
		t.Fatalf("ListDashboardTodos board: %v", err)
	}
	if len(byBoard) != 2 || byBoard[0].ID != t1.ID {
		t.Fatalf("board order: want lower project_id first, got %+v", byBoard)
	}
}

func TestListDashboardTodos_BoardOrderLanesAndRankTiebreaker(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	project, err := st.CreateProjectWithWorkflow(ctx, "Lanes", []WorkflowColumn{
		{Key: "lane_a", Name: "A", Color: "#9CA3AF", Position: 0},
		{Key: "lane_b", Name: "B", Color: "#10B981", Position: 1},
		{Key: "done_lane", Name: "Done", Color: "#EF4444", Position: 2, IsDone: true},
	})
	if err != nil {
		t.Fatalf("CreateProjectWithWorkflow: %v", err)
	}

	tBacklog, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Backlog item", ColumnKey: "lane_a", AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo backlog: %v", err)
	}
	tBuild, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Build item", ColumnKey: "lane_b", AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo build: %v", err)
	}

	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "board")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(items))
	}
	if items[0].ID != tBacklog.ID || items[1].ID != tBuild.ID {
		t.Fatalf("expected lane order lane_a then lane_b, got ids %v, %v", items[0].ID, items[1].ID)
	}

	tLate, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Later id", ColumnKey: "lane_b", AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo late: %v", err)
	}
	tEarly, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Earlier id", ColumnKey: "lane_b", AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo early: %v", err)
	}
	r := int64(999)
	if _, err := st.db.ExecContext(ctx, `UPDATE todos SET rank = ? WHERE id IN (?, ?)`, r, tEarly.ID, tLate.ID); err != nil {
		t.Fatalf("set equal rank: %v", err)
	}

	items2, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "board")
	if err != nil {
		t.Fatalf("ListDashboardTodos: %v", err)
	}
	// Same lane + equal rank → t.id ASC: lower internal id first.
	pos := func(id int64) int {
		for i, it := range items2 {
			if it.ID == id {
				return i
			}
		}
		return -1
	}
	pE, pL := pos(tEarly.ID), pos(tLate.ID)
	if pE < 0 || pL < 0 {
		t.Fatalf("missing todos in board list: %+v", items2)
	}
	if (tEarly.ID < tLate.ID && pE > pL) || (tLate.ID < tEarly.ID && pL > pE) {
		t.Fatalf("equal rank same lane: want lower todo id first, positions %d vs %d for ids %d and %d", pE, pL, tEarly.ID, tLate.ID)
	}
}

func TestListDashboardTodos_BoardCursorRoundTrip(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)

	p1, err := st.CreateProject(ctx, "P1")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	p2, err := st.CreateProject(ctx, "P2")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, pid := range []int64{p1.ID, p2.ID} {
		_, err := st.CreateTodo(ctx, pid, CreateTodoInput{
			Title: "Todo", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		_, err = st.CreateTodo(ctx, pid, CreateTodoInput{
			Title: "Todo2", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID,
		}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
	}

	page1, cur, err := st.ListDashboardTodos(ctx, user.ID, 1, nil, "board")
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 1 || cur == nil || *cur == "" {
		t.Fatalf("expected one item and cursor, got len=%d cur=%v", len(page1), cur)
	}
	page2, _, err := st.ListDashboardTodos(ctx, user.ID, 10, cur, "board")
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("expected 3 todos on second page, got %d", len(page2))
	}
	seen := map[int64]bool{page1[0].ID: true}
	for _, it := range page2 {
		if seen[it.ID] {
			t.Fatalf("duplicate id %d across pages", it.ID)
		}
		seen[it.ID] = true
	}
}

func TestListDashboardTodos_CursorSortMismatch(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)
	project, err := st.CreateProject(ctx, "P")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err = st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "T", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	activityCursor := "12:34"
	_, _, err = st.ListDashboardTodos(ctx, user.ID, 10, &activityCursor, "board")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("board sort + activity-shaped cursor: want ErrValidation, got %v", err)
	}

	boardCursor := "1:2:3:4"
	_, _, err = st.ListDashboardTodos(ctx, user.ID, 10, &boardCursor, "activity")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("activity sort + board-shaped cursor: want ErrValidation, got %v", err)
	}
}

func TestNormalizeDashboardTodoPageLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		limit          int
		wantPageLimit  int
		wantFetchLimit int
	}{
		{name: "zero defaults", limit: 0, wantPageLimit: 20, wantFetchLimit: 21},
		{name: "negative defaults", limit: -1, wantPageLimit: 20, wantFetchLimit: 21},
		{name: "min page", limit: 1, wantPageLimit: 1, wantFetchLimit: 2},
		{name: "near max", limit: 99, wantPageLimit: 99, wantFetchLimit: 100},
		{name: "exact max", limit: 100, wantPageLimit: 100, wantFetchLimit: 101},
		{name: "above max clamps", limit: 101, wantPageLimit: 100, wantFetchLimit: 101},
		{name: "far above max clamps", limit: 1000, wantPageLimit: 100, wantFetchLimit: 101},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pageLimit, fetchLimit := normalizeDashboardTodoPageLimits(tt.limit)
			if pageLimit != tt.wantPageLimit || fetchLimit != tt.wantFetchLimit {
				t.Fatalf("normalizeDashboardTodoPageLimits(%d) = (%d, %d); want (%d, %d)",
					tt.limit, pageLimit, fetchLimit, tt.wantPageLimit, tt.wantFetchLimit)
			}
			if fetchLimit != pageLimit+1 {
				t.Fatalf("fetchLimit %d is not pageLimit+1 (%d)", fetchLimit, pageLimit+1)
			}
			if pageLimit < 1 || pageLimit > 100 {
				t.Fatalf("pageLimit %d outside [1,100]", pageLimit)
			}
			if fetchLimit < 2 || fetchLimit > 101 {
				t.Fatalf("fetchLimit %d outside [2,101]", fetchLimit)
			}
		})
	}
}
