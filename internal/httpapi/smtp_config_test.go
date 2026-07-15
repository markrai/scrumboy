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
		{"bare valid address", "smtp.example.com", 587, "no-reply@example.com", true},
		{"valid display-name address", "smtp.example.com", 587, "Scrumboy <no-reply@example.com>", true},
		{"from empty", "smtp.example.com", 587, "", false},
		{"from whitespace only", "smtp.example.com", 587, "   ", false},
		{"malformed From", "smtp.example.com", 587, "not-an-address", false},
		{"CRLF From", "smtp.example.com", 587, "no-reply@example.com\r\nBcc: evil@example.com", false},
		{"host empty", "", 587, "no-reply@example.com", false},
		{"port zero", "smtp.example.com", 0, "no-reply@example.com", false},
		{"everything empty", "", 0, "", false},
		{"whitespace only host", "   ", 587, "no-reply@example.com", false},
		{"port 65535", "smtp.example.com", 65535, "no-reply@example.com", true},
		{"port 65536", "smtp.example.com", 65536, "no-reply@example.com", false},
		{"port negative", "smtp.example.com", -1, "no-reply@example.com", false},
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
		name         string
		host         string
		port         int
		from         string
		portExplicit bool
		want         bool
	}{
		{"all set (fully configured, not partial)", "smtp.example.com", 587, "no-reply@example.com", false, false},
		{"nothing + default port", "", 587, "", false, false},
		{"explicit port only", "", 587, "", true, true},
		{"invalid explicit port only", "", 0, "", true, true},
		{"host only", "smtp.example.com", 587, "", false, true},
		{"from only", "", 587, "no-reply@example.com", false, true},
		{"host+from implicit default", "smtp.example.com", 587, "no-reply@example.com", false, false},
		{"host+from valid explicit", "smtp.example.com", 465, "no-reply@example.com", true, false},
		{"host+from invalid explicit", "smtp.example.com", 0, "no-reply@example.com", true, true},
		{"host+from port 65536", "smtp.example.com", 65536, "no-reply@example.com", false, true},
		{"host+from port -1", "smtp.example.com", -1, "no-reply@example.com", false, true},
		{"host and port explicit, no from", "smtp.example.com", 587, "", true, true},
		{"host+malformed From", "smtp.example.com", 587, "not-an-address", false, true},
		{"host+CRLF From", "smtp.example.com", 587, "no-reply@example.com\r\nBcc: evil@example.com", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SMTPPartiallyConfigured(tc.host, tc.port, tc.from, tc.portExplicit); got != tc.want {
				t.Fatalf("SMTPPartiallyConfigured(%q, %d, %q, %v) = %v, want %v",
					tc.host, tc.port, tc.from, tc.portExplicit, got, tc.want)
			}
		})
	}
}
