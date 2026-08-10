package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func createLifecycleSprint(t *testing.T, st *Store, projectID int64, name string, start, end time.Time) Sprint {
	t.Helper()
	sp, err := st.CreateSprint(context.Background(), projectID, name, start, end)
	if err != nil {
		t.Fatalf("CreateSprint(%q): %v", name, err)
	}
	return sp
}

func getLifecycleSprint(t *testing.T, st *Store, sprintID int64) Sprint {
	t.Helper()
	sp, err := st.GetSprintByID(context.Background(), sprintID)
	if err != nil {
		t.Fatalf("GetSprintByID(%d): %v", sprintID, err)
	}
	return sp
}

func assertLifecycleState(t *testing.T, st *Store, sprintID int64, want string) Sprint {
	t.Helper()
	sp := getLifecycleSprint(t, st, sprintID)
	if sp.State != want {
		t.Fatalf("sprint %d state=%q, want %q", sprintID, sp.State, want)
	}
	return sp
}

func TestSprintLifecycleStoreActivateContract(t *testing.T) {
	t.Run("planned future succeeds", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()

		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-activate-future")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Future", now.Add(-time.Hour), now.Add(24*time.Hour))

		if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("ActivateSprint: %v", err)
		}
		got := assertLifecycleState(t, st, sp.ID, SprintStateActive)
		if got.StartedAt == nil || got.ClosedAt != nil {
			t.Fatalf("activated sprint timestamps started=%v closed=%v", got.StartedAt, got.ClosedAt)
		}
	})

	t.Run("planned safely past fails", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()

		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-activate-past")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Past", now.Add(-48*time.Hour), now.Add(-24*time.Hour))

		err = st.ActivateSprint(ctx, project.ID, sp.ID)
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "on or before now") {
			t.Fatalf("ActivateSprint error=%v, want end-in-past validation", err)
		}
		got := assertLifecycleState(t, st, sp.ID, SprintStatePlanned)
		if got.StartedAt != nil || got.ClosedAt != nil {
			t.Fatalf("rejected sprint timestamps started=%v closed=%v", got.StartedAt, got.ClosedAt)
		}
	})

	t.Run("already active is idempotent", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()

		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-activate-idempotent")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Active", now.Add(-time.Hour), now.Add(24*time.Hour))
		if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("first ActivateSprint: %v", err)
		}
		before := getLifecycleSprint(t, st, sp.ID)

		if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("second ActivateSprint: %v", err)
		}
		after := getLifecycleSprint(t, st, sp.ID)
		if after.State != SprintStateActive || after.StartedAt == nil || before.StartedAt == nil {
			t.Fatalf("idempotent sprint before=%+v after=%+v", before, after)
		}
		if !after.StartedAt.Equal(*before.StartedAt) || !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Fatalf("idempotent activation changed timestamps before=%+v after=%+v", before, after)
		}
	})

	t.Run("closed and missing fail", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()

		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-activate-invalid")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Closed", now.Add(-time.Hour), now.Add(24*time.Hour))
		if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("ActivateSprint setup: %v", err)
		}
		if err := st.CloseSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("CloseSprint setup: %v", err)
		}

		err = st.ActivateSprint(ctx, project.ID, sp.ID)
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "must be PLANNED") {
			t.Fatalf("closed ActivateSprint error=%v, want planned-state validation", err)
		}
		if err := st.ActivateSprint(ctx, project.ID, sp.ID+100000); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing ActivateSprint error=%v, want ErrNotFound", err)
		}
	})

	t.Run("foreign project is validation and leaves target unchanged", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()

		ctx := context.Background()
		projectA, err := st.CreateProject(ctx, "lifecycle-activate-project-a")
		if err != nil {
			t.Fatalf("CreateProject A: %v", err)
		}
		projectB, err := st.CreateProject(ctx, "lifecycle-activate-project-b")
		if err != nil {
			t.Fatalf("CreateProject B: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, projectB.ID, "Foreign", now.Add(-time.Hour), now.Add(24*time.Hour))

		err = st.ActivateSprint(ctx, projectA.ID, sp.ID)
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "does not belong to project") {
			t.Fatalf("foreign ActivateSprint error=%v, want project validation", err)
		}
		assertLifecycleState(t, st, sp.ID, SprintStatePlanned)
	})
}

