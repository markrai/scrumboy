package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/textproto"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/mailer"
)

// fakeMailSender is a hand-rolled stand-in for mailSender (no mocking
// framework is used elsewhere in this repo).
type fakeMailSender struct {
	mu        sync.Mutex
	calls     int
	failUntil int // fail this many calls before succeeding; 0 = always succeed
	alwaysErr bool
	err       error // when alwaysErr and set, returned instead of generic error
	sent      []mailer.Message
}

func (f *fakeMailSender) Send(m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.alwaysErr {
		if f.err != nil {
			return f.err
		}
		return errors.New("permanent failure")
	}
	if f.calls <= f.failUntil {
		return errors.New("transient failure")
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeMailSender) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestMailWorker_RetriesThenSucceeds(t *testing.T) {
	sender := &fakeMailSender{failUntil: 2}
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, sender, discardLogger())

	start := time.Now()
	w.deliver(mailDelivery{To: "a@example.com", LogRef: "test"})
	elapsed := time.Since(start)

	if sender.callCount() != 3 {
		t.Fatalf("expected 3 attempts, got %d", sender.callCount())
	}
	// Backoff is [0, 100ms, 400ms]; a successful 3rd attempt waits through
	// both non-zero delays.
	if elapsed < 480*time.Millisecond {
		t.Fatalf("expected backoff delay >= 480ms, got %v", elapsed)
	}
}

func TestMailWorker_AlwaysFails_LogsAfterThreeAttempts(t *testing.T) {
	sender := &fakeMailSender{alwaysErr: true}
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	q := newMailQueue(logger)
	w := newMailWorker(q, sender, logger)

	w.deliver(mailDelivery{To: "a@example.com", LogRef: "always-fails"})

	if sender.callCount() != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", sender.callCount())
	}
	if !bytes.Contains(buf.Bytes(), []byte("mail delivery failed after 3 attempts: always-fails")) {
		t.Fatalf("expected failure log line, got: %s", buf.String())
	}
}

func TestMailWorker_PermanentSMTPError_SingleAttempt(t *testing.T) {
	sender := &fakeMailSender{
		alwaysErr: true,
		err:       &textproto.Error{Code: 550, Msg: "no such user"},
	}
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	q := newMailQueue(logger)
	w := newMailWorker(q, sender, logger)

	w.deliver(mailDelivery{To: "bad@example.com", LogRef: "perm-rcpt"})

	if sender.callCount() != 1 {
		t.Fatalf("expected exactly 1 attempt for permanent SMTP error, got %d", sender.callCount())
	}
	if !bytes.Contains(buf.Bytes(), []byte("mail delivery permanently failed after 1 attempt: perm-rcpt")) {
		t.Fatalf("expected permanent failure log line, got: %s", buf.String())
	}
}

func TestMailWorker_GracefulShutdownFlushesPending(t *testing.T) {
	sender := &fakeMailSender{}
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, sender, discardLogger())

	q.Enqueue(mailDelivery{To: "pending@example.com", LogRef: "pending"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if sender.callCount() < 1 {
		t.Fatal("expected pending item to be delivered on graceful shutdown")
	}
}

func TestServerClose_WaitsForMailFlush(t *testing.T) {
	sender := &fakeMailSender{}
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, sender, discardLogger())

	mailCtx, mailCancel := context.WithCancel(context.Background())
	go w.Run(mailCtx)

	q.Enqueue(mailDelivery{To: "pending@example.com", LogRef: "pending"})

	srv := &Server{
		logger:     discardLogger(),
		mailQueue:  q,
		mailWorker: w,
		mailCancel: mailCancel,
		mailDone:   w.Done(),
	}

	done := make(chan struct{})
	go func() {
		srv.Close(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after mail worker flushed")
	}

	if sender.callCount() < 1 {
		t.Fatal("expected Close to wait until the pending mail was delivered")
	}
}

// TestServerClose_PreservesMailRetriesDuringActiveFlush starts the worker,
// blocks the first send, then Close while that attempt is in flight. After
// releasing a transient failure, attempts 2 and 3 must still run under the
// open Close deadline.
func TestServerClose_PreservesMailRetriesDuringActiveFlush(t *testing.T) {
	gate := newGatedMailSender(2)
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, gate, discardLogger())

	mailCtx, mailCancel := context.WithCancel(context.Background())
	go w.Run(mailCtx)

	q.Enqueue(mailDelivery{To: "retry@example.com", LogRef: "retry-on-close"})

	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("first mail attempt did not start")
	}

	srv := &Server{
		logger:     discardLogger(),
		mailQueue:  q,
		mailWorker: w,
		mailCancel: mailCancel,
		mailDone:   w.Done(),
	}

	closeDone := make(chan struct{})
	go func() {
		srv.Close(context.Background())
		close(closeDone)
	}()

	waitQueueSealed(t, q)
	gate.release <- errors.New("transient")

	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after mail retries completed")
	}

	if gate.callCount() != 3 {
		t.Fatalf("expected 3 send attempts under Close drain, got %d", gate.callCount())
	}
}

