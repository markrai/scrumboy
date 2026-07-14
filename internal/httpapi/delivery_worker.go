package httpapi

import (
	"context"
	"log"
	"time"
)

// deliveryBackoff is the fixed 3-attempt retry schedule shared by every
// delivery worker: immediate, then 100ms, then 400ms.
var deliveryBackoff = [3]time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond}

// retryWorker drains a deliveryQueue and hands each item to send, retrying
// up to len(deliveryBackoff) times before giving up and logging. It powers
// both the webhook worker and the mail worker.
type retryWorker[T deliveryItem] struct {
	queue  *deliveryQueue[T]
	logger *log.Logger
	kind   string // e.g. "mail", "webhook" — used in the failure log line
	send   func(T) error
	done   chan struct{}
}

func newRetryWorker[T deliveryItem](queue *deliveryQueue[T], logger *log.Logger, kind string, send func(T) error) *retryWorker[T] {
	return &retryWorker[T]{queue: queue, logger: logger, kind: kind, send: send, done: make(chan struct{})}
}

// Done returns a channel that's closed once Run has returned, i.e. once the
// final shutdown flush has completed. Callers can wait on it (with a
// deadline of their own) to avoid dropping in-flight deliveries on process exit.
func (w *retryWorker[T]) Done() <-chan struct{} {
	return w.done
}

func (w *retryWorker[T]) Run(ctx context.Context) {
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			w.flush()
			return
		case <-w.queue.Wait():
			w.flush()
		}
	}
}

func (w *retryWorker[T]) flush() {
	for {
		batch := w.queue.Drain()
		if len(batch) == 0 {
			return
		}
		for _, d := range batch {
			w.deliver(d)
		}
	}
}

func (w *retryWorker[T]) deliver(d T) {
	var lastErr error
	for attempt := 0; attempt < len(deliveryBackoff); attempt++ {
		if deliveryBackoff[attempt] > 0 {
			time.Sleep(deliveryBackoff[attempt])
		}
		lastErr = w.send(d)
		if lastErr == nil {
			return
		}
	}
	w.logger.Printf("%s delivery failed after %d attempts: %s err=%v", w.kind, len(deliveryBackoff), d.logRef(), lastErr)
}
