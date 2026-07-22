package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/mailer/mailertest"
	"scrumboy/internal/store"
)

var testEncryptionKey = []byte("0123456789abcdef0123456789abcdef")

func newRequestPasswordResetTestServer(t *testing.T, smtpConfigured bool) (*httptest.Server, *mailertest.Server, func()) {
	return newRequestPasswordResetTestServerWith(t, smtpConfigured, false)
}

func newRequestPasswordResetTestServerWith(t *testing.T, smtpConfigured, trustProxy bool) (*httptest.Server, *mailertest.Server, func()) {
	ts, fake, _, cleanup := newRequestPasswordResetTestServerWithDB(t, smtpConfigured, trustProxy)
	return ts, fake, cleanup
}

func newRequestPasswordResetTestServerWithDB(t *testing.T, smtpConfigured, trustProxy bool) (*httptest.Server, *mailertest.Server, *sql.DB, func()) {
	t.Helper()

	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	host, port := fake.HostPort()

	opts := Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		TrustProxy:     trustProxy,
	}
	if smtpConfigured {
		opts.SMTPHost = host
		opts.SMTPPort = port
		opts.SMTPFrom = "no-reply@example.com"
		// Self-service reset now refuses to send without a configured base
		// URL (see resetBaseURL); set one so tests that expect delivery keep
		// exercising the happy path rather than the fail-closed one.
		opts.PublicBaseURL = "https://scrumboy.example.com"
	}
	ts, database, cleanup := newTestHTTPServerWithOptions(t, opts)
	return ts, fake, database, func() {
		cleanup()
		fake.Close()
	}
}

func waitForMessages(t *testing.T, fake *mailertest.Server, want int) []mailertest.Message {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := fake.Messages(); len(msgs) >= want {
			return msgs
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d message(s), got %d", want, len(fake.Messages()))
	return nil
}

func assertStillNoMessages(t *testing.T, fake *mailertest.Server) {
	t.Helper()
	// Give the async worker a beat to (incorrectly) fire before asserting absence.
	time.Sleep(150 * time.Millisecond)
	if msgs := fake.Messages(); len(msgs) != 0 {
		t.Fatalf("expected no messages, got %+v", msgs)
	}
}

var resetURLRe = regexp.MustCompile(`token=([^\s&]+)`)

func TestRequestPasswordReset_ExistingUser_DeliversEmail(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	client := newCookieClient(t)
	user := bootstrapUserClient(t, client, ts.URL, "Alice", "alice@example.com", "password123")
	userID := int64(user["id"].(float64))

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "alice@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	if out["message"] == nil {
		t.Fatalf("expected generic message field, got %+v", out)
	}

	msgs := waitForMessages(t, fake, 1)
	m := msgs[0]
	if m.To != "alice@example.com" {
		t.Fatalf("To = %q", m.To)
	}
	match := resetURLRe.FindStringSubmatch(m.Body)
	if match == nil {
		t.Fatalf("expected token in email body, got: %s", m.Body)
	}
	token, err := url.QueryUnescape(match[1])
	if err != nil {
		t.Fatalf("unescape token: %v", err)
	}
	gotUserID, _, _, err := tokens.ParsePasswordResetToken(token)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if gotUserID != userID {
		t.Fatalf("token user id = %d, want %d", gotUserID, userID)
	}
}

