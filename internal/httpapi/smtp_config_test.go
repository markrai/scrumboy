package httpapi

import "testing"

func TestSMTPConfigured(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		from string
		want bool
	}{
		{"all set", "smtp.example.com", 587, "no-reply@example.com", true},
		{"from empty", "smtp.example.com", 587, "", false},
		{"host empty", "", 587, "no-reply@example.com", false},
		{"port zero", "smtp.example.com", 0, "no-reply@example.com", false},
		{"everything empty", "", 0, "", false},
		{"whitespace only host", "   ", 587, "no-reply@example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SMTPConfigured(tc.host, tc.port, tc.from); got != tc.want {
				t.Fatalf("SMTPConfigured(%q, %d, %q) = %v, want %v", tc.host, tc.port, tc.from, got, tc.want)
			}
		})
	}
}

func TestSMTPPartiallyConfigured(t *testing.T) {
	cases := []struct {
		name string
		host string
		port int
		from string
		want bool
	}{
		{"all set (fully configured, not partial)", "smtp.example.com", 587, "no-reply@example.com", false},
		{"everything empty (not partial)", "", 0, "", false},
		{"host only", "smtp.example.com", 0, "", true},
		{"from only", "", 0, "no-reply@example.com", true},
		{"host and port, no from", "smtp.example.com", 587, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SMTPPartiallyConfigured(tc.host, tc.port, tc.from); got != tc.want {
				t.Fatalf("SMTPPartiallyConfigured(%q, %d, %q) = %v, want %v", tc.host, tc.port, tc.from, got, tc.want)
			}
		})
	}
}
