package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	publicboardapp "scrumboy/internal/application/publicboard"
)

const heartbeatInterval = 25 * time.Second

const (
	defaultPublicStreamHeartbeatInterval  = 15 * time.Second
	defaultPublicStreamRevalidateInterval = 15 * time.Second
)

var (
	publicRefreshNeededPayload = []byte(`{"type":"refresh_needed"}`)
	publicAccessRevokedPayload = []byte(`{"type":"access_revoked"}`)
)

func normalizedPublicStreamInterval(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// ssePingPayload is sent as a data: line on the heartbeat ticker so browser EventSource
// clients can observe keepalives (comment-only : heartbeat is not exposed as onmessage).
var ssePingPayload = mustJSON(map[string]string{"type": "ping"})

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// handlePublicBoardEvents serves one isolated public stream. Admission order is
// owned by handlePublicBoard (namespace limit, stream-attempt limit, complete
// eligibility resolution); this handler then reserves hub resources, repeats
// the authoritative eligibility check bound to the resolved slug and project,
// and only then commits the 200 response.
//
// Revocation policy:
//   - A committed unpublish or project deletion calls PublicHub.RevokePublicProject,
//     which queues access_revoked for and closes every local subscriber.
//   - A failed per-connection revalidation affects only this connection. An
//     authoritative ineligible result sends access_revoked; uncertainty
//     (storage error) or cancellation closes silently so the client reconnects
//     through full admission. Neither path revokes other subscribers.
func (s *Server) handlePublicBoardEvents(
	w http.ResponseWriter,
	r *http.Request,
	prepared publicboardapp.PreparedRead,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
		return
	}

	subscription, err := s.publicHub.Subscribe(prepared.ProjectID(), "ip:"+s.clientIP(r))
	if err != nil {
		if errors.Is(err, ErrPublicHubClosed) {
			writeError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "service unavailable", nil)
			return
		}
		s.logger.Printf("public board stream concurrency limit reached")
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
		return
	}
	defer subscription.Unsubscribe()

	// Register first and then revalidate. A committed revoke either closed this
	// registration or is already visible to this read, so no stream can be
	// admitted against a pre-revocation eligibility decision.
	if err := prepared.Revalidate(r.Context()); err != nil {
		s.writePublicBoardError(w, err)
		return
	}
	if !subscription.Active() {
		s.writePublicBoardError(w, publicboardapp.ErrPublicNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(s.publicStreamHeartbeatInterval)
	defer heartbeat.Stop()
	revalidate := time.NewTicker(s.publicStreamRevalidateInterval)
	defer revalidate.Stop()

	writeEvent := func(payload []byte) bool {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	// stillEligible repeats the complete predicate for this connection only.
	stillEligible := func() bool {
		err := prepared.Revalidate(r.Context())
		if err == nil {
			return true
		}
		if r.Context().Err() != nil {
			return false
		}
		if errors.Is(err, publicboardapp.ErrPublicNotFound) {
			_ = writeEvent(publicAccessRevokedPayload)
			return false
		}
		s.logger.Printf("public board stream revalidation failed; closing stream")
		return false
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-revalidate.C:
			if !stillEligible() {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event, open := <-subscription.Events:
			if !open {
				return
			}
			switch event {
			case PublicEventAccessRevoked:
				_ = writeEvent(publicAccessRevokedPayload)
				return
			case PublicEventRefreshNeeded:
				// refresh_needed is idempotent: collapse queued duplicates so a
				// burst costs one revalidation and one write, and a healthy
				// stream is not evicted by its own backlog.
				switch drainQueuedPublicRefreshes(subscription.Events) {
				case publicQueueRevoked:
					_ = writeEvent(publicAccessRevokedPayload)
					return
				case publicQueueClosed:
					return
				}
				if !stillEligible() {
					return
				}
				// A revocation recognized while revalidating has already
				// replaced queued refreshes with access_revoked; let the
				// channel deliver it instead of this stale invalidation.
				if !subscription.Active() {
					continue
				}
				if !writeEvent(publicRefreshNeededPayload) {
					return
				}
			}
		}
	}
}

type publicQueueState uint8

const (
	publicQueueOpen publicQueueState = iota
	publicQueueRevoked
	publicQueueClosed
)

// drainQueuedPublicRefreshes consumes already-queued refresh signals without
// blocking and reports a terminal state if one was reached.
func drainQueuedPublicRefreshes(events <-chan PublicEvent) publicQueueState {
	for {
		select {
		case event, open := <-events:
			if !open {
				return publicQueueClosed
			}
			if event == PublicEventAccessRevoked {
				return publicQueueRevoked
			}
		default:
			return publicQueueOpen
		}
	}
}

func (s *Server) handleBoardEvents(w http.ResponseWriter, r *http.Request, projectID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "streaming not supported", nil)
		return
	}

	// SSE + proxy safety headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsubscribe := s.hub.Subscribe(projectID)
	defer unsubscribe()

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", ssePingPayload); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg, ok := <-events:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleMeRealtime streams a merged SSE feed: user-scoped hub events plus every project the user can access.
// User-scoped delivery is the long-term primary mechanism for assignee-targeted events; merged project
// subscriptions keep refresh_needed / members_updated working without refactoring every Emit(projectID) site.
//
// Scaling: one forward goroutine per subscribed hub channel (1 user + N projects). Very large N increases
// goroutines and fanout work per connection; a future improvement is server-side fan-in so user-channel
// delivery carries project-scoped events without N Subscribe(projectID) calls here.
func (s *Server) handleMeRealtime(w http.ResponseWriter, r *http.Request, ctx context.Context, userID int64) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "streaming not supported", nil)
		return
	}

	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		writeStoreErr(w, err, true)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx = r.Context()
	merged := make(chan []byte, 512)
	var unsubs []func()
	cleanup := func() {
		for _, u := range unsubs {
			u()
		}
	}
	defer cleanup()

	forward := func(ch <-chan []byte) {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				select {
				case merged <- msg:
				case <-ctx.Done():
					return
				}
			}
		}
	}

	chUser, unsubUser := s.hub.SubscribeUser(userID)
	unsubs = append(unsubs, unsubUser)
	go forward(chUser)

	for _, pe := range projects {
		pid := pe.Project.ID
		ch, unsub := s.hub.Subscribe(pid)
		unsubs = append(unsubs, unsub)
		go forward(ch)
	}

	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", ssePingPayload); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case msg, ok := <-merged:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
