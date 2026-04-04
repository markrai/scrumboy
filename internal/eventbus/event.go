package eventbus

import (
	"context"
	"encoding/json"
	"time"
)

// Event is the canonical domain event passed through the bus.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Time      time.Time       `json:"timestamp"`
	ProjectID int64           `json:"projectId"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// Consumer receives events from the bus.
type Consumer interface {
	OnEvent(ctx context.Context, e Event)
}

// Publisher sends events into the bus.
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}
