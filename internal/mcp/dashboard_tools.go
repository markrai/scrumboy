package mcp

import (
	"context"
	"net/http"
	"strings"

	"scrumboy/internal/store"
)

type dashboardGetSummaryInput struct {
	Timezone string `json:"timezone"`
}

type dashboardListTodosInput struct {
	Limit  *int    `json:"limit"`
	Cursor *string `json:"cursor"`
	Sort   string  `json:"sort"`
}

// normalizeDashboardTodosLimit validates and defaults the limit for
// dashboard_listTodos, matching the bounds used elsewhere in the MCP server
// (see board_tools.go / todos_tools.go). Pure function so it is unit testable
// without a store.
func normalizeDashboardTodosLimit(limit *int) (int, *adapterError) {
	if limit == nil {
		return 20, nil
	}
	if *limit <= 0 || *limit > 100 {
		return 0, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
	}
	return *limit, nil
}

func dashboardProjectToItem(p store.DashboardProject) dashboardProjectItem {
	item := dashboardProjectItem{
		ProjectID:      p.ProjectID,
		ProjectName:    p.ProjectName,
		ProjectSlug:    p.ProjectSlug,
		ActiveSprint:   nil,
		SprintSections: make([]sprintSectionInfoItem, 0, len(p.SprintSections)),
	}
	if p.ActiveSprint != nil {
		item.ActiveSprint = &activeSprintInfoItem{
			ID:      p.ActiveSprint.ID,
			Name:    p.ActiveSprint.Name,
			StartAt: p.ActiveSprint.StartAt,
			EndAt:   p.ActiveSprint.EndAt,
		}
	}
	for _, sec := range p.SprintSections {
		item.SprintSections = append(item.SprintSections, sprintSectionInfoItem{
			ID:      sec.ID,
			Name:    sec.Name,
			State:   sec.State,
			StartAt: sec.StartAt,
			EndAt:   sec.EndAt,
		})
	}
	return item
}

func dashboardSummaryToItem(s store.DashboardSummary) dashboardSummaryItem {
	projects := make([]dashboardProjectItem, 0, len(s.Projects))
	for _, p := range s.Projects {
		projects = append(projects, dashboardProjectToItem(p))
	}

	item := dashboardSummaryItem{
		AssignedCount:            s.AssignedCount,
		TotalAssignedStoryPoints: s.TotalAssignedStoryPoints,
		PointsCompletedThisWeek:  s.PointsCompletedThisWeek,
		StoriesCompletedThisWeek: s.StoriesCompletedThisWeek,
		Projects:                 projects,
		WipCount:                 s.WipCount,
		WipInProgressCount:       s.WipInProgressCount,
		WipTestingCount:          s.WipTestingCount,
		WeeklyThroughput:         make([]weeklyThroughputPointItem, 0, len(s.WeeklyThroughput)),
		AvgLeadTimeDays:          s.AvgLeadTimeDays,
	}
	if s.AssignedSplit != nil {
		item.AssignedSplit = &assignedSplitItem{
			SprintStories:  s.AssignedSplit.SprintCount,
			SprintPoints:   s.AssignedSplit.SprintPoints,
			BacklogStories: s.AssignedSplit.BacklogCount,
			BacklogPoints:  s.AssignedSplit.BacklogPoints,
		}
	}
	if s.SprintCompletion != nil {
		item.SprintCompletion = &sprintCompletionItem{
			TotalStories: s.SprintCompletion.TotalStories,
			DoneStories:  s.SprintCompletion.DoneStories,
			TotalPoints:  s.SprintCompletion.TotalPoints,
			DonePoints:   s.SprintCompletion.DonePoints,
		}
	}
	if s.SprintCompletionAllUsers != nil {
		item.SprintCompletionAllUsers = &sprintCompletionItem{
			TotalStories: s.SprintCompletionAllUsers.TotalStories,
			DoneStories:  s.SprintCompletionAllUsers.DoneStories,
			TotalPoints:  s.SprintCompletionAllUsers.TotalPoints,
			DonePoints:   s.SprintCompletionAllUsers.DonePoints,
		}
	}
	for _, p := range s.WeeklyThroughput {
		item.WeeklyThroughput = append(item.WeeklyThroughput, weeklyThroughputPointItem{
			WeekStart: p.WeekStart,
			Stories:   p.Stories,
			Points:    p.Points,
		})
	}
	if s.OldestWip != nil {
		item.OldestWip = &oldestWipItem{
			LocalID:     s.OldestWip.LocalID,
			Title:       s.OldestWip.Title,
			AgeDays:     s.OldestWip.AgeDays,
			ProjectName: s.OldestWip.ProjectName,
			ProjectSlug: s.OldestWip.ProjectSlug,
		}
	}
	return item
}

func dashboardTodoToItem(t store.DashboardTodo) dashboardTodoItem {
	return dashboardTodoItem{
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

func (a *Adapter) handleDashboardGetSummary(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "dashboard_getSummary is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "dashboard_getSummary is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in dashboardGetSummaryInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	summary, sErr := a.store.GetDashboardSummary(ctx, userID, strings.TrimSpace(in.Timezone))
	if sErr != nil {
		return nil, nil, mapStoreError(sErr)
	}

	return map[string]any{
		"summary": dashboardSummaryToItem(summary),
	}, map[string]any{}, nil
}

func (a *Adapter) handleDashboardListTodos(ctx context.Context, input any) (any, map[string]any, *adapterError) {
	auth, bootstrapAvailable, err := a.authState(ctx)
	if err != nil {
		return nil, nil, err
	}

	switch {
	case a.mode == "anonymous":
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "dashboard_listTodos is unavailable in anonymous mode", nil)
	case bootstrapAvailable:
		return nil, nil, newAdapterError(http.StatusForbidden, CodeCapabilityUnavailable, "dashboard_listTodos is unavailable before bootstrap", nil)
	case !auth.Authenticated:
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	var in dashboardListTodosInput
	if err := decodeInput(input, &in); err != nil {
		return nil, nil, newAdapterError(http.StatusBadRequest, CodeValidationError, "invalid input", map[string]any{"detail": err.Error()})
	}

	limit, limitErr := normalizeDashboardTodosLimit(in.Limit)
	if limitErr != nil {
		return nil, nil, limitErr
	}

	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, nil, newAdapterError(http.StatusUnauthorized, CodeAuthRequired, "Sign-in required for this tool", nil)
	}

	items, nextCursor, listErr := a.store.ListDashboardTodos(ctx, userID, limit, in.Cursor, in.Sort)
	if listErr != nil {
		return nil, nil, mapStoreError(listErr)
	}

	out := make([]dashboardTodoItem, 0, len(items))
	for _, t := range items {
		out = append(out, dashboardTodoToItem(t))
	}

	return map[string]any{
			"items": out,
		}, map[string]any{
			"nextCursor": nextCursor,
			"hasMore":    nextCursor != nil,
		}, nil
}
