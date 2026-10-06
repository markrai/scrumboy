package todo

import (
	"context"
	"errors"
	"time"

	"scrumboy/internal/store"
)

var (
	ErrUnsupportedCompletionPeriod = errors.New("unsupported completion period")
	ErrInvalidCompletionTimezone   = errors.New("invalid completion timezone")
)

const CompletionPeriodThisWeek = "this-week"

type CompletionCountAccessStore interface {
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
}

type CompletedTodoCountStore interface {
	CountCompletedTodosForProject(
		ctx context.Context,
		projectID int64,
		start time.Time,
		end time.Time,
	) (int, error)
}

type MCPCompletionCountServiceDependencies struct {
	Access CompletionCountAccessStore
	Counts CompletedTodoCountStore
	Now    func() time.Time
}

type MCPCompletionCountService struct {
	access CompletionCountAccessStore
	counts CompletedTodoCountStore
	now    func() time.Time
}

func NewMCPCompletionCountService(deps MCPCompletionCountServiceDependencies) *MCPCompletionCountService {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &MCPCompletionCountService{access: deps.Access, counts: deps.Counts, now: now}
}

type SlugCompletionCountTarget struct {
	Slug string
	Mode store.Mode
}

type CompletionCountCommand struct {
	Period   string
	Timezone string
}

type CompletionCountResult struct {
	Project  store.Project
	Count    int
	Period   string
	Timezone string
	Start    time.Time
	End      time.Time
}

func (s *MCPCompletionCountService) Count(
	ctx context.Context,
	target SlugCompletionCountTarget,
	command CompletionCountCommand,
) (CompletionCountResult, error) {
	if command.Period != CompletionPeriodThisWeek {
		return CompletionCountResult{}, ErrUnsupportedCompletionPeriod
	}
	timezone := command.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return CompletionCountResult{}, ErrInvalidCompletionTimezone
	}

	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.Slug, target.Mode)
	if err != nil {
		return CompletionCountResult{}, err
	}
	start, end := currentCalendarWeek(s.now(), location)
	count, err := s.counts.CountCompletedTodosForProject(
		ctx,
		projectContext.Project.ID,
		start,
		end,
	)
	if err != nil {
		return CompletionCountResult{}, err
	}
	return CompletionCountResult{
		Project:  projectContext.Project,
		Count:    count,
		Period:   command.Period,
		Timezone: timezone,
		Start:    start,
		End:      end,
	}, nil
}

func currentCalendarWeek(now time.Time, location *time.Location) (time.Time, time.Time) {
	localNow := now.In(location)
	weekday := int(localNow.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		0, 0, 0, 0,
		location,
	).AddDate(0, 0, -(weekday - 1))
	return start, start.AddDate(0, 0, 7)
}
