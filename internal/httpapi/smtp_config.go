package httpapi

import (
	"strings"

	"scrumboy/internal/mailer"
)

// SMTPConfigured reports whether SMTP settings are statically valid enough
// to send email: nonempty host, TCP port in 1–65535, and a parseable From
// (RFC 5322, no CR/LF). Username/Password are optional (some relays/local
// catchers allow anonymous submission). Partial or invalid config is treated
// as NOT configured, same convention as PushConfigured.
func SMTPConfigured(host string, port int, from string) bool {
	return strings.TrimSpace(host) != "" &&
		port >= 1 && port <= 65535 &&
		mailer.ValidateFrom(from) == nil
}

// SMTPPartiallyConfigured reports whether some but not all of the required
// fields are set (or From fails validation) — used only for the startup log
// line, to warn operators of a likely typo rather than silently doing
// nothing. The default port alone (portExplicit=false) does not count as
// operator intent.
func SMTPPartiallyConfigured(host string, port int, from string, portExplicit bool) bool {
	anySet := strings.TrimSpace(host) != "" ||
		strings.TrimSpace(from) != "" ||
		portExplicit
	return anySet && !SMTPConfigured(host, port, from)
}

func (s *Server) selfServicePasswordResetEnabled() bool {
	return s.smtpConfigured &&
		len(s.encryptionKey) > 0 &&
		s.publicBaseURL != ""
}