func TestSprintLifecycleStoreActivateReplacesCurrentAtomically(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "lifecycle-activate-replace")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	otherProject, err := st.CreateProject(ctx, "lifecycle-activate-other")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	now := time.Now().UTC()
	active := createLifecycleSprint(t, st, project.ID, "Current", now.Add(-time.Hour), now.Add(48*time.Hour))
	target := createLifecycleSprint(t, st, project.ID, "Target", now.Add(-time.Hour), now.Add(72*time.Hour))
	other := createLifecycleSprint(t, st, otherProject.ID, "Other active", now.Add(-time.Hour), now.Add(48*time.Hour))
	if err := st.ActivateSprint(ctx, project.ID, active.ID); err != nil {
		t.Fatalf("ActivateSprint current: %v", err)
	}
	if err := st.ActivateSprint(ctx, otherProject.ID, other.ID); err != nil {
		t.Fatalf("ActivateSprint other: %v", err)
	}
	activeBefore := getLifecycleSprint(t, st, active.ID)

	if err := st.ActivateSprint(ctx, project.ID, target.ID); err != nil {
		t.Fatalf("ActivateSprint target: %v", err)
	}
	activeAfter := assertLifecycleState(t, st, active.ID, SprintStateClosed)
	targetAfter := assertLifecycleState(t, st, target.ID, SprintStateActive)
	otherAfter := assertLifecycleState(t, st, other.ID, SprintStateActive)
	if activeAfter.StartedAt == nil || activeBefore.StartedAt == nil || !activeAfter.StartedAt.Equal(*activeBefore.StartedAt) {
		t.Fatalf("previous start timestamp changed before=%v after=%v", activeBefore.StartedAt, activeAfter.StartedAt)
	}
	if activeAfter.ClosedAt == nil || targetAfter.StartedAt == nil || !activeAfter.ClosedAt.Equal(*targetAfter.StartedAt) {
		t.Fatalf("replacement timestamps closed=%v started=%v, want equal transaction time", activeAfter.ClosedAt, targetAfter.StartedAt)
	}
	if otherAfter.ClosedAt != nil {
		t.Fatalf("other project active sprint was closed: %+v", otherAfter)
	}
	gotActive, err := st.GetActiveSprintByProjectID(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetActiveSprintByProjectID: %v", err)
	}
	if gotActive == nil || gotActive.ID != target.ID {
		t.Fatalf("active sprint=%+v, want target %d", gotActive, target.ID)
	}
}

func TestSprintLifecycleStoreActivateRollbackRestoresPriorActive(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "lifecycle-activate-rollback")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	active := createLifecycleSprint(t, st, project.ID, "Current", now.Add(-time.Hour), now.Add(48*time.Hour))
	target := createLifecycleSprint(t, st, project.ID, "Target", now.Add(-time.Hour), now.Add(72*time.Hour))
	if err := st.ActivateSprint(ctx, project.ID, active.ID); err != nil {
		t.Fatalf("ActivateSprint current: %v", err)
	}
	activeBefore := getLifecycleSprint(t, st, active.ID)

	const triggerName = "sprint_lifecycle_abort_target_activation"
	const triggerMarker = "checkpoint-one-target-activation-abort"
	triggerSQL := fmt.Sprintf(`
		CREATE TRIGGER %s
		BEFORE UPDATE OF state ON sprints
		WHEN OLD.id = %d
		 AND OLD.state = '%s'
		 AND NEW.state = '%s'
		BEGIN
			SELECT RAISE(ABORT, '%s');
		END
	`, triggerName, target.ID, SprintStatePlanned, SprintStateActive, triggerMarker)
	if _, err := st.db.ExecContext(ctx, triggerSQL); err != nil {
		t.Fatalf("create activation fault trigger: %v", err)
	}

	err = st.ActivateSprint(ctx, project.ID, target.ID)
	if err == nil || !strings.Contains(err.Error(), triggerMarker) {
		t.Fatalf("ActivateSprint error=%v, want trigger marker %q", err, triggerMarker)
	}
	activeAfter := assertLifecycleState(t, st, active.ID, SprintStateActive)
	targetAfter := assertLifecycleState(t, st, target.ID, SprintStatePlanned)
	if activeAfter.ClosedAt != nil || activeAfter.StartedAt == nil || activeBefore.StartedAt == nil || !activeAfter.StartedAt.Equal(*activeBefore.StartedAt) {
		t.Fatalf("prior active was partially changed before=%+v after=%+v", activeBefore, activeAfter)
	}
	if targetAfter.StartedAt != nil || targetAfter.ClosedAt != nil {
		t.Fatalf("target was partially changed: %+v", targetAfter)
	}

	if _, err := st.db.ExecContext(ctx, `DROP TRIGGER `+triggerName); err != nil {
		t.Fatalf("drop activation fault trigger: %v", err)
	}
	if err := st.ActivateSprint(ctx, project.ID, target.ID); err != nil {
		t.Fatalf("ActivateSprint after trigger removal: %v", err)
	}
	assertLifecycleState(t, st, active.ID, SprintStateClosed)
	assertLifecycleState(t, st, target.ID, SprintStateActive)
}

