package httpapi

import (
	"log"
	"sync"
)

const defaultMailQueueCapacity = 1024

// mailDelivery is one queued outbound email.
type mailDelivery struct {
	To      string
	Subject string
	Body    string
	// LogRef is a non-sensitive identifier for log correlation (e.g.
	// "password-reset user=123"), never the email address or token.
	LogRef string
}

type mailQueue struct {
	mu     sync.Mutex
	items  []mailDelivery
	cap    int
	notify chan struct{}
	logger *log.Logger
}

func newMailQueue(logger *log.Logger) *mailQueue {
	return newMailQueueWithCapacity(logger, defaultMailQueueCapacity)
}

func newMailQueueWithCapacity(logger *log.Logger, capacity int) *mailQueue {
	return &mailQueue{
		cap:    capacity,
		notify: make(chan struct{}, 1),
		logger: logger,
	}
}

func (q *mailQueue) Enqueue(d mailDelivery) {
	q.mu.Lock()
	if len(q.items) >= q.cap {
		q.mu.Unlock()
		q.logger.Printf("mail queue full, dropping delivery: %s", d.LogRef)
		return
	}
	q.items = append(q.items, d)
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *mailQueue) Drain() []mailDelivery {
	q.mu.Lock()
	if len(q.items) == 0 {
		q.mu.Unlock()
		return nil
	}
	batch := q.items
	q.items = nil
	q.mu.Unlock()
	return batch
}

func (q *mailQueue) Wait() <-chan struct{} {
	return q.notify
}
