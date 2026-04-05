package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const heartbeatInterval = 25 * time.Second

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
