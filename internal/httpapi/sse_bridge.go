package httpapi

import (
	"context"
	"encoding/json"

	"scrumboy/internal/eventbus"
)

// sseBridge is an eventbus.Consumer that translates domain events into the
// existing SSE wire format and pushes them through the Hub.
type sseBridge struct {
	hub *Hub
}

func newSSEBridge(hub *Hub) *sseBridge {
	return &sseBridge{hub: hub}
}

func (b *sseBridge) OnEvent(_ context.Context, e eventbus.Event) {
	switch e.Type {
	case "board.refresh_needed":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		data, err := json.Marshal(refreshNeededEvent{
			Type:      "refresh_needed",
			ProjectID: e.ProjectID,
			Reason:    p.Reason,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, data)

	case "board.members_updated":
		data, err := json.Marshal(membersUpdatedEvent{
			Type:      "members_updated",
			ProjectID: e.ProjectID,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, data)

	case "todo.assigned":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		reason := p.Reason
		if reason == "" {
			reason = "todo_assigned"
		}
		data, err := json.Marshal(refreshNeededEvent{
			Type:      "refresh_needed",
			ProjectID: e.ProjectID,
			Reason:    reason,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, data)
	}
}
