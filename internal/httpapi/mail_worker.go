package httpapi

import (
	"log"

	"scrumboy/internal/mailer"
)

// mailSender is the subset of *mailer.Sender the worker depends on, so tests
// can substitute a hand-rolled fake without a real network listener.
type mailSender interface {
	Send(mailer.Message) error
}

type mailWorker struct {
	*retryWorker[mailDelivery]
}

func newMailWorker(queue *mailQueue, sender mailSender, logger *log.Logger) *mailWorker {
	return newMailWorkerWithKind(queue, sender, logger, "mail")
}

func newMailWorkerWithKind(queue *mailQueue, sender mailSender, logger *log.Logger, kind string) *mailWorker {
	send := func(d mailDelivery) error {
		return sender.Send(mailer.Message{To: d.To, Subject: d.Subject, Body: d.Body})
	}
	worker := newRetryWorker(queue, logger, kind, send)
	worker.isPermanent = mailer.IsPermanent
	return &mailWorker{retryWorker: worker}
}
