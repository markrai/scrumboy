package httpapi

import (
	"context"
	"log"
	"sync"
	"time"
)

// deliveryBackoff is the fixed 3-attempt retry schedule shared by every
// delivery worker: immediate, then 100ms, then 400ms.
var deliveryBackoff = [3]time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond}

// retryWorker drains a deliveryQueue and hands each item to send, retrying
// up to len(deliveryBackoff) times before giving up and logging. It powers
// both the webhook worker and the mail worker.
//
// Two contexts:
//
//   - runCtx (Run argument): stops accepting new queue wake-ups
//   - retryCtx (worker-owned): bounds retries and remaining drain work
//
// Server.Close seals the queue, then beginShutdown(closeCtx) so retryCtx is
// cancelled when closeCtx expires, then cancels runCtx. Steady-state and
// shutdown flushes both use retryCtx, so an in-flight flush observes the
// Close deadline. An expired retryCtx starts no new queued item and no new
// send attempt; a send already in progress may finish under its own timeout.
type retryWorker[T deliveryItem] struct {
	queue       *deliveryQueue[T]
	logger      *log.Logger
	kind        string // e.g. "mail", "webhook" — used in the failure log line
	send        func(T) error
	isPermanent func(error) bool
	done        chan struct{}

	retryCtx    context.Context
	retryCancel context.CancelFunc

	mu              sync.Mutex
	shutdownStarted bool
}

func newRetryWorker[T deliveryItem](queue *deliveryQueue[T], logger *log.Logger, kind string, send func(T) error) *retryWorker[T] {
	retryCtx, retryCancel := context.WithCancel(context.Background())
	return &retryWorker[T]{
		queue:       queue,
		logger:      logger,
		kind:        kind,
		send:        send,
		done:        make(chan struct{}),
		retryCtx:    retryCtx,
		retryCancel: retryCancel,
	}
}

// Done returns a channel that's closed once Run has returned, i.e. once the
// final shutdown flush has completed. Callers can wait on it (with a
// deadline of their own) to avoid dropping in-flight deliveries on process exit.
func (w *retryWorker[T]) Done() <-chan struct{} {
	return w.done
}

// beginShutdown arranges for retryCtx to cancel when closeCtx expires.
// If closeCtx is already done, retryCtx is cancelled synchronously before
// return so Server.Close cannot cancel runCtx while retryCtx is still live.
// Idempotent.
func (w *retryWorker[T]) beginShutdown(closeCtx context.Context) {
	w.mu.Lock()
	if w.shutdownStarted {
		w.mu.Unlock()
		return
	}
	w.shutdownStarted = true
	w.mu.Unlock()

	context.AfterFunc(closeCtx, w.retryCancel)
	if closeCtx.Err() != nil {
		w.retryCancel()
	}
}

func (w *retryWorker[T]) Run(runCtx context.Context) {
	defer close(w.done)
	defer w.retryCancel()
	for {
		select {
		case <-runCtx.Done():
			w.flush()
			return
		case <-w.queue.Wait():
			select {
			case <-runCtx.Done():
				w.flush()
				return
			default:
				w.flush()
			}
		}
	}
}

func (w *retryWorker[T]) flush() {
	for {
		if w.retryCtx.Err() != nil {
			return
		}
		batch := w.queue.Drain()
		if len(batch) == 0 {
			return
		}
		for _, d := range batch {
			if w.retryCtx.Err() != nil {
				return
			}
			w.deliver(d)
		}
	}
}

// deliver sends d with up to three attempts while retryCtx remains active.
// A send already in progress is allowed to finish if retryCtx cancels mid-call.
func (w *retryWorker[T]) deliver(d T) {
	var lastErr error
	attemptsMade := 0
	for attempt, backoff := range deliveryBackoff {
		if w.retryCtx.Err() != nil {
			if attemptsMade > 0 {
				w.logFailure(d, attemptsMade, lastErr)
			}
			return
		}
		if attempt > 0 {
			timer := time.NewTimer(backoff)
			select {
			case <-w.retryCtx.Done():
				timer.Stop()
				w.logFailure(d, attemptsMade, lastErr)
				return
			case <-timer.C:
			}
			if w.retryCtx.Err() != nil {
				w.logFailure(d, attemptsMade, lastErr)
				return
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