func TestSprintLifecycleStoreCloseCurrentContract(t *testing.T) {
	t.Run("active closes and repeat is not found", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-close-active")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Active", now.Add(-time.Hour), now.Add(24*time.Hour))
		if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("ActivateSprint: %v", err)
		}
		before := getLifecycleSprint(t, st, sp.ID)

		if err := st.CloseSprint(ctx, project.ID, sp.ID); err != nil {
			t.Fatalf("CloseSprint: %v", err)
		}
		after := assertLifecycleState(t, st, sp.ID, SprintStateClosed)
		if after.ClosedAt == nil || after.StartedAt == nil || before.StartedAt == nil || !after.StartedAt.Equal(*before.StartedAt) {
			t.Fatalf("closed sprint timestamps before=%+v after=%+v", before, after)
		}
		if err := st.CloseSprint(ctx, project.ID, sp.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("repeat CloseSprint error=%v, want ErrNotFound", err)
		}
	})

	t.Run("planned and missing are not found", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		project, err := st.CreateProject(ctx, "lifecycle-close-invalid")
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, project.ID, "Planned", now.Add(-time.Hour), now.Add(24*time.Hour))

		if err := st.CloseSprint(ctx, project.ID, sp.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("planned CloseSprint error=%v, want ErrNotFound", err)
		}
		if err := st.CloseSprint(ctx, project.ID, sp.ID+100000); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing CloseSprint error=%v, want ErrNotFound", err)
		}
		assertLifecycleState(t, st, sp.ID, SprintStatePlanned)
	})

	t.Run("foreign project is rejected by durable mutation authority", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		projectA, err := st.CreateProject(ctx, "lifecycle-close-global-a")
		if err != nil {
			t.Fatalf("CreateProject A: %v", err)
		}
		projectB, err := st.CreateProject(ctx, "lifecycle-close-global-b")
		if err != nil {
			t.Fatalf("CreateProject B: %v", err)
		}
		if projectA.ID == projectB.ID {
			t.Fatal("fixture project IDs unexpectedly equal")
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, projectB.ID, "Foreign active", now.Add(-time.Hour), now.Add(24*time.Hour))
		if err := st.ActivateSprint(ctx, projectB.ID, sp.ID); err != nil {
			t.Fatalf("ActivateSprint B: %v", err)
		}

		if err := st.CloseSprint(ctx, projectA.ID, sp.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("CloseSprint(project A, sprint B) error=%v, want ErrNotFound", err)
		}
		assertLifecycleState(t, st, sp.ID, SprintStateActive)
		if err := st.CloseSprint(ctx, projectB.ID, sp.ID); err != nil {
			t.Fatalf("CloseSprint(project B, sprint B): %v", err)
		}
		assertLifecycleState(t, st, sp.ID, SprintStateClosed)
	})
}

func TestSprintLifecycleStoreDeleteContract(t *testing.T) {
	for _, state := range []string{SprintStatePlanned, SprintStateActive, SprintStateClosed} {
		state := state
		t.Run(strings.ToLower(state), func(t *testing.T) {
			st, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()
			project, err := st.CreateProject(ctx, "lifecycle-delete-"+strings.ToLower(state))
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			now := time.Now().UTC()
			sp := createLifecycleSprint(t, st, project.ID, state, now.Add(-time.Hour), now.Add(24*time.Hour))
			if state != SprintStatePlanned {
				if err := st.ActivateSprint(ctx, project.ID, sp.ID); err != nil {
					t.Fatalf("ActivateSprint setup: %v", err)
				}
			}
			if state == SprintStateClosed {
				if err := st.CloseSprint(ctx, project.ID, sp.ID); err != nil {
					t.Fatalf("CloseSprint setup: %v", err)
				}
			}

			if err := st.DeleteSprint(ctx, project.ID, sp.ID); err != nil {
				t.Fatalf("DeleteSprint(%s): %v", state, err)
			}
			if _, err := st.GetSprintByID(ctx, sp.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetSprintByID after delete error=%v, want ErrNotFound", err)
			}
			active, err := st.GetActiveSprintByProjectID(ctx, project.ID)
			if err != nil {
				t.Fatalf("GetActiveSprintByProjectID: %v", err)
			}
			if active != nil {
				t.Fatalf("active sprint after %s delete=%+v, want nil", state, active)
			}
		})
	}

	t.Run("foreign and missing preserve target", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		projectA, err := st.CreateProject(ctx, "lifecycle-delete-project-a")
		if err != nil {
			t.Fatalf("CreateProject A: %v", err)
		}
		projectB, err := st.CreateProject(ctx, "lifecycle-delete-project-b")
		if err != nil {
			t.Fatalf("CreateProject B: %v", err)
		}
		now := time.Now().UTC()
		sp := createLifecycleSprint(t, st, projectB.ID, "Foreign", now.Add(-time.Hour), now.Add(24*time.Hour))

		if err := st.DeleteSprint(ctx, projectA.ID, sp.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign DeleteSprint error=%v, want ErrNotFound", err)
		}
		assertLifecycleState(t, st, sp.ID, SprintStatePlanned)
		if err := st.DeleteSprint(ctx, projectA.ID, sp.ID+100000); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing DeleteSprint error=%v, want ErrNotFound", err)
		}
	})
}

