package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCreateProject_SeedsDefaultPriorities(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-defaults")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	tiers, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities: %v", err)
	}
	want := []string{"low", "medium", "high", "urgent"}
	if len(tiers) != len(want) {
		t.Fatalf("expected %d default tiers, got %d", len(want), len(tiers))
	}
	for i, tier := range tiers {
		if tier.Key != want[i] {
			t.Fatalf("tier %d: want key %q, got %q", i, want[i], tier.Key)
		}
		if tier.Position != i {
			t.Fatalf("tier %q: want position %d, got %d", tier.Key, i, tier.Position)
		}
	}
}

func TestCreateAnonymousBoardPrioritySeedFailureRollsBackProjectAndAudit(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER reject_priority_seed BEFORE INSERT ON project_priorities BEGIN SELECT RAISE(ABORT, 'reject priority seed'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	var projectsBefore, auditsBefore int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projectsBefore); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditsBefore); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if _, err := st.CreateAnonymousBoard(ctx); err == nil {
		t.Fatal("CreateAnonymousBoard should fail when priority seeding fails")
	}
	var projectsAfter, auditsAfter int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&projectsAfter); err != nil {
		t.Fatalf("count projects after: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&auditsAfter); err != nil {
		t.Fatalf("count audits after: %v", err)
	}
	if projectsAfter != projectsBefore || auditsAfter != auditsBefore {
		t.Fatalf("rollback projects %d->%d audits %d->%d", projectsBefore, projectsAfter, auditsBefore, auditsAfter)
	}
}

func TestPriorityTierValidationLimits(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-limits")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	name200 := strings.Repeat("a", 200)
	if _, err := st.AddPriorityTier(ctx, project.ID, name200); err != nil {
		t.Fatalf("200-character name: %v", err)
	}
	if _, err := st.AddPriorityTier(ctx, project.ID, strings.Repeat("b", 201)); !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonInvalidPriorityTierName {
		t.Fatalf("201-character name error=%v reason=%q", err, ErrorReason(err))
	}
	for i := 5; i < maxPriorityTiers; i++ {
		if _, err := st.AddPriorityTier(ctx, project.ID, "limit-tier-"+string(rune('a'+i))); err != nil {
			t.Fatalf("fill tier %d: %v", i, err)
		}
	}
	if _, err := st.AddPriorityTier(ctx, project.ID, "one too many"); !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonPriorityTierLimitReached {
		t.Fatalf("thirteenth tier error=%v reason=%q", err, ErrorReason(err))
	}
}

