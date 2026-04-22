package httpapi

import (
	"context"
	"encoding/json"

	"scrumboy/internal/eventbus"
)

// wallRefreshNeededEvent is the SSE wire event emitted after durable wall
// mutations. Clients that have the wall open refetch the wall; clients that
// do not have it open ignore it.
type wallRefreshNeededEvent struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
	Reason    string `json:"reason,omitempty"`
}

// wallTransientEvent is the SSE wire event emitted for realtime drag/move
// updates. Transient events are never persisted; they only live on the SSE
// wire and each client applies them optimistically to its local wall state.
type wallTransientEvent struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"`
	ProjectID int64          `json:"projectId"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func (s *Server) emitWallRefreshNeeded(ctx context.Context, projectID int64, reason string) {
	payload, _ := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: reason})
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "wall.refresh_needed",
		ProjectID: projectID,
		Payload:   payload,
	})
}

// emitWallTransient publishes an ephemeral drag/move event. The payload is
// the caller-provided raw bytes (e.g. {noteId, x, y}) and is forwarded to the
// SSE bridge without any storage.
func (s *Server) emitWallTransient(ctx context.Context, projectID int64, payload json.RawMessage) {
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "wall.transient",
		ProjectID: projectID,
		Payload:   payload,
	})
}
