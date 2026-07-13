package config

import "testing"

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
		cfg := FromEnv()
		if cfg.SMTPPort != 587 {
			t.Fatalf("SMTPPort default = %d, want 587", cfg.SMTPPort)
		}
		if cfg.SMTPTLSMode != "starttls" {
			t.Fatalf("SMTPTLSMode default = %q, want starttls", cfg.SMTPTLSMode)
		}
	})

	t.Run("invalid port falls back to default", func(t *testing.T) {
		t.Setenv("SCRUMBOY_SMTP_PORT", "not-a-number")
		t.Setenv("DATA_DIR", t.TempDir())
		cfg := FromEnv()
		if cfg.SMTPPort != 587 {
			t.Fatalf("SMTPPort = %d, want fallback 587", cfg.SMTPPort)
		}
	})
}
