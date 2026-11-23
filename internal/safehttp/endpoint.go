package safehttp

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

func ValidPublicHTTPSEndpoint(raw string) bool {
	if raw == "" || strings.ContainsAny(raw, "#\\") {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" {
		return false
	}
	host := u.Hostname()
	if host == "" || strings.Contains(host, "%") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, "..") || strings.HasSuffix(u.Host, ":") {
		return false
	}
	normalized := strings.TrimSuffix(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsForbiddenIP(ip) {
			return false
		}
	} else if normalizedDigits := strings.ReplaceAll(host, ".", ""); normalizedDigits != "" && strings.Trim(normalizedDigits, "0123456789") == "" {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}
