package httpapi

import (
	"context"

	"scrumboy/internal/eventbus"
)

// publicSSEBridge is the only eventbus-to-PublicHub translator. It accepts
// committed board invalidations and assignment events only when the publisher
// attached the in-process public-projection marker to the publication context.
// It never reads event payloads, so no payload field can reach public output
// or forge a public invalidation.
type publicSSEBridge struct {
	hub *PublicHub
}

func newPublicSSEBridge(hub *PublicHub) *publicSSEBridge {
	return &publicSSEBridge{hub: hub}
}

func (b *publicSSEBridge) OnEvent(ctx context.Context, event eventbus.Event) {
	if b == nil || b.hub == nil || event.ProjectID <= 0 {
		return
	}
	switch event.Type {
	case "board.refresh_needed", "todo.assigned":
	default:
		return
	}
	if publicProjectionChangedFromContext(ctx) {
		b.hub.RefreshPublicProject(event.ProjectID)
	}
}
