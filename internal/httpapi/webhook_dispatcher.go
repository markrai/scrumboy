package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

const (
	defaultWebhookSnapshotTimeout  = 5 * time.Second
	defaultWebhookAdmissionTimeout = 5 * time.Second
	defaultWebhookAdmissionSlots   = 16
)

type webhookDispatcher struct {
	store           webhookDispatchStore
	queue           *webhookQueue
	durableNotifier durableWebhookNotifier
	logger          *log.Logger
	admissionSlots  chan struct{}
	mu              sync.Mutex
	closed          bool
	wg              sync.WaitGroup
}

type webhookDispatchStore interface {
	ListWebhooksByProject(context.Context, int64) ([]store.WebhookRow, error)
	AdmitDurableWebhookDelivery(context.Context, store.AdmitDurableWebhookDeliveryInput) (string, error)
}

type durableWebhookAdmissionSnapshot struct {
	webhookID               int64
	authorizationGeneration int64
}

func newWebhookDispatcher(st webhookDispatchStore, queue *webhookQueue, logger *log.Logger) *webhookDispatcher {
	return newWebhookDispatcherWithDurableNotifier(st, queue, nil, logger)
}

func newWebhookDispatcherWithDurableNotifier(st webhookDispatchStore, queue *webhookQueue, notifier durableWebhookNotifier, logger *log.Logger) *webhookDispatcher {
	return &webhookDispatcher{
		store:           st,
		queue:           queue,
		durableNotifier: notifier,
		logger:          logger,
		admissionSlots:  make(chan struct{}, defaultWebhookAdmissionSlots),
	}
}

func (d *webhookDispatcher) OnEvent(_ context.Context, e eventbus.Event) {
	if e.Type == "board.refresh_needed" ||
		e.Type == "board.members_updated" ||
		e.Type == eventbus.TodoCreatorNotificationRequestedEventType ||
		e.Type == eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
		return
	}
	if e.Type != eventbus.TodoCreatedEventType {
		go d.dispatchBestEffort(e)
		return
	}
	d.dispatchTodoCreated(e)
}

func (d *webhookDispatcher) dispatchTodoCreated(e eventbus.Event) {
	snapshotCtx, cancel := context.WithTimeout(context.Background(), defaultWebhookSnapshotTimeout)
	hooks, err := d.store.ListWebhooksByProject(snapshotCtx, e.ProjectID)
	cancel()
	if err != nil {
		d.logger.Printf("webhook dispatcher: snapshot hooks for project %d: %v", e.ProjectID, err)
		return
	}
	body, err := canonicalWebhookBody(e)
	if err != nil {
		d.logger.Printf("webhook dispatcher: marshal event %s: %v", e.ID, err)
		return
	}
	durable := make([]durableWebhookAdmissionSnapshot, 0, len(hooks))
	for _, hook := range hooks {
		if !matchesEventType(hook.Events, e.Type) {
			continue
		}
		if hook.DeliveryMode == store.WebhookDeliveryDurable {
			if e.Type == eventbus.TodoCreatedEventType {
				durable = append(durable, durableWebhookAdmissionSnapshot{
					webhookID:               hook.ID,
					authorizationGeneration: hook.AuthorizationGeneration,
				})
			}
			continue
		}
		d.queue.Enqueue(webhookDelivery{
			WebhookID: hook.ID,
			URL:       hook.URL,
			Secret:    hook.Secret,
			EventID:   e.ID,
			EventType: e.Type,
			Timestamp: e.Time,
			Body:      body,
		})
	}
	if len(durable) == 0 {
		return
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		d.logger.Printf("durable webhook admission rejected during shutdown event=%s subscriptions=%d", e.ID, len(durable))
		return
	}
	select {
	case d.admissionSlots <- struct{}{}:
		d.wg.Add(1)
		d.mu.Unlock()
		go d.admitSnapshot(e, body, durable)
	default:
		d.mu.Unlock()
		d.logger.Printf("durable webhook admission saturated event=%s subscriptions=%d", e.ID, len(durable))
	}
}

func (d *webhookDispatcher) enqueueWork(e eventbus.Event) {
	if e.Type == eventbus.TodoCreatedEventType {
		d.dispatchTodoCreated(e)
	} else {
		d.dispatchBestEffort(e)
	}
	d.wg.Wait()
}

func (d *webhookDispatcher) dispatchBestEffort(e eventbus.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultWebhookSnapshotTimeout)
	hooks, err := d.store.ListWebhooksByProject(ctx, e.ProjectID)
	cancel()
	if err != nil {
		d.logger.Printf("webhook dispatcher: list hooks for project %d: %v", e.ProjectID, err)
		return
	}
	body, err := canonicalWebhookBody(e)
	if err != nil {
		d.logger.Printf("webhook dispatcher: marshal event %s: %v", e.ID, err)
		return
	}
	for _, hook := range hooks {
		if hook.DeliveryMode == store.WebhookDeliveryDurable || !matchesEventType(hook.Events, e.Type) {
			continue
		}
		d.queue.Enqueue(webhookDelivery{
			WebhookID: hook.ID,
			URL:       hook.URL,
			Secret:    hook.Secret,
			EventID:   e.ID,
			EventType: e.Type,
			Timestamp: e.Time,
			Body:      body,
		})
	}
}

func (d *webhookDispatcher) admitSnapshot(e eventbus.Event, body []byte, hooks []durableWebhookAdmissionSnapshot) {
	defer d.wg.Done()
	defer func() { <-d.admissionSlots }()
	ctx, cancel := context.WithTimeout(context.Background(), defaultWebhookAdmissionTimeout)
	defer cancel()
	for _, hook := range hooks {
		admission, err := d.store.AdmitDurableWebhookDelivery(ctx, store.AdmitDurableWebhookDeliveryInput{
			WebhookID:               hook.webhookID,
			ProjectID:               e.ProjectID,
			AuthorizationGeneration: hook.authorizationGeneration,
			EventID:                 e.ID,
			EventType:               e.Type,
			EventTimestamp:          e.Time,
			Body:                    body,
		})
		if err != nil {
			d.logger.Printf("durable webhook admission failed webhook_id=%d event=%s: %v", hook.webhookID, e.ID, err)
			continue
		}
		if admission == store.DurableWebhookAdmissionDuplicate {
			d.logger.Printf("durable webhook admission duplicate webhook_id=%d event=%s", hook.webhookID, e.ID)
		}
		if admission == store.DurableWebhookAdmissionInserted && d.durableNotifier != nil {
			d.durableNotifier.Notify()
		}
	}
}

func (d *webhookDispatcher) Close(ctx context.Context) bool {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
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
	for _, event := range subscribed {
		if event == eventType || event == "*" {
			return true
		}
	}
	return false
}
