package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type DashboardTodo struct {
	ID                   int64
	LocalID              int64
	Title                string
	ProjectID            int64
	ProjectName          string
	ProjectSlug          string
	ProjectImage         *string
	ProjectDominantColor string
	EstimationPoints     *int64
	SprintID             *int64
	ColumnKey            string
	StatusName           string
	StatusColor          string
	UpdatedAt            time.Time
}

// SprintSectionInfo holds section metadata for dashboard grouping (sprint or Unscheduled).
type SprintSectionInfo struct {
	ID      *int64
	Name    string
	State   string
	StartAt int64 // Unix ms; 0 for Unscheduled
	EndAt   int64 // Unix ms; 0 for Unscheduled
}

// DashboardProject holds per-project data for dashboard sections, including activeSprint.
type DashboardProject struct {
	ProjectID      int64
	ProjectName    string
	ProjectSlug    string
	ActiveSprint   *ActiveSprintInfo   // nil when no ACTIVE sprint
	SprintSections []SprintSectionInfo // always non-nil; at least [Unscheduled] when no sprints
}

// SprintCompletion holds sprint completion metrics for the user's ACTIVE sprint(s), nil when no ACTIVE sprint.
type SprintCompletion struct {
	TotalStories int
	DoneStories  int
	TotalPoints  int64
	DonePoints   int64
}

// WeeklyThroughputPoint holds stories and points completed in a single week.
type WeeklyThroughputPoint struct {
	WeekStart string // ISO date "YYYY-MM-DD"
	Stories   int
	Points    int64
}

// OldestWip holds the oldest in-progress todo assigned to the user, nil when no WIP.
type OldestWip struct {
	LocalID     int64
	Title       string
	AgeDays     int
	ProjectName string
	ProjectSlug string
}

// AssignedSplit holds assigned count/points split by backlog vs active sprint.
type AssignedSplit struct {
	SprintCount   int
	SprintPoints  int64
	BacklogCount  int
	BacklogPoints int64
}

type DashboardSummary struct {
	AssignedCount            int
	TotalAssignedStoryPoints int64
	PointsCompletedThisWeek  int64
	StoriesCompletedThisWeek int
	Projects                 []DashboardProject // per-project sections with activeSprint (MUST include null when no active sprint)
	// Personal analytics
	AssignedSplit            *AssignedSplit
	SprintCompletion         *SprintCompletion
	SprintCompletionAllUsers *SprintCompletion
	WipCount                 int
	WipInProgressCount int
	WipTestingCount    int
	WeeklyThroughput   []WeeklyThroughputPoint
	AvgLeadTimeDays    *float64 // created_at → done_at (lead time; we don't have first IN_PROGRESS)
	OldestWip          *OldestWip
}

type dashboardWorkflowSemantics struct {
	DoneKey       string
	InProgressKey string
	TestingKey    string
}

func buildDashboardWorkflowSemantics(cols []WorkflowColumn) dashboardWorkflowSemantics {
	var sem dashboardWorkflowSemantics
	for _, col := range cols {
		if col.IsDone {
			sem.DoneKey = col.Key
		}
		if !col.IsDone && col.Key == DefaultColumnDoing {
			sem.InProgressKey = col.Key
		}
		if !col.IsDone && col.Key == DefaultColumnTesting {
			sem.TestingKey = col.Key
		}
	}
	return sem
}

