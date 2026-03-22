package httpapi

import (
	"sync"
)

const (
	defaultSubscriberBuffer = 32
)

type EventSink interface {
	Emit(projectID int64, event []byte)
}

type noopEventSink struct{}

func (noopEventSink) Emit(_ int64, _ []byte) {}

type subscriber struct {
	ch        chan []byte
	closeOnce sync.Once
}

func (s *subscriber) close() {
	s.closeOnce.Do(func() {
		close(s.ch)
	})
}

type Hub struct {
	mu            sync.RWMutex
	byProjectID   map[int64]map[*subscriber]struct{}
	channelBuffer int
}

func NewHub(channelBuffer int) *Hub {
	if channelBuffer <= 0 {
		channelBuffer = defaultSubscriberBuffer
	}
	return &Hub{
		byProjectID:   make(map[int64]map[*subscriber]struct{}),
		channelBuffer: channelBuffer,
	}
}

func (h *Hub) Subscribe(projectID int64) (<-chan []byte, func()) {
	sub := &subscriber{
		ch: make(chan []byte, h.channelBuffer),
	}
	h.mu.Lock()
	if _, ok := h.byProjectID[projectID]; !ok {
		h.byProjectID[projectID] = make(map[*subscriber]struct{})
	}
	h.byProjectID[projectID][sub] = struct{}{}
	h.mu.Unlock()

	return sub.ch, func() {
		h.remove(projectID, sub)
	}
}

func (h *Hub) Emit(projectID int64, event []byte) {
	h.mu.RLock()
	subs, ok := h.byProjectID[projectID]
	if !ok || len(subs) == 0 {
		h.mu.RUnlock()
		return
	}
	snapshot := make([]*subscriber, 0, len(subs))
	for sub := range subs {
		snapshot = append(snapshot, sub)
	}
	h.mu.RUnlock()

	for _, sub := range snapshot {
		select {
		case sub.ch <- event:
		default:
			// Deterministic backpressure policy: slow subscribers are disconnected.
			h.remove(projectID, sub)
		}
	}
}

func (h *Hub) remove(projectID int64, sub *subscriber) {
	h.mu.Lock()
	projectSubs, ok := h.byProjectID[projectID]
	if ok {
		if _, exists := projectSubs[sub]; exists {
			delete(projectSubs, sub)
			if len(projectSubs) == 0 {
				delete(h.byProjectID, projectID)
			}
		}
	}
	h.mu.Unlock()
	sub.close()
}
