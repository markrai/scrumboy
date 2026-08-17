package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"testing"
	"time"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func TestCreatorNotificationPipelineContainmentAndPrivateSSE(t *testing.T) {
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
	authorizer := todoapp.NewCreatorNotificationAuthorizationService(st)
	fanout := eventbus.NewFanout(
		newSSEBridge(hub, authorizer),
		newWebhookDispatcher(st, webhookQueue, logger),
		newPushNotifier(st, logger, "public", "private", "mailto:test@example.com", true, false),
		newEmailNotifier(st, mailQueue, "https://scrumboy.example.com", true, logger),
		collector,
	)
	server := &Server{
		fanout:                        fanout,
		creatorNotificationAuthorizer: authorizer,
	}
	request := todoapp.CreatorNotificationRequest{
		ProjectID:             project.ID,
		ProjectSlug:           project.Slug,
		TodoID:                71,
		LocalID:               4,
		Title:                 "Internal title",
		ActivityReason:        "todo_updated",
		CreatedByUserID:       creator.ID,
		ActorUserID:           owner.ID,
		MaterialChanged:       true,
		CardActivityCandidate: true,
	}

	t.Run("cancelled authorization lookup fails closed", func(t *testing.T) {
		collector.events = nil
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		server.PublishCreatorNotificationRequest(ctx, request)
		if len(collector.events) != 1 || collector.events[0].Type != eventbus.TodoCreatorNotificationRequestedEventType {
			t.Fatalf("internal collector events=%+v, want request only", collector.events)
		}
		assertCreatorPipelineNoDelivery(t, projectEvents, creatorEvents, webhookQueue, mailQueue)
	})

	t.Run("current member produces one private SSE and no other delivery", func(t *testing.T) {
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
		select {
		case message := <-creatorEvents:
			var wire creatorActivityWireEvent
			if err := json.Unmarshal(message, &wire); err != nil {
				t.Fatalf("decode creator activity: %v", err)
			}
			if wire.Type != "todo.creator_activity" || wire.ProjectID != project.ID ||
				wire.ProjectSlug != project.Slug || wire.Payload.Title != request.Title {
				t.Fatalf("creator activity=%+v", wire)
			}
		default:
			t.Fatal("current creator received no private SSE")
		}
		assertCreatorPipelineNoNonSSEDelivery(t, projectEvents, webhookQueue, mailQueue, 1)
		assertNoCreatorActivityHubMessage(t, creatorEvents, "duplicate creator")
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
		assertCreatorPipelineNoDelivery(t, projectEvents, creatorEvents, webhookQueue, mailQueue)
	})
}

func assertCreatorPipelineNoDelivery(
	t *testing.T,
	projectEvents <-chan []byte,
	creatorEvents <-chan []byte,
	webhookQueue *webhookQueue,
	mailQueue *mailQueue,
) {
	t.Helper()
	assertCreatorPipelineNoNonSSEDelivery(t, projectEvents, webhookQueue, mailQueue, 0)
	select {
	case message := <-creatorEvents:
		t.Fatalf("denied creator pipeline leaked to creator SSE: %s", message)
	case <-time.After(100 * time.Millisecond):
	}
}

func assertCreatorPipelineNoNonSSEDelivery(
	t *testing.T,
	projectEvents <-chan []byte,
	webhookQueue *webhookQueue,
	mailQueue *mailQueue,
	wantMailCandidates int,
) {
	t.Helper()
	select {
	case message := <-projectEvents:
		t.Fatalf("creator pipeline leaked to project SSE: %s", message)
	default:
	}
	if deliveries := webhookQueue.Drain(); len(deliveries) != 0 {
		t.Fatalf("exact/wildcard webhooks received creator internal events: %+v", deliveries)
	}
	deliveries := drainAfterAsync(mailQueue)
	if len(deliveries) != wantMailCandidates {
		t.Fatalf("email candidates=%+v, want %d", deliveries, wantMailCandidates)
	}
	for _, delivery := range deliveries {
		if delivery.Prepare == nil || delivery.To != "" || delivery.Subject != "" || delivery.Body != "" {
			t.Fatalf("creator queue item was not a deferred minimum-data candidate: %+v", delivery)
		}
		prepared, ok, err := delivery.Prepare(context.Background())
		if err != nil || ok || prepared.To != "" || prepared.Subject != "" || prepared.Body != "" {
			t.Fatalf("default master-off candidate did not drop at send time: %+v ok=%v err=%v", prepared, ok, err)
		}
	}
}
