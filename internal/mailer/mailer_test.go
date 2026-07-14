package mailer

import (
	"bytes"
	"log"
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
	if err := s.Send(Message{To: "eve@example.com", Subject: "Hi", Body: "body"}); err == nil {
		t.Fatal("expected dial error, got nil")
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
			}
		})
	}
	if len(srv.Messages()) != 0 {
		t.Fatalf("expected no message delivered")
	}
}
