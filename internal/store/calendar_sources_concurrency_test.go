package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentCalendarSourceCreateAtLimitReturnsDomainError(t *testing.T) {
	primary, concurrent, _, _ := newSprintConcurrencyStores(t)
	ctx, _, project := createSprintConcurrencyProject(t, primary, "Concurrent calendar source limit")
	for i := 0; i < MaxCalendarSources-1; i++ {
		if _, err := primary.CreateCalendarSource(ctx, project.ID, CreateCalendarSourceInput{
			Name:      "Feed",
			Enabled:   true,
			SecretEnc: "v1:secret",
			URLHash:   "hash-" + strconv.Itoa(i),
		}); err != nil {
			t.Fatalf("seed source %d: %v", i, err)
		}
	}

	start := make(chan struct{})
	type result struct {
		src CalendarSource
		err error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	run := func(st *Store, hash string) {
		ready.Done()
		<-start
		src, err := st.CreateCalendarSource(ctx, project.ID, CreateCalendarSourceInput{
			Name:      "Boundary",
			Enabled:   true,
			SecretEnc: "v1:secret",
			URLHash:   hash,
		})
		results <- result{src: src, err: err}
	}
	go run(primary, "hash-boundary-a")
	go run(concurrent, "hash-boundary-b")
	ready.Wait()
	close(start)

	successes, validations := 0, 0
	for i := 0; i < 2; i++ {
		got := <-results
		switch {
		case got.err == nil:
			successes++
		case errors.Is(got.err, ErrValidation) && ErrorReason(got.err) == ReasonCalendarSourceLimit:
			validations++
		case strings.Contains(strings.ToLower(fmt.Sprint(got.err)), "busy"):
			t.Fatalf("raw SQLite busy escaped: %v", got.err)
		default:
			t.Fatalf("unexpected result: %v", got.err)
		}
	}
	if successes != 1 || validations != 1 {
		t.Fatalf("successes=%d validations=%d", successes, validations)
	}
	count, err := primary.CountCalendarSources(ctx, project.ID)
	if err != nil {
		t.Fatalf("CountCalendarSources: %v", err)
	}
	if count != MaxCalendarSources {
		t.Fatalf("count=%d, want %d", count, MaxCalendarSources)
	}
}
