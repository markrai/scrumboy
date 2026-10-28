package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrLimited is the sentinel for a rejected multi-budget admission.
var ErrLimited = errors.New("rate limited")

// Cost is one named budget to consume atomically with the others in Admit.
type Cost struct {
	Key    string
	Limit  int
	Window time.Duration
	N      int
}

// Rejected describes the first exhausted budget. No cost is consumed.
type Rejected struct {
	Budget     string
	RetryAfter time.Duration
}

func (r *Rejected) Error() string {
	if r == nil {
		return ErrLimited.Error()
	}
	return ErrLimited.Error()
}

func (r *Rejected) Unwrap() error { return ErrLimited }

type admissionEntry struct {
	count    int
	windowAt time.Time
	window   time.Duration
}

// Admission is a process-local multi-budget fixed-window limiter. Peek then
// commit under one mutex so a rejected request consumes no budget.
type Admission struct {
	mu        sync.Mutex
	entries   map[string]*admissionEntry
	cleanup   time.Duration
	lastClean time.Time
	now       func() time.Time
}

// NewAdmission constructs an empty limiter. now may be nil (time.Now).
func NewAdmission(now func() time.Time) *Admission {
	if now == nil {
		now = time.Now
	}
	t := now()
	return &Admission{
		entries:   make(map[string]*admissionEntry),
		cleanup:   2 * time.Hour,
		lastClean: t,
		now:       now,
	}
}

type ctxKey int

const clientIPContextKey ctxKey = 1

// WithClientIP stores the trusted rate-limit IP on ctx.
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPContextKey, ip)
}

// ClientIPFrom returns the IP stored by WithClientIP, or "".
func ClientIPFrom(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPContextKey).(string)
	return ip
}

// Admit checks every cost and either increments all of them or none.
// N defaults to 1. Empty keys and non-positive limits are skipped.
func (a *Admission) Admit(costs []Cost) *Rejected {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if now.Sub(a.lastClean) > a.cleanup {
		a.clean(now)
		a.lastClean = now
	}

	type pending struct {
		cost  Cost
		entry *admissionEntry
		fresh bool
	}
	var work []pending
	for _, cost := range costs {
		if cost.Key == "" || cost.Limit <= 0 || cost.Window <= 0 {
			continue
		}
		n := cost.N
		if n <= 0 {
			n = 1
		}
		cost.N = n
		e, ok := a.entries[cost.Key]
		fresh := !ok || now.Sub(e.windowAt) > cost.Window
		if fresh {
			if n > cost.Limit {
				return &Rejected{Budget: cost.Key, RetryAfter: cost.Window}
			}
			work = append(work, pending{cost: cost, fresh: true})
			continue
		}
		if e.count > cost.Limit-n {
			retry := cost.Window - now.Sub(e.windowAt)
			if retry < time.Second {
				retry = time.Second
			}
			return &Rejected{Budget: cost.Key, RetryAfter: retry}
		}
		work = append(work, pending{cost: cost, entry: e})
	}

	for _, item := range work {
		if item.fresh {
			a.entries[item.cost.Key] = &admissionEntry{
				count:    item.cost.N,
				windowAt: now,
				window:   item.cost.Window,
			}
			continue
		}
		item.entry.count += item.cost.N
	}
	return nil
}

func (a *Admission) clean(now time.Time) {
	for k, e := range a.entries {
		if now.Sub(e.windowAt) > e.window {
			delete(a.entries, k)
		}
	}
}

// LenForTest returns the live map size.
func (a *Admission) LenForTest() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.entries)
}
