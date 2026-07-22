package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"scrumboy/internal/httpapi/ratelimit"
)

func TestSensitiveRateLimitsAreIndependentByUserAndTrustedIP(t *testing.T) {
	s := &Server{}
	userLimiter := ratelimit.New(1, time.Minute)
	req1 := httptest.NewRequest("POST", "http://scrumboy.test/", nil)
	req1.RemoteAddr = "192.0.2.1:1000"
	if !s.allowSensitive(userLimiter, req1, 1) {
		t.Fatal("first attempt denied")
	}
	reqOtherIP := httptest.NewRequest("POST", "http://scrumboy.test/", nil)
	reqOtherIP.RemoteAddr = "192.0.2.2:1000"
	if s.allowSensitive(userLimiter, reqOtherIP, 1) {
		t.Fatal("rotating IP bypassed per-user bucket")
	}

	ipLimiter := ratelimit.New(1, time.Minute)
	if !s.allowSensitive(ipLimiter, req1, 1) {
		t.Fatal("first IP attempt denied")
	}
	if s.allowSensitive(ipLimiter, req1, 2) {
		t.Fatal("rotating user bypassed per-IP bucket")
	}

	isolated := ratelimit.New(1, time.Minute)
	if !s.allowSensitive(isolated, req1, 1) {
		t.Fatal("isolated operation limiter inherited another operation's exhaustion")
	}
}

func TestAggregateSecondFactorBucketCannotBeDoubledByFormatSwitching(t *testing.T) {
	if classifySecondFactor("123456") != "totp" || classifySecondFactor("ABCD-EFGH") != "recovery" {
		t.Fatal("second-factor formats were not classified before verification")
	}
	s := &Server{secondFactorLimiter: ratelimit.New(1, time.Minute)}
	req := httptest.NewRequest("POST", "http://scrumboy.test/", nil)
	req.RemoteAddr = "192.0.2.4:1000"
	if !s.allowSensitive(s.secondFactorLimiter, req, 7) {
		t.Fatal("first aggregate attempt denied")
	}
	if s.allowSensitive(s.secondFactorLimiter, req, 7) {
		t.Fatal("format switch could obtain a second aggregate attempt")
	}
}
