package store

import (
	"errors"
	"testing"
	"time"
)

func TestSprintsDisabledCapabilityIsReversibleAndNonDestructive(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx, user := dashboardTestContext(t, st)
	project, err := st.CreateProject(ctx, "Suspended sprints")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if !project.SprintsEnabled {
		t.Fatal("new project must default to sprints enabled")
	}
	viewer, err := st.CreateUser(ctx, "sprints-disabled-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	if err := st.AddProjectMember(ctx, user.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember viewer: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ctx, project.ID, viewer.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer disable error=%v, want ErrForbidden", err)
	}
	outsider, err := st.CreateUser(ctx, "sprints-disabled-outsider@example.com", "password123", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}
	if err := st.UpdateProjectSprintsEnabled(ctx, project.ID, outsider.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider disable error=%v, want ErrNotFound", err)
	}

	now := time.Now().UTC()
	active, err := st.CreateSprint(ctx, project.ID, "Active", now.Add(-time.Hour), now.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint active: %v", err)
	}
	planned, err := st.CreateSprint(ctx, project.ID, "Planned", now.Add(time.Hour), now.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint planned: %v", err)
	}
	if err := st.ActivateSprint(ctx, project.ID, active.ID); err != nil {
		t.Fatalf("ActivateSprint: %v", err)
	}

	dormant, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Dormant assignment", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID, SprintID: &active.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo dormant: %v", err)
	}
	preservedDormant, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Preserved dormant assignment", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID, SprintID: &active.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo preserved dormant: %v", err)
	}
	backlog, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Backlog", ColumnKey: DefaultColumnDoing, AssigneeUserID: &user.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo backlog: %v", err)
	}

	if err := st.UpdateProjectSprintsEnabled(ctx, project.ID, user.ID, false); err != nil {
		t.Fatalf("disable sprints: %v", err)
	}
	disabledActive, err := st.GetActiveSprintByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("disabled GetActiveSprintByProjectID: %v", err)
	}
	if disabledActive != nil {
		t.Fatalf("disabled project exposed active sprint: %+v", disabledActive)
	}

	newName := "Blocked rename"
	mutations := map[string]func() error{
		"create": func() error {
			_, err := st.CreateSprint(ctx, project.ID, "Blocked", now, now.Add(24*time.Hour))
			return err
		},
		"update":   func() error { return st.UpdateSprint(ctx, planned.ID, UpdateSprintInput{Name: &newName}) },
		"activate": func() error { return st.ActivateSprint(ctx, project.ID, planned.ID) },
		"close":    func() error { return st.CloseSprint(ctx, project.ID, active.ID) },
		"delete":   func() error { return st.DeleteSprint(ctx, project.ID, planned.ID) },
	}
	for name, mutate := range mutations {
		t.Run("reject "+name, func(t *testing.T) {
			if err := mutate(); !errors.Is(err, ErrSprintsDisabled) {
				t.Fatalf("%s error=%v, want ErrSprintsDisabled", name, err)
			}
		})
	}

	storedActive, err := st.GetSprintByID(ctx, active.ID)
	if err != nil {
		t.Fatalf("GetSprintByID active: %v", err)
	}
	if storedActive.State != SprintStateActive || storedActive.StartedAt == nil || storedActive.ClosedAt != nil {
		t.Fatalf("disabled mutation changed active sprint: %+v", storedActive)
	}
	storedPlanned, err := st.GetSprintByID(ctx, planned.ID)
	if err != nil {
		t.Fatalf("GetSprintByID planned: %v", err)
	}
	if storedPlanned.Name != "Planned" || storedPlanned.State != SprintStatePlanned {
		t.Fatalf("disabled mutation changed planned sprint: %+v", storedPlanned)
	}

	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Blocked assignment", ColumnKey: DefaultColumnBacklog, SprintID: &active.ID,
	}, ModeFull); !errors.Is(err, ErrSprintsDisabled) {
		t.Fatalf("assigned CreateTodo error=%v, want ErrSprintsDisabled", err)
	}
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Allowed unscheduled", ColumnKey: DefaultColumnBacklog,
	}, ModeFull); err != nil {
		t.Fatalf("unscheduled CreateTodo: %v", err)
	}

	update := UpdateTodoInput{
		Title: "Edited while dormant", Body: dormant.Body, Tags: dormant.Tags,
		EstimationPoints: dormant.EstimationPoints, AssigneeUserID: dormant.AssigneeUserID,
	}
	edited, err := st.UpdateTodo(ctx, dormant.ID, update, ModeFull)
	if err != nil {
		t.Fatalf("unrelated dormant edit: %v", err)
	}
	if edited.SprintID == nil || *edited.SprintID != active.ID {
		t.Fatalf("unrelated edit lost dormant sprint: %+v", edited)
	}
	update.SprintID = &active.ID
	if _, err := st.UpdateTodo(ctx, dormant.ID, update, ModeFull); err != nil {
		t.Fatalf("same sprint semantic no-op: %v", err)
	}
	update.SprintID = &planned.ID
	if _, err := st.UpdateTodo(ctx, dormant.ID, update, ModeFull); !errors.Is(err, ErrSprintsDisabled) {
		t.Fatalf("changed sprint error=%v, want ErrSprintsDisabled", err)
	}
	update.SprintID = nil
	update.ClearSprint = true
	cleared, err := st.UpdateTodo(ctx, dormant.ID, update, ModeFull)
	if err != nil {
		t.Fatalf("clear dormant sprint: %v", err)
	}
	if cleared.SprintID != nil {
		t.Fatalf("clear left sprint assignment: %+v", cleared)
	}

	summary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("disabled dashboard summary: %v", err)
	}
	if len(summary.Projects) != 1 || summary.Projects[0].ActiveSprint != nil {
		t.Fatalf("disabled dashboard exposed active sprint: %+v", summary.Projects)
	}
	if len(summary.Projects[0].SprintSections) != 1 || summary.Projects[0].SprintSections[0].ID != nil {
		t.Fatalf("disabled dashboard exposed sprint sections: %+v", summary.Projects[0].SprintSections)
	}
	if summary.SprintCompletion != nil || summary.SprintCompletionAllUsers != nil {
		t.Fatalf("disabled dashboard exposed completion: %+v %+v", summary.SprintCompletion, summary.SprintCompletionAllUsers)
	}
	if summary.AssignedSplit == nil || summary.AssignedSplit.SprintCount != 0 || summary.AssignedSplit.BacklogCount != 3 {
		t.Fatalf("disabled dashboard split=%+v, want all assigned work unscheduled", summary.AssignedSplit)
	}
	items, _, err := st.ListDashboardTodos(ctx, user.ID, 10, nil, "activity")
	if err != nil {
		t.Fatalf("disabled dashboard todos: %v", err)
	}
	for _, item := range items {
		if item.SprintID != nil {
			t.Fatalf("disabled dashboard todo exposed sprint: %+v", item)
		}
	}

	if err := st.UpdateProjectSprintsEnabled(ctx, project.ID, user.ID, true); err != nil {
		t.Fatalf("re-enable sprints: %v", err)
	}
	effectiveActive, err := st.GetActiveSprintByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetActiveSprintByProjectID: %v", err)
	}
	if effectiveActive == nil || effectiveActive.ID != active.ID {
		t.Fatalf("re-enabled active sprint=%+v, want %d", effectiveActive, active.ID)
	}
	preservedAssignment, err := st.GetTodoByLocalID(ctx, project.ID, preservedDormant.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID preserved dormant: %v", err)
	}
	if preservedAssignment.SprintID == nil || *preservedAssignment.SprintID != active.ID {
		t.Fatalf("re-enable did not restore preserved assignment: %+v", preservedAssignment)
	}
	reenabledSummary, err := st.GetDashboardSummary(ctx, user.ID, "UTC")
	if err != nil {
		t.Fatalf("re-enabled dashboard summary: %v", err)
	}
	if len(reenabledSummary.Projects) != 1 || reenabledSummary.Projects[0].ActiveSprint == nil || reenabledSummary.Projects[0].ActiveSprint.ID != active.ID {
		t.Fatalf("re-enabled dashboard active sprint: %+v", reenabledSummary.Projects)
	}
	if reenabledSummary.AssignedSplit == nil || reenabledSummary.AssignedSplit.SprintCount != 1 || reenabledSummary.AssignedSplit.BacklogCount != 2 {
		t.Fatalf("re-enabled dashboard split=%+v", reenabledSummary.AssignedSplit)
	}
	preservedBacklog, err := st.GetTodoByLocalID(ctx, project.ID, backlog.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID backlog: %v", err)
	}
	if preservedBacklog.SprintID != nil {
		t.Fatalf("unscheduled todo changed across re-enable: %+v", preservedBacklog)
	}
}