func TestSprintLifecycleStoreDeleteDetachesTodos(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "lifecycle-delete-detach")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	target := createLifecycleSprint(t, st, project.ID, "Target", now.Add(-time.Hour), now.Add(24*time.Hour))
	controlSprint := createLifecycleSprint(t, st, project.ID, "Control", now.Add(-time.Hour), now.Add(48*time.Hour))
	points := int64(5)
	assignedOne, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Assigned one", Body: "body one", Tags: []string{"phase20"}, ColumnKey: DefaultColumnBacklog,
		EstimationPoints: &points, SprintID: &target.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo assigned one: %v", err)
	}
	assignedTwo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Assigned two", Body: "body two", ColumnKey: DefaultColumnDoing, SprintID: &target.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo assigned two: %v", err)
	}
	controlAssigned, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Control assigned", ColumnKey: DefaultColumnBacklog, SprintID: &controlSprint.ID,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo control assigned: %v", err)
	}
	backlog, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{
		Title: "Backlog", ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo backlog: %v", err)
	}

	if err := st.DeleteSprint(ctx, project.ID, target.ID); err != nil {
		t.Fatalf("DeleteSprint: %v", err)
	}

	for _, before := range []Todo{assignedOne, assignedTwo} {
		after, err := st.GetTodoByLocalID(ctx, project.ID, before.LocalID, ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID(%d): %v", before.LocalID, err)
		}
		if after.SprintID != nil {
			t.Fatalf("todo %d SprintID=%v, want nil after delete", before.LocalID, after.SprintID)
		}
		if after.ID != before.ID || after.ProjectID != before.ProjectID || after.LocalID != before.LocalID || after.Title != before.Title || after.Body != before.Body || after.ColumnKey != before.ColumnKey || after.Rank != before.Rank {
			t.Fatalf("todo changed beyond sprint detachment before=%+v after=%+v", before, after)
		}
	}
	afterControl, err := st.GetTodoByLocalID(ctx, project.ID, controlAssigned.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID control: %v", err)
	}
	if afterControl.SprintID == nil || *afterControl.SprintID != controlSprint.ID {
		t.Fatalf("control todo SprintID=%v, want %d", afterControl.SprintID, controlSprint.ID)
	}
	afterBacklog, err := st.GetTodoByLocalID(ctx, project.ID, backlog.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID backlog: %v", err)
	}
	if afterBacklog.SprintID != nil {
		t.Fatalf("backlog SprintID=%v, want nil", afterBacklog.SprintID)
	}
}

func TestSprintLifecycleStoreCanceledActivationDoesNotPartiallyTransition(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "lifecycle-canceled-activate")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	now := time.Now().UTC()
	active := createLifecycleSprint(t, st, project.ID, "Current", now.Add(-time.Hour), now.Add(24*time.Hour))
	target := createLifecycleSprint(t, st, project.ID, "Target", now.Add(-time.Hour), now.Add(48*time.Hour))
	if err := st.ActivateSprint(ctx, project.ID, active.ID); err != nil {
		t.Fatalf("ActivateSprint current: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	err = st.ActivateSprint(canceled, project.ID, target.ID)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateSprint canceled error=%v, want context.Canceled", err)
	}
	assertLifecycleState(t, st, active.ID, SprintStateActive)
	assertLifecycleState(t, st, target.ID, SprintStatePlanned)
}
