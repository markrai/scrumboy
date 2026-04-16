package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type BurndownPoint struct {
	Date             time.Time `json:"date"`
	IncompleteCount  *int      `json:"incompleteCount,omitempty"` // Nullable: nil for pre-project days
	TotalScope       *int      `json:"totalScope,omitempty"`      // Total todos created by end of day (for scope line)
	IncompletePoints *int      `json:"incompletePoints,omitempty"`
	TotalScopePoints *int      `json:"totalScopePoints,omitempty"`
	NewTodosCount    int       `json:"newTodosCount"` // Todos created on this day
}

type RealBurndownPoint struct {
	Date               time.Time `json:"date"`
	RemainingWork      *int      `json:"remainingWork,omitempty"` // Nullable: nil for pre-scope days
	InitialScope       int       `json:"initialScope"`            // Fixed scope at window start
	RemainingPoints    *int      `json:"remainingPoints,omitempty"`
	InitialScopePoints *int      `json:"initialScopePoints,omitempty"`
}

type burndownTodoEvent struct {
	id               int64
	columnKey        string
	createdAt        time.Time
	updatedAt        time.Time
	doneAt           *time.Time
	estimationPoints *int64
}

// intPtr returns a pointer to a new int with the given value
// This avoids pointer reuse bugs when taking addresses of loop variables
func intPtr(v int) *int {
	return &v
}

func hasAnyEstimatedWork(todos []burndownTodoEvent) bool {
	for _, t := range todos {
		if t.estimationPoints != nil {
			return true
		}
	}
	return false
}

func isIncompleteAtDayEnd(t burndownTodoEvent, dayEnd time.Time, doneColumnKey string) bool {
	if t.columnKey != doneColumnKey {
		return true
	}
	// Use done_at when set; fall back to updated_at for legacy rows
	completionTime := t.updatedAt
	if t.doneAt != nil {
		completionTime = *t.doneAt
	}
	return completionTime.After(dayEnd)
}

func workValue(t burndownTodoEvent, usePoints bool) int {
	if !usePoints {
		return 1
	}
	if t.estimationPoints == nil {
		return 0
	}
	return int(*t.estimationPoints)
}

func (s *Store) loadBurndownTodos(ctx context.Context, projectID int64) ([]burndownTodoEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, column_key, created_at, updated_at, done_at, estimation_points
		FROM todos
		WHERE project_id = ?
		ORDER BY created_at ASC
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("query todos: %w", err)
	}
	defer rows.Close()

	var todos []burndownTodoEvent
	for rows.Next() {
		var t burndownTodoEvent
		var columnKey string
		var createdAtMs, updatedAtMs int64
		var doneAtMs sql.NullInt64
		var estimationPoints sql.NullInt64
		if err := rows.Scan(&t.id, &columnKey, &createdAtMs, &updatedAtMs, &doneAtMs, &estimationPoints); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		t.columnKey = columnKey
		t.createdAt = time.UnixMilli(createdAtMs).UTC()
		t.updatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.doneAt = &dt
		}
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.estimationPoints = &v
		}
		todos = append(todos, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}
	return todos, nil
}

