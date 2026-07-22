package httpapi

import "scrumboy/internal/publicorigin"

// parseHTTPAuthority validates an HTTP authority consisting of a hostname and
// an optional port. It deliberately does not rely on net/url's permissive
// authority parsing: callers need to distinguish no port from an explicit
// empty port and reject URL components smuggled into Host-like inputs.
//
// The returned authority is the validated input unchanged. The returned
// hostname omits IPv6 brackets so callers can perform address checks without
// parsing the authority a second time.
func parseHTTPAuthority(raw string) (authority, hostname string, ok bool) {
	return publicorigin.ParseHTTPAuthority(raw)
}
