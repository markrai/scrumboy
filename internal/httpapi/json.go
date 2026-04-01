package httpapi

import (
	"strings"
	"time"

	"scrumboy/internal/store"
)

func tagsToJSON(tags []store.TagWithColor) []tagWithColorJSON {
	out := make([]tagWithColorJSON, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagWithColorJSON{TagID: t.TagID, Name: t.Name, Color: t.Color, CanDelete: t.CanDelete})
	}
	return out
}

type sprintJSON struct {
	ID             int64     `json:"id"`
	ProjectID      int64     `json:"projectId"`
	Number         int64     `json:"number"`
	Name           string    `json:"name"`
	PlannedStartAt int64     `json:"plannedStartAt"`
	PlannedEndAt   int64     `json:"plannedEndAt"`
	StartedAt      *int64    `json:"startedAt,omitempty"`
	ClosedAt       *int64    `json:"closedAt,omitempty"`
	State          string    `json:"state"`
	TodoCount      int64     `json:"todoCount,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func sprintToJSON(sp store.Sprint) sprintJSON {
	var startedAt *int64
	if sp.StartedAt != nil {
		v := sp.StartedAt.UnixMilli()
		startedAt = &v
	}
	var closedAt *int64
	if sp.ClosedAt != nil {
		v := sp.ClosedAt.UnixMilli()
		closedAt = &v
	}
	return sprintJSON{
		ID:             sp.ID,
		ProjectID:      sp.ProjectID,
		Number:         sp.Number,
		Name:           sp.Name,
		PlannedStartAt: sp.PlannedStartAt.UnixMilli(),
		PlannedEndAt:   sp.PlannedEndAt.UnixMilli(),
		StartedAt:      startedAt,
		ClosedAt:       closedAt,
		State:          sp.State,
		CreatedAt:      sp.CreatedAt,
		UpdatedAt:      sp.UpdatedAt,
	}
}

func sprintWithTodoCountToJSON(sp store.SprintWithTodoCount) sprintJSON {
	var startedAt *int64
	if sp.StartedAt != nil {
		v := sp.StartedAt.UnixMilli()
		startedAt = &v
	}
	var closedAt *int64
	if sp.ClosedAt != nil {
		v := sp.ClosedAt.UnixMilli()
		closedAt = &v
	}
	return sprintJSON{
		ID:             sp.ID,
		ProjectID:      sp.ProjectID,
		Number:         sp.Number,
		Name:           sp.Name,
		PlannedStartAt: sp.PlannedStartAt.UnixMilli(),
		PlannedEndAt:   sp.PlannedEndAt.UnixMilli(),
		StartedAt:      startedAt,
		ClosedAt:       closedAt,
		State:          sp.State,
		TodoCount:      sp.TodoCount,
		CreatedAt:      sp.CreatedAt,
		UpdatedAt:      sp.UpdatedAt,
	}
}

func sprintsToJSON(sprints []store.Sprint) []sprintJSON {
	out := make([]sprintJSON, 0, len(sprints))
	for _, sp := range sprints {
		out = append(out, sprintToJSON(sp))
	}
	return out
}

func sprintsWithTodoCountToJSON(sprints []store.SprintWithTodoCount) []sprintJSON {
	out := make([]sprintJSON, 0, len(sprints))
	for _, sp := range sprints {
		out = append(out, sprintWithTodoCountToJSON(sp))
	}
	return out
}

// JSON shaping (keep UI stable even if store structs change).

type projectJSON struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	Image              *string    `json:"image,omitempty"`
	DominantColor      string     `json:"dominantColor"`
	DefaultSprintWeeks int        `json:"defaultSprintWeeks"`
	ExpiresAt          *time.Time `json:"expiresAt"`
	CreatorUserID      *int64     `json:"creatorUserId,omitempty"`
	Slug               string     `json:"slug"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	Role               string     `json:"role,omitempty"` // present only in list response; for UI to gate Delete/Rename
}

