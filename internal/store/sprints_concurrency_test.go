package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func newSprintConcurrencyStores(t *testing.T) (*Store, *Store, *sql.DB, *sql.DB) {
	t.Helper()
	databasePath := filepath.Join(t.TempDir(), "app.db")
	options := db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"}
	primaryDB, err := db.Open(databasePath, options)
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	if err := migrate.Apply(context.Background(), primaryDB); err != nil {
		_ = primaryDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	secondaryDB, err := db.Open(databasePath, options)
	if err != nil {
		_ = primaryDB.Close()
		t.Fatalf("open secondary db: %v", err)
	}
	t.Cleanup(func() {
		_ = secondaryDB.Close()
		_ = primaryDB.Close()
	})
	return New(primaryDB, nil), New(secondaryDB, nil), primaryDB, secondaryDB
}

func createSprintConcurrencyProject(t *testing.T, st *Store, name string) (context.Context, User, Project) {
	t.Helper()
	ctx, user := dashboardTestContext(t, st)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return ctx, user, project
}

func beginAuthoritativeSprintDisable(t *testing.T, sqlDB *sql.DB, projectID int64) *sql.Tx {
	t.Helper()
	tx, err := sqlDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin disable transaction: %v", err)
	}
	if _, err := tx.Exec(`UPDATE projects SET sprints_enabled = 0, updated_at = ? WHERE id = ?`, time.Now().UTC().UnixMilli(), projectID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("make disable authoritative: %v", err)
	}
	return tx
}

func awaitSprintRaceError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent sprint operation did not finish")
		return nil
	}
}

func TestSprintCapabilitySerializationDisableFirstRejectsSprintMutations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(context.Context, *Store, Project, Sprint) error
	}{
		{
			name: "create",
			mutate: func(ctx context.Context, st *Store, project Project, _ Sprint) error {
				now := time.Now().UTC()
				_, err := st.CreateSprint(ctx, project.ID, "Blocked concurrent create", now, now.Add(7*24*time.Hour))
				return err
			},
		},
		{
			name: "update existing",
			mutate: func(ctx context.Context, st *Store, _ Project, sprint Sprint) error {
				name := "Blocked concurrent rename"
				return st.UpdateSprint(ctx, sprint.ID, UpdateSprintInput{Name: &name})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary, concurrent, primaryDB, _ := newSprintConcurrencyStores(t)
			ctx, _, project := createSprintConcurrencyProject(t, primary, "Disable first "+tc.name)
			now := time.Now().UTC()
			existing, err := primary.CreateSprint(ctx, project.ID, "Existing", now, now.Add(7*24*time.Hour))
			if err != nil {
				t.Fatalf("CreateSprint fixture: %v", err)
			}

			disableTx := beginAuthoritativeSprintDisable(t, primaryDB, project.ID)
			started := make(chan struct{})
			result := make(chan error, 1)
			go func() {
				close(started)
				result <- tc.mutate(ctx, concurrent, project, existing)
			}()
			<-started
			if err := disableTx.Commit(); err != nil {
				t.Fatalf("commit disable transaction: %v", err)
			}
			if err := awaitSprintRaceError(t, result); !errors.Is(err, ErrSprintsDisabled) {
				t.Fatalf("concurrent mutation error=%v, want ErrSprintsDisabled", err)
			}

			sprints, err := primary.ListSprints(ctx, project.ID)
			if err != nil {
				t.Fatalf("ListSprints: %v", err)
			}
			if len(sprints) != 1 || sprints[0].ID != existing.ID || sprints[0].Name != "Existing" {
				t.Fatalf("disabled-first mutation changed retained sprints: %+v", sprints)
			}
			storedProject, err := primary.GetProject(ctx, project.ID)
			if err != nil || storedProject.SprintsEnabled {
				t.Fatalf("project after disable=%+v err=%v, want disabled", storedProject, err)
			}
		})
	}
}

