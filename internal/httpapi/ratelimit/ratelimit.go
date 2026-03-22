package ratelimit

import (
	"strings"
	"sync"
	"time"
)

// Limiter is an in-memory rate limiter with dual-key (IP + email) enforcement.
// Both keys must be under the limit for the request to be allowed.
type Limiter struct {
	mu       sync.Mutex
	entries  map[string]*entry
	limit    int
	window   time.Duration
	cleanup  time.Duration
	lastClean time.Time
}

type entry struct {
	count    int
	windowAt time.Time
}

// New creates a limiter allowing `limit` requests per `window` per key.
func New(limit int, window time.Duration) *Limiter {
	return &Limiter{
		entries:  make(map[string]*entry),
		limit:    limit,
		window:   window,
		cleanup:  window * 2,
		lastClean: time.Now(),
	}
}

// Allow checks both keys; returns false (rate limited) if either key exceeds the limit.
func (l *Limiter) Allow(ipKey, emailKey string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.lastClean) > l.cleanup {
		l.clean(now)
		l.lastClean = now
	}

	if !l.allowKey(ipKey, now) {
		return false
	}
	if emailKey != "" && !l.allowKey(emailKey, now) {
		return false
	}
	return true
}

func (l *Limiter) allowKey(key string, now time.Time) bool {
	if key == "" {
		return true
	}
	e, ok := l.entries[key]
	if !ok || now.Sub(e.windowAt) > l.window {
		l.entries[key] = &entry{count: 1, windowAt: now}
		return true
	}
	if e.count >= l.limit {
		return false
	}
	e.count++
	return true
}

func (l *Limiter) clean(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.windowAt) > l.window {
			delete(l.entries, k)
		}
	}
}

// NormalizeEmail trims and lowercases for rate limit key (no Gmail-dot tricks).
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
