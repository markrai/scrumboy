package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSprintDefinitionCreate_InsertRemainsCommittedWhenReturnReadFails(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "sprint-definition-committed-create")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	const corruptValue = "checkpoint-one-return-read-failure"
	if _, err := st.db.ExecContext(ctx, `
		CREATE TRIGGER sprint_definition_corrupt_created_row
		AFTER INSERT ON sprints
		BEGIN
			UPDATE sprints
			SET planned_start_at = '`+corruptValue+`'
			WHERE id = NEW.id;
		END
	`); err != nil {
		t.Fatalf("create post-insert fault trigger: %v", err)
	}

	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	created, err := st.CreateSprint(ctx, project.ID, "Committed sprint", start, start.Add(7*24*time.Hour))
	if err == nil {
		t.Fatalf("CreateSprint returned %+v, want return-read failure", created)
	}
	if !strings.Contains(err.Error(), "get sprint") || !strings.Contains(err.Error(), corruptValue) {
		t.Fatalf("CreateSprint error=%q, want typed return-read failure containing %q", err, corruptValue)
	}

	var (
		rowCount       int
		storedName     string
		storedStartRaw string
		storedNumber   int64
	)
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(name), MIN(CAST(planned_start_at AS TEXT)), MIN(number)
		FROM sprints
		WHERE project_id = ?
	`, project.ID).Scan(&rowCount, &storedName, &storedStartRaw, &storedNumber); err != nil {
		t.Fatalf("query committed sprint row: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("committed sprint rows=%d, want exactly one insert attempt", rowCount)
	}
	if storedName != "Committed sprint" || storedStartRaw != corruptValue || storedNumber != 1 {
		t.Fatalf("committed row=(name=%q start=%q number=%d), want inserted sprint with post-insert fault", storedName, storedStartRaw, storedNumber)
	}

	if _, err := st.GetSprintByProjectNumber(ctx, project.ID, storedNumber); err == nil || !strings.Contains(err.Error(), corruptValue) {
		t.Fatalf("GetSprintByProjectNumber error=%v, want independent proof the committed row remains unreadable", err)
	}
}

func TestSprintDefinitionStoreOwnsDefinitionValidationByState(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	project, err := st.CreateProject(ctx, "sprint-definition-store-policy")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	start := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

	t.Run("create trims names and rejects duplicates and reversed dates", func(t *testing.T) {
		created, err := st.CreateSprint(ctx, project.ID, "  Definition A  ", start, start.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("CreateSprint: %v", err)
		}
		if created.Name != "Definition A" {
			t.Fatalf("created name=%q, want store-trimmed name", created.Name)
		}
		if _, err := st.CreateSprint(ctx, project.ID, "Definition A", start, start.Add(7*24*time.Hour)); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("duplicate CreateSprint error=%v", err)
		}
		if _, err := st.CreateSprint(ctx, project.ID, "Definition B", start, start.Add(-time.Millisecond)); err == nil || !strings.Contains(err.Error(), "end_at must be >= start_at") {
			t.Fatalf("reversed-date CreateSprint error=%v", err)
		}
	})

	t.Run("active allows only planned end", func(t *testing.T) {
		active, err := st.CreateSprint(ctx, project.ID, "Active definition", start, start.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("CreateSprint: %v", err)
		}
		if err := st.ActivateSprint(ctx, project.ID, active.ID); err != nil {
			t.Fatalf("ActivateSprint: %v", err)
		}
		newName := "renamed active"
		if err := st.UpdateSprint(ctx, active.ID, UpdateSprintInput{Name: &newName}); err == nil || !strings.Contains(err.Error(), "only endAt") {
			t.Fatalf("active name UpdateSprint error=%v", err)
		}
		newEnd := start.Add(8 * 24 * time.Hour)
		if err := st.UpdateSprint(ctx, active.ID, UpdateSprintInput{PlannedEndAt: &newEnd}); err != nil {
			t.Fatalf("active end UpdateSprint: %v", err)
		}
	})

	t.Run("closed allows only name", func(t *testing.T) {
		closed, err := st.CreateSprint(ctx, project.ID, "Closed definition", start.Add(20*24*time.Hour), start.Add(27*24*time.Hour))
		if err != nil {
			t.Fatalf("CreateSprint: %v", err)
		}
		if _, err := st.db.ExecContext(ctx, `UPDATE sprints SET state = ?, started_at = ?, closed_at = ? WHERE id = ?`, SprintStateClosed, start.UnixMilli(), start.Add(24*time.Hour).UnixMilli(), closed.ID); err != nil {
			t.Fatalf("set closed fixture state: %v", err)
		}
		newEnd := start.Add(28 * 24 * time.Hour)
		if err := st.UpdateSprint(ctx, closed.ID, UpdateSprintInput{PlannedEndAt: &newEnd}); err == nil || !strings.Contains(err.Error(), "dates cannot be updated") {
			t.Fatalf("closed date UpdateSprint error=%v", err)
		}
		newName := "Renamed closed definition"
		if err := st.UpdateSprint(ctx, closed.ID, UpdateSprintInput{Name: &newName}); err != nil {
			t.Fatalf("closed name UpdateSprint: %v", err)
		}
		got, err := st.GetSprintByID(ctx, closed.ID)
		if err != nil {
			t.Fatalf("GetSprintByID: %v", err)
		}
		if got.Name != newName || got.State != SprintStateClosed {
			t.Fatalf("closed sprint=%+v, want renamed definition without state change", got)
		}
	})
}
