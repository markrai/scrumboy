package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"scrumboy/internal/eventbus"
)

// webhookDispatcher is an eventbus.Consumer that matches events against
// webhook subscriptions and enqueues deliveries asynchronously.
type webhookDispatcher struct {
	store  storeAPI
	queue  *webhookQueue
	logger *log.Logger
}

func newWebhookDispatcher(store storeAPI, queue *webhookQueue, logger *log.Logger) *webhookDispatcher {
	return &webhookDispatcher{store: store, queue: queue, logger: logger}
}

func (d *webhookDispatcher) OnEvent(_ context.Context, e eventbus.Event) {
	// Internal SSE bridge events are not delivered as webhooks.
	if e.Type == "board.refresh_needed" || e.Type == "board.members_updated" {
		return
	}
	go d.enqueueWork(e)
}

func (d *webhookDispatcher) enqueueWork(e eventbus.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hooks, err := d.store.ListWebhooksByProject(ctx, e.ProjectID)
	if err != nil {
		d.logger.Printf("webhook dispatcher: list hooks for project %d: %v", e.ProjectID, err)
		return
	}

	body, err := json.Marshal(struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Timestamp string          `json:"timestamp"`
		ProjectID int64           `json:"projectId"`
		Payload   json.RawMessage `json:"payload,omitempty"`
	}{
		ID:        e.ID,
		Type:      e.Type,
		Timestamp: e.Time.Format("2006-01-02T15:04:05Z07:00"),
		ProjectID: e.ProjectID,
		Payload:   e.Payload,
	})
	if err != nil {
		d.logger.Printf("webhook dispatcher: marshal event %s: %v", e.ID, err)
		return
	}

	for _, h := range hooks {
		if !matchesEventType(h.Events, e.Type) {
			continue
		}
		d.queue.Enqueue(webhookDelivery{
			WebhookID: h.ID,
			URL:       h.URL,
			Secret:    h.Secret,
			EventID:   e.ID,
			EventType: e.Type,
			Timestamp: e.Time,
			Body:      body,
		})
	}
}

func matchesEventType(subscribed []string, eventType string) bool {
	for _, s := range subscribed {
		if s == eventType || s == "*" {
			return true
		}
	}
	return false
}
