// Package mailer sends plain-text email over SMTP using only the Go
// standard library (net/smtp + crypto/tls). It has no knowledge of
// Scrumboy's HTTP layer, config format, or retry policy — those live in
// internal/config and internal/httpapi respectively.
package mailer

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
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

	// Debug, if true, logs each send attempt's connection details (host,
	// port, TLS mode, whether auth is used) to Logger. Never logs
	// credentials, the message body, or the recipient address.
	Debug  bool
	Logger *log.Logger

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

// New returns a Sender for cfg. A zero Timeout defaults to 10s. Empty or
// unrecognized TLSMode values are normalized to "starttls" so a typo cannot
// silently skip TLS and use plaintext (unlike a bare switch default of "none").
func New(cfg Config) *Sender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	cfg.TLSMode = normalizeTLSMode(cfg.TLSMode)
	return &Sender{cfg: cfg}
}

// normalizeTLSMode returns a canonical TLS mode. Empty and unrecognized
// values become "starttls" (safe default matching FromEnv).
func normalizeTLSMode(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "implicit", "none", "starttls":
		return v
	default:
		return "starttls"
	}
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
	fromHeader, fromAddr, err := parseFrom(s.cfg.From)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))

	if s.cfg.Debug && s.cfg.Logger != nil {
		s.cfg.Logger.Printf("smtp: send attempt addr=%s tls_mode=%s auth=%v",
			addr, s.cfg.TLSMode, strings.TrimSpace(s.cfg.Username) != "")
	}

	var client *smtp.Client
	client, err = s.dial(addr)
	if err != nil {
		return err
	}
	defer client.Close()

	if s.cfg.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if tlsErr := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, RootCAs: s.cfg.rootCAs}); tlsErr != nil {
				return fmt.Errorf("smtp: starttls: %w", tlsErr)
			}
		} else {
			return permanent(fmt.Errorf("smtp: server does not support STARTTLS (required by SCRUMBOY_SMTP_TLS_MODE=starttls)"))
		}
	}

	if strings.TrimSpace(s.cfg.Username) != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if authErr := client.Auth(auth); authErr != nil {
			return fmt.Errorf("smtp: auth: %w", authErr)
		}
	}

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
	msg := buildMessage(fromHeader, m.To, m.Subject, m.Body)
	if _, err := wc.Write([]byte(msg)); err != nil {
		wc.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp: close data: %w", err)
	}

	// The server has already accepted the message (final 250 after DATA).
	// Treat QUIT as best-effort so a failed/timed-out quit cannot cause the
	// delivery worker to retry an already-accepted message (duplicate email).
	_ = client.Quit()
	return nil
}

// dial opens an SMTP client using one absolute deadline for both connection
// establishment and the subsequent SMTP dialogue (greeting through QUIT).
func (s *Sender) dial(addr string) (*smtp.Client, error) {
	deadline := time.Now().Add(s.cfg.Timeout)
	dialer := &net.Dialer{Deadline: deadline}

	var conn net.Conn
	var err error
	switch s.cfg.TLSMode {
	case "implicit":
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: s.cfg.Host,
			RootCAs:    s.cfg.rootCAs,
		})
		if err != nil {
			return nil, fmt.Errorf("smtp: implicit tls dial: %w", err)
		}
	case "starttls", "none":
		conn, err = dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("smtp: dial: %w", err)
		}
	default:
		return nil, permanent(fmt.Errorf("smtp: invalid TLS mode %q", s.cfg.TLSMode))
	}

	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp: set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp: new client: %w", err)
	}
	return client, nil
}

// validateHeaderValue rejects CR/LF in values that end up in RFC 5322
// headers, as cheap defense-in-depth against header injection.
func validateHeaderValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return permanent(fmt.Errorf("smtp: %s must not contain CR/LF", field))
	}
	return nil
}

// ValidateFrom checks that from is a usable RFC 5322 From value (trim,
// nonempty, no CR/LF, mail.ParseAddress). Use for readiness gates that must
// match send-time validation.
func ValidateFrom(from string) error {
	_, _, err := parseFrom(from)
	return err
}

// parseFrom validates the configured From value (CR/LF reject + RFC 5322
// parse) and returns the header form to write and the bare envelope address
// for MAIL FROM.
func parseFrom(from string) (header, envelope string, err error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", "", permanent(fmt.Errorf("smtp: From is empty"))
	}
	if err := validateHeaderValue("From", from); err != nil {
		return "", "", err
	}
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return "", "", permanent(fmt.Errorf("smtp: From: %w", err))
	}
	return from, addr.Address, nil
}

type permanentError struct {
	err error
}

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent reports whether err is a non-retryable SMTP failure: explicitly
// marked local validation/config errors or an SMTP 5xx reply.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var marked *permanentError
	if errors.As(err, &marked) {
		return true
	}
	var smtpErr *textproto.Error
	return errors.As(err, &smtpErr) &&
		smtpErr.Code >= 500 && smtpErr.Code <= 599
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
