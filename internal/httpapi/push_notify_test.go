package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
)

func TestPushNotifier_OnEvent_IgnoresNonTodoAssigned(t *testing.T) {
	st := newTestStore(t)
	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "pub", "priv", "mailto:t@t", false)
	ctx := context.Background()
	p.OnEvent(ctx, eventbus.Event{Type: "todo.created", Payload: []byte(`{}`)})
	// No crash; synchronous return (no vapid check reached for wrong type).
}

func TestPushNotifier_OnEvent_NoVapidDoesNotPanic(t *testing.T) {
	st := newTestStore(t)
	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "", "", "mailto:t@t", false)
	ctx := context.Background()
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:     1,
		TodoID:        1,
		ActorUserID:   2,
		ToAssigneeUID: ptrInt64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	p.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", Payload: payload})
}

func TestPushNotifier_OnEvent_SelfAssignLeavesSubscriptions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "U")
	if err != nil {
		t.Fatal(err)
	}
	ep := "https://push.example.com/sub"
	if err := st.UpsertPushSubscription(ctx, u.ID, ep, "p256", "auth", nil); err != nil {
		t.Fatal(err)
	}

	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "pub", "priv", "mailto:t@t", false)
	assignee := u.ID
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:     1,
		TodoID:        1,
		Title:         "t",
		ActorUserID:   assignee,
		ToAssigneeUID: &assignee,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", ID: "e1", Payload: payload})
	time.Sleep(150 * time.Millisecond)

	subs, err := st.ListPushSubscriptionsByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Endpoint != ep {
		t.Fatalf("self-assign must not remove subscription; got %+v", subs)
	}
}

func ptrInt64(v int64) *int64 { return &v }
