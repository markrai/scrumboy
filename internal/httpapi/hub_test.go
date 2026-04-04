package httpapi

import (
	"testing"
	"time"
)

func TestHub_EmitDeliversToProjectSubscribers(t *testing.T) {
	h := NewHub(4)
	ch, _ := h.Subscribe(42)

	h.Emit(42, []byte(`{"type":"refresh_needed","projectId":42}`))

	select {
	case msg := <-ch:
		if string(msg) == "" {
			t.Fatalf("expected non-empty event payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for event")
	}
}

func TestHub_EmitIsProjectScoped(t *testing.T) {
	h := NewHub(4)
	chA, _ := h.Subscribe(1)
	chB, _ := h.Subscribe(2)

	h.Emit(1, []byte(`{"type":"refresh_needed","projectId":1}`))

	select {
	case <-chA:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected event for project subscriber")
	}

	select {
	case <-chB:
		t.Fatalf("did not expect event for different project")
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

func TestHub_BackpressureDropsSlowSubscriber(t *testing.T) {
	h := NewHub(1)
	ch, _ := h.Subscribe(5)

	h.Emit(5, []byte("first"))
	h.Emit(5, []byte("second")) // overflow -> subscriber is removed and closed

	msg, ok := <-ch
	if !ok {
		t.Fatalf("expected buffered message before closure")
	}
	if string(msg) != "first" {
		t.Fatalf("expected first buffered message, got %q", string(msg))
	}

	_, ok = <-ch
	if ok {
		t.Fatalf("expected subscriber channel to be closed after overflow")
	}
}

func TestHub_UnsubscribeClosesSubscriberChannel(t *testing.T) {
	h := NewHub(4)
	ch, unsubscribe := h.Subscribe(7)
	unsubscribe()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected closed channel after unsubscribe")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for unsubscribe close")
	}
}

func TestHub_EmitUserDeliversToUserSubscribers(t *testing.T) {
	h := NewHub(4)
	ch, _ := h.SubscribeUser(99)

	h.EmitUser(99, []byte(`{"type":"todo.assigned","id":"x"}`))

	select {
	case msg := <-ch:
		if string(msg) == "" {
			t.Fatalf("expected non-empty event payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for user event")
	}
}

func TestHub_EmitUserIsUserScoped(t *testing.T) {
	h := NewHub(4)
	chA, _ := h.SubscribeUser(1)
	chB, _ := h.SubscribeUser(2)

	h.EmitUser(1, []byte(`hello`))

	select {
	case msg := <-chA:
		if string(msg) != "hello" {
			t.Fatalf("unexpected payload %q", string(msg))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected event for user subscriber")
	}

	select {
	case <-chB:
		t.Fatalf("did not expect event for different user")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHub_EmitUserBackpressureDropsSlowSubscriber(t *testing.T) {
	h := NewHub(1)
	ch, _ := h.SubscribeUser(5)

	h.EmitUser(5, []byte("first"))
	h.EmitUser(5, []byte("second"))

	msg, ok := <-ch
	if !ok {
		t.Fatalf("expected buffered message before closure")
	}
	if string(msg) != "first" {
		t.Fatalf("expected first buffered message, got %q", string(msg))
	}

	_, ok = <-ch
	if ok {
		t.Fatalf("expected subscriber channel to be closed after overflow")
	}
}
