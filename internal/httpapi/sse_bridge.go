package httpapi

import (
	"context"
	"encoding/json"
	"fmt"

	"scrumboy/internal/eventbus"
)

// sseBridge is an eventbus.Consumer that translates domain events into the
// existing SSE wire format and pushes them through the Hub.
// todo.assigned unmarshals eventbus.TodoAssignedPayload (see internal/eventbus/todo_assigned.go).
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
			ID:        e.ID,
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
			ID:        e.ID,
			Type:      "members_updated",
			ProjectID: e.ProjectID,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, data)

	case "todo.assigned":
		var domain eventbus.TodoAssignedPayload
		if err := json.Unmarshal(e.Payload, &domain); err != nil {
			return
		}
		reason := domain.Reason
		if reason == "" {
			reason = "todo_assigned"
		}
		// Distinct id from the structured todo.assigned payload (same domain event id) so clients
		// can dedupe assignments without swallowing this refresh line.
		refreshWireID := fmt.Sprintf("%s:refresh_needed", e.ID)
		refreshData, err := json.Marshal(refreshNeededEvent{
			ID:        refreshWireID,
			Type:      "refresh_needed",
			ProjectID: e.ProjectID,
			Reason:    reason,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, refreshData)

		if domain.ToAssigneeUID != nil {
			type assigneeWire struct {
				ID          string `json:"id"`
				Type        string `json:"type"`
				ProjectID   int64  `json:"projectId"`
				ProjectSlug string `json:"projectSlug,omitempty"`
				Payload     struct {
					TodoID      int64  `json:"todoId"`
					Title       string `json:"title"`
					AssigneeID  int64  `json:"assigneeId"`
					ActorUserID int64  `json:"actorUserId"`
				} `json:"payload"`
			}
			var w assigneeWire
			w.ID = e.ID
			w.Type = "todo.assigned"
			w.ProjectID = e.ProjectID
			w.ProjectSlug = domain.ProjectSlug
			w.Payload.TodoID = domain.TodoID
			w.Payload.Title = domain.Title
			w.Payload.AssigneeID = *domain.ToAssigneeUID
			w.Payload.ActorUserID = domain.ActorUserID
			assignedData, err := json.Marshal(w)
			if err != nil {
				return
			}
			b.hub.Emit(e.ProjectID, assignedData)
			b.hub.EmitUser(*domain.ToAssigneeUID, assignedData)
		}
	}
}
