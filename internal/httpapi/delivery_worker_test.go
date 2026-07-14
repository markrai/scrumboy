package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testDelivery struct {
	LogRef string
}

func (d testDelivery) logRef() string { return d.LogRef }

// TestRetryWorker_Cancelled_SingleAttemptPerItem_NoBackoff verifies that
// cancellation stops retry backoff and limits each drained item to one send
// attempt while still processing the whole shutdown batch.
func TestRetryWorker_Cancelled_SingleAttemptPerItem_NoBackoff(t *testing.T) {
	var sendCalls atomic.Int32
	logger := log.New(io.Discard, "", 0)
	queue := newDeliveryQueue[testDelivery](logger, 16, "test")
	send := func(d testDelivery) error {
		sendCalls.Add(1)
		return errors.New("fail")
	}
	w := newRetryWorker(queue, logger, "test", send)

	// Enqueue before starting the worker, then cancel immediately so Run
	// enters shutdown flush with a cancelled ctx. That avoids a race where
	// the worker wakes on the first enqueue and begins normal retries before
	// cancel arrives.
	queue.Enqueue(testDelivery{LogRef: "one"})
	queue.Enqueue(testDelivery{LogRef: "two"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(runDone)
	}()

	select {
	case <-w.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker Done did not close promptly after cancel")
	}
	if got := sendCalls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 send calls (one per item), got %d", got)
	}

	select {
	case <-runDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after cancel flush")
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

	w.deliver(context.Background(), testDelivery{LogRef: "perm"})

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

	w.deliver(context.Background(), testDelivery{LogRef: "transient"})

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

	w.deliver(context.Background(), testDelivery{LogRef: "webhook-safe"})

	if got := sendCalls.Load(); got != 3 {
		t.Fatalf("expected exactly 3 send calls with nil classifier, got %d", got)
	}
}
