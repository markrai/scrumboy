package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type creatorActivityAccessStoreFake struct {
	project      store.Project
	projectErr   error
	role         store.ProjectRole
	roleErr      error
	honorContext bool
	projectCalls []int64
	roleCalls    [][2]int64
}

func (f *creatorActivityAccessStoreFake) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	f.projectCalls = append(f.projectCalls, projectID)
	if f.honorContext && ctx.Err() != nil {
		return store.Project{}, ctx.Err()
	}
	return f.project, f.projectErr
}

func (f *creatorActivityAccessStoreFake) GetProjectRole(_ context.Context, projectID int64, userID int64) (store.ProjectRole, error) {
	f.roleCalls = append(f.roleCalls, [2]int64{projectID, userID})
	return f.role, f.roleErr
}

func creatorActivityAuthorizedPayload() eventbus.TodoCreatorNotificationRecipientAuthorizedPayload {
	return eventbus.TodoCreatorNotificationRecipientAuthorizedPayload{
		ProjectID:       7,
		ProjectSlug:     "authorization-time-slug",
		TodoID:          81,
		LocalID:         5,
		Title:           "Committed title",
		ActivityReason:  todoapp.RefreshReasonTodoUpdated,
		RecipientUserID: 11,
		ActorUserID:     22,
	}
}

func creatorActivityAuthorizedEvent(t *testing.T, payload eventbus.TodoCreatorNotificationRecipientAuthorizedPayload) eventbus.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal authorized payload: %v", err)
	}
	return eventbus.Event{
		ID:        "authorized-event-id",
		Type:      eventbus.TodoCreatorNotificationRecipientAuthorizedEventType,
		ProjectID: payload.ProjectID,
		Payload:   raw,
	}
}

func TestCreatorActivitySSEReauthorizesAndEmitsOnlyToRecipient(t *testing.T) {
	access := &creatorActivityAccessStoreFake{
		project: store.Project{ID: 7, Slug: "delivery-time-slug"},
		role:    store.RoleViewer,
	}
	hub := NewHub(defaultSubscriberBuffer)
	recipientEvents, unsubscribeRecipient := hub.SubscribeUser(11)
	defer unsubscribeRecipient()
	otherUserEvents, unsubscribeOther := hub.SubscribeUser(12)
	defer unsubscribeOther()
	projectEvents, unsubscribeProject := hub.Subscribe(7)
	defer unsubscribeProject()
	bridge := newSSEBridge(hub, todoapp.NewCreatorNotificationAuthorizationService(access))

	bridge.OnEvent(context.Background(), creatorActivityAuthorizedEvent(t, creatorActivityAuthorizedPayload()))

	var rawWire []byte
	var wire creatorActivityWireEvent
	select {
	case raw := <-recipientEvents:
		rawWire = raw
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("decode creator activity wire event: %v", err)
		}
	default:
		t.Fatal("current creator received no private SSE event")
	}
	if wire.ID != "authorized-event-id" || wire.Type != "todo.creator_activity" ||
		wire.ProjectID != 7 || wire.ProjectSlug != "delivery-time-slug" ||
		wire.Payload.TodoID != 81 || wire.Payload.LocalID != 5 ||
		wire.Payload.Title != "Committed title" ||
		wire.Payload.ActivityReason != todoapp.RefreshReasonTodoUpdated {
		t.Fatalf("creator activity wire payload=%+v", wire)
	}
	if bytes.Contains(rawWire, []byte("actorUserId")) || bytes.Contains(rawWire, []byte("recipientUserId")) {
		t.Fatalf("creator activity wire exposes internal user identifiers: %s", rawWire)
	}
	if wire.Type == eventbus.TodoCreatorNotificationRecipientAuthorizedEventType {
		t.Fatal("internal authorized-recipient event was emitted verbatim")
	}
	assertNoCreatorActivityHubMessage(t, recipientEvents, "duplicate recipient")
	assertNoCreatorActivityHubMessage(t, otherUserEvents, "other user")
	assertNoCreatorActivityHubMessage(t, projectEvents, "project")
	if !reflect.DeepEqual(access.projectCalls, []int64{7}) ||
		!reflect.DeepEqual(access.roleCalls, [][2]int64{{7, 11}}) {
		t.Fatalf("delivery access calls project=%v role=%v", access.projectCalls, access.roleCalls)
	}
}

