package mailer

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/mailer/mailertest"
)

func TestSend_STARTTLS_NoAuth(t *testing.T) {
	cert, err := mailertest.GenerateSelfSignedCert("127.0.0.1")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	pool, err := mailertest.CertPool(cert)
	if err != nil {
		t.Fatalf("cert pool: %v", err)
	}
	srv, err := mailertest.Start(mailertest.Options{OfferSTARTTLS: true, TLSCert: &cert})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "Scrumboy <no-reply@example.com>", TLSMode: "starttls", rootCAs: pool, Timeout: 3 * time.Second})

	if err := s.Send(Message{To: "alice@example.com", Subject: "Reset your password", Body: "link here"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.From != "no-reply@example.com" {
		t.Fatalf("From: got %q", m.From)
	}
	if m.To != "alice@example.com" {
		t.Fatalf("To: got %q", m.To)
	}
	if m.Subject != "Reset your password" {
		t.Fatalf("Subject: got %q", m.Subject)
	}
	if !strings.Contains(m.Body, "link here") {
		t.Fatalf("Body: got %q", m.Body)
	}
	if srv.AuthAttempts() != 0 {
		t.Fatalf("expected no auth attempts, got %d", srv.AuthAttempts())
	}
}

func TestSend_STARTTLS_WithAuth(t *testing.T) {
	cert, err := mailertest.GenerateSelfSignedCert("127.0.0.1")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	pool, err := mailertest.CertPool(cert)
	if err != nil {
		t.Fatalf("cert pool: %v", err)
	}
	srv, err := mailertest.Start(mailertest.Options{
		OfferSTARTTLS: true, TLSCert: &cert,
		RequireAuth: true, Username: "smtpuser", Password: "s3cret",
	})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{
		Host: host, Port: port, Username: "smtpuser", Password: "s3cret",
		From: "no-reply@example.com", TLSMode: "starttls", rootCAs: pool, Timeout: 3 * time.Second,
	})

	if err := s.Send(Message{To: "bob@example.com", Subject: "Hi", Body: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if srv.AuthAttempts() != 1 {
		t.Fatalf("expected 1 auth attempt, got %d", srv.AuthAttempts())
	}
	if len(srv.Messages()) != 1 {
		t.Fatalf("expected 1 delivered message")
	}
}

func TestSend_AuthFailure(t *testing.T) {
	cert, err := mailertest.GenerateSelfSignedCert("127.0.0.1")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	pool, err := mailertest.CertPool(cert)
	if err != nil {
		t.Fatalf("cert pool: %v", err)
	}
	srv, err := mailertest.Start(mailertest.Options{
		OfferSTARTTLS: true, TLSCert: &cert,
		RequireAuth: true, Username: "smtpuser", Password: "correct",
	})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{
		Host: host, Port: port, Username: "smtpuser", Password: "wrong",
		From: "no-reply@example.com", TLSMode: "starttls", rootCAs: pool, Timeout: 3 * time.Second,
	})

	err = s.Send(Message{To: "bob@example.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsPermanent(err) {
		t.Fatalf("expected permanent auth failure, got: %v", err)
	}
	if !strings.Contains(err.Error(), "auth") {
		t.Fatalf("expected auth error, got: %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered on auth failure")
	}
}

func TestSend_ImplicitTLS(t *testing.T) {
	cert, err := mailertest.GenerateSelfSignedCert("127.0.0.1")
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}
	pool, err := mailertest.CertPool(cert)
	if err != nil {
		t.Fatalf("cert pool: %v", err)
	}
	srv, err := mailertest.Start(mailertest.Options{ImplicitTLS: true, TLSCert: &cert})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "implicit", rootCAs: pool, Timeout: 3 * time.Second})

	if err := s.Send(Message{To: "carol@example.com", Subject: "Hi", Body: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(srv.Messages()) != 1 {
		t.Fatalf("expected 1 message")
	}
}

func TestSend_NoneMode_Plaintext(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none", Timeout: 3 * time.Second})

	if err := s.Send(Message{To: "dave@example.com", Subject: "Hi", Body: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(srv.Messages()) != 1 {
		t.Fatalf("expected 1 message")
	}
}

func TestSend_Debug_LogsAttemptWithoutCredentialsBodyOrRecipient(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{RequireAuth: true, Username: "svcuser", Password: "hunter2"})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	s := New(Config{
		Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none",
		Username: "svcuser", Password: "hunter2", Timeout: 3 * time.Second,
		Debug: true, Logger: logger,
	})

	if err := s.Send(Message{To: "dave@example.com", Subject: "Hi", Body: "super secret body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "smtp: send attempt") {
		t.Fatalf("expected a debug log line for the send attempt, got: %s", out)
	}
	if !strings.Contains(out, "auth=true") {
		t.Fatalf("expected the log to note auth is in use, got: %s", out)
	}
	for _, secret := range []string{"hunter2", "dave@example.com", "super secret body"} {
		if strings.Contains(out, secret) {
			t.Fatalf("debug log must never contain credentials, recipient, or body; found %q in: %s", secret, out)
		}
	}
}

func TestSend_Debug_Disabled_LogsNothing(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none", Timeout: 3 * time.Second, Logger: logger})

	if err := s.Send(Message{To: "dave@example.com", Subject: "Hi", Body: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no log output when Debug is false, got: %s", buf.String())
	}
}

func TestSend_DialFailure(t *testing.T) {
	// Start and immediately close to get a port nothing is listening on.
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	host, port := srv.HostPort()
	srv.Close()

	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none", Timeout: 1 * time.Second})
	err = s.Send(Message{To: "eve@example.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("expected dial error, got nil")
	}
	if IsPermanent(err) {
		t.Fatalf("expected transient dial failure, got permanent: %v", err)
	}
}

func TestSend_RCPTRejected(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{RejectRCPT: true})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none", Timeout: 3 * time.Second})

	err = s.Send(Message{To: "frank@example.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsPermanent(err) {
		t.Fatalf("expected permanent RCPT failure, got: %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered")
	}
}

func TestSend_STARTTLSRequiredButUnsupported(t *testing.T) {
	// Server never advertises STARTTLS; Sender configured for "starttls" must
	// fail closed rather than silently falling back to plaintext.
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "starttls", Timeout: 3 * time.Second})

	err = s.Send(Message{To: "grace@example.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsPermanent(err) {
		t.Fatalf("expected permanent STARTTLS misconfig, got: %v", err)
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS-not-supported error, got: %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered")
	}
}

func TestNew_NormalizesTLSMode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "starttls"},
		{"  ", "starttls"},
		{"STARTTLS", "starttls"},
		{"bogus", "starttls"},
		{"none", "none"},
		{"IMPLICIT", "implicit"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.in), func(t *testing.T) {
			s := New(Config{TLSMode: tc.in, Timeout: time.Second})
			if s.cfg.TLSMode != tc.want {
				t.Fatalf("TLSMode = %q, want %q", s.cfg.TLSMode, tc.want)
			}
		})
	}
}

