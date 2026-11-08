package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

// webhookDispatcher is an eventbus.Consumer that matches events against
// webhook subscriptions and enqueues deliveries asynchronously.
type webhookDispatcher struct {
	store           webhookDispatchStore
	queue           *webhookQueue
	durableNotifier durableWebhookNotifier
	logger          *log.Logger
}

type webhookDispatchStore interface {
	ListWebhooksByProject(context.Context, int64) ([]store.WebhookRow, error)
	AdmitDurableWebhookDelivery(context.Context, store.AdmitDurableWebhookDeliveryInput) (string, error)
}

func newWebhookDispatcher(store webhookDispatchStore, queue *webhookQueue, logger *log.Logger) *webhookDispatcher {
	return newWebhookDispatcherWithDurableNotifier(store, queue, nil, logger)
}

func newWebhookDispatcherWithDurableNotifier(store webhookDispatchStore, queue *webhookQueue, notifier durableWebhookNotifier, logger *log.Logger) *webhookDispatcher {
	return &webhookDispatcher{store: store, queue: queue, durableNotifier: notifier, logger: logger}
}

func (d *webhookDispatcher) OnEvent(_ context.Context, e eventbus.Event) {
	// Internal routing events are not delivered as webhooks.
	if e.Type == "board.refresh_needed" ||
		e.Type == "board.members_updated" ||
		e.Type == eventbus.TodoCreatorNotificationRequestedEventType ||
		e.Type == eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
		return
	}
	// Fanout calls consumers synchronously in order (SSE bridge first). Do not block this path on
	// DB or other slow work — enqueue in a goroutine so webhook ListWebhooksByProject cannot delay SSE.
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

	body, err := canonicalWebhookBody(e)
	if err != nil {
		d.logger.Printf("webhook dispatcher: marshal event %s: %v", e.ID, err)
		return
	}

	for _, h := range hooks {
		if !matchesEventType(h.Events, e.Type) {
			continue
		}
		if h.DeliveryMode == store.WebhookDeliveryDurable {
			// The first durable slice is deliberately limited to todo.created.
			// Corrupt or future subscription rows must not widen that boundary.
			if e.Type != eventbus.TodoCreatedEventType {
				continue
			}
			admission, err := d.store.AdmitDurableWebhookDelivery(ctx, store.AdmitDurableWebhookDeliveryInput{
				WebhookID:               h.ID,
				ProjectID:               e.ProjectID,
				AuthorizationGeneration: h.AuthorizationGeneration,
				EventID:                 e.ID,
				EventType:               e.Type,
				EventTimestamp:          e.Time,
				Body:                    body,
			})
			if err != nil {
				d.logger.Printf("durable webhook admission failed webhook_id=%d event=%s: %v", h.ID, e.ID, err)
				continue // Never fall back to the in-memory queue.
			}
			if admission == store.DurableWebhookAdmissionDuplicate {
				d.logger.Printf("durable webhook admission duplicate webhook_id=%d event=%s", h.ID, e.ID)
			}
			if admission == store.DurableWebhookAdmissionInserted && d.durableNotifier != nil {
				d.durableNotifier.Notify()
			}
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

func canonicalWebhookBody(e eventbus.Event) ([]byte, error) {
	return json.Marshal(struct {
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
}

func matchesEventType(subscribed []string, eventType string) bool {
	for _, s := range subscribed {
		if s == eventType || s == "*" {
			return true
		}
	}
	return false
}