func TestRequestPasswordReset_QueueRejectionIsLoggedWithGenericResponse(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "reset-queue@example.com", "password123", "Reset User")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	queue := newMailQueueWithCapacityAndKind(log.New(&logs, "", 0), 1, "transactional mail")
	if !queue.Enqueue(mailDelivery{To: "occupied@example.com", LogRef: "occupied"}) {
		t.Fatal("expected queue prefill to succeed")
	}
	srv := &Server{
		store:                  st,
		logger:                 log.New(&logs, "", 0),
		maxBody:                1 << 20,
		mode:                   "full",
		encryptionKey:          testEncryptionKey,
		smtpConfigured:         true,
		publicBaseURL:          "https://scrumboy.example.com",
		transactionalMailQueue: queue,
	}
	request := func(email string) *httptest.ResponseRecorder {
		t.Helper()
		body, marshalErr := json.Marshal(map[string]string{"email": email})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/request-password-reset", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		srv.handleAuthRequestPasswordReset(recorder, req)
		return recorder
	}

	existing := request(user.Email)
	missing := request("missing-reset-queue@example.com")
	if existing.Code != http.StatusOK || missing.Code != http.StatusOK || existing.Body.String() != missing.Body.String() {
		t.Fatalf("queue rejection changed anti-enumeration response: existing=%d %q missing=%d %q", existing.Code, existing.Body.String(), missing.Code, missing.Body.String())
	}
	if !strings.Contains(logs.String(), "transactional mail queue rejected user=") {
		t.Fatalf("expected internal queue rejection log, got %q", logs.String())
	}
}

func TestRequestPasswordReset_NonexistentEmail_IdenticalResponseNoEmail(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "alice2@example.com", "password123")

	var existing, nonexistent map[string]any
	resp1, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "alice2@example.com",
	}, &existing)
	waitForMessages(t, fake, 1) // let the existing-user path finish before comparing

	resp2, _ := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "nobody-here@example.com",
	}, &nonexistent)

	if resp1.StatusCode != resp2.StatusCode {
		t.Fatalf("status codes differ: %d vs %d", resp1.StatusCode, resp2.StatusCode)
	}
	b1, _ := json.Marshal(existing)
	b2, _ := json.Marshal(nonexistent)
	if string(b1) != string(b2) {
		t.Fatalf("expected byte-identical bodies, got %s vs %s", b1, b2)
	}

	if len(fake.Messages()) != 1 {
		t.Fatalf("expected exactly 1 message (from the existing-user request only), got %d", len(fake.Messages()))
	}
}

func TestRequestPasswordReset_UnusableLocalPasswordReturnsGenericResponseNoEmail(t *testing.T) {
	for _, tc := range []struct {
		name      string
		malformed bool
	}{
		{name: "OIDC only"},
		{name: "malformed hash", malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts, fake, database, cleanup := newRequestPasswordResetTestServerWithDB(t, true, false)
			defer cleanup()
			client := newCookieClient(t)
			if tc.malformed {
				u := bootstrapUserClient(t, client, ts.URL, "Malformed", "passwordless@example.com", "password123")
				if _, err := database.ExecContext(context.Background(), `UPDATE users SET password_hash='not-bcrypt' WHERE id=?`, int64(u["id"].(float64))); err != nil {
					t.Fatal(err)
				}
			} else {
				st := store.New(database, &store.StoreOptions{ConfiguredOIDCIssuer: "https://idp.example"})
				if _, err := st.CreateUserOIDC(context.Background(), "https://idp.example", "https://idp.example", "passwordless-subject", "passwordless@example.com", "Passwordless"); err != nil {
					t.Fatal(err)
				}
			}

			var out map[string]any
			resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{"email": "passwordless@example.com"}, &out)
			if resp.StatusCode != http.StatusOK || out["message"] == nil {
				t.Fatalf("generic response status=%d body=%s", resp.StatusCode, string(body))
			}
			assertStillNoMessages(t, fake)
		})
	}
}

