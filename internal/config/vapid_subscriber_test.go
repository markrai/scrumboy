package config

import "testing"

func TestNormalizeVAPIDSubscriber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"ops@example.com", "mailto:ops@example.com"},
		{"mailto:ops@example.com", "mailto:ops@example.com"},
		{"Mailto:ops@example.com", "Mailto:ops@example.com"},
		{"https://example.com/contact", "https://example.com/contact"},
		{"not an email", "not an email"},
	}
	for _, tc := range tests {
		if got := NormalizeVAPIDSubscriber(tc.in); got != tc.want {
			t.Errorf("NormalizeVAPIDSubscriber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
