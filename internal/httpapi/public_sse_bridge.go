package httpapi

import (
	"context"
	"encoding/json"

	"scrumboy/internal/eventbus"
)

// publicSSEBridge is the only eventbus-to-PublicHub translator. It accepts
// existing committed domain events only when their internal marker says the
// allowlisted public projection changed, then discards every payload field.
type publicSSEBridge struct {
	hub *PublicHub
}

func newPublicSSEBridge(hub *PublicHub) *publicSSEBridge {
	return &publicSSEBridge{hub: hub}
}

func (b *publicSSEBridge) OnEvent(_ context.Context, event eventbus.Event) {
	if b == nil || b.hub == nil || event.ProjectID <= 0 {
		return
	}

	changed := false
	switch event.Type {
	case "board.refresh_needed":
		var payload refreshNeededPayload
		if json.Unmarshal(event.Payload, &payload) == nil {
			changed = payload.PublicProjectionChanged
		}
	case "todo.assigned":
		var payload eventbus.TodoAssignedPayload
		if json.Unmarshal(event.Payload, &payload) == nil && payload.ProjectID == event.ProjectID {
			changed = payload.PublicProjectionChanged
		}
	default:
		return
	}

	if changed {
		b.hub.RefreshPublicProject(event.ProjectID)
	}
}
