package safehttp

import "testing"

func TestValidPublicHTTPSEndpoint(t *testing.T) {
	tests := []struct{
		endpoint string
		want bool
	}{
		{"https://push.example.com/path/token?key=a%23b", true},
		{"https://push.example.com:8443/send", true},
		{"https://[2001:4860:4860::8888]:443/path", true},
		{"http://push.example.com/path", false},
		{"", false},
		{"https://", false},
		{"https://user:pass@push.example.com/send", false},
		{"https://push.example.com/path#frag", false},
		{"https://push.example.com/path#", false},
		{"https://push.example.com:99999/send", false},
		{"https://push.example.com:0/send", false},
		{"https://push.example.com:/send", false},
		{"https://127.0.0.1/send", false},
		{"https://127.1/send", false},
		{"https://10.1.2.3/send", false},
		{"https://169.254.169.254/latest/meta-data", false},
		{"https://192.168.1.1/send", false},
		{"https://[::1]/send", false},
		{"https://[::ffff:127.0.0.1]/send", false},
		{"https://[fc00::1]/send", false},
		{"https://[64:ff9b::8.8.8.8]/send", false},
		{"https://localhost/send", false},
		{"https://api.localhost/send", false},
		{"https://LOCALHOST./send", false},
		{"https://push.example.com\\@localhost/send", false},
		{"https://push.example.com/hello world", false},
		{"https://push.example.com/hello\nworld", false},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := ValidPublicHTTPSEndpoint(tt.endpoint); got != tt.want {
				t.Fatalf("ValidPublicHTTPSEndpoint(%q) = %t, want %t", tt.endpoint, got, tt.want)
			}
		})
	}
}