func TestSprintCapabilitySerializationMutationFirstCommitsBeforeDisable(t *testing.T) {
	primary, concurrent, primaryDB, _ := newSprintConcurrencyStores(t)
	ctx, user, project := createSprintConcurrencyProject(t, primary, "Mutation first")
	now := time.Now().UTC()

	mutationTx, err := primaryDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin sprint mutation transaction: %v", err)
	}
	if err := lockProjectSprintsEnabledTx(ctx, mutationTx, project.ID); err != nil {
		_ = mutationTx.Rollback()
		t.Fatalf("lock enabled capability: %v", err)
	}
	if _, err := mutationTx.Exec(`
		INSERT INTO sprints(project_id, name, planned_start_at, planned_end_at, state, number, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
	`, project.ID, "Serialized first", now.UnixMilli(), now.Add(7*24*time.Hour).UnixMilli(), SprintStatePlanned, now.UnixMilli(), now.UnixMilli()); err != nil {
		_ = mutationTx.Rollback()
		t.Fatalf("insert serialized sprint: %v", err)
	}

	started := make(chan struct{})
	disableResult := make(chan error, 1)
	go func() {
		close(started)
		disableResult <- concurrent.UpdateProjectSprintsEnabled(ctx, project.ID, user.ID, false)
	}()
	<-started
	if err := mutationTx.Commit(); err != nil {
		t.Fatalf("commit sprint mutation: %v", err)
	}
	if err := awaitSprintRaceError(t, disableResult); err != nil {
		t.Fatalf("disable after committed mutation: %v", err)
	}
	sprints, err := primary.ListSprints(ctx, project.ID)
	if err != nil || len(sprints) != 1 || sprints[0].Name != "Serialized first" {
		t.Fatalf("retained first mutation sprints=%+v err=%v", sprints, err)
	}
	storedProject, err := primary.GetProject(ctx, project.ID)
	if err != nil || storedProject.SprintsEnabled {
		t.Fatalf("project after serialized disable=%+v err=%v", storedProject, err)
	}
}

func TestSprintCapabilitySerializationDisableFirstRejectsTodoSprintAssignment(t *testing.T) {
	primary, concurrent, primaryDB, secondaryDB := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Disable versus assignment")
	now := time.Now().UTC()
	sprint, err := primary.CreateSprint(ctx, project.ID, "Assignment target", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	todo, err := primary.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "Unscheduled", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	var beforeUpdatedAt int64
	if err := secondaryDB.QueryRow(`SELECT updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&beforeUpdatedAt); err != nil {
		t.Fatalf("read todo sentinel: %v", err)
	}

	disableTx := beginAuthoritativeSprintDisable(t, primaryDB, project.ID)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		_, err := concurrent.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
			Title: todo.Title, Body: todo.Body, Tags: todo.Tags, EstimationPoints: todo.EstimationPoints,
			AssigneeUserID: todo.AssigneeUserID, SprintID: &sprint.ID,
		}, ModeFull)
		result <- err
	}()
	<-started
	if err := disableTx.Commit(); err != nil {
		t.Fatalf("commit disable transaction: %v", err)
	}
	if err := awaitSprintRaceError(t, result); !errors.Is(err, ErrSprintsDisabled) {
		t.Fatalf("concurrent assignment error=%v, want ErrSprintsDisabled", err)
	}

	var sprintID sql.NullInt64
	var updatedAt int64
	if err := secondaryDB.QueryRow(`SELECT sprint_id, updated_at FROM todos WHERE id = ?`, todo.ID).Scan(&sprintID, &updatedAt); err != nil {
		t.Fatalf("read todo after race: %v", err)
	}
	if sprintID.Valid || updatedAt != beforeUpdatedAt {
		t.Fatalf("disabled-first assignment mutated todo: sprint=%+v updatedAt=%d want=%d", sprintID, updatedAt, beforeUpdatedAt)
	}
	var auditCount int
	if err := secondaryDB.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE target_type = 'todo' AND target_id = ? AND action = 'todo_updated'`, todo.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count assignment audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("disabled-first assignment audit rows=%d want=0", auditCount)
	}
}

func TestConcurrentSprintCreationAllocatesDistinctNumbers(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Concurrent sprint numbers")
	now := time.Now().UTC()
	start := make(chan struct{})
	results := make(chan struct {
		sprint Sprint
		err    error
	}, 2)

	var ready sync.WaitGroup
	ready.Add(2)
	for index, st := range []*Store{primary, concurrent} {
		go func(index int, st *Store) {
			ready.Done()
			<-start
			sprint, err := st.CreateSprint(ctx, project.ID, []string{"Concurrent A", "Concurrent B"}[index], now, now.Add(7*24*time.Hour))
			results <- struct {
				sprint Sprint
				err    error
			}{sprint: sprint, err: err}
		}(index, st)
	}
	ready.Wait()
	close(start)

	numbers := make([]int64, 0, 2)
	ids := make(map[int64]struct{}, 2)
	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Fatalf("concurrent CreateSprint: %v", result.err)
			}
			numbers = append(numbers, result.sprint.Number)
			ids[result.sprint.ID] = struct{}{}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent CreateSprint did not finish")
		}
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	if len(ids) != 2 || len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("concurrent sprint identities ids=%v numbers=%v, want two distinct IDs numbered 1 and 2", ids, numbers)
	}
}
