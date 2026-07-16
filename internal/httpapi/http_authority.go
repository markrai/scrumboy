package httpapi

import (
	"net"
	"strconv"
	"strings"
	"unicode"
)

// parseHTTPAuthority validates an HTTP authority consisting of a hostname and
// an optional port. It deliberately does not rely on net/url's permissive
// authority parsing: callers need to distinguish no port from an explicit
// empty port and reject URL components smuggled into Host-like inputs.
//
// The returned authority is the validated input unchanged. The returned
// hostname omits IPv6 brackets so callers can perform address checks without
// parsing the authority a second time.
func parseHTTPAuthority(raw string) (authority, hostname string, ok bool) {
	if raw == "" {
		return "", "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", "", false
		}
	}
	// Percent escapes are intentionally rejected. Accepting them here would
	// make different callers disagree about whether an encoded delimiter is
	// part of the hostname or another authority/URL component.
	if strings.ContainsAny(raw, `,/?#@%\`) {
		return "", "", false
	}

	if raw[0] == '[' {
		closeBracket := strings.IndexByte(raw, ']')
		if closeBracket <= 1 {
			return "", "", false
		}
		hostname = raw[1:closeBracket]
		// Brackets are reserved for IPv6 literals. IPv4 and registered names
		// must use their ordinary unbracketed form.
		if ip := net.ParseIP(hostname); ip == nil || !strings.Contains(hostname, ":") {
			return "", "", false
		}
		remainder := raw[closeBracket+1:]
		if remainder == "" {
			return raw, hostname, true
		}
		if remainder[0] != ':' || !validHTTPAuthorityPort(remainder[1:]) {
			return "", "", false
		}
		return raw, hostname, true
	}

	if strings.ContainsAny(raw, "[]") || strings.Count(raw, ":") > 1 {
		return "", "", false
	}
	hostname = raw
	if colon := strings.LastIndexByte(raw, ':'); colon >= 0 {
		hostname = raw[:colon]
		if !validHTTPAuthorityPort(raw[colon+1:]) {
			return "", "", false
		}
	}
	if hostname == "" || !validHTTPRegName(hostname) {
		return "", "", false
	}
	return raw, hostname, true
}

func validHTTPAuthorityPort(raw string) bool {
	if raw == "" {
		return false
	}
	for i := range raw {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	port, err := strconv.Atoi(raw)
	return err == nil && port >= 1 && port <= 65535
}

// validHTTPRegName accepts the ASCII registered-name characters permitted in
// an authority, except percent escapes and comma, which parseHTTPAuthority
// rejects above. IP literals without colons (including IPv4) are covered by
// the same character set.
func validHTTPRegName(raw string) bool {
	for i := range raw {
		c := raw[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '-', '.', '_', '~', '!', '$', '&', '\'', '(', ')', '*', '+', ';', '=':
			continue
		default:
			return false
		}
	}
	return true
}
