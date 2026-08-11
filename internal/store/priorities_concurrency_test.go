package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/version"
)

type priorityMutationResult struct {
	tier PriorityTier
	err  error
}

func runConcurrentPriorityCreates(t *testing.T, stores []*Store, ctxs []context.Context, projectID int64, names []string) []priorityMutationResult {
	t.Helper()
	start := make(chan struct{})
	results := make(chan priorityMutationResult, len(stores))
	var ready sync.WaitGroup
	ready.Add(len(stores))
	for i, st := range stores {
		go func(i int, st *Store) {
			ready.Done()
			<-start
			tier, err := st.AddPriorityTier(ctxs[i], projectID, names[i])
			results <- priorityMutationResult{tier: tier, err: err}
		}(i, st)
	}
	ready.Wait()
	close(start)
	got := make([]priorityMutationResult, 0, len(stores))
	for range stores {
		got = append(got, <-results)
	}
	return got
}

func TestConcurrentPriorityCreateAllocatesDistinctKeysAndPositions(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Concurrent priorities")
	results := runConcurrentPriorityCreates(t, []*Store{primary, concurrent}, []context.Context{ctx, ctx}, project.ID, []string{"Critical", "Critical"})
	keys := map[string]struct{}{}
	positions := map[int]struct{}{}
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent create: %v", result.err)
		}
		keys[result.tier.Key] = struct{}{}
		positions[result.tier.Position] = struct{}{}
	}
	if len(keys) != 2 || len(positions) != 2 {
		t.Fatalf("results=%+v want distinct keys and positions", results)
	}
	tiers, err := primary.GetProjectPriorities(ctx, project.ID)
	if err != nil || len(tiers) != 6 {
		t.Fatalf("tiers=%+v err=%v", tiers, err)
	}
	for i, tier := range tiers {
		if tier.Position != i {
			t.Fatalf("tier[%d]=%+v", i, tier)
		}
	}
}

func TestConcurrentPriorityCreateAtLimitReturnsDomainError(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Concurrent priority limit")
	for i := 4; i < maxPriorityTiers-1; i++ {
		if _, err := primary.AddPriorityTier(ctx, project.ID, "Tier "+strconv.Itoa(i)); err != nil {
			t.Fatalf("seed tier %d: %v", i, err)
		}
	}
	results := runConcurrentPriorityCreates(t, []*Store{primary, concurrent}, []context.Context{ctx, ctx}, project.ID, []string{"Boundary A", "Boundary B"})
	successes, validations := 0, 0
	for _, result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrValidation) && ErrorReason(result.err) == ReasonPriorityTierLimitReached:
			validations++
		case strings.Contains(strings.ToLower(result.err.Error()), "busy"):
			t.Fatalf("raw SQLite busy escaped: %v", result.err)
		default:
			t.Fatalf("unexpected result: %v", result.err)
		}
	}
	if successes != 1 || validations != 1 {
		t.Fatalf("results=%+v successes=%d validations=%d", results, successes, validations)
	}
}

func TestConcurrentPriorityDeleteVersusTodoAssignmentIsLinear(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Priority delete assignment")
	todo, err := primary.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "Unassigned"}, ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	high := "high"
	start := make(chan struct{})
	deleteResult := make(chan error, 1)
	assignmentResult := make(chan error, 1)
	go func() {
		<-start
		deleteResult <- primary.DeletePriorityTier(ctx, project.ID, high)
	}()
	go func() {
		<-start
		_, err := concurrent.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
			Title: todo.Title, Body: todo.Body, Tags: todo.Tags, EstimationPoints: todo.EstimationPoints,
			AssigneeUserID: todo.AssigneeUserID, PriorityKey: &high, PriorityKeyPresent: true,
		}, ModeFull)
		assignmentResult <- err
	}()
	close(start)
	deleteErr, assignmentErr := <-deleteResult, <-assignmentResult
	deleteFirst := deleteErr == nil && errors.Is(assignmentErr, ErrValidation)
	assignmentFirst := errors.Is(deleteErr, ErrConflict) && assignmentErr == nil
	if !deleteFirst && !assignmentFirst {
		t.Fatalf("delete=%v assignment=%v want a linear domain outcome", deleteErr, assignmentErr)
	}
	for _, err := range []error{deleteErr, assignmentErr} {
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "busy") {
			t.Fatalf("raw SQLite busy escaped: %v", err)
		}
	}
	var dangling int
	if err := primary.db.QueryRow(`SELECT COUNT(*) FROM todos t LEFT JOIN project_priorities pp ON pp.project_id=t.project_id AND pp.key=t.priority_key WHERE t.project_id=? AND t.priority_key IS NOT NULL AND pp.id IS NULL`, project.ID).Scan(&dangling); err != nil || dangling != 0 {
		t.Fatalf("dangling=%d err=%v", dangling, err)
	}
}