func TestCreatorActivitySSERemovalAfterPhaseThreeFailsClosed(t *testing.T) {
	access := &creatorActivityAccessStoreFake{
		project: store.Project{ID: 7, Slug: "durable"},
		role:    store.RoleViewer,
	}
	service := todoapp.NewCreatorNotificationAuthorizationService(access)
	request := todoapp.CreatorNotificationRequest{
		ProjectID: 7, ProjectSlug: "mutation-slug", TodoID: 81, LocalID: 5,
		Title: "Sensitive committed title", ActivityReason: todoapp.RefreshReasonTodoUpdated,
		CreatedByUserID: 11, ActorUserID: 22,
	}
	authorized, ok, err := service.Authorize(context.Background(), request)
	if err != nil || !ok {
		t.Fatalf("Phase 3 authorization = (%+v, %v, %v), want authorized", authorized, ok, err)
	}

	access.role = ""
	hub := NewHub(defaultSubscriberBuffer)
	recipientEvents, unsubscribe := hub.SubscribeUser(11)
	defer unsubscribe()
	bridge := newSSEBridge(hub, service)
	payload := eventbus.TodoCreatorNotificationRecipientAuthorizedPayload{
		ProjectID: authorized.ProjectID, ProjectSlug: authorized.ProjectSlug,
		TodoID: authorized.TodoID, LocalID: authorized.LocalID, Title: authorized.Title,
		ActivityReason: authorized.ActivityReason, RecipientUserID: authorized.RecipientUserID,
		ActorUserID: authorized.ActorUserID,
	}
	bridge.OnEvent(context.Background(), creatorActivityAuthorizedEvent(t, payload))

	assertNoCreatorActivityHubMessage(t, recipientEvents, "removed recipient")
	if !reflect.DeepEqual(access.projectCalls, []int64{7, 7}) ||
		!reflect.DeepEqual(access.roleCalls, [][2]int64{{7, 11}, {7, 11}}) {
		t.Fatalf("access calls project=%v role=%v, want independent Phase 3 and delivery reads", access.projectCalls, access.roleCalls)
	}
}

func TestCreatorActivitySSEFailClosedMatrix(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	lookupErr := errors.New("lookup failure")
	tests := []struct {
		name          string
		access        *creatorActivityAccessStoreFake
		mutatePayload func(*eventbus.TodoCreatorNotificationRecipientAuthorizedPayload)
		mutateEvent   func(*eventbus.Event)
		cancel        bool
	}{
		{name: "project deleted or unavailable", access: &creatorActivityAccessStoreFake{projectErr: lookupErr}},
		{name: "temporary project", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "temporary", ExpiresAt: &expires}, role: store.RoleViewer}},
		{name: "recipient deleted or nonexistent", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}}},
		{name: "membership lookup error", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, roleErr: lookupErr}},
		{name: "cancelled delivery authorization", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer, honorContext: true}, cancel: true},
		{name: "self recipient", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer}, mutatePayload: func(p *eventbus.TodoCreatorNotificationRecipientAuthorizedPayload) { p.ActorUserID = p.RecipientUserID }},
		{name: "malformed recipient", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer}, mutatePayload: func(p *eventbus.TodoCreatorNotificationRecipientAuthorizedPayload) { p.RecipientUserID = 0 }},
		{name: "event project mismatch", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer}, mutateEvent: func(e *eventbus.Event) { e.ProjectID = 8 }},
		{name: "malformed payload", access: &creatorActivityAccessStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer}, mutateEvent: func(e *eventbus.Event) { e.Payload = json.RawMessage(`{"projectId":`) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := creatorActivityAuthorizedPayload()
			if tt.mutatePayload != nil {
				tt.mutatePayload(&payload)
			}
			event := creatorActivityAuthorizedEvent(t, payload)
			if tt.mutateEvent != nil {
				tt.mutateEvent(&event)
			}
			hub := NewHub(defaultSubscriberBuffer)
			recipientEvents, unsubscribeRecipient := hub.SubscribeUser(11)
			defer unsubscribeRecipient()
			projectEvents, unsubscribeProject := hub.Subscribe(7)
			defer unsubscribeProject()
			bridge := newSSEBridge(hub, todoapp.NewCreatorNotificationAuthorizationService(tt.access))
			ctx := context.Background()
			if tt.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}

			bridge.OnEvent(ctx, event)

			assertNoCreatorActivityHubMessage(t, recipientEvents, "denied recipient")
			assertNoCreatorActivityHubMessage(t, projectEvents, "denied project")
		})
	}
}

