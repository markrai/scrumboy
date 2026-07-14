package httpapi

import (
	"log"
	"sync"
)

// deliveryItem is anything that can be queued and retried by a retryWorker /
// deliveryQueue: it must describe itself for logging.
type deliveryItem interface {
	logRef() string
}

// deliveryQueue is a bounded FIFO with a wake-up notification channel,
// shared by the webhook and mail delivery workers so queue-full and
// high-water-mark behavior only has to be implemented once.
type deliveryQueue[T deliveryItem] struct {
	mu     sync.Mutex
	items  []T
	cap    int
	notify chan struct{}
	logger *log.Logger
	kind   string // e.g. "mail", "webhook" — used in log lines
}

func newDeliveryQueue[T deliveryItem](logger *log.Logger, capacity int, kind string) *deliveryQueue[T] {
	return &deliveryQueue[T]{
		cap:    capacity,
		notify: make(chan struct{}, 1),
		logger: logger,
		kind:   kind,
	}
}

func (q *deliveryQueue[T]) Enqueue(d T) {
	q.mu.Lock()
	if len(q.items) >= q.cap {
		q.mu.Unlock()
		q.logger.Printf("%s queue full, dropping delivery: %s", q.kind, d.logRef())
		return
	}
	q.items = append(q.items, d)
	depth := len(q.items)
	q.mu.Unlock()

	// Warn well before anything is actually dropped, so an operator has a
	// chance to notice a stuck relay/endpoint before deliveries are lost.
	if q.cap > 0 && depth*10 >= q.cap*9 {
		q.logger.Printf("%s queue at %d/%d capacity", q.kind, depth, q.cap)
	}

	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *deliveryQueue[T]) Drain() []T {
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

func (q *deliveryQueue[T]) Wait() <-chan struct{} {
	return q.notify
}
