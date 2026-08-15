package httpapi

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func TestCreatorNotificationRequestRemainsInternalWithCancelledContext(t *testing.T) {
	st := newTestStore(t)
	owner, err := st.BootstrapUser(context.Background(), "containment-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	ownerCtx := store.WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator request containment")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, events := range [][]string{{eventbus.TodoCreatorNotificationRequestedEventType}, {"*"}} {
		if _, err := st.CreateWebhook(ownerCtx, owner.ID, store.CreateWebhookInput{
			ProjectID: project.ID,
			URL:       "https://example.invalid/hook",
			Events:    events,
		}); err != nil {
			t.Fatalf("create webhook %v: %v", events, err)
		}
	}

	logger := log.New(io.Discard, "", 0)
	hub := NewHub(defaultSubscriberBuffer)
	projectEvents, unsubscribeProject := hub.Subscribe(project.ID)
	defer unsubscribeProject()
	creatorEvents, unsubscribeCreator := hub.SubscribeUser(91)
	defer unsubscribeCreator()
	webhookQueue := newWebhookQueue(logger)
	mailQueue := newMailQueue(logger)
	collector := &collectingConsumer{}
	fanout := eventbus.NewFanout(
		newSSEBridge(hub),
		newWebhookDispatcher(st, webhookQueue, logger),
		newPushNotifier(st, logger, "public", "private", "mailto:test@example.com", true, false),
		newEmailNotifier(st, mailQueue, "https://scrumboy.example.com", true, logger),
		collector,
	)
	server := &Server{fanout: fanout}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server.PublishCreatorNotificationRequest(ctx, todoapp.CreatorNotificationRequest{
		ProjectID:       project.ID,
		ProjectSlug:     project.Slug,
		TodoID:          71,
		LocalID:         4,
		Title:           "Internal title",
		ActivityReason:  "todo_updated",
		CreatedByUserID: 91,
		ActorUserID:     owner.ID,
	})

	if len(collector.events) != 1 || collector.events[0].Type != eventbus.TodoCreatorNotificationRequestedEventType {
		t.Fatalf("internal collector events=%+v, want one request", collector.events)
	}
	if collector.events[0].ID == "" || collector.events[0].Time.IsZero() {
		t.Fatalf("internal event lacks fanout identity/time: %+v", collector.events[0])
	}

	select {
	case message := <-projectEvents:
		t.Fatalf("request leaked to project SSE: %s", message)
	case message := <-creatorEvents:
		t.Fatalf("request leaked to creator SSE: %s", message)
	case <-time.After(100 * time.Millisecond):
	}
	if deliveries := webhookQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("exact/wildcard webhooks received internal request: %+v", deliveries)
	}
	if deliveries := mailQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("email queue received internal request: %+v", deliveries)
	}
}
