package httpapi

import (
	"errors"
	"strings"
	"sync"
)

const (
	defaultPublicSubscriberBuffer  = 8
	defaultPublicStreamsGlobal     = 500
	defaultPublicStreamsPerIP      = 5
	defaultPublicStreamsPerProject = 100
)

var (
	ErrPublicStreamLimit = errors.New("public stream concurrency limit reached")
	ErrPublicHubClosed   = errors.New("public stream hub is closed")
)

// PublicEvent is deliberately closed: callers can request only constant-shape
// invalidation or revocation signals, never provide public wire bytes.
type PublicEvent uint8

const (
	PublicEventRefreshNeeded PublicEvent = iota + 1
	PublicEventAccessRevoked
)

// PublicHubLimits are independent from authenticated Hub resources. Zero or
// negative values select the conservative production defaults.
type PublicHubLimits struct {
	Global     int
	PerIP      int
	PerProject int
	Buffer     int
}

func normalizePublicHubLimits(limits PublicHubLimits) PublicHubLimits {
	if limits.Global <= 0 {
		limits.Global = defaultPublicStreamsGlobal
	}
	if limits.PerIP <= 0 {
		limits.PerIP = defaultPublicStreamsPerIP
	}
	if limits.PerProject <= 0 {
		limits.PerProject = defaultPublicStreamsPerProject
	}
	if limits.Buffer <= 0 {
		limits.Buffer = defaultPublicSubscriberBuffer
	}
	return limits
}

type publicSubscriber struct {
	projectID int64
	ipKey     string
	events    chan PublicEvent
	active    bool
}

// PublicSubscription owns one bounded hub registration.
type PublicSubscription struct {
	hub  *PublicHub
	sub  *publicSubscriber
	once sync.Once

	Events <-chan PublicEvent
}

// Unsubscribe is safe to call repeatedly and concurrently with refresh,
// revocation, overflow eviction, and hub shutdown.
func (s *PublicSubscription) Unsubscribe() {
	if s == nil || s.hub == nil || s.sub == nil {
		return
	}
	s.once.Do(func() { s.hub.remove(s.sub) })
}

func (s *PublicSubscription) Active() bool {
	if s == nil || s.hub == nil || s.sub == nil {
		return false
	}
	return s.hub.active(s.sub)
}

// PublicHub is intentionally separate from Hub. All channel sends and closes
// are serialized under mu, and sends are nonblocking, eliminating the existing
// Hub's snapshot/send-after-unlock close race without holding a lock for I/O.
type PublicHub struct {
	mu        sync.Mutex
	limits    PublicHubLimits
	closed    bool
	activeN   int
	byIP      map[string]int
	byProject map[int64]map[*publicSubscriber]struct{}
}

func NewPublicHub(limits PublicHubLimits) *PublicHub {
	return &PublicHub{
		limits:    normalizePublicHubLimits(limits),
		byIP:      make(map[string]int),
		byProject: make(map[int64]map[*publicSubscriber]struct{}),
	}
}

func (h *PublicHub) Subscribe(projectID int64, ipKey string) (*PublicSubscription, error) {
	if projectID <= 0 {
		return nil, ErrPublicStreamLimit
	}
	ipKey = strings.TrimSpace(ipKey)
	if ipKey == "" {
		ipKey = "unknown"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrPublicHubClosed
	}
	if h.activeN >= h.limits.Global || h.byIP[ipKey] >= h.limits.PerIP || len(h.byProject[projectID]) >= h.limits.PerProject {
		return nil, ErrPublicStreamLimit
	}

	sub := &publicSubscriber{
		projectID: projectID,
		ipKey:     ipKey,
		events:    make(chan PublicEvent, h.limits.Buffer),
		active:    true,
	}
	if h.byProject[projectID] == nil {
		h.byProject[projectID] = make(map[*publicSubscriber]struct{})
	}
	h.byProject[projectID][sub] = struct{}{}
	h.byIP[ipKey]++
	h.activeN++
	return &PublicSubscription{hub: h, sub: sub, Events: sub.events}, nil
}

// RefreshPublicProject emits only a typed invalidation. A full subscriber
// buffer deterministically evicts that slow subscriber without blocking the
// mutation/eventbus caller or creating a goroutine.
func (h *PublicHub) RefreshPublicProject(projectID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.byProject[projectID] {
		select {
		case sub.events <- PublicEventRefreshNeeded:
		default:
			h.removeLocked(sub)
		}
	}
}

// RevokePublicProject drops queued invalidations, queues at most one terminal
// access_revoked signal, then closes every active subscription for this one
// project. The buffered terminal event remains readable after close.
func (h *PublicHub) RevokePublicProject(projectID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.byProject[projectID] {
		for {
			select {
			case <-sub.events:
				continue
			default:
				sub.events <- PublicEventAccessRevoked
				h.removeLocked(sub)
				goto nextSubscriber
			}
		}
	nextSubscriber:
	}
}

// Shutdown closes all current streams and permanently rejects new admission.
func (h *PublicHub) Shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for _, subscribers := range h.byProject {
		for sub := range subscribers {
			h.removeLocked(sub)
		}
	}
}

func (h *PublicHub) active(sub *publicSubscriber) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return sub.active
}

func (h *PublicHub) remove(sub *publicSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(sub)
}

func (h *PublicHub) removeLocked(sub *publicSubscriber) {
	if sub == nil || !sub.active {
		return
	}
	sub.active = false
	if subscribers := h.byProject[sub.projectID]; subscribers != nil {
		delete(subscribers, sub)
		if len(subscribers) == 0 {
			delete(h.byProject, sub.projectID)
		}
	}
	if count := h.byIP[sub.ipKey]; count <= 1 {
		delete(h.byIP, sub.ipKey)
	} else {
		h.byIP[sub.ipKey] = count - 1
	}
	if h.activeN > 0 {
		h.activeN--
	}
	close(sub.events)
}
