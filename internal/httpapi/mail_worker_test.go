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