// loadBurndownTodosForSprint loads todos assigned to the given sprint for burndown.
// Scope = todos currently in the sprint (sprint_id = sprintID).
func (s *Store) loadBurndownTodosForSprint(ctx context.Context, projectID, sprintID int64) ([]burndownTodoEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, column_key, created_at, updated_at, done_at, estimation_points
		FROM todos
		WHERE project_id = ? AND sprint_id = ?
		ORDER BY created_at ASC
	`, projectID, sprintID)
	if err != nil {
		return nil, fmt.Errorf("query todos for sprint: %w", err)
	}
	defer rows.Close()

	var todos []burndownTodoEvent
	for rows.Next() {
		var t burndownTodoEvent
		var columnKey string
		var createdAtMs, updatedAtMs int64
		var doneAtMs sql.NullInt64
		var estimationPoints sql.NullInt64
		if err := rows.Scan(&t.id, &columnKey, &createdAtMs, &updatedAtMs, &doneAtMs, &estimationPoints); err != nil {
			return nil, fmt.Errorf("scan todo: %w", err)
		}
		t.columnKey = columnKey
		t.createdAt = time.UnixMilli(createdAtMs).UTC()
		t.updatedAt = time.UnixMilli(updatedAtMs).UTC()
		if doneAtMs.Valid {
			dt := time.UnixMilli(doneAtMs.Int64).UTC()
			t.doneAt = &dt
		}
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.estimationPoints = &v
		}
		todos = append(todos, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows todos: %w", err)
	}
	return todos, nil
}

// GetBacklogSize returns backlog size data anchored to actual data
// Returns points with:
// - incompleteCount: nil for days before first todo, actual count otherwise
// - totalScope: total todos created by end of day (for scope overlay)
// - newTodosCount: todos created on this specific day
//
// PERFORMANCE NOTE: This implementation uses a naive O(days × todos) algorithm.
// For typical projects (< 1000 todos, 14-day window), this is acceptable and simple.
// If performance becomes an issue with large datasets, consider optimizing with:
// - Pre-bucketing todos by creation date
// - Single-pass aggregation with date bucketing
// - Caching results with TTL
func (s *Store) GetBacklogSize(ctx context.Context, projectID int64, mode Mode) ([]BurndownPoint, error) {
	// Verify project exists
	project, err := s.getProjectForRead(ctx, projectID, mode)
	if err != nil {
		return nil, err
	}

	doneColumnKey, err := s.GetWorkflowDoneColumnKey(ctx, projectID)
	if err != nil {
		doneColumnKey = DefaultColumnDone
	}

	todos, err := s.loadBurndownTodos(ctx, projectID)
	if err != nil {
		return nil, err
	}
	pointsMode := project.EstimationMode == EstimationModeModifiedFibonacci && hasAnyEstimatedWork(todos)

	// Calculate backlog size window: anchor to actual data or last 14 days, whichever is shorter
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Find earliest todo creation day (UTC midnight)
	var firstTodoDayStart *time.Time
	if len(todos) > 0 {
		firstCreated := todos[0].createdAt
		firstDayStart := time.Date(firstCreated.Year(), firstCreated.Month(), firstCreated.Day(), 0, 0, 0, 0, time.UTC)
		firstTodoDayStart = &firstDayStart
	}

	// Calculate start date: min(first todo creation day, today - 13 days)
	// This ensures we show at most 14 days, but start from when data actually exists
	startDate := todayStart.AddDate(0, 0, -13) // Default: 14 days ago
	if firstTodoDayStart != nil {
		if firstTodoDayStart.Before(startDate) {
			startDate = *firstTodoDayStart
		}
	}

	// Generate points with nulls for pre-project days
	// NOTE: O(days × todos) - intentionally naive for simplicity and correctness
	points := make([]BurndownPoint, 0, 14)
	currentDate := startDate

	for !currentDate.After(todayStart) {
		dayStart := currentDate
		dayEnd := dayStart.Add(24 * time.Hour)

		// Count todos created on this day
		newTodosCount := 0
		// Count total todos created by end of this day (for scope line)
		totalScope := 0
		// Count incomplete todos at end of this day
		incompleteCount := 0
		var totalScopePoints int
		var incompletePoints int

		// Single pass: count and (when points mode) sum points
		for _, t := range todos {
			if t.createdAt.After(dayStart) && !t.createdAt.After(dayEnd) {
				newTodosCount++
			}
			if !t.createdAt.After(dayEnd) {
				totalScope++
				if pointsMode {
					totalScopePoints += workValue(t, true)
				}
			}
			if t.createdAt.After(dayEnd) {
				continue
			}
			if isIncompleteAtDayEnd(t, dayEnd, doneColumnKey) {
				incompleteCount++
				if pointsMode {
					incompletePoints += workValue(t, true)
				}
			}
		}

		// Use nil for days before first todo exists
		// Use helper function to allocate new integers for pointers (avoids aliasing bugs)
		var incompleteCountPtr *int
		var totalScopePtr *int
		var incompletePointsPtr *int
		var totalScopePointsPtr *int

		if firstTodoDayStart == nil || !dayStart.Before(*firstTodoDayStart) {
			// Allocate new ints via helper to avoid pointer reuse bugs
			// Safe even if someone refactors and moves variables outside the loop
			incompleteCountPtr = intPtr(incompleteCount)
			totalScopePtr = intPtr(totalScope)
			if pointsMode {
				incompletePointsPtr = intPtr(incompletePoints)
				totalScopePointsPtr = intPtr(totalScopePoints)
			}
		}
		// else: leave as nil (pre-project days)

		points = append(points, BurndownPoint{
			Date:             dayStart,
			IncompleteCount:  incompleteCountPtr,
			TotalScope:       totalScopePtr,
			IncompletePoints: incompletePointsPtr,
			TotalScopePoints: totalScopePointsPtr,
			NewTodosCount:    newTodosCount,
		})

		// Move to next day
		currentDate = currentDate.Add(24 * time.Hour)
	}

	return points, nil
}

// GetRealBurndown returns burndown data with fixed scope from window start
// Only counts todos that existed at the start of the window
// Shows downward trend as those todos are completed
//
// PERFORMANCE NOTE: Same O(days × todos) algorithm as GetBacklogSize.
// See GetBacklogSize for performance considerations and optimization notes.
func (s *Store) GetRealBurndown(ctx context.Context, projectID int64, mode Mode) ([]RealBurndownPoint, error) {
	// Verify project exists
	project, err := s.getProjectForRead(ctx, projectID, mode)
	if err != nil {
		return nil, err
	}

	doneColumnKey, err := s.GetWorkflowDoneColumnKey(ctx, projectID)
	if err != nil {
		doneColumnKey = DefaultColumnDone
	}

	todos, err := s.loadBurndownTodos(ctx, projectID)
	if err != nil {
		return nil, err
	}

	// Calculate window start (same logic as GetBacklogSize)
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Find earliest todo creation date
	var firstTodoDate *time.Time
	if len(todos) > 0 {
		firstCreated := todos[0].createdAt
		firstTodoDate = &firstCreated
	}

	// Calculate window start: min(firstTodoDate, today - 13 days)
	windowStart := todayStart.AddDate(0, 0, -13)
	if firstTodoDate != nil {
		firstDayStart := time.Date(firstTodoDate.Year(), firstTodoDate.Month(), firstTodoDate.Day(), 0, 0, 0, 0, time.UTC)
		if firstDayStart.Before(windowStart) {
			windowStart = firstDayStart
		}
	}
	windowStartEnd := windowStart.Add(24 * time.Hour)

	// Find todos that existed at window start (createdAt <= windowStartEnd)
	// These form the fixed scope for the burndown
	initialScopeTodos := make([]burndownTodoEvent, 0)
	for _, t := range todos {
		if !t.createdAt.After(windowStartEnd) {
			initialScopeTodos = append(initialScopeTodos, t)
		}
	}
	initialScope := len(initialScopeTodos)
	pointsMode := project.EstimationMode == EstimationModeModifiedFibonacci && hasAnyEstimatedWork(initialScopeTodos)
	var initialScopePointsPtr *int
	if pointsMode {
		initialScopePoints := 0
		for _, t := range initialScopeTodos {
			initialScopePoints += workValue(t, true)
		}
		initialScopePointsPtr = intPtr(initialScopePoints)
	}

	// Generate points from windowStart to today
	points := make([]RealBurndownPoint, 0, 14)
	currentDate := windowStart

	for !currentDate.After(todayStart) {
		dayStart := currentDate
		dayEnd := dayStart.Add(24 * time.Hour)

		// Count how many of the initial scope todos are still incomplete at end of this day
		remainingWork := 0
		remainingPoints := 0
		for _, t := range initialScopeTodos {
			if isIncompleteAtDayEnd(t, dayEnd, doneColumnKey) {
				remainingWork++
				if pointsMode {
					remainingPoints += workValue(t, true)
				}
			}
		}

		// Use nil for days before window start (shouldn't happen, but for consistency)
		var remainingWorkPtr *int
		var remainingPointsPtr *int
		if !dayStart.Before(windowStart) {
			remainingWorkPtr = intPtr(remainingWork)
			if pointsMode {
				remainingPointsPtr = intPtr(remainingPoints)
			}
		}

		points = append(points, RealBurndownPoint{
			Date:               dayStart,
			RemainingWork:      remainingWorkPtr,
			InitialScope:       initialScope,
			RemainingPoints:    remainingPointsPtr,
			InitialScopePoints: initialScopePointsPtr,
		})

		currentDate = currentDate.Add(24 * time.Hour)
	}

	return points, nil
}

// GetRealBurndownForSprint returns sprint-scoped burndown data.
// Scope = todos currently assigned to the sprint (sprint_id = sprintID).
// Range = days from sprint start to min(sprint end, today), UTC midnight boundaries.
// Point dates are UTC midnight; values = remaining (incomplete at end of day).
func (s *Store) GetRealBurndownForSprint(ctx context.Context, projectID, sprintID int64, mode Mode) ([]RealBurndownPoint, error) {
	project, err := s.getProjectForRead(ctx, projectID, mode)
	if err != nil {
		return nil, err
	}

	doneColumnKey, err := s.GetWorkflowDoneColumnKey(ctx, projectID)
	if err != nil {
		doneColumnKey = DefaultColumnDone
	}

	sp, err := s.GetSprintByID(ctx, sprintID)
	if err != nil {
		return nil, err
	}
	if sp.ProjectID != projectID {
		return nil, fmt.Errorf("%w: sprint does not belong to project", ErrValidation)
	}

	todos, err := s.loadBurndownTodosForSprint(ctx, projectID, sprintID)
	if err != nil {
		return nil, err
	}

	// Sprint date range: truncate to UTC midnight for consistent day boundaries
	sprintStartDay := time.Date(sp.PlannedStartAt.Year(), sp.PlannedStartAt.Month(), sp.PlannedStartAt.Day(), 0, 0, 0, 0, time.UTC)
	sprintEndDay := time.Date(sp.PlannedEndAt.Year(), sp.PlannedEndAt.Month(), sp.PlannedEndAt.Day(), 0, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	// Cap end at today so we don't generate future points
	effectiveEnd := sprintEndDay
	if effectiveEnd.After(todayStart) {
		effectiveEnd = todayStart
	}

	initialScope := len(todos)
	pointsMode := project.EstimationMode == EstimationModeModifiedFibonacci && hasAnyEstimatedWork(todos)
	var initialScopePointsPtr *int
	if pointsMode {
		scopePts := 0
		for _, t := range todos {
			scopePts += workValue(t, true)
		}
		initialScopePointsPtr = intPtr(scopePts)
	}

	points := make([]RealBurndownPoint, 0, 14)
	currentDate := sprintStartDay

	for !currentDate.After(effectiveEnd) {
		dayStart := currentDate
		dayEnd := dayStart.Add(24 * time.Hour)

		remainingWork := 0
		remainingPoints := 0
		for _, t := range todos {
			if isIncompleteAtDayEnd(t, dayEnd, doneColumnKey) {
				remainingWork++
				if pointsMode {
					remainingPoints += workValue(t, true)
				}
			}
		}

		remainingWorkPtr := intPtr(remainingWork)
		var remainingPointsPtr *int
		if pointsMode {
			remainingPointsPtr = intPtr(remainingPoints)
		}

		points = append(points, RealBurndownPoint{
			Date:               dayStart,
			RemainingWork:      remainingWorkPtr,
			InitialScope:       initialScope,
			RemainingPoints:    remainingPointsPtr,
			InitialScopePoints: initialScopePointsPtr,
		})

		currentDate = currentDate.Add(24 * time.Hour)
	}

	return points, nil
}