func TestConcurrentPriorityDeleteVersusUpdateIsLinear(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Priority delete update")
	start := make(chan struct{})
	deleteResult := make(chan error, 1)
	updateResult := make(chan error, 1)
	go func() {
		<-start
		deleteResult <- primary.DeletePriorityTier(ctx, project.ID, "high")
	}()
	go func() {
		<-start
		updateResult <- concurrent.UpdatePriorityTier(ctx, project.ID, "high", "Elevated", "#123456")
	}()
	close(start)
	deleteErr, updateErr := <-deleteResult, <-updateResult
	if deleteErr != nil || (updateErr != nil && !errors.Is(updateErr, ErrNotFound)) {
		t.Fatalf("delete=%v update=%v want update-then-delete or delete-then-not-found", deleteErr, updateErr)
	}
	for _, err := range []error{deleteErr, updateErr} {
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "busy") {
			t.Fatalf("raw SQLite busy escaped: %v", err)
		}
	}
}

func TestConcurrentPriorityDeletesRemainContiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys [2]string
	}{
		{name: "same tier", keys: [2]string{"high", "high"}},
		{name: "different tiers", keys: [2]string{"high", "medium"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primary, concurrent, _, _ := newSprintConcurrencyStores(t)
			ctx, _, project := createSprintConcurrencyProject(t, primary, "Concurrent priority deletes")
			start := make(chan struct{})
			results := make(chan error, 2)
			for i, st := range []*Store{primary, concurrent} {
				go func(st *Store, key string) {
					<-start
					results <- st.DeletePriorityTier(ctx, project.ID, key)
				}(st, tc.keys[i])
			}
			close(start)
			errs := []error{<-results, <-results}
			successes, notFound := 0, 0
			for _, err := range errs {
				switch {
				case err == nil:
					successes++
				case errors.Is(err, ErrNotFound):
					notFound++
				case strings.Contains(strings.ToLower(err.Error()), "busy"):
					t.Fatalf("raw SQLite busy escaped: %v", err)
				default:
					t.Fatalf("unexpected delete result: %v", err)
				}
			}
			if tc.keys[0] == tc.keys[1] {
				if successes != 1 || notFound != 1 {
					t.Fatalf("same-tier results=%v", errs)
				}
			} else if successes != 2 {
				t.Fatalf("different-tier results=%v", errs)
			}
			tiers, err := primary.GetProjectPriorities(ctx, project.ID)
			if err != nil {
				t.Fatalf("list tiers: %v", err)
			}
			for position, tier := range tiers {
				if tier.Position != position {
					t.Fatalf("tiers=%+v are not contiguous", tiers)
				}
			}
		})
	}
}

func TestImportPriorityConcurrentAssignmentIsLinear(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Import priority race")
	todo, err := primary.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "Race target"}, ModeFull)
	if err != nil {
		t.Fatalf("create todo: %v", err)
	}
	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion,
		Mode:    "full",
		Scope:   "full",
		Projects: []ProjectExport{{
			Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now,
			PriorityTiersPresent: true,
			PriorityTiers: []PriorityTierExport{
				{Key: "low", Name: "Low", Color: "#9CA3AF", Position: 0},
				{Key: "medium", Name: "Medium", Color: "#F59E0B", Position: 1},
				{Key: "urgent", Name: "Urgent", Color: "#EF4444", Position: 2},
			},
			Todos: []TodoExport{{
				LocalID: todo.LocalID, Title: todo.Title, Status: todo.ColumnKey, Rank: todo.Rank,
				CreatedAt: now, UpdatedAt: now,
			}},
		}},
	}
	high := "high"
	start := make(chan struct{})
	importResult := make(chan error, 1)
	assignmentResult := make(chan error, 1)
	go func() {
		<-start
		_, err := primary.ImportProjects(ctx, data, ModeFull, "merge")
		importResult <- err
	}()
	go func() {
		<-start
		_, err := concurrent.UpdateTodo(ctx, todo.ID, UpdateTodoInput{
			Title: todo.Title, Body: todo.Body, Tags: todo.Tags, EstimationPoints: todo.EstimationPoints,
			AssigneeUserID: todo.AssigneeUserID, PriorityKey: &high, PriorityKeyPresent: true,
		}, ModeFull)
		assignmentResult <- err
	}()
	close(start)
	importErr, assignmentErr := <-importResult, <-assignmentResult
	importFirst := importErr == nil && errors.Is(assignmentErr, ErrValidation)
	assignmentFirst := errors.Is(importErr, ErrValidation) && assignmentErr == nil
	if !importFirst && !assignmentFirst {
		t.Fatalf("import=%v assignment=%v want a linear domain outcome", importErr, assignmentErr)
	}
	for _, err := range []error{importErr, assignmentErr} {
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "busy") {
			t.Fatalf("raw SQLite busy escaped: %v", err)
		}
	}
	var dangling int
	if err := primary.db.QueryRow(`SELECT COUNT(*) FROM todos t LEFT JOIN project_priorities pp ON pp.project_id=t.project_id AND pp.key=t.priority_key WHERE t.project_id=? AND t.priority_key IS NOT NULL AND pp.id IS NULL`, project.ID).Scan(&dangling); err != nil || dangling != 0 {
		t.Fatalf("dangling=%d err=%v", dangling, err)
	}
}