// TestSend_UnknownTLSMode_DoesNotUsePlaintext ensures a typo at the mailer
// boundary normalizes to starttls (and thus fails closed against a plaintext-
// only relay) rather than skipping STARTTLS like TLSMode "none".
func TestSend_UnknownTLSMode_DoesNotUsePlaintext(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "typo-mode", Timeout: 3 * time.Second})

	err = s.Send(Message{To: "grace@example.com", Subject: "Hi", Body: "body"})
	if err == nil {
		t.Fatal("expected STARTTLS failure for unknown TLSMode, got success (would mean plaintext downgrade)")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS-not-supported error, got: %v", err)
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered")
	}
}

func TestSend_HeaderInjectionRejected(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{Host: host, Port: port, From: "no-reply@example.com", TLSMode: "none", Timeout: 3 * time.Second})

	cases := []struct {
		name string
		msg  Message
	}{
		{"CRLFInSubject", Message{To: "h@example.com", Subject: "Hi\r\nBcc: evil@example.com", Body: "body"}},
		{"CRLFInTo", Message{To: "h@example.com\r\nBcc: evil@example.com", Subject: "Hi", Body: "body"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.Send(tc.msg); err == nil {
				t.Fatal("expected error, got nil")
			} else if !IsPermanent(err) {
				t.Fatalf("expected permanent header injection error, got: %v", err)
			}
		})
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered")
	}
}