func TestGetProjectPriorities_IsReadOnlyWhenDefinitionsAreMissing(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-read-only")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_priorities WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("delete fixture priorities: %v", err)
	}
	tiers, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities: %v", err)
	}
	if len(tiers) != 0 {
		t.Fatalf("tiers=%v want empty read", tiers)
	}
	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_priorities WHERE project_id = ?`, project.ID).Scan(&count); err != nil {
		t.Fatalf("count priorities: %v", err)
	}
	if count != 0 {
		t.Fatalf("read inserted %d tiers", count)
	}
}

func TestAddPriorityTier_AppendsAtEnd(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-add")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	added, err := st.AddPriorityTier(ctx, project.ID, "Critical")
	if err != nil {
		t.Fatalf("AddPriorityTier: %v", err)
	}
	if added.Key != "critical" {
		t.Fatalf("expected generated key %q, got %q", "critical", added.Key)
	}
	if added.Position != 4 {
		t.Fatalf("expected new tier appended at position 4, got %d", added.Position)
	}

	tiers, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities: %v", err)
	}
	if len(tiers) != 5 {
		t.Fatalf("expected 5 tiers after add, got %d", len(tiers))
	}
	if tiers[4].Key != "critical" {
		t.Fatalf("expected last tier to be %q, got %q", "critical", tiers[4].Key)
	}
}

func TestAddPriorityTier_RejectsEmptyName(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-add-empty")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if _, err := st.AddPriorityTier(ctx, project.ID, "   "); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for empty name, got %v", err)
	}
}

func TestUpdatePriorityTier_ChangesNameAndColor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-update")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.UpdatePriorityTier(ctx, project.ID, "low", "Chill", "#112233"); err != nil {
		t.Fatalf("UpdatePriorityTier: %v", err)
	}

	tiers, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities: %v", err)
	}
	if tiers[0].Name != "Chill" || tiers[0].Color != "#112233" {
		t.Fatalf("expected updated tier, got %+v", tiers[0])
	}
	if tiers[0].Key != "low" || tiers[0].Position != 0 {
		t.Fatalf("expected key/position unchanged, got %+v", tiers[0])
	}
}

func TestUpdatePriorityTier_RejectsInvalidColor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-update-badcolor")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.UpdatePriorityTier(ctx, project.ID, "low", "Chill", "not-a-color"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for invalid color, got %v", err)
	}
}

func TestUpdatePriorityTier_NotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-update-missing")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.UpdatePriorityTier(ctx, project.ID, "does_not_exist", "Name", "#112233"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeletePriorityTier_ResequencesPositions(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-delete")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.DeletePriorityTier(ctx, project.ID, "medium"); err != nil {
		t.Fatalf("DeletePriorityTier: %v", err)
	}

	tiers, err := st.GetProjectPriorities(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectPriorities: %v", err)
	}
	want := []string{"low", "high", "urgent"}
	if len(tiers) != len(want) {
		t.Fatalf("expected %d tiers after delete, got %d", len(want), len(tiers))
	}
	for i, tier := range tiers {
		if tier.Key != want[i] {
			t.Fatalf("tier %d: want key %q, got %q", i, want[i], tier.Key)
		}
		if tier.Position != i {
			t.Fatalf("tier %q: want resequenced position %d, got %d", tier.Key, i, tier.Position)
		}
	}
}

func TestDeletePriorityTier_BlockedWhenInUse(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-delete-inuse")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	low := "low"
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "t1", PriorityKey: &low}, ModeFull); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := st.DeletePriorityTier(ctx, project.ID, "low"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict for in-use tier, got %v", err)
	}
}

func TestDeletePriorityTier_BlockedWhenLastTier(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-delete-last")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for _, key := range []string{"medium", "high", "urgent"} {
		if err := st.DeletePriorityTier(ctx, project.ID, key); err != nil {
			t.Fatalf("DeletePriorityTier(%q): %v", key, err)
		}
	}

	if err := st.DeletePriorityTier(ctx, project.ID, "low"); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation deleting the last remaining tier, got %v", err)
	}
}

func TestDeletePriorityTier_NotFound(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-delete-missing")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := st.DeletePriorityTier(ctx, project.ID, "does_not_exist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCreateTodo_ValidatesPriorityKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-todo-create")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	bogus := "not-a-real-tier"
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "t1", PriorityKey: &bogus}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for unknown priority key, got %v", err)
	}

	urgent := "urgent"
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "t2", PriorityKey: &urgent}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo with valid priority: %v", err)
	}
	if todo.PriorityKey == nil || *todo.PriorityKey != "urgent" {
		t.Fatalf("expected priority key %q, got %v", "urgent", todo.PriorityKey)
	}
}

func TestUpdateTodoByLocalID_ChangesPriorityKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-todo-update")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "t1"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if todo.PriorityKey != nil {
		t.Fatalf("expected no priority set on create, got %v", *todo.PriorityKey)
	}

	high := "high"
	updated, err := st.UpdateTodoByLocalID(ctx, project.ID, todo.LocalID, UpdateTodoInput{
		Title:              todo.Title,
		PriorityKey:        &high,
		PriorityKeyPresent: true,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodoByLocalID: %v", err)
	}
	if updated.PriorityKey == nil || *updated.PriorityKey != "high" {
		t.Fatalf("expected priority key %q, got %v", "high", updated.PriorityKey)
	}

	// Omission preserves the existing assignment.
	preserved, err := st.UpdateTodoByLocalID(ctx, project.ID, todo.LocalID, UpdateTodoInput{Title: todo.Title}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodoByLocalID (preserve): %v", err)
	}
	if preserved.PriorityKey == nil || *preserved.PriorityKey != "high" {
		t.Fatalf("expected omitted priority to preserve high, got %v", preserved.PriorityKey)
	}

	// Explicitly present nil clears the priority.
	cleared, err := st.UpdateTodoByLocalID(ctx, project.ID, todo.LocalID, UpdateTodoInput{
		Title:              todo.Title,
		PriorityKeyPresent: true,
	}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodoByLocalID (clear): %v", err)
	}
	if cleared.PriorityKey != nil {
		t.Fatalf("expected priority cleared, got %v", *cleared.PriorityKey)
	}

	// Reload from DB to confirm persistence, not just the in-memory struct.
	reloaded, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if reloaded.PriorityKey != nil {
		t.Fatalf("expected persisted priority cleared, got %v", *reloaded.PriorityKey)
	}
}

func TestUpdateTodo_ValidatesPriorityKey(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "priority-todo-update-invalid")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "t1"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	bogus := "not-a-real-tier"
	if _, err := st.UpdateTodoByLocalID(ctx, project.ID, todo.LocalID, UpdateTodoInput{
		Title:              todo.Title,
		PriorityKey:        &bogus,
		PriorityKeyPresent: true,
	}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for unknown priority key, got %v", err)
	}
}
