package httpapi

import "strings"

// SMTPConfigured reports whether enough SMTP settings are present to send
// email. Host, From, and a valid TCP port (1–65535) are required;
// Username/Password are optional (some relays/local catchers allow anonymous
// submission). Partial config is treated as NOT configured, same convention
// as PushConfigured.
func SMTPConfigured(host string, port int, from string) bool {
	return strings.TrimSpace(host) != "" &&
		port >= 1 && port <= 65535 &&
		strings.TrimSpace(from) != ""
}

// SMTPPartiallyConfigured reports whether some but not all of the required
// fields are set — used only for the startup log line, to warn operators of
// a likely typo rather than silently doing nothing. The default port alone
// (portExplicit=false) does not count as operator intent.
func SMTPPartiallyConfigured(host string, port int, from string, portExplicit bool) bool {
	anySet := strings.TrimSpace(host) != "" ||
		strings.TrimSpace(from) != "" ||
		portExplicit
	return anySet && !SMTPConfigured(host, port, from)
}
