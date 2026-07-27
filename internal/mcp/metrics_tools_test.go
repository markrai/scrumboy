package mcp

import (
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBurndownPointsToItems_mapsFields(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	incomplete := 3
	scope := 5
	points := []store.BurndownPoint{
		{Date: date, IncompleteCount: &incomplete, TotalScope: &scope, NewTodosCount: 2},
		{Date: date.AddDate(0, 0, 1)}, // pre-project day: nil pointers
	}
	items := burndownPointsToItems(points)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].IncompleteCount == nil || *items[0].IncompleteCount != 3 {
		t.Fatalf("expected IncompleteCount 3, got %#v", items[0].IncompleteCount)
	}
	if items[0].TotalScope == nil || *items[0].TotalScope != 5 {
		t.Fatalf("expected TotalScope 5, got %#v", items[0].TotalScope)
	}
	if items[0].NewTodosCount != 2 {
		t.Fatalf("expected NewTodosCount 2, got %d", items[0].NewTodosCount)
	}
	if items[1].IncompleteCount != nil {
		t.Fatalf("expected nil IncompleteCount for pre-project day, got %#v", items[1].IncompleteCount)
	}
}

func TestRealBurndownPointsToItems_mapsFields(t *testing.T) {
	t.Parallel()
	date := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	remaining := 4
	points := []store.RealBurndownPoint{
		{Date: date, RemainingWork: &remaining, InitialScope: 10},
	}
	items := realBurndownPointsToItems(points)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].RemainingWork == nil || *items[0].RemainingWork != 4 {
		t.Fatalf("expected RemainingWork 4, got %#v", items[0].RemainingWork)
	}
	if items[0].InitialScope != 10 {
		t.Fatalf("expected InitialScope 10, got %d", items[0].InitialScope)
	}
}
