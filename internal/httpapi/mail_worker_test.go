package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log"
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
	sent      []mailer.Message
}

func (f *fakeMailSender) Send(m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.alwaysErr {
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
	w.deliver(context.Background(), mailDelivery{To: "a@example.com", LogRef: "test"})
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

	w.deliver(context.Background(), mailDelivery{To: "a@example.com", LogRef: "always-fails"})

	if sender.callCount() != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", sender.callCount())
	}
	if !bytes.Contains(buf.Bytes(), []byte("mail delivery failed after 3 attempts: always-fails")) {
		t.Fatalf("expected failure log line, got: %s", buf.String())
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
	q.Enqueue(mailDelivery{To: "pending@example.com", LogRef: "pending"})

	mailCtx, mailCancel := context.WithCancel(context.Background())
	go w.Run(mailCtx)

	srv := &Server{
		logger:     discardLogger(),
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
