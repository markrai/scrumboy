package httpapi

import (
	"log"
	"sync"
)

const defaultQueueCapacity = 1024

type webhookQueue struct {
	mu     sync.Mutex
	items  []webhookDelivery
	cap    int
	notify chan struct{} // closed/re-created to wake worker
	logger *log.Logger
}

func newWebhookQueue(logger *log.Logger) *webhookQueue {
	return &webhookQueue{
		cap:    defaultQueueCapacity,
		notify: make(chan struct{}, 1),
		logger: logger,
	}
}

func (q *webhookQueue) Enqueue(d webhookDelivery) {
	q.mu.Lock()
	if len(q.items) >= q.cap {
		q.mu.Unlock()
		q.logger.Printf("webhook queue full, dropping delivery for event %s to %s", d.EventID, d.URL)
		return
	}
	q.items = append(q.items, d)
	q.mu.Unlock()

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *webhookQueue) Drain() []webhookDelivery {
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

func (q *webhookQueue) Wait() <-chan struct{} {
	return q.notify
}
