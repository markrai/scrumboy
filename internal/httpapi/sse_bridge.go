package httpapi

import (
	"context"
	"encoding/json"
	"fmt"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
)

// sseBridge is an eventbus.Consumer that translates domain events into the
// existing SSE wire format and pushes them through the Hub.
// todo.assigned unmarshals eventbus.TodoAssignedPayload (see internal/eventbus/todo_assigned.go).
type sseBridge struct {
	hub                           *Hub
	creatorNotificationAuthorizer *todoapp.CreatorNotificationAuthorizationService
}

func newSSEBridge(
	hub *Hub,
	creatorNotificationAuthorizer *todoapp.CreatorNotificationAuthorizationService,
) *sseBridge {
	return &sseBridge{
		hub:                           hub,
		creatorNotificationAuthorizer: creatorNotificationAuthorizer,
	}
}

func (b *sseBridge) OnEvent(ctx context.Context, e eventbus.Event) {
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

	case "wall.refresh_needed":
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		data, err := json.Marshal(wallRefreshNeededEvent{
			ID:        e.ID,
			Type:      "wall.refresh_needed",
			ProjectID: e.ProjectID,
			Reason:    p.Reason,
		})
		if err != nil {
			return
		}
		b.hub.Emit(e.ProjectID, data)

	case "wall.transient":
		// Transient events carry arbitrary note-move payloads; forward as-is.
		var payload map[string]any
		if len(e.Payload) > 0 {
			_ = json.Unmarshal(e.Payload, &payload)
		}
		data, err := json.Marshal(wallTransientEvent{
			ID:        e.ID,
			Type:      "wall.transient",
			ProjectID: e.ProjectID,
			Payload:   payload,
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
		// Distinct id from the structured todo.assigned payload (same domain event id) so clients
		// can dedupe assignments without swallowing this refresh line.
		refreshWireID := fmt.Sprintf("%s:refresh_needed", e.ID)
		refreshData, err := json.Marshal(refreshNeededEvent{
			ID:        refreshWireID,
			Type:      "refresh_needed",
			ProjectID: e.ProjectID,
			Reason:    "todo_assigned",
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

	case eventbus.TodoCreatorNotificationRecipientAuthorizedEventType:
		b.emitCreatorActivity(ctx, e)

	}
}

type creatorActivityWireEvent struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	ProjectID   int64  `json:"projectId"`
	ProjectSlug string `json:"projectSlug"`
	Payload     struct {
		TodoID         int64  `json:"todoId"`
		LocalID        int64  `json:"localId"`
		Title          string `json:"title"`
		ActivityReason string `json:"activityReason"`
	} `json:"payload"`
}

// emitCreatorActivity treats the Phase 3 event as a candidate only. It repeats
// the durable-project and current-role lookup with the fanout context directly
// before disclosure, then emits solely on the recipient's private user channel.
// Cancellation and lookup failures are best-effort delivery drops and cannot
// affect the already-successful todo mutation.
func (b *sseBridge) emitCreatorActivity(ctx context.Context, e eventbus.Event) {
	if b == nil || b.hub == nil || b.creatorNotificationAuthorizer == nil {
		return
	}
	var payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload
	if err := json.Unmarshal(e.Payload, &payload); err != nil ||
		e.ProjectID <= 0 || e.ProjectID != payload.ProjectID {
		return
	}

	authorized, ok, err := b.creatorNotificationAuthorizer.ReauthorizeRecipient(ctx, todoapp.AuthorizedCreatorNotification{
		ProjectID:             payload.ProjectID,
		ProjectSlug:           payload.ProjectSlug,
		TodoID:                payload.TodoID,
		LocalID:               payload.LocalID,
		Title:                 payload.Title,
		ActivityReason:        payload.ActivityReason,
		RecipientUserID:       payload.RecipientUserID,
		ActorUserID:           payload.ActorUserID,
		MaterialChanged:       payload.MaterialChanged,
		AssignmentChanged:     payload.AssignmentChanged,
		ToAssigneeUserID:      payload.ToAssigneeUserID,
		CardActivityCandidate: payload.CardActivityCandidate,
	})
	if err != nil || !ok {
		return
	}

	var wire creatorActivityWireEvent
	wire.ID = e.ID
	wire.Type = "todo.creator_activity"
	wire.ProjectID = authorized.ProjectID
	wire.ProjectSlug = authorized.ProjectSlug
	wire.Payload.TodoID = authorized.TodoID
	wire.Payload.LocalID = authorized.LocalID
	wire.Payload.Title = authorized.Title
	wire.Payload.ActivityReason = authorized.ActivityReason
	data, err := json.Marshal(wire)
	if err != nil {
		return
	}
	b.hub.EmitUser(authorized.RecipientUserID, data)
}
