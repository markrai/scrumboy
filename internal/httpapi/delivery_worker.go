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
	queue       *deliveryQueue[T]
	logger      *log.Logger
	kind        string // e.g. "mail", "webhook" — used in the failure log line
	send        func(T) error
	isPermanent func(error) bool
	done        chan struct{}
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
			w.flush(ctx)
			return
		case <-w.queue.Wait():
			w.flush(ctx)
		}
	}
}

func (w *retryWorker[T]) flush(ctx context.Context) {
	for {
		batch := w.queue.Drain()
		if len(batch) == 0 {
			return
		}
		for _, d := range batch {
			w.deliver(ctx, d)
		}
	}
}

// deliver sends d with up to three attempts. The first attempt always runs;
// retries and backoff happen only while ctx is still active.
func (w *retryWorker[T]) deliver(ctx context.Context, d T) {
	var lastErr error
	attemptsMade := 0
	for attempt, backoff := range deliveryBackoff {
		if attempt > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				w.logFailure(d, attemptsMade, lastErr)
				return
			case <-timer.C:
			}
		}
		lastErr = w.send(d)
		attemptsMade++
		if lastErr == nil {
			return
		}
		if w.isPermanent != nil && w.isPermanent(lastErr) {
			w.logPermanentFailure(d, attemptsMade, lastErr)
			return
		}
	}
	w.logFailure(d, attemptsMade, lastErr)
}

func (w *retryWorker[T]) logPermanentFailure(d T, attemptsMade int, lastErr error) {
	if attemptsMade == 0 {
		return
	}
	attemptWord := "attempt"
	if attemptsMade != 1 {
		attemptWord = "attempts"
	}
	w.logger.Printf("%s delivery permanently failed after %d %s: %s err=%v",
		w.kind, attemptsMade, attemptWord, d.logRef(), lastErr)
}

func (w *retryWorker[T]) logFailure(d T, attemptsMade int, lastErr error) {
	if attemptsMade == 0 {
		return
	}
	w.logger.Printf("%s delivery failed after %d attempts: %s err=%v", w.kind, attemptsMade, d.logRef(), lastErr)
}
