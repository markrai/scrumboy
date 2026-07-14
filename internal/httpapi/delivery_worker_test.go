package httpapi

import (
	"context"
	"errors"
	"io"
	"log"
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

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(runDone)
	}()

	queue.Enqueue(testDelivery{LogRef: "one"})
	queue.Enqueue(testDelivery{LogRef: "two"})
	cancel()

	start := time.Now()
	select {
	case <-w.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("worker Done did not close promptly after cancel")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("expected no 100ms+400ms backoff after cancel, took %v", elapsed)
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
