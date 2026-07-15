package httpapi

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestServerClose_PreservesWebhookRetriesDuringActiveFlush is the webhook
// counterpart to the mail active-flush Close test: Close mid-attempt must not
// truncate the remaining retry policy while the Close deadline is still open.
func TestServerClose_PreservesWebhookRetriesDuringActiveFlush(t *testing.T) {
	var sendCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan error)
	var startOnce sync.Once

	q := newWebhookQueue(discardLogger())
	send := func(d webhookDelivery) error {
		n := sendCalls.Add(1)
		if n == 1 {
			startOnce.Do(func() { close(firstStarted) })
			return <-releaseFirst
		}
		return errors.New("fail")
	}
	inner := newRetryWorker(q, discardLogger(), "webhook", send)
	w := &webhookWorker{retryWorker: inner}

	runCtx, runCancel := context.WithCancel(context.Background())
	go w.Run(runCtx)

	q.Enqueue(webhookDelivery{WebhookID: 1, EventID: "e1", URL: "http://example.invalid/hook"})

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first webhook attempt did not start")
	}

	srv := &Server{
		logger:        discardLogger(),
		webhookQueue:  q,
		webhookWorker: w,
		webhookCancel: runCancel,
		webhookDone:   w.Done(),
	}

	closeDone := make(chan struct{})
	go func() {
		srv.Close(context.Background())
		close(closeDone)
	}()

	waitQueueSealed(t, q)
	releaseFirst <- errors.New("transient")

	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after webhook retries completed")
	}

	if got := sendCalls.Load(); got != 3 {
		t.Fatalf("expected 3 webhook send attempts under Close drain, got %d", got)
	}
}

// TestServerClose_WebhookDeadlineExpiresDuringBackoff ensures cancelling the
// Close deadline after attempt one starts neither another attempt nor another
// queued item.
func TestServerClose_WebhookDeadlineExpiresDuringBackoff(t *testing.T) {
	var sendCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan error)
	var startOnce sync.Once

	q := newWebhookQueue(discardLogger())
	send := func(d webhookDelivery) error {
		n := sendCalls.Add(1)
		if n == 1 {
			startOnce.Do(func() { close(firstStarted) })
			return <-releaseFirst
		}
		return errors.New("fail")
	}
	inner := newRetryWorker(q, discardLogger(), "webhook", send)
	w := &webhookWorker{retryWorker: inner}

	runCtx, runCancel := context.WithCancel(context.Background())
	go w.Run(runCtx)

	q.Enqueue(webhookDelivery{WebhookID: 1, EventID: "e1", URL: "http://example.invalid/hook"})
	q.Enqueue(webhookDelivery{WebhookID: 2, EventID: "e2", URL: "http://example.invalid/hook2"})

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first webhook attempt did not start")
	}

	srv := &Server{
		logger:        discardLogger(),
		webhookQueue:  q,
		webhookWorker: w,
		webhookCancel: runCancel,
		webhookDone:   w.Done(),
	}

	closeCtx, closeCancel := context.WithCancel(context.Background())
	closeDone := make(chan struct{})
	go func() {
		srv.Close(closeCtx)
		close(closeDone)
	}()

	waitQueueSealed(t, q)
	releaseFirst <- errors.New("transient")
	closeCancel()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after deadline")
	}

	select {
	case <-w.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("webhook worker did not finish after deadline")
	}

	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("expected only the in-progress first webhook attempt, got %d", got)
	}
}