func TestRequestPasswordReset_TimingIndistinguishable(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "alice3@example.com", "password123")

	measure := func(email string) time.Duration {
		var out map[string]any
		start := time.Now()
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
			"email": email,
		}, &out)
		elapsed := time.Since(start)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
		}
		return elapsed
	}

	existingElapsed := measure("alice3@example.com")
	waitForMessages(t, fake, 1) // let the existing-user path's async enqueue settle
	nonexistentElapsed := measure("nobody-timing@example.com")

	// Both paths are floored to minPasswordResetRequestDuration, so their
	// wall-clock times should land close together regardless of the extra DB
	// calls and token generation on the existing-user path. Allow generous
	// slack for scheduler jitter in CI.
	diff := existingElapsed - nonexistentElapsed
	if diff < 0 {
		diff = -diff
	}
	if diff > 150*time.Millisecond {
		t.Fatalf("expected response times within 150ms of each other, existing=%v nonexistent=%v diff=%v",
			existingElapsed, nonexistentElapsed, diff)
	}
	if existingElapsed < minPasswordResetRequestDuration || nonexistentElapsed < minPasswordResetRequestDuration {
		t.Fatalf("expected both responses to be floored to at least %v, got existing=%v nonexistent=%v",
			minPasswordResetRequestDuration, existingElapsed, nonexistentElapsed)
	}
}

func TestRequestPasswordReset_SMTPNotConfigured_GenericResponseNoEmail(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, false)
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "alice3@example.com", "password123")

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "alice3@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertStillNoMessages(t, fake)
}

func TestRequestPasswordReset_InvalidSMTPFrom_GenericResponseNoEmail(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "not-an-address",
		PublicBaseURL:  "https://scrumboy.example.com",
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "bad-from@example.com", "password123")

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "bad-from@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertStillNoMessages(t, fake)
}

// TestRequestPasswordReset_InvalidSMTPPort_GenericResponseNoEmail ensures direct
// Options construction with an out-of-range port does not enable SMTP at the
// server boundary.
func TestRequestPasswordReset_InvalidSMTPPort_GenericResponseNoEmail(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		SMTPHost:       "smtp.example.com",
		SMTPPort:       70000,
		SMTPFrom:       "no-reply@example.com",
		PublicBaseURL:  "https://scrumboy.example.com",
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "invalid-port@example.com", "password123")

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "invalid-port@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertStillNoMessages(t, fake)
}

// TestRequestPasswordReset_NoPublicBaseURL_GenericResponseNoEmail guards
// against password-reset-link poisoning: with SMTP configured but
// SCRUMBOY_PUBLIC_BASE_URL unset, this unauthenticated endpoint must not
// build a reset link from the (attacker-controlled) request Host header. It
// fails closed — generic response, no email sent — rather than falling back.
func TestRequestPasswordReset_NoPublicBaseURL_GenericResponseNoEmail(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "no-reply@example.com",
		// PublicBaseURL intentionally left unset.
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "no-base-url@example.com", "password123")

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset",
		strings.NewReader(`{"email":"no-base-url@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "attacker.evil")
	req.Host = "attacker.evil"

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	assertStillNoMessages(t, fake)
}

// TestRequestPasswordReset_InvalidPublicBaseURL_GenericResponseNoEmail ensures
// malformed PublicBaseURL in Options collapses to fail-closed at the server
// boundary (not only when loaded via FromEnv).
func TestRequestPasswordReset_InvalidPublicBaseURL_GenericResponseNoEmail(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		EncryptionKey:  testEncryptionKey,
		SMTPTLSMode:    "none",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "no-reply@example.com",
		PublicBaseURL:  "https://evil.example/phish",
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "invalid-base@example.com", "password123")

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "invalid-base@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertStillNoMessages(t, fake)
}

func TestRequestPasswordReset_EncryptionKeyNotConfigured_GenericResponse(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()
	host, port := fake.HostPort()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFrom:       "no-reply@example.com",
		SMTPTLSMode:    "none",
		// EncryptionKey intentionally left unset.
	})
	defer cleanup()

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Alice", "alice4@example.com", "password123")

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "alice4@example.com",
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(body))
	}
	assertStillNoMessages(t, fake)
}

func TestRequestPasswordReset_AnonymousMode_NotFound(t *testing.T) {
	fake, err := mailertest.Start(mailertest.Options{})
	if err != nil {
		t.Fatalf("start fake smtp server: %v", err)
	}
	defer fake.Close()

	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "anonymous",
		EncryptionKey:  testEncryptionKey,
	})
	defer cleanup()

	resp, _ := doJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/auth/request-password-reset", map[string]any{
		"email": "whoever@example.com",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestRequestPasswordReset_WrongMethod_MethodNotAllowed(t *testing.T) {
	ts, _, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	resp, err := ts.Client().Get(ts.URL + "/api/auth/request-password-reset")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestRequestPasswordReset_MalformedJSON_BadRequest(t *testing.T) {
	ts, _, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset", strings.NewReader("{not json"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRequestPasswordReset_MissingXScrumboyHeaderForbidden(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset", strings.NewReader(`{"email":"nobody@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Deliberately omit X-Scrumboy — same CSRF gate as login.
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without X-Scrumboy, got %d", resp.StatusCode)
	}
	assertStillNoMessages(t, fake)
}