func TestSend_InvalidFromRejected(t *testing.T) {
	cases := []struct {
		name string
		from string
	}{
		{"CRLF", "no-reply@example.com\r\nBcc: evil@example.com"},
		{"malformed", "not-an-address"},
		{"empty", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Config{
				Host: "127.0.0.1", Port: 1, From: tc.from,
				TLSMode: "none", Timeout: 1 * time.Second,
			})
			err := s.Send(Message{To: "alice@example.com", Subject: "Hi", Body: "body"})
			if err == nil {
				t.Fatal("expected From validation error, got nil")
			}
			if !IsPermanent(err) {
				t.Fatalf("expected permanent From validation error, got: %v", err)
			}
			if !strings.Contains(err.Error(), "From") && !strings.Contains(err.Error(), "from") {
				// parseFrom errors are "smtp: From ..." or "smtp: From is empty"
				t.Fatalf("expected From-related error, got %v", err)
			}
		})
	}
}

func TestParseFrom(t *testing.T) {
	header, envelope, err := parseFrom("Scrumboy <no-reply@example.com>")
	if err != nil {
		t.Fatalf("parseFrom: %v", err)
	}
	if header != "Scrumboy <no-reply@example.com>" {
		t.Fatalf("header = %q", header)
	}
	if envelope != "no-reply@example.com" {
		t.Fatalf("envelope = %q", envelope)
	}
}

func TestValidateFrom(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		wantErr bool
	}{
		{name: "display name", from: "Scrumboy <no-reply@example.com>", wantErr: false},
		{name: "bare address", from: "no-reply@example.com", wantErr: false},
		{name: "empty", from: "   ", wantErr: true},
		{name: "malformed", from: "not-an-address", wantErr: true},
		{name: "CRLF", from: "no-reply@example.com\r\nBcc: evil@example.com", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFrom(tc.from)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !IsPermanent(err) {
					t.Fatalf("expected permanent error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateFrom: %v", err)
			}
		})
	}
}

// TestSend_QUITFailureAfterDATA_StillSucceeds ensures that once the server
// has accepted the message (250 after DATA/wc.Close), a failed QUIT does not
// surface as a Send error — otherwise the delivery worker would retry and
// risk duplicate emails.
func TestSend_QUITFailureAfterDATA_StillSucceeds(t *testing.T) {
	srv, err := mailertest.Start(mailertest.Options{FailQUIT: true})
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	host, port := srv.HostPort()
	s := New(Config{
		Host: host, Port: port, From: "no-reply@example.com",
		TLSMode: "none", Timeout: 3 * time.Second,
	})

	if err := s.Send(Message{To: "alice@example.com", Subject: "Reset", Body: "link"}); err != nil {
		t.Fatalf("Send after successful DATA must ignore QUIT failure, got: %v", err)
	}

	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 accepted message, got %d", len(msgs))
	}
	if msgs[0].To != "alice@example.com" {
		t.Fatalf("To: got %q", msgs[0].To)
	}
}

// TestSend_StallsAfterGreeting_ReturnsWithinTimeout ensures the absolute
// send deadline covers the SMTP dialogue after the initial 220 greeting, not
// just TCP connect / NewClient.
func TestSend_StallsAfterGreeting_ReturnsWithinTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = fmt.Fprintf(conn, "220 stalltest ESMTP\r\n")
		buf := make([]byte, 4096)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	}()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	s := New(Config{
		Host: host, Port: port, From: "no-reply@example.com",
		TLSMode: "none", Timeout: 200 * time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		done <- s.Send(Message{To: "a@example.com", Subject: "Hi", Body: "body"})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if IsPermanent(err) {
			t.Fatalf("expected transient timeout failure, got permanent: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not respect SMTP timeout")
	}
}

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"marked", permanent(fmt.Errorf("local")), true},
		{"wrappedMarked", fmt.Errorf("wrap: %w", permanent(fmt.Errorf("local"))), true},
		{"smtp550", &textproto.Error{Code: 550, Msg: "no such user"}, true},
		{"wrapped550", fmt.Errorf("rcpt: %w", &textproto.Error{Code: 550, Msg: "no"}), true},
		{"smtp535", &textproto.Error{Code: 535, Msg: "auth failed"}, true},
		{"smtp451", &textproto.Error{Code: 451, Msg: "try again"}, false},
		{"generic", fmt.Errorf("fail"), false},
		{"timeout", &net.OpError{Op: "dial", Err: fmt.Errorf("timeout")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanent(tc.err); got != tc.want {
				t.Fatalf("IsPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
