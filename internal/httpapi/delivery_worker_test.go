package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testDelivery struct {
	LogRef string
}

func (d testDelivery) logRef() string { return d.LogRef }

// TestRetryWorker_ShutdownDrain_PreservesRetries verifies that cancelling the
// Run accept-loop context does not cut retries while the Close deadline is open.
func TestRetryWorker_ShutdownDrain_PreservesRetries(t *testing.T) {
	var sendCalls atomic.Int32
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	queue.Enqueue(testDelivery{LogRef: "one"})
	queue.Enqueue(testDelivery{LogRef: "two"})

	runCtx, cancel := context.WithCancel(context.Background())
	w.beginShutdown(context.Background()) // open deadline
	cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(runCtx)
		close(runDone)
	}()

	select {
	case <-w.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("worker Done did not close after shutdown drain")
	}
	if got := sendCalls.Load(); got != 6 {
		t.Fatalf("expected 6 send calls (3 retries × 2 items), got %d", got)
	}

	select {
	case <-runDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after shutdown drain")
	}
}

// TestRetryWorker_ShutdownDrain_ExpiredBeforeDrain_NoAttempts verifies that an
// already-expired Close deadline starts no send attempts for queued items.
func TestRetryWorker_ShutdownDrain_ExpiredBeforeDrain_NoAttempts(t *testing.T) {
	var sendCalls atomic.Int32
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	queue.Enqueue(testDelivery{LogRef: "one"})
	queue.Enqueue(testDelivery{LogRef: "two"})

	runCtx, cancel := context.WithCancel(context.Background())
	drainCtx, drainCancel := context.WithCancel(context.Background())
	drainCancel()
	w.beginShutdown(drainCtx)
	if w.retryCtx.Err() == nil {
		t.Fatal("retryCtx remained active after beginShutdown with expired context")
	}
	cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(runCtx)
		close(runDone)
	}()

	select {
	case <-w.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker Done did not close promptly after expired drain ctx")
	}
	if got := sendCalls.Load(); got != 0 {
		t.Fatalf("expected 0 send calls when drain ctx expired before flush, got %d", got)
	}

	select {
	case <-runDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after expired drain flush")
	}
}

func TestRetryWorker_PermanentClassifier_SingleAttempt(t *testing.T) {
	var sendCalls atomic.Int32
	var buf strings.Builder
	logger := log.New(&buf, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("permanent")
	}
	w := newRetryWorker(queue, logger, "test", send)
	w.isPermanent = func(err error) bool { return true }

	w.deliver(testDelivery{LogRef: "perm"})

	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 send call, got %d", got)
	}
	out := buf.String()
	if !strings.Contains(out, "test delivery permanently failed after 1 attempt: perm") {
		t.Fatalf("expected permanent failure log line, got: %s", out)
	}
}

func TestRetryWorker_TransientClassifier_ThreeAttempts(t *testing.T) {
	var sendCalls atomic.Int32
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("transient")
	}
	w := newRetryWorker(queue, logger, "test", send)
	w.isPermanent = func(err error) bool { return false }

	w.deliver(testDelivery{LogRef: "transient"})

	if got := sendCalls.Load(); got != 3 {
		t.Fatalf("expected exactly 3 send calls, got %d", got)
	}
}

func TestRetryWorker_NilClassifier_ThreeAttempts(t *testing.T) {
	var sendCalls atomic.Int32
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	w.deliver(testDelivery{LogRef: "webhook-safe"})

	if got := sendCalls.Load(); got != 3 {
		t.Fatalf("expected exactly 3 send calls with nil classifier, got %d", got)
	}
}

// TestRetryWorker_DeadlineExpiresDuringBackoff_StopsFurtherAttempts blocks the
// first send, releases a transient failure, then cancels the Close deadline
// during the backoff window so attempt 2 and a second queued item never start.
func TestRetryWorker_DeadlineExpiresDuringBackoff_StopsFurtherAttempts(t *testing.T) {
	var sendCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan error)
	var once sync.Once
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		n := sendCalls.Add(1)
		if n == 1 {
			once.Do(func() { close(firstStarted) })
			return <-releaseFirst
		}
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	runCtx, cancel := context.WithCancel(context.Background())
	closeCtx, closeCancel := context.WithCancel(context.Background())
	w.beginShutdown(closeCtx)
	go w.Run(runCtx)

	queue.Enqueue(testDelivery{LogRef: "first"})
	queue.Enqueue(testDelivery{LogRef: "second"})

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first send attempt did not start")
	}

	releaseFirst <- errors.New("fail")
	closeCancel()
	cancel()

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish after deadline during backoff")
	}

	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("expected only the in-flight first attempt, got %d", got)
	}
}

// TestRetryWorker_ActiveFlushObservesCloseDeadline blocks the first send,
// installs an already-expired close deadline, and verifies a mid-flush
// delivery starts no further attempts after the in-flight send finishes.
func TestRetryWorker_ActiveFlushObservesCloseDeadline(t *testing.T) {
	var sendCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var once sync.Once

	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		n := sendCalls.Add(1)
		if n == 1 {
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
			return errors.New("transient")
		}
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	runCtx, cancel := context.WithCancel(context.Background())
	go w.Run(runCtx)

	queue.Enqueue(testDelivery{LogRef: "active"})

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first send did not start")
	}

	expired, expiredCancel := context.WithCancel(context.Background())
	expiredCancel()
	w.beginShutdown(expired)
	if w.retryCtx.Err() == nil {
		t.Fatal("retryCtx remained active after beginShutdown with expired context")
	}
	cancel()

	close(releaseFirst)

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish")
	}

	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("expected only the in-progress attempt after expired Close deadline, got %d", got)
	}
}