type userJSON struct {
	ID               int64     `json:"id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	Image            *string   `json:"image"`
	IsBootstrap      bool      `json:"isBootstrap"`
	SystemRole       string    `json:"systemRole"`
	CreatedAt        time.Time `json:"createdAt"`
	TwoFactorEnabled bool      `json:"twoFactorEnabled"`
}

// userStatusJSON omits createdAt for /api/auth/status (status response contract).
func userStatusJSON(u store.User) map[string]any {
	return map[string]any{
		"id":               u.ID,
		"email":            u.Email,
		"name":             u.Name,
		"isBootstrap":      u.IsBootstrap,
		"systemRole":       u.SystemRole.String(),
		"twoFactorEnabled": u.IsTwoFactorActive(),
	}
}

func userToJSON(u store.User) userJSON {
	return userJSON{
		ID:               u.ID,
		Email:            u.Email,
		Name:             u.Name,
		Image:            u.Image,
		IsBootstrap:      u.IsBootstrap,
		SystemRole:       u.SystemRole.String(),
		CreatedAt:        u.CreatedAt,
		TwoFactorEnabled: u.IsTwoFactorActive(),
	}
}

type projectMemberJSON struct {
	UserID    int64     `json:"userId"`
	Name      string    `json:"name"`
	Image     *string   `json:"image,omitempty"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

func projectMemberToJSON(m store.ProjectMember) projectMemberJSON {
	return projectMemberJSON{
		UserID:    m.UserID,
		Name:      m.Name,
		Image:     m.Image,
		Role:      m.Role.String(),
		CreatedAt: m.CreatedAt,
	}
}

func projectMembersToJSON(members []store.ProjectMember) []projectMemberJSON {
	out := make([]projectMemberJSON, 0, len(members))
	for _, m := range members {
		out = append(out, projectMemberToJSON(m))
	}
	return out
}

func projectToJSON(p store.Project) projectJSON {
	return projectJSON{
		ID:                 p.ID,
		Name:               p.Name,
		Image:              p.Image,
		DominantColor:      p.DominantColor,
		DefaultSprintWeeks: p.DefaultSprintWeeks,
		ExpiresAt:          p.ExpiresAt,
		CreatorUserID:      p.CreatorUserID,
		Slug:               p.Slug,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

func projectListEntryToJSON(e store.ProjectListEntry) projectJSON {
	j := projectToJSON(e.Project)
	j.Role = e.Role.String()
	return j
}

func projectsToJSON(entries []store.ProjectListEntry) []projectJSON {
	out := make([]projectJSON, 0, len(entries))
	for _, e := range entries {
		out = append(out, projectListEntryToJSON(e))
	}
	return out
}

type todoJSON struct {
	ID               int64      `json:"id"`
	ProjectID        int64      `json:"projectId"`
	LocalID          int64      `json:"localId"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	Status           string     `json:"status"`
	ColumnKey        string     `json:"columnKey"`
	Rank             int64      `json:"rank"`
	EstimationPoints *int64     `json:"estimationPoints,omitempty"`
	AssigneeUserId   *int64     `json:"assigneeUserId,omitempty"`
	SprintId         *int64     `json:"sprintId,omitempty"`
	Tags             []string   `json:"tags"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DoneAt           *time.Time `json:"doneAt,omitempty"`
}

func todoToJSON(t store.Todo) todoJSON {
	return todoJSON{
		ID:               t.ID,
		ProjectID:        t.ProjectID,
		LocalID:          t.LocalID,
		Title:            t.Title,
		Body:             t.Body,
		Status:           strings.ToUpper(t.ColumnKey),
		ColumnKey:        t.ColumnKey,
		Rank:             t.Rank,
		EstimationPoints: t.EstimationPoints,
		AssigneeUserId:   t.AssigneeUserID,
		SprintId:         t.SprintID,
		Tags:             t.Tags,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
		DoneAt:           t.DoneAt,
	}
}

type activeSprintInfoJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	StartAt int64  `json:"startAt"`
	EndAt   int64  `json:"endAt"`
}

type sprintSectionInfoJSON struct {
	ID      *int64 `json:"id,omitempty"`
	Name    string `json:"name"`
	State   string `json:"state,omitempty"`
	StartAt int64  `json:"startAt,omitempty"`
	EndAt   int64  `json:"endAt,omitempty"`
}

type dashboardProjectJSON struct {
	ProjectID      int64                   `json:"projectId"`
	ProjectName    string                  `json:"projectName"`
	ProjectSlug    string                  `json:"projectSlug"`
	ActiveSprint   *activeSprintInfoJSON   `json:"activeSprint"`   // null when no ACTIVE sprint
	SprintSections []sprintSectionInfoJSON `json:"sprintSections"` // never omitted; always non-nil
}

type assignedSplitJSON struct {
	SprintStories  int   `json:"sprintStories"`
	SprintPoints   int64 `json:"sprintPoints"`
	BacklogStories int   `json:"backlogStories"`
	BacklogPoints  int64 `json:"backlogPoints"`
}

type sprintCompletionJSON struct {
	TotalStories int   `json:"totalStories"`
	DoneStories  int   `json:"doneStories"`
	TotalPoints  int64 `json:"totalPoints"`
	DonePoints   int64 `json:"donePoints"`
}

type weeklyThroughputPointJSON struct {
	WeekStart string `json:"weekStart"`
	Stories   int    `json:"stories"`
	Points    int64  `json:"points"`
}

type oldestWipJSON struct {
	LocalID     int64  `json:"localId"`
	Title       string `json:"title"`
	AgeDays     int    `json:"ageDays"`
	ProjectName string `json:"projectName"`
	ProjectSlug string `json:"projectSlug"`
}

type dashboardSummaryJSON struct {
	AssignedCount            int                         `json:"assignedCount"`
	TotalAssignedStoryPoints int64                       `json:"totalAssignedStoryPoints"`
	PointsCompletedThisWeek  int64                       `json:"pointsCompletedThisWeek"`
	StoriesCompletedThisWeek int                         `json:"storiesCompletedThisWeek"`
	Projects                 []dashboardProjectJSON      `json:"projects"`
	AssignedSplit            *assignedSplitJSON          `json:"assignedSplit,omitempty"`
	SprintCompletion         *sprintCompletionJSON       `json:"sprintCompletion,omitempty"`
	SprintCompletionAllUsers *sprintCompletionJSON       `json:"sprintCompletionAllUsers,omitempty"`
	WipCount                 int                         `json:"wipCount"`
	WipInProgressCount       int                         `json:"wipInProgressCount"`
	WipTestingCount          int                         `json:"wipTestingCount"`
	WeeklyThroughput         []weeklyThroughputPointJSON `json:"weeklyThroughput"`
	AvgLeadTimeDays          *float64                    `json:"avgLeadTimeDays,omitempty"`
	OldestWip                *oldestWipJSON              `json:"oldestWip,omitempty"`
}

type dashboardTodoJSON struct {
	ID                   int64     `json:"id"`
	LocalID              int64     `json:"localId"`
	Title                string    `json:"title"`
	ProjectID            int64     `json:"projectId"`
	ProjectName          string    `json:"projectName"`
	ProjectSlug          string    `json:"projectSlug"`
	ProjectImage         *string   `json:"projectImage,omitempty"`
	ProjectDominantColor string    `json:"projectDominantColor"`
	EstimationPoints     *int64    `json:"estimationPoints,omitempty"`
	SprintId             *int64    `json:"sprintId,omitempty"`
	Status               string    `json:"status"`
	StatusName           string    `json:"statusName"`
	StatusColor          string    `json:"statusColor"`
	ColumnKey            string    `json:"columnKey"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type dashboardTodosResponseJSON struct {
	Items      []dashboardTodoJSON `json:"items"`
	NextCursor *string             `json:"nextCursor,omitempty"`
}

func dashboardSummaryToJSON(s store.DashboardSummary) dashboardSummaryJSON {
	projects := make([]dashboardProjectJSON, 0, len(s.Projects))
	for _, p := range s.Projects {
		jp := dashboardProjectJSON{
			ProjectID:      p.ProjectID,
			ProjectName:    p.ProjectName,
			ProjectSlug:    p.ProjectSlug,
			ActiveSprint:   nil,
			SprintSections: make([]sprintSectionInfoJSON, 0, len(p.SprintSections)),
		}
		if p.ActiveSprint != nil {
			jp.ActiveSprint = &activeSprintInfoJSON{
				ID:      p.ActiveSprint.ID,
				Name:    p.ActiveSprint.Name,
				StartAt: p.ActiveSprint.StartAt,
				EndAt:   p.ActiveSprint.EndAt,
			}
		}
		for _, sec := range p.SprintSections {
			jp.SprintSections = append(jp.SprintSections, sprintSectionInfoJSON{
				ID:      sec.ID,
				Name:    sec.Name,
				State:   sec.State,
				StartAt: sec.StartAt,
				EndAt:   sec.EndAt,
			})
		}
		projects = append(projects, jp)
	}
	j := dashboardSummaryJSON{
		AssignedCount:            s.AssignedCount,
		TotalAssignedStoryPoints: s.TotalAssignedStoryPoints,
		PointsCompletedThisWeek:  s.PointsCompletedThisWeek,
		StoriesCompletedThisWeek: s.StoriesCompletedThisWeek,
		Projects:                 projects,
		WipCount:                 s.WipCount,
		WipInProgressCount:       s.WipInProgressCount,
		WipTestingCount:          s.WipTestingCount,
		WeeklyThroughput:         make([]weeklyThroughputPointJSON, 0, len(s.WeeklyThroughput)),
		AvgLeadTimeDays:          s.AvgLeadTimeDays,
	}
	if s.AssignedSplit != nil {
		j.AssignedSplit = &assignedSplitJSON{
			SprintStories:  s.AssignedSplit.SprintCount,
			SprintPoints:   s.AssignedSplit.SprintPoints,
			BacklogStories: s.AssignedSplit.BacklogCount,
			BacklogPoints:  s.AssignedSplit.BacklogPoints,
		}
	}
	if s.SprintCompletion != nil {
		j.SprintCompletion = &sprintCompletionJSON{
			TotalStories: s.SprintCompletion.TotalStories,
			DoneStories:  s.SprintCompletion.DoneStories,
			TotalPoints:  s.SprintCompletion.TotalPoints,
			DonePoints:   s.SprintCompletion.DonePoints,
		}
	}
	if s.SprintCompletionAllUsers != nil {
		j.SprintCompletionAllUsers = &sprintCompletionJSON{
			TotalStories: s.SprintCompletionAllUsers.TotalStories,
			DoneStories:  s.SprintCompletionAllUsers.DoneStories,
			TotalPoints:  s.SprintCompletionAllUsers.TotalPoints,
			DonePoints:   s.SprintCompletionAllUsers.DonePoints,
		}
	}
	for _, p := range s.WeeklyThroughput {
		j.WeeklyThroughput = append(j.WeeklyThroughput, weeklyThroughputPointJSON{
			WeekStart: p.WeekStart,
			Stories:   p.Stories,
			Points:    p.Points,
		})
	}
	if s.OldestWip != nil {
		j.OldestWip = &oldestWipJSON{
			LocalID:     s.OldestWip.LocalID,
			Title:       s.OldestWip.Title,
			AgeDays:     s.OldestWip.AgeDays,
			ProjectName: s.OldestWip.ProjectName,
			ProjectSlug: s.OldestWip.ProjectSlug,
		}
	}
	return j
}

func dashboardTodoToJSON(t store.DashboardTodo) dashboardTodoJSON {
	return dashboardTodoJSON{
		ID:                   t.ID,
		LocalID:              t.LocalID,
		Title:                t.Title,
		ProjectID:            t.ProjectID,
		ProjectName:          t.ProjectName,
		ProjectSlug:          t.ProjectSlug,
		ProjectImage:         t.ProjectImage,
		ProjectDominantColor: t.ProjectDominantColor,
		EstimationPoints:     t.EstimationPoints,
		SprintId:             t.SprintID,
		Status:               strings.ToUpper(t.ColumnKey),
		StatusName:           t.StatusName,
		StatusColor:          t.StatusColor,
		ColumnKey:            t.ColumnKey,
		UpdatedAt:            t.UpdatedAt,
	}
}

func dashboardTodosToJSON(items []store.DashboardTodo, nextCursor *string) dashboardTodosResponseJSON {
	out := make([]dashboardTodoJSON, 0, len(items))
	for _, item := range items {
		out = append(out, dashboardTodoToJSON(item))
	}
	return dashboardTodosResponseJSON{
		Items:      out,
		NextCursor: nextCursor,
	}
}

type tagCountJSON struct {
	TagID     int64   `json:"tagId"`
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	Color     *string `json:"color,omitempty"`
	CanDelete bool    `json:"canDelete"`
}

type tagWithColorJSON struct {
	TagID     int64   `json:"tagId"`
	Name      string  `json:"name"`
	Color     *string `json:"color,omitempty"`
	CanDelete bool    `json:"canDelete"`
}

type columnMetaJSON struct {
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"` // null when !hasMore
	TotalCount int     `json:"totalCount"` // total todos in lane (with same filters)
}

type boardJSON struct {
	Project     projectJSON               `json:"project"`
	ColumnOrder []workflowColumnJSON      `json:"columnOrder"`
	Tags        []tagCountJSON            `json:"tags"`
	Columns     map[string][]todoJSON     `json:"columns"`
	ColumnsMeta map[string]columnMetaJSON `json:"columnsMeta,omitempty"`
}

type lanePageJSON struct {
	Items      []todoJSON `json:"items"`
	NextCursor *string    `json:"nextCursor"` // null when !hasMore
	HasMore    bool       `json:"hasMore"`
}

func lanePageToJSON(items []store.Todo, nextCursor string, hasMore bool) lanePageJSON {
	out := make([]todoJSON, 0, len(items))
	for _, t := range items {
		out = append(out, todoToJSON(t))
	}
	var next *string
	if hasMore && nextCursor != "" {
		next = &nextCursor
	}
	return lanePageJSON{Items: out, NextCursor: next, HasMore: hasMore}
}

type workflowColumnJSON struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	IsDone   bool   `json:"isDone"`
	Position int    `json:"position"`
}

// workflowLaneCountsJSON is the body for GET /api/board/{slug}/workflow/counts.
// Zero-count lanes may be omitted from countsByColumnKey; clients treat a missing key as 0.
type workflowLaneCountsJSON struct {
	Slug              string         `json:"slug"`
	CountsByColumnKey map[string]int `json:"countsByColumnKey"`
}

func boardToJSON(p store.Project, workflow []store.WorkflowColumn, tags []store.TagCount, cols map[string][]store.Todo) boardJSON {
	return boardToJSONWithMeta(p, workflow, tags, cols, nil)
}

func boardToJSONWithMeta(p store.Project, workflow []store.WorkflowColumn, tags []store.TagCount, cols map[string][]store.Todo, meta map[string]store.LaneMeta) boardJSON {
	out := boardJSON{
		Project:     projectToJSON(p),
		ColumnOrder: make([]workflowColumnJSON, 0, len(workflow)),
		Tags:        make([]tagCountJSON, 0, len(tags)),
		Columns:     map[string][]todoJSON{},
	}
	for _, wc := range workflow {
		out.ColumnOrder = append(out.ColumnOrder, workflowColumnJSON{
			Key: wc.Key, Name: wc.Name, Color: wc.Color, IsDone: wc.IsDone, Position: wc.Position,
		})
	}
	for _, tc := range tags {
		out.Tags = append(out.Tags, tagCountJSON{TagID: tc.TagID, Name: tc.Name, Count: tc.Count, Color: tc.Color, CanDelete: tc.CanDelete})
	}
	for key, todos := range cols {
		outTodos := make([]todoJSON, 0, len(todos))
		for _, t := range todos {
			outTodos = append(outTodos, todoToJSON(t))
		}
		out.Columns[key] = outTodos
	}
	if meta != nil {
		out.ColumnsMeta = make(map[string]columnMetaJSON)
		for key, m := range meta {
			var next *string
			if m.HasMore && m.NextCursor != "" {
				next = &m.NextCursor
			}
			out.ColumnsMeta[key] = columnMetaJSON{HasMore: m.HasMore, NextCursor: next, TotalCount: m.TotalCount}
		}
	}
	return out
}

// burndownPointJSON: count fields (IncompleteCount, TotalScope) are always populated when applicable.
// Points fields (IncompletePoints, TotalScopePoints) are optional; clients must use them only when non-null.
type burndownPointJSON struct {
	Date             time.Time `json:"date"`
	IncompleteCount  *int      `json:"incompleteCount,omitempty"`
	TotalScope       *int      `json:"totalScope,omitempty"`
	IncompletePoints *int      `json:"incompletePoints,omitempty"`
	TotalScopePoints *int      `json:"totalScopePoints,omitempty"`
	NewTodosCount    int       `json:"newTodosCount"`
}

func burndownToJSON(points []store.BurndownPoint) []burndownPointJSON {
	out := make([]burndownPointJSON, 0, len(points))
	for _, p := range points {
		out = append(out, burndownPointJSON{
			Date:             p.Date,
			IncompleteCount:  p.IncompleteCount,
			TotalScope:       p.TotalScope,
			IncompletePoints: p.IncompletePoints,
			TotalScopePoints: p.TotalScopePoints,
			NewTodosCount:    p.NewTodosCount,
		})
	}
	return out
}

// realBurndownPointJSON: count fields (RemainingWork, InitialScope) are always populated when applicable.
// Points fields (RemainingPoints, InitialScopePoints) are optional; clients must use them only when non-null.
type realBurndownPointJSON struct {
	Date               time.Time `json:"date"`
	RemainingWork      *int      `json:"remainingWork,omitempty"`
	InitialScope       int       `json:"initialScope"`
	RemainingPoints    *int      `json:"remainingPoints,omitempty"`
	InitialScopePoints *int      `json:"initialScopePoints,omitempty"`
}

func realBurndownToJSON(points []store.RealBurndownPoint) []realBurndownPointJSON {
	out := make([]realBurndownPointJSON, 0, len(points))
	for _, p := range points {
		out = append(out, realBurndownPointJSON{
			Date:               p.Date,
			RemainingWork:      p.RemainingWork,
			InitialScope:       p.InitialScope,
			RemainingPoints:    p.RemainingPoints,
			InitialScopePoints: p.InitialScopePoints,
		})
	}
	return out
}

// Backup export/import JSON structures

type exportDataJSON struct {
	Version    string              `json:"version"`
	ExportedAt time.Time           `json:"exportedAt"`
	Mode       string              `json:"mode"`
	Scope      string              `json:"scope"`
	ExportedBy *string             `json:"exportedBy,omitempty"`
	Projects   []projectExportJSON `json:"projects"`
}

// projectExportJSON: EstimationMode is exported for backup readability; on import the server ignores it and uses store.EstimationModeModifiedFibonacci (v1).
type projectExportJSON struct {
	Slug           string           `json:"slug"`
	Name           string           `json:"name"`
	EstimationMode string           `json:"estimationMode,omitempty"`
	Image          *string          `json:"image,omitempty"`
	DominantColor  string           `json:"dominantColor,omitempty"`
	ExpiresAt      *time.Time       `json:"expiresAt"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
	Todos          []todoExportJSON `json:"todos"`
	Tags           []tagExportJSON  `json:"tags"`
	Links          []linkExportJSON `json:"links,omitempty"`
}

type todoExportJSON struct {
	LocalID          int64     `json:"localId"`
	Title            string    `json:"title"`
	Body             string    `json:"body"`
	Status           string    `json:"status"`
	Rank             int64     `json:"rank"`
	EstimationPoints *int64    `json:"estimationPoints,omitempty"`
	AssigneeUserId   *int64    `json:"assigneeUserId,omitempty"`
	Tags             []string  `json:"tags"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type tagExportJSON struct {
	Name  string  `json:"name"`
	Color *string `json:"color,omitempty"`
}

type linkExportJSON struct {
	FromLocalID int64  `json:"fromLocalId"`
	ToLocalID   int64  `json:"toLocalId"`
	LinkType    string `json:"linkType"`
}

func exportDataToJSON(data *store.ExportData) exportDataJSON {
	projects := make([]projectExportJSON, 0, len(data.Projects))
	for _, p := range data.Projects {
		todos := make([]todoExportJSON, 0, len(p.Todos))
		for _, t := range p.Todos {
			todos = append(todos, todoExportJSON{
				LocalID:          t.LocalID,
				Title:            t.Title,
				Body:             t.Body,
				Status:           t.Status,
				Rank:             t.Rank,
				EstimationPoints: t.EstimationPoints,
				AssigneeUserId:   t.AssigneeUserId,
				Tags:             t.Tags,
				CreatedAt:        t.CreatedAt,
				UpdatedAt:        t.UpdatedAt,
			})
		}

		tags := make([]tagExportJSON, 0, len(p.Tags))
		for _, tag := range p.Tags {
			tags = append(tags, tagExportJSON{
				Name:  tag.Name,
				Color: tag.Color,
			})
		}

		links := make([]linkExportJSON, 0, len(p.Links))
		for _, l := range p.Links {
			links = append(links, linkExportJSON{
				FromLocalID: l.FromLocalID,
				ToLocalID:   l.ToLocalID,
				LinkType:    l.LinkType,
			})
		}

		projects = append(projects, projectExportJSON{
			Slug:           p.Slug,
			Name:           p.Name,
			EstimationMode: p.EstimationMode,
			Image:          p.Image,
			DominantColor:  p.DominantColor,
			ExpiresAt:      p.ExpiresAt,
			CreatedAt:      p.CreatedAt,
			UpdatedAt:      p.UpdatedAt,
			Todos:          todos,
			Tags:           tags,
			Links:          links,
		})
	}

	return exportDataJSON{
		Version:    data.Version,
		ExportedAt: data.ExportedAt,
		Mode:       data.Mode,
		Scope:      data.Scope,
		ExportedBy: data.ExportedBy,
		Projects:   projects,
	}
}

type importResultJSON struct {
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Created  int      `json:"created"`
	Warnings []string `json:"warnings,omitempty"`
}

func importResultToJSON(result *store.ImportResult) importResultJSON {
	return importResultJSON{
		Imported: result.Imported,
		Updated:  result.Updated,
		Created:  result.Created,
		Warnings: result.Warnings,
	}
}

type previewResultJSON struct {
	Projects   int      `json:"projects"`
	Todos      int      `json:"todos"`
	Tags       int      `json:"tags"`
	Links      int      `json:"links,omitempty"`
	WillDelete int      `json:"willDelete,omitempty"`
	WillUpdate int      `json:"willUpdate,omitempty"`
	WillCreate int      `json:"willCreate,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

func previewResultToJSON(result *store.PreviewResult) previewResultJSON {
	return previewResultJSON{
		Projects:   result.Projects,
		Todos:      result.Todos,
		Tags:       result.Tags,
		Links:      result.Links,
		WillDelete: result.WillDelete,
		WillUpdate: result.WillUpdate,
		WillCreate: result.WillCreate,
		Warnings:   result.Warnings,
	}
}
