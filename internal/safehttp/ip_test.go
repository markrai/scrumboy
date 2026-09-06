package safehttp

import (
	"net"
	"testing"
)

func TestIsForbiddenIP(t *testing.T) {
	t.Parallel()

	forbidden := []string{
		"127.0.0.1",
		"127.0.0.2",
		"0.0.0.0",
		"10.1.2.3",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.1.1",
		"169.254.169.254",
		"224.0.0.1",
		"100.64.0.1",
		"100.127.255.254",
		"192.0.2.1",
		"::1",
		"::",
		"fc00::1",
		"fe80::1",
		"ff02::1",
		"::ffff:127.0.0.1",
		"::ffff:10.1.2.3",
		"::ffff:169.254.169.254",
		"::ffff:100.64.0.1",
	}
	for _, raw := range forbidden {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("ParseIP(%q) nil", raw)
		}
		if !IsForbiddenIP(ip) {
			t.Errorf("%s: want forbidden", raw)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"}
	for _, raw := range allowed {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("ParseIP(%q) nil", raw)
		}
		if IsForbiddenIP(ip) {
			t.Errorf("%s: want allowed", raw)
		}
	}

	if !IsForbiddenIP(nil) {
		t.Fatal("nil IP must be forbidden")
	}
}
