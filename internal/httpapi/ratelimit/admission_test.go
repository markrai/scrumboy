package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestAdmitEmptyCostsSucceeds(t *testing.T) {
	a := NewAdmission(nil)
	if a.Admit(nil) != nil {
		t.Fatal("empty costs should succeed")
	}
}

func TestAdmitRejectsWithoutConsumingOtherBudgets(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := NewAdmission(func() time.Time { return now })
	ip := Cost{Key: "mut-ip:1", Limit: 2, Window: time.Minute}
	board := Cost{Key: "mut-board:9", Limit: 1, Window: time.Minute}

	if a.Admit([]Cost{ip, board}) != nil {
		t.Fatal("first admit")
	}
	if rej := a.Admit([]Cost{ip, board}); rej == nil || rej.Budget != "mut-board:9" {
		t.Fatalf("rejected=%+v", rej)
	}
	if a.Admit([]Cost{ip, Cost{Key: "mut-board:8", Limit: 1, Window: time.Minute}}) != nil {
		t.Fatal("IP budget was consumed by the rejected request")
	}
}

func TestAdmitMultiTokenRejectsAtomically(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := NewAdmission(func() time.Time { return now })
	create := Cost{Key: "create-global", Limit: 3, Window: time.Hour, N: 2}
	if a.Admit([]Cost{create}) != nil {
		t.Fatal("first")
	}
	if a.Admit([]Cost{Cost{Key: "create-global", Limit: 3, Window: time.Hour, N: 2}}) == nil {
		t.Fatal("second N=2 should fail")
	}
	if a.Admit([]Cost{Cost{Key: "create-global", Limit: 3, Window: time.Hour, N: 1}}) != nil {
		t.Fatal("remaining 1 token should succeed")
	}
}

func TestAdmitWindowReset(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := NewAdmission(func() time.Time { return now })
	cost := Cost{Key: "burst", Limit: 1, Window: 10 * time.Second}
	if a.Admit([]Cost{cost}) != nil {
		t.Fatal("first")
	}
	if a.Admit([]Cost{cost}) == nil {
		t.Fatal("expected reject")
	}
	now = now.Add(10*time.Second + time.Nanosecond)
	if a.Admit([]Cost{cost}) != nil {
		t.Fatal("after window")
	}
}

func TestAdmitConcurrentBoundary(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := NewAdmission(func() time.Time { return now })
	cost := Cost{Key: "board", Limit: 40, Window: time.Minute}
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 80; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.Admit([]Cost{cost}) == nil {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 40 {
		t.Fatalf("allowed=%d want 40", allowed)
	}
}

func TestAdmitCleansStaleKeys(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	a := NewAdmission(func() time.Time { return now })
	a.cleanup = time.Minute
	if a.Admit([]Cost{{Key: "old", Limit: 1, Window: time.Minute}}) != nil {
		t.Fatal("first")
	}
	now = now.Add(2 * time.Minute)
	if a.Admit([]Cost{{Key: "new", Limit: 1, Window: time.Minute}}) != nil {
		t.Fatal("second")
	}
	if a.LenForTest() != 1 {
		t.Fatalf("len=%d", a.LenForTest())
	}
}
