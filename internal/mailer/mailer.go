// Package mailer sends plain-text email over SMTP using only the Go
// standard library (net/smtp + crypto/tls). It has no knowledge of
// Scrumboy's HTTP layer, config format, or retry policy — those live in
// internal/config and internal/httpapi respectively.
package mailer

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Config holds validated SMTP connection settings.
type Config struct {
	Host     string
	Port     int
	Username string // optional
	Password string // optional
	From     string // envelope + header From, e.g. "Scrumboy <no-reply@example.com>"
	TLSMode  string // "starttls" (default) | "implicit" | "none"
	Timeout  time.Duration

	// rootCAs overrides the trust store used for TLS verification. Always
	// nil (system pool) in production; only set directly by white-box tests
	// in this package against a self-signed test listener. Deliberately
	// unexported — never weaken TLS verification for real SMTP relays.
	rootCAs *x509.CertPool
}

// Message is a plain-text email to a single recipient. Password-reset email
// is always single-recipient; there is no need for multi-To/Cc/Bcc here.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender sends Messages over SMTP.
type Sender struct {
	cfg Config
}

// New returns a Sender for cfg. A zero Timeout defaults to 10s.
func New(cfg Config) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Sender{cfg: cfg}
}

// Send connects, optionally negotiates TLS, authenticates if credentials are
// set, and delivers m. It is synchronous and blocking; callers wanting async
// delivery must run it off the calling goroutine.
func (s *Sender) Send(m Message) error {
	if err := validateHeaderValue("To", m.To); err != nil {
		return err
	}
	if err := validateHeaderValue("Subject", m.Subject); err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))

	var (
		client *smtp.Client
		err    error
	)
	switch s.cfg.TLSMode {
	case "implicit":
		conn, dialErr := tls.DialWithDialer(&net.Dialer{Timeout: s.cfg.Timeout}, "tcp", addr, &tls.Config{ServerName: s.cfg.Host, RootCAs: s.cfg.rootCAs})
		if dialErr != nil {
			return fmt.Errorf("smtp: implicit tls dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, s.cfg.Host)
	default: // "starttls" or "none"
		conn, dialErr := net.DialTimeout("tcp", addr, s.cfg.Timeout)
		if dialErr != nil {
			return fmt.Errorf("smtp: dial: %w", dialErr)
		}
		client, err = smtp.NewClient(conn, s.cfg.Host)
	}
	if err != nil {
		return fmt.Errorf("smtp: new client: %w", err)
	}
	defer client.Close()

	if s.cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if tlsErr := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, RootCAs: s.cfg.rootCAs}); tlsErr != nil {
				return fmt.Errorf("smtp: starttls: %w", tlsErr)
			}
		} else {
			return fmt.Errorf("smtp: server does not support STARTTLS (required by SCRUMBOY_SMTP_TLS_MODE=starttls)")
		}
	}

	if strings.TrimSpace(s.cfg.Username) != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if authErr := client.Auth(auth); authErr != nil {
			return fmt.Errorf("smtp: auth: %w", authErr)
		}
	}

	fromAddr := extractAddr(s.cfg.From)
	if err := client.Mail(fromAddr); err != nil {
		return fmt.Errorf("smtp: mail from: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp: rcpt to: %w", err)
	}

	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: data: %w", err)
	}
	msg := buildMessage(s.cfg.From, m.To, m.Subject, m.Body)
	if _, err := wc.Write([]byte(msg)); err != nil {
		wc.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp: close data: %w", err)
	}

	return client.Quit()
}

// validateHeaderValue rejects CR/LF in values that end up in RFC 5322
// headers, as cheap defense-in-depth against header injection.
func validateHeaderValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("smtp: %s must not contain CR/LF", field)
	}
	return nil
}

// extractAddr pulls the bare address out of a "Name <addr>" From header value.
func extractAddr(from string) string {
	from = strings.TrimSpace(from)
	if i := strings.LastIndex(from, "<"); i >= 0 {
		if j := strings.LastIndex(from, ">"); j > i {
			return from[i+1 : j]
		}
	}
	return from
}

// buildMessage assembles a minimal RFC 5322 plain-text message.
func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + subject + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return b.String()
}
