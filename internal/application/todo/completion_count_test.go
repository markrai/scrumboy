package todo

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type completionCountAccessFake struct {
	context store.ProjectContext
	calls   int
}

func (f *completionCountAccessFake) GetProjectContextBySlug(
	context.Context,
	string,
	store.Mode,
) (store.ProjectContext, error) {
	f.calls++
	return f.context, nil
}

type completedTodoCountFake struct {
	projectID int64
	start     time.Time
	end       time.Time
	count     int
	err       error
}

func (f *completedTodoCountFake) CountCompletedTodosForProject(
	_ context.Context,
	projectID int64,
	start time.Time,
	end time.Time,
) (int, error) {
	f.projectID = projectID
	f.start = start
	f.end = end
	return f.count, f.err
}

func TestMCPCompletionCountUsesProjectScopeAndMondayTimezoneBoundary(t *testing.T) {
	access := &completionCountAccessFake{context: store.ProjectContext{
		Project: store.Project{ID: 42, Slug: "alpha"},
	}}
	counts := &completedTodoCountFake{count: 12}
	service := NewMCPCompletionCountService(MCPCompletionCountServiceDependencies{
		Access: access,
		Counts: counts,
		Now: func() time.Time {
			return time.Date(2026, 3, 11, 16, 30, 0, 0, time.UTC)
		},
	})

	result, err := service.Count(context.Background(), SlugCompletionCountTarget{
		Slug: "alpha",
		Mode: store.ModeFull,
	}, CompletionCountCommand{
		Period:   CompletionPeriodThisWeek,
		Timezone: "America/New_York",
	})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if access.calls != 1 || counts.projectID != 42 || result.Count != 12 {
		t.Fatalf("unexpected authority/result: access=%d project=%d result=%+v", access.calls, counts.projectID, result)
	}
	if got := counts.start.Format(time.RFC3339); got != "2026-03-09T00:00:00-04:00" {
		t.Fatalf("start=%s", got)
	}
	if got := counts.end.Format(time.RFC3339); got != "2026-03-16T00:00:00-04:00" {
		t.Fatalf("end=%s", got)
	}
}

func TestMCPCompletionCountRejectsUnsupportedPolicyBeforeAccess(t *testing.T) {
	access := &completionCountAccessFake{}
	service := NewMCPCompletionCountService(MCPCompletionCountServiceDependencies{
		Access: access,
		Counts: &completedTodoCountFake{},
	})

	_, err := service.Count(context.Background(), SlugCompletionCountTarget{}, CompletionCountCommand{
		Period:   "last-month",
		Timezone: "UTC",
	})
	if !errors.Is(err, ErrUnsupportedCompletionPeriod) || access.calls != 0 {
		t.Fatalf("unsupported period err=%v access=%d", err, access.calls)
	}

	_, err = service.Count(context.Background(), SlugCompletionCountTarget{}, CompletionCountCommand{
		Period:   CompletionPeriodThisWeek,
		Timezone: "Not/A_Timezone",
	})
	if !errors.Is(err, ErrInvalidCompletionTimezone) || access.calls != 0 {
		t.Fatalf("invalid timezone err=%v access=%d", err, access.calls)
	}
}

func TestMCPCompletionCountPreservesCountFailure(t *testing.T) {
	want := errors.New("count failed")
	service := NewMCPCompletionCountService(MCPCompletionCountServiceDependencies{
		Access: &completionCountAccessFake{context: store.ProjectContext{
			Project: store.Project{ID: 42, Slug: "alpha"},
		}},
		Counts: &completedTodoCountFake{err: want},
	})
	_, err := service.Count(context.Background(), SlugCompletionCountTarget{
		Slug: "alpha",
		Mode: store.ModeFull,
	}, CompletionCountCommand{
		Period:   CompletionPeriodThisWeek,
		Timezone: "UTC",
	})
	if !errors.Is(err, want) {
		t.Fatalf("err=%v want=%v", err, want)
	}
}