func (s *Store) GetDashboardSummary(ctx context.Context, userID int64, timezone string) (DashboardSummary, error) {
	// Assigned count and total points: per project, use sprint filter when ACTIVE sprint exists
	// We'll compute after we have project list and active sprints

	// Week boundaries for calendar-week fallback
	loc := time.UTC
	if timezone != "" {
		if l, err := time.LoadLocation(timezone); err == nil {
			loc = l
		}
	}
	nowInTZ := time.Now().In(loc)
	weekday := int(nowInTZ.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := time.Date(nowInTZ.Year(), nowInTZ.Month(), nowInTZ.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(weekday - 1))
	weekEnd := weekStart.AddDate(0, 0, 7)
	weekStartMs := weekStart.UnixMilli()
	weekEndMs := weekEnd.UnixMilli()

	// Projects with assigned todos (for activeSprint per project)
	projectRows, err := s.db.QueryContext(ctx, `
SELECT DISTINCT p.id, p.name, p.slug
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
ORDER BY p.name
`, userID)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("list dashboard projects: %w", err)
	}
	var projectIDs []int64
	var projects []DashboardProject
	for projectRows.Next() {
		var dp DashboardProject
		var slug sql.NullString
		if err := projectRows.Scan(&dp.ProjectID, &dp.ProjectName, &slug); err != nil {
			projectRows.Close()
			return DashboardSummary{}, fmt.Errorf("scan dashboard project: %w", err)
		}
		if slug.Valid {
			dp.ProjectSlug = slug.String
		}
		projectIDs = append(projectIDs, dp.ProjectID)
		projects = append(projects, dp)
	}
	projectRows.Close()
	if err := projectRows.Err(); err != nil {
		return DashboardSummary{}, fmt.Errorf("rows dashboard projects: %w", err)
	}

	workflowByProject, err := s.GetProjectWorkflows(ctx, projectIDs)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("get project workflows: %w", err)
	}
	workflowSemantics := make(map[int64]dashboardWorkflowSemantics, len(workflowByProject))
	for _, projectID := range projectIDs {
		workflowSemantics[projectID] = buildDashboardWorkflowSemantics(workflowByProject[projectID])
	}

	// Batched active sprints per project (MUST be null when no ACTIVE sprint)
	activeSprints, err := s.GetActiveSprintsByProjectIDs(ctx, projectIDs)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("get active sprints: %w", err)
	}
	for i := range projects {
		if as := activeSprints[projects[i].ProjectID]; as != nil {
			projects[i].ActiveSprint = as
		} else {
			projects[i].ActiveSprint = nil // MUST be null, not omitted
		}
	}

	// SprintSections: populate immediately after activeSprints, before analytics.
	sprintSectionsByProject, err := s.listSprintSectionsForProjects(ctx, projectIDs)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("list sprint sections for projects: %w", err)
	}
	for i := range projects {
		projects[i].SprintSections = sprintSectionsByProject[projects[i].ProjectID]
	}

	// Assigned split: Scheduled (in active sprints) vs Unscheduled (not in any active sprint).
	// Current Sprints = sprint_id IN (active sprint ids). Unscheduled = sprint_id IS NULL OR sprint_id NOT IN (active ids).
	// Total = Scheduled + Unscheduled so the two lines always add up.
	var count int
	var points int64
	var sprintCount, backlogCount int
	var sprintPoints, backlogPoints int64

	activeSprintIDs := make([]int64, 0, len(activeSprints))
	projectIDsWithActive := make(map[int64]bool)
	for pid, as := range activeSprints {
		if as != nil {
			activeSprintIDs = append(activeSprintIDs, as.ID)
			projectIDsWithActive[pid] = true
		}
	}

	if len(activeSprintIDs) > 0 {
		ph := makePlaceholders(len(activeSprintIDs))
		args := []any{userID}
		for i := 0; i < 4; i++ {
			for _, id := range activeSprintIDs {
				args = append(args, id)
			}
		}
		var spPts, blPts sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE WHEN sprint_id IN `+ph+` THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN sprint_id IN `+ph+` THEN estimation_points ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN sprint_id IS NULL OR sprint_id NOT IN `+ph+` THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN sprint_id IS NULL OR sprint_id NOT IN `+ph+` THEN estimation_points ELSE 0 END), 0)