// TestServerClose_MailDeadlineExpiresDuringBackoff verifies that once the
// Close deadline fires after attempt one, no further attempt begins.
func TestServerClose_MailDeadlineExpiresDuringBackoff(t *testing.T) {
	gate := newGatedMailSender(99)
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, gate, discardLogger())

	mailCtx, mailCancel := context.WithCancel(context.Background())
	go w.Run(mailCtx)

	q.Enqueue(mailDelivery{To: "retry@example.com", LogRef: "deadline-backoff"})
	q.Enqueue(mailDelivery{To: "second@example.com", LogRef: "should-not-start"})

	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("first mail attempt did not start")
	}

	srv := &Server{
		logger:     discardLogger(),
		mailQueue:  q,
		mailWorker: w,
		mailCancel: mailCancel,
		mailDone:   w.Done(),
	}

	closeCtx, closeCancel := context.WithCancel(context.Background())
	closeDone := make(chan struct{})
	go func() {
		srv.Close(closeCtx)
		close(closeDone)
	}()

	waitQueueSealed(t, q)
	gate.release <- errors.New("transient")
	closeCancel()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after deadline")
	}

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("mail worker did not finish after deadline")
	}

	if got := gate.callCount(); got != 1 {
		t.Fatalf("expected only the in-progress first attempt, got %d", got)
	}
}

func TestServerClose_ReturnsAtDeadlineIfMailFlushHangs(t *testing.T) {
	blockingDone := make(chan struct{}) // never closed: simulates a flush that hasn't finished
	_, mailCancel := context.WithCancel(context.Background())

	srv := &Server{
		logger:     discardLogger(),
		mailCancel: mailCancel,
		mailDone:   blockingDone,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	srv.Close(ctx)
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("Close should have returned at the context deadline, took %v", elapsed)
	}
}

// gatedMailSender blocks the first Send until release receives an error to
// return, then fails until failUntil calls have been made.
type gatedMailSender struct {
	mu        sync.Mutex
	calls     int
	failUntil int
	started   chan struct{}
	release   chan error
}

func newGatedMailSender(failUntil int) *gatedMailSender {
	return &gatedMailSender{
		failUntil: failUntil,
		started:   make(chan struct{}),
		release:   make(chan error),
	}
}

func (g *gatedMailSender) Send(m mailer.Message) error {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()

	if n == 1 {
		close(g.started)
		return <-g.release
	}
	if n <= g.failUntil {
		return errors.New("transient failure")
	}
	return nil
}

func (g *gatedMailSender) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func TestMailWorker_EmptyQueue_RunExitsCleanlyOnCancel(t *testing.T) {
	sender := &fakeMailSender{}
	q := newMailQueue(discardLogger())
	w := newMailWorker(q, sender, discardLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if sender.callCount() != 0 {
		t.Fatalf("expected no send attempts on empty queue, got %d", sender.callCount())
	}
}
