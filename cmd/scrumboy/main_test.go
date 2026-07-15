package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestLogWebPushConfiguration(t *testing.T) {
	cases := []struct {
		name string
		mode string
		pub  string
		priv string
		want string
	}{
		{name: "enabled", mode: "full", pub: "pub", priv: "priv", want: "web push: enabled"},
		{name: "disabled", mode: "full", pub: "", priv: "", want: "web push: disabled (set SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY)"},
		{name: "partial public only", mode: "full", pub: "pub", priv: "", want: "web push: partial config ignored"},
		{name: "partial private only", mode: "full", pub: "", priv: "priv", want: "web push: partial config ignored"},
		{name: "trimmed disabled", mode: "full", pub: "   ", priv: "\t", want: "web push: disabled (set SCRUMBOY_VAPID_PUBLIC_KEY and SCRUMBOY_VAPID_PRIVATE_KEY)"},
		{name: "anonymous with keys", mode: "anonymous", pub: "pub", priv: "priv", want: "web push: disabled (anonymous mode)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.New(&buf, "", 0)
			logWebPushConfiguration(logger, tc.mode, tc.pub, tc.priv)
			if got := strings.TrimSpace(buf.String()); got != tc.want {
				t.Fatalf("log output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogSMTPConfiguration(t *testing.T) {
	cases := []struct {
		name         string
		host         string
		port         int
		from         string
		portExplicit bool
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "disabled implicit default only",
			host:         "",
			port:         587,
			from:         "",
			portExplicit: false,
			wantContains: []string{"smtp: disabled"},
			wantAbsent:   []string{"smtp: enabled", "smtp: partial or invalid config ignored"},
		},
		{
			name:         "partial host only",
			host:         "smtp.example.com",
			port:         587,
			from:         "",
			portExplicit: false,
			wantContains: []string{"smtp: partial or invalid config ignored"},
			wantAbsent:   []string{"smtp: enabled"},
		},
		{
			name:         "partial host+from invalid port",
			host:         "smtp.example.com",
			port:         0,
			from:         "no-reply@example.com",
			portExplicit: true,
			wantContains: []string{"smtp: partial or invalid config ignored"},
			wantAbsent:   []string{"smtp: enabled"},
		},
		{
			name:         "enabled host+from implicit default",
			host:         "smtp.example.com",
			port:         587,
			from:         "no-reply@example.com",
			portExplicit: false,
			wantContains: []string{"smtp: enabled"},
			wantAbsent:   []string{"smtp: partial or invalid config ignored", "smtp: disabled"},
		},
		{
			name:         "partial host+malformed From",
			host:         "smtp.example.com",
			port:         587,
			from:         "not-an-address",
			portExplicit: false,
			wantContains: []string{"smtp: partial or invalid config ignored"},
			wantAbsent:   []string{"smtp: enabled"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := log.New(&buf, "", 0)
			logSMTPConfiguration(logger, tc.host, tc.port, tc.from, tc.portExplicit, "")
			got := buf.String()
			for _, sub := range tc.wantContains {
				if !strings.Contains(got, sub) {
					t.Fatalf("log output = %q, want substring %q", got, sub)
				}
			}
			for _, sub := range tc.wantAbsent {
				if strings.Contains(got, sub) {
					t.Fatalf("log output = %q, must not contain %q", got, sub)
				}
			}
		})
	}
}