func TestCreatorActivitySSEDeletedRecipientFailsClosed(t *testing.T) {
	st := newTestStore(t)
	owner, err := st.BootstrapUser(context.Background(), "creator-sse-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}
	ownerCtx := store.WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ownerCtx, "Creator SSE deleted recipient")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	recipient, err := st.CreateUser(context.Background(), "creator-sse-deleted@example.com", "password123", "Recipient")
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	if err := st.AddProjectMember(ownerCtx, owner.ID, project.ID, recipient.ID, store.RoleViewer); err != nil {
		t.Fatalf("add recipient: %v", err)
	}

	hub := NewHub(defaultSubscriberBuffer)
	recipientEvents, unsubscribe := hub.SubscribeUser(recipient.ID)
	defer unsubscribe()
	if err := st.DeleteUser(ownerCtx, owner.ID, recipient.ID); err != nil {
		t.Fatalf("delete recipient: %v", err)
	}
	payload := creatorActivityAuthorizedPayload()
	payload.ProjectID = project.ID
	payload.ProjectSlug = project.Slug
	payload.RecipientUserID = recipient.ID
	payload.ActorUserID = owner.ID
	bridge := newSSEBridge(hub, todoapp.NewCreatorNotificationAuthorizationService(st))

	bridge.OnEvent(context.Background(), creatorActivityAuthorizedEvent(t, payload))

	assertNoCreatorActivityHubMessage(t, recipientEvents, "deleted recipient")
}

func TestCreatorActivitySSEIgnoresInternalRequestEvenIfMembershipIsAddedLater(t *testing.T) {
	access := &creatorActivityAccessStoreFake{
		project: store.Project{ID: 7, Slug: "durable"},
		role:    store.RoleViewer,
	}
	hub := NewHub(defaultSubscriberBuffer)
	recipientEvents, unsubscribe := hub.SubscribeUser(11)
	defer unsubscribe()
	bridge := newSSEBridge(hub, todoapp.NewCreatorNotificationAuthorizationService(access))
	raw, err := json.Marshal(eventbus.TodoCreatorNotificationRequestedPayload{
		ProjectID: 7, ProjectSlug: "durable", TodoID: 81, LocalID: 5,
		Title: "Never disclose", ActivityReason: todoapp.RefreshReasonTodoUpdated,
		CreatedByUserID: 11, ActorUserID: 22,
	})
	if err != nil {
		t.Fatal(err)
	}

	bridge.OnEvent(context.Background(), eventbus.Event{
		ID: "request-only", Type: eventbus.TodoCreatorNotificationRequestedEventType,
		ProjectID: 7, Payload: raw,
	})

	assertNoCreatorActivityHubMessage(t, recipientEvents, "request-only recipient")
	if len(access.projectCalls) != 0 || len(access.roleCalls) != 0 {
		t.Fatalf("internal request unexpectedly invoked delivery authorization: project=%v role=%v", access.projectCalls, access.roleCalls)
	}
}

func assertNoCreatorActivityHubMessage(t *testing.T, ch <-chan []byte, channel string) {
	t.Helper()
	select {
	case message := <-ch:
		t.Fatalf("unexpected creator activity on %s channel: %s", channel, message)
	default:
	}
}