FROM todos t
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE assignee_user_id = ? AND wc.is_done = 0
`, args...).Scan(&sprintCount, &spPts, &backlogCount, &blPts); err != nil {
			return DashboardSummary{}, fmt.Errorf("assigned split: %w", err)
		}
		if spPts.Valid {
			sprintPoints = spPts.Int64
		}
		if blPts.Valid {
			backlogPoints = blPts.Int64
		}
	} else {
		// No active sprints: all assigned non-DONE is unscheduled
		var pUnsched sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(SUM(estimation_points), 0)
FROM todos t
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE assignee_user_id = ? AND wc.is_done = 0`, userID).Scan(&backlogCount, &pUnsched); err != nil {
			return DashboardSummary{}, fmt.Errorf("count assigned: %w", err)
		}
		if pUnsched.Valid {
			backlogPoints = pUnsched.Int64
		}
	}
	count = backlogCount + sprintCount
	points = backlogPoints + sprintPoints

	assignedSplit := &AssignedSplit{
		SprintCount:   sprintCount,
		SprintPoints:  sprintPoints,
		BacklogCount:  backlogCount,
		BacklogPoints: backlogPoints,
	}

	// Completion metrics: batched queries
	// Sprint-based: single query joining todos to sprints, filter by done_at in sprint window
	// Calendar-week: for projects without active sprint
	var pointsCompleted int64
	var storiesCompleted int

	if len(activeSprintIDs) > 0 {
		ph := makePlaceholders(len(activeSprintIDs))
		args := []any{SprintStateActive}
		for _, id := range activeSprintIDs {
			args = append(args, id)
		}
		args = append(args, userID)
		if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(t.estimation_points), 0), COUNT(*)
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
JOIN sprints s ON s.id = t.sprint_id AND s.state = ? AND s.id IN `+ph+`
WHERE t.assignee_user_id = ?
  AND s.started_at IS NOT NULL
  AND t.done_at >= s.started_at AND t.done_at < COALESCE(s.closed_at, s.planned_end_at)
