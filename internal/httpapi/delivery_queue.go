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
	sealed bool   // after Seal, Enqueue drops new items (shutdown)
}

func newDeliveryQueue[T deliveryItem](logger *log.Logger, capacity int, kind string) *deliveryQueue[T] {
	return &deliveryQueue[T]{
		cap:    capacity,
		notify: make(chan struct{}, 1),
		logger: logger,
		kind:   kind,
	}
}

// Seal stops accepting new entries. Already-queued items remain for the
// shutdown drain. Safe to call more than once.
func (q *deliveryQueue[T]) Seal() {
	q.mu.Lock()
	q.sealed = true
	q.mu.Unlock()
}

func (q *deliveryQueue[T]) Enqueue(d T) bool {
	q.mu.Lock()
	if q.sealed {
		q.mu.Unlock()
		q.logger.Printf("%s queue sealed, dropping delivery: %s", q.kind, d.logRef())
		return false
	}
	if len(q.items) >= q.cap {
		q.mu.Unlock()
		q.logger.Printf("%s queue full, dropping delivery: %s", q.kind, d.logRef())
		return false
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
	return true
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
