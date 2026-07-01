package eventbus

import (
	"context"
	"reflect"
	"testing"
	"time"
)

type recordedEvent struct {
	ctx   context.Context
	event Event
}

type recordingConsumer struct {
	calls   []recordedEvent
	onEvent func(ctx context.Context, e Event)
}

func (c *recordingConsumer) OnEvent(ctx context.Context, e Event) {
	c.calls = append(c.calls, recordedEvent{ctx: ctx, event: e})
	if c.onEvent != nil {
		c.onEvent(ctx, e)
	}
}

func TestFanoutPublishWithNoConsumersSucceeds(t *testing.T) {
	fanout := NewFanout()

	if err := fanout.Publish(context.Background(), Event{Type: "test.event"}); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
}

func TestFanoutPublishPopulatesMissingIDAndTime(t *testing.T) {
	consumer := &recordingConsumer{}
	fanout := NewFanout(consumer)

	before := time.Now().UTC().Add(-time.Second)
	if err := fanout.Publish(context.Background(), Event{
		Type:      "test.event",
		ProjectID: 123,
	}); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	if len(consumer.calls) != 1 {
		t.Fatalf("consumer calls = %d, want 1", len(consumer.calls))
	}
	got := consumer.calls[0].event
	if got.ID == "" {
		t.Fatal("generated event ID is empty")
	}
	if got.Time.IsZero() {
		t.Fatal("generated event time is zero")
	}
	if got.Time.Location() != time.UTC {
		t.Fatalf("generated event time location = %v, want UTC", got.Time.Location())
	}
	if got.Time.Before(before) || got.Time.After(after) {
		t.Fatalf("generated event time = %v, want between %v and %v", got.Time, before, after)
	}
}

func TestFanoutPublishPreservesExistingIDAndTime(t *testing.T) {
	consumer := &recordingConsumer{}
	fanout := NewFanout(consumer)
	want := Event{
		ID:        "evt-fixed",
		Type:      "test.event",
		Time:      time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC),
		ProjectID: 456,
		Payload:   []byte(`{"ok":true}`),
	}

	if err := fanout.Publish(context.Background(), want); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(consumer.calls) != 1 {
		t.Fatalf("consumer calls = %d, want 1", len(consumer.calls))
	}
	got := consumer.calls[0].event
	if got.ID != want.ID {
		t.Fatalf("event ID = %q, want %q", got.ID, want.ID)
	}
	if !got.Time.Equal(want.Time) {
		t.Fatalf("event time = %v, want %v", got.Time, want.Time)
	}
	if got.Type != want.Type {
		t.Fatalf("event type = %q, want %q", got.Type, want.Type)
	}
	if got.ProjectID != want.ProjectID {
		t.Fatalf("event project ID = %d, want %d", got.ProjectID, want.ProjectID)
	}
	if !reflect.DeepEqual(got.Payload, want.Payload) {
		t.Fatalf("event payload = %s, want %s", got.Payload, want.Payload)
	}
}

func TestFanoutPublishDeliversToConsumersInOrder(t *testing.T) {
	var order []string
	first := &recordingConsumer{onEvent: func(context.Context, Event) { order = append(order, "first") }}
	second := &recordingConsumer{onEvent: func(context.Context, Event) { order = append(order, "second") }}
	third := &recordingConsumer{onEvent: func(context.Context, Event) { order = append(order, "third") }}
	fanout := NewFanout(first, second, third)
	want := Event{
		ID:        "evt-ordered",
		Type:      "test.event",
		Time:      time.Date(2025, time.February, 3, 4, 5, 6, 0, time.UTC),
		ProjectID: 789,
		Payload:   []byte(`{"ordered":true}`),
	}

	if err := fanout.Publish(context.Background(), want); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if !reflect.DeepEqual(order, []string{"first", "second", "third"}) {
		t.Fatalf("delivery order = %v, want [first second third]", order)
	}
	for name, consumer := range map[string]*recordingConsumer{
		"first":  first,
		"second": second,
		"third":  third,
	} {
		if len(consumer.calls) != 1 {
			t.Fatalf("%s consumer calls = %d, want 1", name, len(consumer.calls))
		}
		if !reflect.DeepEqual(consumer.calls[0].event, want) {
			t.Fatalf("%s consumer event = %+v, want %+v", name, consumer.calls[0].event, want)
		}
	}
}

func TestFanoutPublishPassesContextThrough(t *testing.T) {
	type contextKey struct{}

	consumer := &recordingConsumer{}
	fanout := NewFanout(consumer)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "test-value"))
	cancel()

	if err := fanout.Publish(ctx, Event{ID: "evt-context", Time: time.Now().UTC()}); err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	if len(consumer.calls) != 1 {
		t.Fatalf("consumer calls = %d, want 1", len(consumer.calls))
	}
	gotCtx := consumer.calls[0].ctx
	if gotCtx.Value(contextKey{}) != "test-value" {
		t.Fatalf("context value = %v, want test-value", gotCtx.Value(contextKey{}))
	}
	if gotCtx.Err() != context.Canceled {
		t.Fatalf("context error = %v, want %v", gotCtx.Err(), context.Canceled)
	}
}
