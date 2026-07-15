package config

import (
	"os"
	"testing"
)

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   ", ""},
		{"valid https", "https://scrumboy.example.com", "https://scrumboy.example.com"},
		{"trailing slash stripped via canonicalize", "https://scrumboy.example.com/", "https://scrumboy.example.com"},
		{"scheme lowercased", "HTTPS://scrumboy.example.com/", "https://scrumboy.example.com"},
		{"port preserved", "https://scrumboy.example.com:8443", "https://scrumboy.example.com:8443"},
		{"localhost with port", "http://localhost:8080", "http://localhost:8080"},
		{"ipv6 with port", "http://[::1]:8080", "http://[::1]:8080"},
		{"ftp scheme rejected", "ftp://scrumboy.example.com", ""},
		{"relative rejected", "//scrumboy.example.com", ""},
		{"missing hostname", "https://:8443", ""},
		{"userinfo rejected", "https://u:p@scrumboy.example.com", ""},
		{"path rejected", "https://scrumboy.example.com/scrumboy", ""},
		{"query rejected", "https://scrumboy.example.com?q=1", ""},
		{"bare question mark rejected", "https://scrumboy.example.com?", ""},
		{"fragment rejected", "https://scrumboy.example.com#frag", ""},
		{"double slash path rejected", "https://scrumboy.example.com//", ""},
		{"encoded slash path rejected", "https://scrumboy.example.com/%2F", ""},
		{"malformed port rejected", "https://scrumboy.example.com:notaport", ""},
		{"max valid port accepted", "https://scrumboy.example.com:65535", "https://scrumboy.example.com:65535"},
		{"port 65536 rejected", "https://scrumboy.example.com:65536", ""},
		{"port 0 rejected", "https://scrumboy.example.com:0", ""},
		{"dangling colon rejected", "https://scrumboy.example.com:", ""},
		{"ipv6 dangling colon rejected", "http://[::1]:", ""},
		{"javascript scheme rejected", "javascript:alert(1)", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeBaseURL(tc.raw); got != tc.want {
				t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestFromEnv_PublicBaseURL(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("SCRUMBOY_PUBLIC_BASE_URL", "HTTPS://scrumboy.example.com/")
	cfg := FromEnv()
	if cfg.PublicBaseURL != "https://scrumboy.example.com" {
		t.Fatalf("PublicBaseURL = %q, want canonical origin", cfg.PublicBaseURL)
	}

	t.Setenv("SCRUMBOY_PUBLIC_BASE_URL", "https://evil.example/phish")
	cfg = FromEnv()
	if cfg.PublicBaseURL != "" {
		t.Fatalf("invalid PublicBaseURL should be empty, got %q", cfg.PublicBaseURL)
	}
}

func TestNormalizeSMTPTLSMode(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty defaults to starttls", "", "starttls"},
		{"whitespace defaults to starttls", "   ", "starttls"},
		{"starttls passthrough", "starttls", "starttls"},
		{"implicit passthrough", "implicit", "implicit"},
		{"none passthrough", "none", "none"},
		{"case insensitive", "IMPLICIT", "implicit"},
		{"trimmed", "  none  ", "none"},
		{"unrecognized falls back to starttls", "bogus", "starttls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSMTPTLSMode(tc.raw); got != tc.want {
				t.Fatalf("normalizeSMTPTLSMode(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestSMTPPortFromEnv(t *testing.T) {
	cases := []struct {
		name      string
		unset     bool
		raw       string
		wantPort  int
		wantExpl  bool
	}{
		{name: "unset", unset: true, wantPort: defaultSMTPPort, wantExpl: false},
		{name: "587", raw: "587", wantPort: 587, wantExpl: true},
		{name: "trimmed 2525", raw: " 2525 ", wantPort: 2525, wantExpl: true},
		{name: "empty explicit", raw: "", wantPort: 0, wantExpl: true},
		{name: "not-a-number", raw: "not-a-number", wantPort: 0, wantExpl: true},
		{name: "zero", raw: "0", wantPort: 0, wantExpl: true},
		{name: "65536", raw: "65536", wantPort: 0, wantExpl: true},
		{name: "65535", raw: "65535", wantPort: 65535, wantExpl: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			if tc.unset {
				unsetEnv(t, "SCRUMBOY_SMTP_PORT")
			} else {
				t.Setenv("SCRUMBOY_SMTP_PORT", tc.raw)
			}
			gotPort, gotExpl := smtpPortFromEnv()
			if gotPort != tc.wantPort || gotExpl != tc.wantExpl {
				t.Fatalf("smtpPortFromEnv() = (%d, %v), want (%d, %v)", gotPort, gotExpl, tc.wantPort, tc.wantExpl)
			}
		})
	}
}

func TestFromEnv_SMTP(t *testing.T) {
	t.Run("full config", func(t *testing.T) {
		t.Setenv("SCRUMBOY_SMTP_HOST", "smtp.example.com")
		t.Setenv("SCRUMBOY_SMTP_PORT", "465")
		t.Setenv("SCRUMBOY_SMTP_USERNAME", "  bot  ")
		t.Setenv("SCRUMBOY_SMTP_PASSWORD", "  s3cret  ")
		t.Setenv("SCRUMBOY_SMTP_FROM", "Scrumboy <no-reply@example.com>")
		t.Setenv("SCRUMBOY_SMTP_TLS_MODE", "implicit")
		t.Setenv("SCRUMBOY_SMTP_DEBUG", "1")
		t.Setenv("DATA_DIR", t.TempDir())

		cfg := FromEnv()
		if cfg.SMTPHost != "smtp.example.com" {
			t.Fatalf("SMTPHost = %q", cfg.SMTPHost)
		}
		if cfg.SMTPPort != 465 {
			t.Fatalf("SMTPPort = %d", cfg.SMTPPort)
		}
		if !cfg.SMTPPortExplicit {
			t.Fatal("expected SMTPPortExplicit true")
		}
		if cfg.SMTPUsername != "bot" {
			t.Fatalf("SMTPUsername = %q, want trimmed", cfg.SMTPUsername)
		}
		if cfg.SMTPPassword != "s3cret" {
			t.Fatalf("SMTPPassword = %q, want trimmed", cfg.SMTPPassword)
		}
		if cfg.SMTPFrom != "Scrumboy <no-reply@example.com>" {
			t.Fatalf("SMTPFrom = %q", cfg.SMTPFrom)
		}
		if cfg.SMTPTLSMode != "implicit" {
			t.Fatalf("SMTPTLSMode = %q", cfg.SMTPTLSMode)
		}
		if !cfg.SMTPDebug {
			t.Fatal("expected SMTPDebug true")
		}
	})

	t.Run("port default when unset", func(t *testing.T) {
		t.Setenv("DATA_DIR", t.TempDir())
		unsetEnv(t, "SCRUMBOY_SMTP_PORT")
		cfg := FromEnv()
		if cfg.SMTPPort != defaultSMTPPort {
			t.Fatalf("SMTPPort default = %d, want %d", cfg.SMTPPort, defaultSMTPPort)
		}
		if cfg.SMTPPortExplicit {
			t.Fatal("expected SMTPPortExplicit false when unset")
		}
		if cfg.SMTPTLSMode != "starttls" {
			t.Fatalf("SMTPTLSMode default = %q, want starttls", cfg.SMTPTLSMode)
		}
	})

	t.Run("invalid explicit port fails closed", func(t *testing.T) {
		t.Setenv("SCRUMBOY_SMTP_PORT", "not-a-number")
		t.Setenv("DATA_DIR", t.TempDir())
		cfg := FromEnv()
		if cfg.SMTPPort != 0 {
			t.Fatalf("SMTPPort = %d, want 0", cfg.SMTPPort)
		}
		if !cfg.SMTPPortExplicit {
			t.Fatal("expected SMTPPortExplicit true for invalid explicit port")
		}
	})
}
