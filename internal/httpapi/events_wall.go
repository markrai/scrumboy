package httpapi

import (
	"context"
	"encoding/json"

	wallapp "scrumboy/internal/application/wall"
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

// wallTransientEvent is the wire event emitted for realtime drag/move updates.
// Transient events are never persisted; common fanout makes them available to
// the SSE bridge and matching webhook consumers.
type wallTransientEvent struct {
	ID        string         `json:"id,omitempty"`
	Type      string         `json:"type"`
	ProjectID int64          `json:"projectId"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type wallRefreshPublisher struct {
	server *Server
}

var _ wallapp.WallRefreshPublisher = wallRefreshPublisher{}

func (p wallRefreshPublisher) PublishWallRefresh(
	ctx context.Context,
	projectID int64,
	reason wallapp.RefreshReason,
) {
	p.server.emitWallRefreshNeeded(ctx, projectID, string(reason))
}

type wallTransientPublisher struct {
	server *Server
}

var _ wallapp.WallTransientPublisher = wallTransientPublisher{}

func (p wallTransientPublisher) PublishWallTransient(
	ctx context.Context,
	projectID int64,
	event wallapp.TransientEvent,
) error {
	payload, err := json.Marshal(map[string]any{
		"noteId": event.NoteID,
		"x":      event.X,
		"y":      event.Y,
		"by":     event.By,
	})
	if err != nil {
		return err
	}
	p.server.emitWallTransient(ctx, projectID, payload)
	return nil
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
// the caller-provided raw bytes (e.g. {noteId, x, y, by}) and is forwarded
// through common fanout without any storage.
func (s *Server) emitWallTransient(ctx context.Context, projectID int64, payload json.RawMessage) {
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "wall.transient",
		ProjectID: projectID,
		Payload:   payload,
	})
}
