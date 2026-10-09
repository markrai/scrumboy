package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"scrumboy/internal/eventbus"
)

func assertNoPublicEvent(t *testing.T, subscription *PublicSubscription, label string) {
	t.Helper()
	select {
	case event := <-subscription.Events:
		t.Fatalf("%s translated to public event %v", label, event)
	default:
	}
}

func TestPublicSSEBridgeTranslatesOnlyContextMarkedProjectionChanges(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 4})
	subscription, err := hub.Subscribe(7, "ip:test")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	bridge := newPublicSSEBridge(hub)

	payload, err := json.Marshal(refreshNeededPayload{
		Reason:      "todo_updated",
		ActorUserID: 99,
		Title:       "private title sentinel",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	marked := withPublicProjectionChange(context.Background(), true)
	bridge.OnEvent(marked, eventbus.Event{
		ID: "private-event-id", Type: "board.refresh_needed", ProjectID: 7, Payload: payload,
	})
	if event, open := receivePublicEvent(t, subscription.Events); !open || event != PublicEventRefreshNeeded {
		t.Fatalf("translated event/open = %v/%v", event, open)
	}

	assignment, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: 7, TodoID: 88, Title: "private assignment sentinel", ActorUserID: 99,
	})
	if err != nil {
		t.Fatalf("Marshal assignment: %v", err)
	}
	bridge.OnEvent(marked, eventbus.Event{Type: "todo.assigned", ProjectID: 7, Payload: assignment})
	if event, open := receivePublicEvent(t, subscription.Events); !open || event != PublicEventRefreshNeeded {
		t.Fatalf("assignment-backed projection event/open = %v/%v", event, open)
	}

	// Unmarked and explicitly false publications never reach public output.
	bridge.OnEvent(context.Background(), eventbus.Event{Type: "board.refresh_needed", ProjectID: 7, Payload: payload})
	bridge.OnEvent(withPublicProjectionChange(marked, false), eventbus.Event{Type: "todo.assigned", ProjectID: 7, Payload: assignment})
	assertNoPublicEvent(t, subscription, "unmarked publication")
}

func TestPublicSSEBridgeRejectsPrivateEventClassesAndPayloadForgery(t *testing.T) {
	privateTypes := []string{
		"board.members_updated",
		"project.membership",
		"wall.refresh_needed",
		"wall.transient",
		eventbus.TodoCreatorNotificationRequestedEventType,
		eventbus.TodoCreatorNotificationRecipientAuthorizedEventType,
		"unknown.event",
	}
	for _, eventType := range privateTypes {
		t.Run(eventType, func(t *testing.T) {
			hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
			subscription, _ := hub.Subscribe(7, "ip:test")
			// Even an inherited true marker cannot promote a private event class.
			newPublicSSEBridge(hub).OnEvent(withPublicProjectionChange(context.Background(), true), eventbus.Event{
				Type: eventType, ProjectID: 7, Payload: json.RawMessage(`{"publicProjectionChanged":true,"secret":"sentinel"}`),
			})
			assertNoPublicEvent(t, subscription, eventType)
		})
	}

	for _, testEvent := range []eventbus.Event{
		{Type: "board.refresh_needed", ProjectID: 7, Payload: json.RawMessage(`{"reason":"agenda_updated"}`)},
		{Type: "board.refresh_needed", ProjectID: 7, Payload: json.RawMessage(`{"publicProjectionChanged":true}`)},
		{Type: "todo.assigned", ProjectID: 7, Payload: json.RawMessage(`{"projectId":7,"publicProjectionChanged":true}`)},
		{Type: "board.refresh_needed", ProjectID: 7, Payload: json.RawMessage(`not-json`)},
	} {
		hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
		subscription, _ := hub.Subscribe(7, "ip:test")
		newPublicSSEBridge(hub).OnEvent(context.Background(), testEvent)
		assertNoPublicEvent(t, subscription, string(testEvent.Payload))
	}

	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
	subscription, _ := hub.Subscribe(7, "ip:test")
	newPublicSSEBridge(hub).OnEvent(withPublicProjectionChange(context.Background(), true), eventbus.Event{
		Type: "board.refresh_needed", ProjectID: 0,
	})
	assertNoPublicEvent(t, subscription, "zero project")
}

func TestPublicProjectionMarkerIsNeverSerialized(t *testing.T) {
	refreshBytes, err := json.Marshal(refreshNeededPayload{Reason: "todo_updated"})
	if err != nil {
		t.Fatalf("Marshal refresh: %v", err)
	}
	assignmentBytes, err := json.Marshal(eventbus.TodoAssignedPayload{ProjectID: 7, TodoID: 1, LocalID: 1, Title: "x"})
	if err != nil {
		t.Fatalf("Marshal assignment: %v", err)
	}
	for _, encoded := range [][]byte{refreshBytes, assignmentBytes} {
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, present := fields["publicProjectionChanged"]; present {
			t.Fatalf("internal marker serialized into private payload: %s", encoded)
		}
	}
}

func TestPublicEventWirePayloadsAreExactAndContentFree(t *testing.T) {
	if got, want := string(publicRefreshNeededPayload), `{"type":"refresh_needed"}`; got != want {
		t.Fatalf("refresh payload = %q, want %q", got, want)
	}
	if got, want := string(publicAccessRevokedPayload), `{"type":"access_revoked"}`; got != want {
		t.Fatalf("revoke payload = %q, want %q", got, want)
	}
}
