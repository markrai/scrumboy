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

func TestCreatorNotificationAuthorizationEventsRemainInternal(t *testing.T) {
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
	creator, err := st.CreateUser(context.Background(), "containment-creator@example.com", "password123", "Creator")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, creator.ID, store.RoleViewer); err != nil {
		t.Fatalf("add creator member: %v", err)
	}
	for _, events := range [][]string{
		{eventbus.TodoCreatorNotificationRequestedEventType},
		{eventbus.TodoCreatorNotificationRecipientAuthorizedEventType},
		{"*"},
	} {
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
	creatorEvents, unsubscribeCreator := hub.SubscribeUser(creator.ID)
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
	server := &Server{
		fanout:                        fanout,
		creatorNotificationAuthorizer: todoapp.NewCreatorNotificationAuthorizationService(st),
	}
	request := todoapp.CreatorNotificationRequest{
		ProjectID:       project.ID,
		ProjectSlug:     project.Slug,
		TodoID:          71,
		LocalID:         4,
		Title:           "Internal title",
		ActivityReason:  "todo_updated",
		CreatedByUserID: creator.ID,
		ActorUserID:     owner.ID,
	}

	t.Run("cancelled authorization lookup fails closed", func(t *testing.T) {
		collector.events = nil
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		server.PublishCreatorNotificationRequest(ctx, request)
		if len(collector.events) != 1 || collector.events[0].Type != eventbus.TodoCreatorNotificationRequestedEventType {
			t.Fatalf("internal collector events=%+v, want request only", collector.events)
		}
		assertCreatorAuthorizationNoDelivery(t, projectEvents, creatorEvents, webhookQueue, mailQueue)
	})

	t.Run("current member produces contained authorized decision", func(t *testing.T) {
		collector.events = nil
		server.PublishCreatorNotificationRequest(context.Background(), request)
		if len(collector.events) != 2 ||
			collector.events[0].Type != eventbus.TodoCreatorNotificationRequestedEventType ||
			collector.events[1].Type != eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
			t.Fatalf("internal collector events=%+v, want request then authorized recipient", collector.events)
		}
		for _, event := range collector.events {
			if event.ID == "" || event.Time.IsZero() {
				t.Fatalf("internal event lacks fanout identity/time: %+v", event)
			}
		}
		assertCreatorAuthorizationNoDelivery(t, projectEvents, creatorEvents, webhookQueue, mailQueue)
	})

	t.Run("removed member produces no recipient", func(t *testing.T) {
		if err := st.RemoveProjectMember(ownerCtx, owner.ID, project.ID, creator.ID); err != nil {
			t.Fatalf("remove creator: %v", err)
		}
		collector.events = nil
		server.PublishCreatorNotificationRequest(context.Background(), request)
		if len(collector.events) != 1 || collector.events[0].Type != eventbus.TodoCreatorNotificationRequestedEventType {
			t.Fatalf("internal collector events=%+v, want denied request only", collector.events)
		}
		assertCreatorAuthorizationNoDelivery(t, projectEvents, creatorEvents, webhookQueue, mailQueue)
	})
}

func assertCreatorAuthorizationNoDelivery(
	t *testing.T,
	projectEvents <-chan []byte,
	creatorEvents <-chan []byte,
	webhookQueue *webhookQueue,
	mailQueue *mailQueue,
) {
	t.Helper()
	select {
	case message := <-projectEvents:
		t.Fatalf("creator authorization leaked to project SSE: %s", message)
	case message := <-creatorEvents:
		t.Fatalf("creator authorization leaked to creator SSE: %s", message)
	case <-time.After(100 * time.Millisecond):
	}
	if deliveries := webhookQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("exact/wildcard webhooks received creator authorization events: %+v", deliveries)
	}
	if deliveries := mailQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("email queue received creator authorization events: %+v", deliveries)
	}
}