func TestRequestPasswordReset_NonJSONContentTypeRejected(t *testing.T) {
	ts, fake, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset", strings.NewReader(`{"email":"nobody@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-JSON Content-Type, got %d", resp.StatusCode)
	}
	assertStillNoMessages(t, fake)
}

func TestRequestPasswordReset_JSONContentTypeWithCharsetAllowed(t *testing.T) {
	ts, _, cleanup := newRequestPasswordResetTestServer(t, true)
	defer cleanup()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/request-password-reset", strings.NewReader(`{"email":"nobody@example.com"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with charset=utf-8 Content-Type, got %d", resp.StatusCode)
	}
}

// doJSONWithIP is like doJSON but sets X-Forwarded-For so the dual-key
// (IP + email) rate limiter sees a distinct IP per call.
func doJSONWithIP(t *testing.T, client *http.Client, url, ip string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode json: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("X-Forwarded-For", ip)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestRequestPasswordReset_RateLimited(t *testing.T) {
	// TrustProxy so doJSONWithIP can distinguish clients via X-Forwarded-For
	// under httptest (RemoteAddr is always the test server loopback).
	ts, _, cleanup := newRequestPasswordResetTestServerWith(t, true, true)
	defer cleanup()

	client := ts.Client()
	var lastStatus int
	for i := 0; i < 6; i++ {
		resp := doJSONWithIP(t, client, ts.URL+"/api/auth/request-password-reset", "203.0.113.10", map[string]any{
			"email": "ratelimited@example.com",
		})
		lastStatus = resp.StatusCode
		resp.Body.Close()
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th attempt from same IP+email, got %d", lastStatus)
	}

	// Different IP AND different email must be unaffected.
	resp := doJSONWithIP(t, client, ts.URL+"/api/auth/request-password-reset", "203.0.113.20", map[string]any{
		"email": "different-email@example.com",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected different IP+email to be unaffected by rate limit, got %d", resp.StatusCode)
	}

	// Same IP as the exhausted one, but a fresh email: still blocked, because
	// the IP-side key alone is already over its limit (dual-key AND semantics).
	resp2 := doJSONWithIP(t, client, ts.URL+"/api/auth/request-password-reset", "203.0.113.10", map[string]any{
		"email": "yet-another-email@example.com",
	})
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected exhausted IP to still be blocked regardless of email, got %d", resp2.StatusCode)
	}
}

func TestRequestPasswordReset_SpoofedXFFIgnoredWithoutTrustProxy(t *testing.T) {
	ts, _, cleanup := newRequestPasswordResetTestServerWith(t, true, false)
	defer cleanup()

	client := ts.Client()
	var lastStatus int
	for i := 0; i < 6; i++ {
		// Rotate XFF each attempt; without TrustProxy all share RemoteAddr.
		ip := fmt.Sprintf("198.51.100.%d", i+1)
		resp := doJSONWithIP(t, client, ts.URL+"/api/auth/request-password-reset", ip, map[string]any{
			"email": fmt.Sprintf("spoof-%d@example.com", i),
		})
		lastStatus = resp.StatusCode
		resp.Body.Close()
	}
	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when rotating XFF without TrustProxy (shared RemoteAddr), got %d", lastStatus)
	}
}
