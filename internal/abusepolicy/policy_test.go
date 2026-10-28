package abusepolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDisabledThrottlingKeepsCeilings(t *testing.T) {
	p := DisabledThrottling()
	if p.RateLimits.Enabled {
		t.Fatal("wanted disabled")
	}
	if p.Safety.MaxTodosPerImport != DefaultMaxImportTodos {
		t.Fatalf("ceilings dropped: %+v", p.Safety)
	}
}

func TestDefaultRateLimitsAreEnabled(t *testing.T) {
	p := Default()
	if p.Version != CurrentVersion || !p.RateLimits.Enabled {
		t.Fatalf("defaults version=%d enabled=%v", p.Version, p.RateLimits.Enabled)
	}
	if p.RateLimits.CreateInstance.Limit != 20 || p.RateLimits.CreateInstanceDaily.Window != 24*time.Hour {
		t.Fatalf("small-host creation windows: %+v daily=%+v", p.RateLimits.CreateInstance, p.RateLimits.CreateInstanceDaily)
	}
	if p.Safety.MaxTodosPerImport != DefaultMaxImportTodos || p.Safety.MaxProjectsPerImport != DefaultMaxImportProjects {
		t.Fatalf("safety ceilings: %+v", p.Safety)
	}
}

func TestParseEmptyFileIsError(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRequiresVersion(t *testing.T) {
	if _, err := Parse([]byte(`{"rateLimits":{"enabled":true}}`)); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsUnsupportedVersion(t *testing.T) {
	if _, err := Parse([]byte(`{"version":2}`)); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	if _, err := Parse([]byte(`{"version":1,"nope":true}`)); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsNull(t *testing.T) {
	if _, err := Parse([]byte(`{"version":1,"rateLimits":null}`)); err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsZeroLimit(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"mutations":{"perIP":{"limit":0,"window":"1m"}}}}`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsNegativeLimit(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"mutations":{"perIP":{"limit":-1,"window":"1m"}}}}`)
	if _, err := Parse(raw); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsFloatLimit(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"mutations":{"perIP":{"limit":1.5,"window":"1m"}}}}`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("err=%v", err)
	}
}

func TestParseRejectsInvalidDuration(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"mutations":{"perIP":{"limit":1,"window":"10"}}}}`)
	if _, err := Parse(raw); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRejectsMillisecondDuration(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"mutations":{"perIP":{"limit":1,"window":"10ms"}}}}`)
	if _, err := Parse(raw); err == nil || !strings.Contains(err.Error(), "s, m, or h") {
		t.Fatalf("err=%v", err)
	}
}

func TestParsePartialMergesDefaults(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"anonymousBoardCreation":{"instance":{"limit":120,"window":"1h"}}}}`)
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.RateLimits.CreateInstance.Limit != 120 {
		t.Fatalf("instance=%d", p.RateLimits.CreateInstance.Limit)
	}
	if p.RateLimits.CreatePerIP.Limit != 5 {
		t.Fatalf("perIP default lost: %d", p.RateLimits.CreatePerIP.Limit)
	}
	if !p.RateLimits.Enabled {
		t.Fatal("enabled should stay default true")
	}
}

func TestParseDisabledThrottlingKeepsCeilings(t *testing.T) {
	raw := []byte(`{"version":1,"rateLimits":{"enabled":false}}`)
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.RateLimits.Enabled {
		t.Fatal("wanted disabled")
	}
	if p.Safety.MaxTodosPerImport != DefaultMaxImportTodos {
		t.Fatalf("ceilings dropped: %+v", p.Safety)
	}
}

func TestLoadMissingPathUsesDefaults(t *testing.T) {
	p, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "defaults" {
		t.Fatalf("source=%s", p.Source)
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"rateLimits":{"enabled":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.RateLimits.Enabled {
		t.Fatal("wanted disabled")
	}
	if p.Source == "defaults" {
		t.Fatal("source should be the file path")
	}
}

func TestLogSummaryOmitsSecrets(t *testing.T) {
	s := Default().LogSummary()
	if !strings.Contains(s, "rateLimits.enabled=true") || strings.Contains(s, "password") {
		t.Fatalf("summary=%s", s)
	}
}

func TestFormatDurationWindows(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "10s"},
		{time.Second, "1s"},
		{time.Minute, "1m"},
		{24 * time.Hour, "24h"},
		{90 * time.Second, "1m30s"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.d); got != tc.want {
			t.Fatalf("formatDuration(%v)=%q want %q", tc.d, got, tc.want)
		}
	}
	s := Default().LogSummary()
	if !strings.Contains(s, "burst=40/10s") || !strings.Contains(s, "perIPDaily=10/24h") {
		t.Fatalf("duration logging: %s", s)
	}
}