`, args...).Scan(&pointsCompleted, &storiesCompleted); err != nil {
			return DashboardSummary{}, fmt.Errorf("points completed in sprints: %w", err)
		}
	}

	// Calendar-week completion for projects WITHOUT active sprint
	if len(projectIDsWithActive) < len(projectIDs) {
		// Some projects have no active sprint: add their week completions
		var noSprintIDs []int64
		for _, pid := range projectIDs {
			if !projectIDsWithActive[pid] {
				noSprintIDs = append(noSprintIDs, pid)
			}
		}
		if len(noSprintIDs) > 0 {
			placeholders := makePlaceholders(len(noSprintIDs))
			args := []any{userID, weekStartMs, weekEndMs}
			for _, pid := range noSprintIDs {
				args = append(args, pid)
			}
			var p int64
			var c int
			if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(estimation_points), 0), COUNT(*)
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
WHERE assignee_user_id = ? AND done_at >= ? AND done_at < ?
  AND t.project_id IN `+placeholders,
				args...).Scan(&p, &c); err != nil {
				return DashboardSummary{}, fmt.Errorf("points completed this week (no sprint): %w", err)
			}
			pointsCompleted += p
			storiesCompleted += c
		}
	}

	// If no project has active sprint, use calendar week globally
	if len(projectIDsWithActive) == 0 {
		if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(estimation_points), 0), COUNT(*)
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
WHERE assignee_user_id = ? AND done_at >= ? AND done_at < ?
`, userID, weekStartMs, weekEndMs).Scan(&pointsCompleted, &storiesCompleted); err != nil {
			return DashboardSummary{}, fmt.Errorf("points completed this week: %w", err)
		}
	}

	// Personal analytics
	// Sprint completion aggregates across ALL active sprints (all projects). If the user has
	// assigned work in 4 projects with active sprints, the metric combines them.
	// Single query returns both user's and all-users metrics via conditional aggregation.
	var sprintCompletion *SprintCompletion
	var sprintCompletionAllUsers *SprintCompletion
	if len(activeSprintIDs) > 0 {
		ph := makePlaceholders(len(activeSprintIDs))
		args := []any{userID, userID, userID, userID, SprintStateActive}
		for _, id := range activeSprintIDs {
			args = append(args, id)
		}
		for _, id := range activeSprintIDs {
			args = append(args, id)
		}
		var total, doneCount int
		var totalPoints, donePoints int64
		var totalAll, doneCountAll int
		var totalPointsAll, donePointsAll int64
		doneCond := `done_wc.id IS NOT NULL AND s.started_at IS NOT NULL AND t.done_at >= s.started_at AND t.done_at < COALESCE(s.closed_at, s.planned_end_at)`
		if err := s.db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE WHEN t.assignee_user_id = ? THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN t.assignee_user_id = ? THEN t.estimation_points ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN t.assignee_user_id = ? AND `+doneCond+` THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN t.assignee_user_id = ? AND `+doneCond+` THEN t.estimation_points ELSE 0 END), 0),
  COUNT(*),
  COALESCE(SUM(t.estimation_points), 0),
  COALESCE(SUM(CASE WHEN `+doneCond+` THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN `+doneCond+` THEN t.estimation_points ELSE 0 END), 0)
FROM todos t
LEFT JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
JOIN sprints s ON s.id = t.sprint_id AND s.state = ? AND s.id IN `+ph+`
WHERE t.sprint_id IN `+ph,
			args...).Scan(&total, &totalPoints, &doneCount, &donePoints, &totalAll, &totalPointsAll, &doneCountAll, &donePointsAll); err != nil {
			return DashboardSummary{}, fmt.Errorf("sprint completion: %w", err)
		}
		sprintCompletion = &SprintCompletion{
			TotalStories: total,
			DoneStories:  doneCount,
			TotalPoints:  totalPoints,
			DonePoints:   donePoints,
		}
		sprintCompletionAllUsers = &SprintCompletion{
			TotalStories: totalAll,
			DoneStories:  doneCountAll,
			TotalPoints:  totalPointsAll,
			DonePoints:   donePointsAll,
		}
	}

	var wipCount, wipInProgressCount, wipTestingCount int
	wipRows, err := s.db.QueryContext(ctx, `
SELECT t.project_id, t.column_key, t.local_id, t.title, t.updated_at, p.name, p.slug
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
`, userID)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("wip rows: %w", err)
	}
	defer wipRows.Close()
	var oldestWip *OldestWip
	var oldestUpdatedAtMs int64
	nowMs := time.Now().UTC().UnixMilli()
	for wipRows.Next() {
		var projectID int64
		var columnKey string
		var localID int64
		var title string
		var updatedAtMs int64
		var projectName string
		var projectSlug string
		var projectSlugNull sql.NullString
		if err := wipRows.Scan(&projectID, &columnKey, &localID, &title, &updatedAtMs, &projectName, &projectSlugNull); err != nil {
			return DashboardSummary{}, fmt.Errorf("scan wip row: %w", err)
		}
		if projectSlugNull.Valid {
			projectSlug = projectSlugNull.String
		}
		wipCount++
		sem := workflowSemantics[projectID]
		isInProgress := sem.InProgressKey != "" && columnKey == sem.InProgressKey
		isTesting := sem.TestingKey != "" && columnKey == sem.TestingKey
		if isInProgress {
			wipInProgressCount++
		}
		if isTesting {
			wipTestingCount++
		}
		if oldestWip == nil || updatedAtMs < oldestUpdatedAtMs {
			oldestUpdatedAtMs = updatedAtMs
			ageDays := int((nowMs - updatedAtMs) / 86400000)
			oldestWip = &OldestWip{
				LocalID:     localID,
				Title:       title,
				AgeDays:     ageDays,
				ProjectName: projectName,
				ProjectSlug: projectSlug,
			}
		}
	}
	if err := wipRows.Err(); err != nil {
		return DashboardSummary{}, fmt.Errorf("rows wip: %w", err)
	}

	// Weekly throughput: last 4 full weeks + current partial week (5 buckets).
	// Query once for the full range and bucket by week index to minimize DB round-trips.
	const weekMillis int64 = 7 * 24 * 60 * 60 * 1000
	throughputWeekStart := weekStart.AddDate(0, 0, -28) // 4 weeks before current week start
	throughputStartMs := throughputWeekStart.UnixMilli()

	weeklyThroughput := make([]WeeklyThroughputPoint, 0, 5)
	for i := 0; i < 5; i++ {
		ws := throughputWeekStart.AddDate(0, 0, i*7)
		weeklyThroughput = append(weeklyThroughput, WeeklyThroughputPoint{
			WeekStart: ws.Format("2006-01-02"),
			Stories:   0,
			Points:    0,
		})
	}

	rows, err := s.db.QueryContext(ctx, `
SELECT
  CAST((done_at - ?) / ? AS INTEGER) AS week_bucket,
  COUNT(*) AS stories,
  COALESCE(SUM(estimation_points), 0) AS points
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
WHERE assignee_user_id = ?
  AND done_at >= ? AND done_at < ?
GROUP BY week_bucket
ORDER BY week_bucket ASC
`, throughputStartMs, weekMillis, userID, throughputStartMs, weekEndMs)
	if err != nil {
		return DashboardSummary{}, fmt.Errorf("weekly throughput buckets: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var bucket int
		var stories int
		var points int64
		if err := rows.Scan(&bucket, &stories, &points); err != nil {
			return DashboardSummary{}, fmt.Errorf("scan weekly throughput bucket: %w", err)
		}
		if bucket >= 0 && bucket < len(weeklyThroughput) {
			weeklyThroughput[bucket].Stories = stories
			weeklyThroughput[bucket].Points = points
		}
	}
	if err := rows.Err(); err != nil {
		return DashboardSummary{}, fmt.Errorf("rows weekly throughput buckets: %w", err)
	}

	// Average lead time: created_at → done_at (not cycle time; we don't have first IN_PROGRESS timestamp).
	var avgLeadTimeDays *float64
	if len(activeSprintIDs) > 0 {
		ph := makePlaceholders(len(activeSprintIDs))
		args := []any{SprintStateActive}
		for _, id := range activeSprintIDs {
			args = append(args, id)
		}
		args = append(args, userID)
		var avg sql.NullFloat64
		if err := s.db.QueryRowContext(ctx, `
SELECT AVG((t.done_at - t.created_at) / 86400000.0)
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
JOIN sprints s ON s.id = t.sprint_id AND s.state = ? AND s.id IN `+ph+`
WHERE t.assignee_user_id = ?
  AND s.started_at IS NOT NULL
  AND t.done_at >= s.started_at AND t.done_at < COALESCE(s.closed_at, s.planned_end_at)
`, args...).Scan(&avg); err != nil {
			return DashboardSummary{}, fmt.Errorf("avg lead time: %w", err)
		}
		if avg.Valid {
			avgLeadTimeDays = &avg.Float64
		}
	} else {
		thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30).UnixMilli()
		var avg sql.NullFloat64
		if err := s.db.QueryRowContext(ctx, `
SELECT AVG((done_at - created_at) / 86400000.0)
FROM todos t
JOIN project_workflow_columns done_wc ON done_wc.project_id = t.project_id AND done_wc.key = t.column_key AND done_wc.is_done = 1
WHERE assignee_user_id = ? AND done_at >= ?
`, userID, thirtyDaysAgo).Scan(&avg); err != nil {
			return DashboardSummary{}, fmt.Errorf("avg lead time fallback: %w", err)
		}
		if avg.Valid {
			avgLeadTimeDays = &avg.Float64
		}
	}

	return DashboardSummary{
		AssignedCount:            count,
		TotalAssignedStoryPoints: points,
		PointsCompletedThisWeek:  pointsCompleted,
		StoriesCompletedThisWeek: storiesCompleted,
		Projects:                 projects,
		AssignedSplit:            assignedSplit,
		SprintCompletion:         sprintCompletion,
		SprintCompletionAllUsers: sprintCompletionAllUsers,
		WipCount:                 wipCount,
		WipInProgressCount:       wipInProgressCount,
		WipTestingCount:          wipTestingCount,
		WeeklyThroughput:         weeklyThroughput,
		AvgLeadTimeDays:          avgLeadTimeDays,
		OldestWip:                oldestWip,
	}, nil
}

