package ratelimit

import (
	"testing"
	"time"
)

func TestNormalizeEmailTrimsAndLowercases(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "surrounding whitespace", input: "  User@Example.COM  ", want: "user@example.com"},
		{name: "tabs and newlines", input: "\tUser@Example.COM\n", want: "user@example.com"},
		{name: "mixed case", input: "MiXeD@Example.COM", want: "mixed@example.com"},
		{name: "plus address text", input: " User.Name+Tag@Example.COM ", want: "user.name+tag@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeEmail(tt.input); got != tt.want {
				t.Fatalf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllowWithNoKeysAlwaysSucceeds(t *testing.T) {
	limiter := New(1, time.Minute)

	for i := 0; i < 5; i++ {
		if !limiter.Allow("", "") {
			t.Fatalf("Allow with empty keys returned false on call %d", i+1)
		}
	}
}

func TestAllowHonorsIPLimit(t *testing.T) {
	limiter := New(2, time.Minute)

	if !limiter.Allow("ip:192.0.2.1", "") {
		t.Fatal("first request for IP was rejected")
	}
	if !limiter.Allow("ip:192.0.2.1", "") {
		t.Fatal("second request for IP was rejected")
	}
	if limiter.Allow("ip:192.0.2.1", "") {
		t.Fatal("third request for same IP was allowed")
	}
	if !limiter.Allow("ip:192.0.2.2", "") {
		t.Fatal("request for different IP was rejected")
	}
}

func TestAllowHonorsEmailLimitAcrossDifferentIPs(t *testing.T) {
	limiter := New(2, time.Minute)
	emailKey := "email:" + NormalizeEmail("User+Tag@Example.COM")

	if !limiter.Allow("ip:192.0.2.1", emailKey) {
		t.Fatal("first request for email was rejected")
	}
	if !limiter.Allow("ip:192.0.2.2", emailKey) {
		t.Fatal("second request for email was rejected")
	}
	if limiter.Allow("ip:192.0.2.3", emailKey) {
		t.Fatal("third request for same email across different IPs was allowed")
	}
}

func TestAllowHonorsIPLimitAcrossDifferentEmails(t *testing.T) {
	limiter := New(2, time.Minute)
	ipKey := "ip:192.0.2.1"

	if !limiter.Allow(ipKey, "email:"+NormalizeEmail("one@example.com")) {
		t.Fatal("first request for IP was rejected")
	}
	if !limiter.Allow(ipKey, "email:"+NormalizeEmail("two@example.com")) {
		t.Fatal("second request for IP was rejected")
	}
	if limiter.Allow(ipKey, "email:"+NormalizeEmail("three@example.com")) {
		t.Fatal("third request for same IP across different emails was allowed")
	}
}

func TestAllowResetsExpiredWindow(t *testing.T) {
	window := time.Minute
	limiter := New(2, window)
	key := "ip:192.0.2.1"

	if !limiter.Allow(key, "") {
		t.Fatal("first request was rejected")
	}
	if !limiter.Allow(key, "") {
		t.Fatal("second request was rejected")
	}
	if limiter.Allow(key, "") {
		t.Fatal("third request before window expiry was allowed")
	}

	limiter.mu.Lock()
	limiter.entries[key].windowAt = time.Now().Add(-window - time.Second)
	limiter.mu.Unlock()

	beforeReset := time.Now().Add(-time.Second)
	if !limiter.Allow(key, "") {
		t.Fatal("request after window expiry was rejected")
	}
	afterReset := time.Now().Add(time.Second)

	limiter.mu.Lock()
	got := limiter.entries[key]
	limiter.mu.Unlock()
	if got == nil {
		t.Fatal("entry was not recreated after expired window")
	}
	if got.count != 1 {
		t.Fatalf("entry count after reset = %d, want 1", got.count)
	}
	if got.windowAt.Before(beforeReset) || got.windowAt.After(afterReset) {
		t.Fatalf("entry windowAt after reset = %v, want between %v and %v", got.windowAt, beforeReset, afterReset)
	}
}

func TestAllowCleansStaleEntries(t *testing.T) {
	window := time.Minute
	limiter := New(2, window)
	now := time.Now()
	staleKey := "ip:stale"
	freshKey := "ip:fresh"
	newKey := "ip:new"

	limiter.mu.Lock()
	limiter.entries[staleKey] = &entry{count: 1, windowAt: now.Add(-window - time.Second)}
	limiter.entries[freshKey] = &entry{count: 1, windowAt: now}
	limiter.lastClean = now.Add(-limiter.cleanup - time.Second)
	limiter.mu.Unlock()

	if !limiter.Allow(newKey, "") {
		t.Fatal("request for new key was rejected")
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if _, ok := limiter.entries[staleKey]; ok {
		t.Fatal("stale entry remained after cleanup")
	}
	if _, ok := limiter.entries[freshKey]; !ok {
		t.Fatal("fresh entry was removed during cleanup")
	}
	if _, ok := limiter.entries[newKey]; !ok {
		t.Fatal("new key entry was not created")
	}
}
