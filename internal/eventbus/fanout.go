package eventbus

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Fanout distributes each published event to all registered consumers in order.
type Fanout struct {
	consumers []Consumer
}

func NewFanout(consumers ...Consumer) *Fanout {
	return &Fanout{consumers: consumers}
}

func (f *Fanout) Publish(ctx context.Context, e Event) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	for _, c := range f.consumers {
		c.OnEvent(ctx, e)
	}
	return nil
}
