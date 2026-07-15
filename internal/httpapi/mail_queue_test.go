package httpapi

import (
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func discardLogger() *log.Logger {
	return log.New(io.Discard, "", 0)
}

func TestMailQueue_EnqueueDrainOrder(t *testing.T) {
	q := newMailQueue(discardLogger())
	q.Enqueue(mailDelivery{To: "a@example.com", LogRef: "a"})
	q.Enqueue(mailDelivery{To: "b@example.com", LogRef: "b"})

	batch := q.Drain()
	if len(batch) != 2 {
		t.Fatalf("expected 2 items, got %d", len(batch))
	}
	if batch[0].To != "a@example.com" || batch[1].To != "b@example.com" {
		t.Fatalf("expected FIFO order, got %+v", batch)
	}

	if got := q.Drain(); got != nil {
		t.Fatalf("expected nil after drain, got %+v", got)
	}
}

func TestMailQueue_CapacityDrop(t *testing.T) {
	q := newMailQueueWithCapacity(discardLogger(), 2)
	q.Enqueue(mailDelivery{LogRef: "1"})
	q.Enqueue(mailDelivery{LogRef: "2"})
	q.Enqueue(mailDelivery{LogRef: "3"}) // dropped, over capacity

	batch := q.Drain()
	if len(batch) != 2 {
		t.Fatalf("expected 2 items (3rd dropped), got %d: %+v", len(batch), batch)
	}
}

func TestMailQueue_WaitSignalsOnEnqueue(t *testing.T) {
	q := newMailQueue(discardLogger())
	select {
	case <-q.Wait():
		t.Fatal("did not expect signal before any Enqueue")
	default:
	}

	q.Enqueue(mailDelivery{LogRef: "1"})
	select {
	case <-q.Wait():
	default:
		t.Fatal("expected signal after Enqueue")
	}

	// Signal channel has buffer 1; a second Enqueue before Drain must not block.
	q.Enqueue(mailDelivery{LogRef: "2"})
	q.Enqueue(mailDelivery{LogRef: "3"})
}

func TestMailQueue_HighWaterMarkWarningBeforeDrop(t *testing.T) {
	var buf strings.Builder
	logger := log.New(&buf, "", 0)
	q := newMailQueueWithCapacity(logger, 10)

	for i := 0; i < 8; i++ {
		q.Enqueue(mailDelivery{LogRef: "warm-up"})
	}
	if strings.Contains(buf.String(), "capacity") {
		t.Fatalf("did not expect a high-water-mark warning below 90%% capacity, got: %s", buf.String())
	}

	q.Enqueue(mailDelivery{LogRef: "9th"})
	if !strings.Contains(buf.String(), "mail queue at 9/10 capacity") {
		t.Fatalf("expected a high-water-mark warning at 90%% capacity, got: %s", buf.String())
	}
}

func TestMailQueue_SealDropsNewEntries(t *testing.T) {
	var buf strings.Builder
	logger := log.New(&buf, "", 0)
	q := newMailQueue(logger)
	q.Enqueue(mailDelivery{LogRef: "before-seal"})
	q.Seal()
	q.Enqueue(mailDelivery{LogRef: "after-seal"})

	batch := q.Drain()
	if len(batch) != 1 || batch[0].LogRef != "before-seal" {
		t.Fatalf("expected only pre-seal item, got %+v", batch)
	}
	if !strings.Contains(buf.String(), "mail queue sealed, dropping delivery: after-seal") {
		t.Fatalf("expected sealed drop log, got: %s", buf.String())
	}
}

func TestMailQueue_ConcurrentEnqueue(t *testing.T) {
	q := newMailQueueWithCapacity(discardLogger(), 1000)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q.Enqueue(mailDelivery{LogRef: "concurrent"})
		}(i)
	}
	wg.Wait()

	total := 0
	for {
		batch := q.Drain()
		if len(batch) == 0 {
			break
		}
		total += len(batch)
	}
	if total != 100 {
		t.Fatalf("expected 100 enqueued items, got %d", total)
	}
}

// waitQueueSealed blocks until q.Seal has been observed. Used by Server.Close
// tests so the first send is only released after Close has performed its
// shutdown wiring (seal + beginShutdown).
func waitQueueSealed[T deliveryItem](t *testing.T, q *deliveryQueue[T]) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		sealed := q.sealed
		q.mu.Unlock()
		if sealed {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queue was not sealed in time")
}
