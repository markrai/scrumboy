package httpapi

import (
	"context"
	"encoding/json"

	"scrumboy/internal/eventbus"
)

type refreshNeededEvent struct {
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
	Reason    string `json:"reason,omitempty"`
}

type membersUpdatedEvent struct {
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
}

func (s *Server) emitRefreshNeeded(ctx context.Context, projectID int64, reason string) {
	payload, _ := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: reason})
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "board.refresh_needed",
		ProjectID: projectID,
		Payload:   payload,
	})
}

func (s *Server) emitMembersUpdated(ctx context.Context, projectID int64) {
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "board.members_updated",
		ProjectID: projectID,
	})
}
