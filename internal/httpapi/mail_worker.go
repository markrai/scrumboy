package httpapi

import (
	"context"
	"log"
	"time"

	"scrumboy/internal/mailer"
)

// mailSender is the subset of *mailer.Sender the worker depends on, so tests
// can substitute a hand-rolled fake without a real network listener.
type mailSender interface {
	Send(mailer.Message) error
}

type mailWorker struct {
	queue  *mailQueue
	sender mailSender
	logger *log.Logger
}

func newMailWorker(queue *mailQueue, sender mailSender, logger *log.Logger) *mailWorker {
	return &mailWorker{queue: queue, sender: sender, logger: logger}
}

func (w *mailWorker) Run(ctx context.Context) {
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

func (w *mailWorker) flush() {
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

// deliver retries up to 3x with the same fixed backoff array used by the
// webhook worker, for consistency within the codebase. Permanent failure
// after all attempts is server-log-only (see docs/smtp.md).
func (w *mailWorker) deliver(d mailDelivery) {
	backoff := [3]time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if backoff[attempt] > 0 {
			time.Sleep(backoff[attempt])
		}
		lastErr = w.sender.Send(mailer.Message{To: d.To, Subject: d.Subject, Body: d.Body})
		if lastErr == nil {
			return
		}
	}
	w.logger.Printf("mail delivery failed after 3 attempts: %s err=%v", d.LogRef, lastErr)
}