func (s *Store) listSprintSectionsForProjects(ctx context.Context, projectIDs []int64) (map[int64][]SprintSectionInfo, error) {
	out := make(map[int64][]SprintSectionInfo, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}

	args := make([]any, 0, len(projectIDs))
	seen := make(map[int64]struct{}, len(projectIDs))
	for _, pid := range projectIDs {
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		args = append(args, pid)
		out[pid] = make([]SprintSectionInfo, 0, 4)
	}

	ph := makePlaceholders(len(args))
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id, id, name, state, planned_start_at, planned_end_at
		FROM sprints
		WHERE project_id IN `+ph+`
		ORDER BY
		  project_id ASC,
		  CASE state
		    WHEN 'ACTIVE' THEN 0
		    WHEN 'PLANNED' THEN 1
		    WHEN 'CLOSED' THEN 2
		    ELSE 3
		  END,
		  CASE WHEN state = 'PLANNED' THEN planned_start_at END ASC,
		  CASE WHEN state = 'CLOSED' THEN planned_end_at END DESC
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("list sprint sections (batched): %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var projectID int64
		var id int64
		var name, state string
		var startAtMs, endAtMs int64
		if err := rows.Scan(&projectID, &id, &name, &state, &startAtMs, &endAtMs); err != nil {
			return nil, fmt.Errorf("scan sprint section: %w", err)
		}
		out[projectID] = append(out[projectID], SprintSectionInfo{
			ID:      &id,
			Name:    name,
			State:   state,
			StartAt: startAtMs,
			EndAt:   endAtMs,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows sprint sections: %w", err)
	}

	for pid := range out {
		out[pid] = append(out[pid], SprintSectionInfo{
			ID: nil, Name: "Unscheduled", State: "", StartAt: 0, EndAt: 0,
		})
	}
	return out, nil
}

const (
	dashboardTodoSortActivity = "activity"
	dashboardTodoSortBoard    = "board"
)

// NormalizeDashboardTodoSort maps query values to activity (default) or board.
func NormalizeDashboardTodoSort(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == dashboardTodoSortBoard {
		return dashboardTodoSortBoard
	}
	return dashboardTodoSortActivity
}

func dashboardCursorRaw(cursor *string) string {
	if cursor == nil {
		return ""
	}
	return strings.TrimSpace(*cursor)
}

func parseActivityCursorValues(raw string) (updatedAtMs, id int64, ok bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	var err error
	updatedAtMs, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	id, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return updatedAtMs, id, true
}

func parseBoardCursorValues(raw string) (projectID, wcPos, rank, todoID int64, ok bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 4 {
		return 0, 0, 0, 0, false
	}
	var err error
	projectID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	wcPos, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	rank, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	todoID, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	return projectID, wcPos, rank, todoID, true
}

func encodeDashboardCursor(updatedAtMs int64, id int64) string {
	return strconv.FormatInt(updatedAtMs, 10) + ":" + strconv.FormatInt(id, 10)
}

func encodeDashboardBoardCursor(projectID, wcPos, rank, todoID int64) string {
	return strconv.FormatInt(projectID, 10) + ":" + strconv.FormatInt(wcPos, 10) + ":" + strconv.FormatInt(rank, 10) + ":" + strconv.FormatInt(todoID, 10)
}

func (s *Store) ListDashboardTodos(ctx context.Context, userID int64, limit int, cursor *string, sort string) ([]DashboardTodo, *string, error) {
	if NormalizeDashboardTodoSort(sort) == dashboardTodoSortBoard {
		return s.listDashboardTodosBoard(ctx, userID, limit, cursor)
	}
	return s.listDashboardTodosActivity(ctx, userID, limit, cursor)
}

func (s *Store) listDashboardTodosActivity(ctx context.Context, userID int64, limit int, cursor *string) ([]DashboardTodo, *string, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	raw := dashboardCursorRaw(cursor)
	var (
		rows *sql.Rows
		err  error
	)
	if raw != "" {
		updatedAtCursor, idCursor, ok := parseActivityCursorValues(raw)
		if !ok {
			return nil, nil, fmt.Errorf("%w: invalid dashboard cursor", ErrValidation)
		}
		rows, err = s.db.QueryContext(ctx, `
SELECT t.id, t.local_id, t.title, t.project_id, t.column_key, t.updated_at, t.estimation_points, t.sprint_id,
       p.name, p.slug, p.image, p.dominant_color,
       wc.name AS status_name, wc.color AS status_color
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
  AND (t.updated_at < ? OR (t.updated_at = ? AND t.id < ?))
ORDER BY t.updated_at DESC, t.id DESC
LIMIT ?
`, userID, updatedAtCursor, updatedAtCursor, idCursor, limit+1)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT t.id, t.local_id, t.title, t.project_id, t.column_key, t.updated_at, t.estimation_points, t.sprint_id,
       p.name, p.slug, p.image, p.dominant_color,
       wc.name AS status_name, wc.color AS status_color
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
ORDER BY t.updated_at DESC, t.id DESC
LIMIT ?
`, userID, limit+1)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list dashboard todos: %w", err)
	}
	defer rows.Close()

	out := make([]DashboardTodo, 0, limit+1)
	for rows.Next() {
		var (
			t           DashboardTodo
			columnKey   string
			updatedAtMs int64
		)
		var projectImage sql.NullString
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var sprintID sql.NullInt64
		var statusName sql.NullString
		var statusColor sql.NullString
		if err := rows.Scan(&t.ID, &localID, &t.Title, &t.ProjectID, &columnKey, &updatedAtMs, &estimationPoints, &sprintID, &t.ProjectName, &t.ProjectSlug, &projectImage, &t.ProjectDominantColor, &statusName, &statusColor); err != nil {
			return nil, nil, fmt.Errorf("scan dashboard todo: %w", err)
		}
		if !localID.Valid {
			return nil, nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = columnKey
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if projectImage.Valid && projectImage.String != "" {
			t.ProjectImage = &projectImage.String
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		if statusName.Valid && statusName.String != "" {
			t.StatusName = statusName.String
		} else {
			t.StatusName = HumanizeColumnKey(columnKey)
		}
		if statusColor.Valid && statusColor.String != "" {
			t.StatusColor = statusColor.String
		} else {
			t.StatusColor = "#64748b"
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("rows dashboard todos: %w", err)
	}

	if len(out) <= limit {
		return out, nil, nil
	}

	page := out[:limit]
	last := page[len(page)-1]
	next := encodeDashboardCursor(last.UpdatedAt.UnixMilli(), last.ID)
	return page, &next, nil
}

type dashboardBoardRow struct {
	todo  DashboardTodo
	wcPos int64
	rank  int64
}

func (s *Store) listDashboardTodosBoard(ctx context.Context, userID int64, limit int, cursor *string) ([]DashboardTodo, *string, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	raw := dashboardCursorRaw(cursor)
	var (
		rows *sql.Rows
		err  error
	)
	if raw != "" {
		pid, wcPos, rank, todoID, ok := parseBoardCursorValues(raw)
		if !ok {
			return nil, nil, fmt.Errorf("%w: invalid dashboard cursor", ErrValidation)
		}
		rows, err = s.db.QueryContext(ctx, `
SELECT t.id, t.local_id, t.title, t.project_id, t.column_key, t.updated_at, t.estimation_points, t.sprint_id,
       t.rank, wc.position,
       p.name, p.slug, p.image, p.dominant_color,
       wc.name AS status_name, wc.color AS status_color
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
  AND (t.project_id, wc.position, t.rank, t.id) > (?, ?, ?, ?)
ORDER BY t.project_id ASC, wc.position ASC, t.rank ASC, t.id ASC
LIMIT ?
`, userID, pid, wcPos, rank, todoID, limit+1)
	} else {
		rows, err = s.db.QueryContext(ctx, `
SELECT t.id, t.local_id, t.title, t.project_id, t.column_key, t.updated_at, t.estimation_points, t.sprint_id,
       t.rank, wc.position,
       p.name, p.slug, p.image, p.dominant_color,
       wc.name AS status_name, wc.color AS status_color
FROM todos t
JOIN projects p ON p.id = t.project_id
JOIN project_workflow_columns wc ON wc.project_id = t.project_id AND wc.key = t.column_key
WHERE t.assignee_user_id = ? AND wc.is_done = 0
ORDER BY t.project_id ASC, wc.position ASC, t.rank ASC, t.id ASC
LIMIT ?
`, userID, limit+1)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("list dashboard todos: %w", err)
	}
	defer rows.Close()

	out := make([]dashboardBoardRow, 0, limit+1)
	for rows.Next() {
		var (
			t           DashboardTodo
			columnKey   string
			updatedAtMs int64
			todoRank    int64
			wcPos       int64
		)
		var projectImage sql.NullString
		var localID sql.NullInt64
		var estimationPoints sql.NullInt64
		var sprintID sql.NullInt64
		var statusName sql.NullString
		var statusColor sql.NullString
		if err := rows.Scan(&t.ID, &localID, &t.Title, &t.ProjectID, &columnKey, &updatedAtMs, &estimationPoints, &sprintID,
			&todoRank, &wcPos,
			&t.ProjectName, &t.ProjectSlug, &projectImage, &t.ProjectDominantColor, &statusName, &statusColor); err != nil {
			return nil, nil, fmt.Errorf("scan dashboard todo: %w", err)
		}
		if !localID.Valid {
			return nil, nil, fmt.Errorf("%w: todos.local_id is NULL (migration incomplete)", ErrConflict)
		}
		t.LocalID = localID.Int64
		t.ColumnKey = columnKey
		t.UpdatedAt = time.UnixMilli(updatedAtMs).UTC()
		if estimationPoints.Valid {
			v := estimationPoints.Int64
			t.EstimationPoints = &v
		}
		if projectImage.Valid && projectImage.String != "" {
			t.ProjectImage = &projectImage.String
		}
		if sprintID.Valid {
			v := sprintID.Int64
			t.SprintID = &v
		}
		if statusName.Valid && statusName.String != "" {
			t.StatusName = statusName.String
		} else {
			t.StatusName = HumanizeColumnKey(columnKey)
		}
		if statusColor.Valid && statusColor.String != "" {
			t.StatusColor = statusColor.String
		} else {
			t.StatusColor = "#64748b"
		}
		out = append(out, dashboardBoardRow{todo: t, wcPos: wcPos, rank: todoRank})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("rows dashboard todos: %w", err)
	}

	if len(out) <= limit {
		todos := make([]DashboardTodo, len(out))
		for i := range out {
			todos[i] = out[i].todo
		}
		return todos, nil, nil
	}

	pageRows := out[:limit]
	todos := make([]DashboardTodo, len(pageRows))
	for i := range pageRows {
		todos[i] = pageRows[i].todo
	}
	last := pageRows[len(pageRows)-1]
	next := encodeDashboardBoardCursor(last.todo.ProjectID, last.wcPos, last.rank, last.todo.ID)
	return todos, &next, nil
}
