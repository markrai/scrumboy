package config

import (
	"reflect"
	"testing"
)

func TestOIDCAllowedEmailDomainsFromEnv(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace", "   ", nil},
		{"single", "example.com", []string{"example.com"}},
		{"multiple", "example.com,example.org", []string{"example.com", "example.org"}},
		{"trims whitespace", " example.com , example.org ", []string{"example.com", "example.org"}},
		{"lowercases", "EXAMPLE.COM", []string{"example.com"}},
		{"strips leading @", "@example.com", []string{"example.com"}},
		{"drops empty entries", "example.com,,example.org,", []string{"example.com", "example.org"}},
		{"only empty entries", ",,", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SCRUMBOY_OIDC_ALLOWED_EMAIL_DOMAINS", tc.env)
			got := oidcAllowedEmailDomainsFromEnv()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("oidcAllowedEmailDomainsFromEnv() = %#v, want %#v (env=%q)", got, tc.want, tc.env)
			}
		})
	}
}
