package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

type durableHTTPFixture struct {
	store   *store.Store
	ctx     context.Context
	owner   store.User
	project store.Project
	hook    store.Webhook
	row     store.WebhookRow
}

func newDurableHTTPFixture(t *testing.T, destination string, secret *string) durableHTTPFixture {
	t.Helper()
	st := newTestStore(t)
	ctx, owner, project := setupAuthenticatedProject(t, st)
	hook, err := st.CreateWebhook(ctx, owner.ID, store.CreateWebhookInput{
		ProjectID: project.ID, URL: destination, Events: []string{eventbus.TodoCreatedEventType},
		Secret: secret, DeliveryMode: store.WebhookDeliveryDurable,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListWebhooksByProject(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == hook.ID {
			return durableHTTPFixture{store: st, ctx: ctx, owner: owner, project: project, hook: hook, row: row}
		}
	}
	t.Fatal("durable webhook missing from delivery listing")
	return durableHTTPFixture{}
}

func (f durableHTTPFixture) admit(t *testing.T, eventID string, at time.Time, body []byte) {
	t.Helper()
	result, err := f.store.AdmitDurableWebhookDelivery(f.ctx, store.AdmitDurableWebhookDeliveryInput{
		WebhookID:               f.hook.ID,
		ProjectID:               f.project.ID,
		AuthorizationGeneration: f.row.AuthorizationGeneration,
		EventID:                 eventID,
		EventType:               eventbus.TodoCreatedEventType,
		EventTimestamp:          at.Add(-time.Second),
		Body:                    body,
		AdmittedAt:              at,
	})
	if err != nil || result != store.DurableWebhookAdmissionInserted {
		t.Fatalf("admit result=%q err=%v", result, err)
	}
}

type manualDurableSchedule struct {
	wake chan struct{}
}

func newManualDurableSchedule() *manualDurableSchedule {
	return &manualDurableSchedule{wake: make(chan struct{}, 1)}
}

func (s *manualDurableSchedule) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *manualDurableSchedule) Wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		return true
	}
}

func (*manualDurableSchedule) Close() {}

type stopDurableSchedule struct{}

func (*stopDurableSchedule) Notify()                   {}
func (*stopDurableSchedule) Wait(context.Context) bool { return false }
func (*stopDurableSchedule) Close()                    {}

func TestDurableWebhookWorkerRestartsPendingDeliveryThroughExistingSender(t *testing.T) {
	const secret = "durable-signing-secret"
	var (
		gotBody      []byte
		gotEvent     string
		gotDelivery  string
		gotSignature string
	)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotEvent = r.Header.Get("X-Scrumboy-Event")
		gotDelivery = r.Header.Get("X-Scrumboy-Delivery")
		gotSignature = r.Header.Get("X-Scrumboy-Signature")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	client, destination := webhookClientToTestServer(t, receiver, "durable-receiver.example")
	f := newDurableHTTPFixture(t, destination+"/hook", stringPointerHTTP(secret))
	now := time.Date(2026, 11, 8, 16, 0, 0, 0, time.UTC)
	event := eventbus.Event{ID: "restart-event", Type: eventbus.TodoCreatedEventType, Time: now.Add(-time.Second), ProjectID: f.project.ID, Payload: []byte(`{"todoId":42}`)}
	body, err := canonicalWebhookBody(event)
	if err != nil {
		t.Fatal(err)
	}
	f.admit(t, event.ID, now, body)

	// Constructing and running a new worker models process startup: Run drains
	// committed rows before waiting for its first scheduler tick.
	schedule := &stopDurableSchedule{}
	worker := newDurableWebhookWorkerWithClient(f.store, discardLogger(), schedule, client)
	worker.now = func() time.Time { return now }
	worker.Run(context.Background())

	if !bytes.Equal(gotBody, body) || gotEvent != event.Type || gotDelivery != event.ID {
		t.Fatalf("body/headers changed body=%q event=%q delivery=%q", gotBody, gotEvent, gotDelivery)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	wantSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSignature != wantSignature {
		t.Fatalf("signature=%q want=%q", gotSignature, wantSignature)
	}
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now.Add(24*time.Hour), 1, time.Minute)
	if err != nil || len(claims) != 0 {
		t.Fatalf("delivered row was claimable: claims=%v err=%v", claims, err)
	}
}

type failFirstCompletionStore struct {
	*store.Store
	mu     sync.Mutex
	failed bool
}

func (s *failFirstCompletionStore) CompleteDurableWebhookDelivery(ctx context.Context, id int64, token string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.failed {
		s.failed = true
		return false, errors.New("simulated acknowledgement failure")
	}
	return s.Store.CompleteDurableWebhookDelivery(ctx, id, token, at)
}

func TestDurableWebhookDuplicateAfterReceiverSuccessBeforeAcknowledgement(t *testing.T) {
	var (
		hits   atomic.Int32
		mu     sync.Mutex
		bodies [][]byte
		ids    []string
	)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		ids = append(ids, r.Header.Get("X-Scrumboy-Delivery"))
		mu.Unlock()
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()
	client, destination := webhookClientToTestServer(t, receiver, "duplicate-receiver.example")
	f := newDurableHTTPFixture(t, destination, nil)
	now := time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"receiver-processed","type":"todo.created"}`)
	f.admit(t, "receiver-processed", now, body)

	flaky := &failFirstCompletionStore{Store: f.store}
	first := newDurableWebhookWorkerWithClient(flaky, discardLogger(), newManualDurableSchedule(), client)
	first.now = func() time.Time { return now }
	first.drain(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("first processing hits=%d", hits.Load())
	}

	secondNow := now.Add(defaultDurableWebhookLease)
	second := newDurableWebhookWorkerWithClient(f.store, discardLogger(), newManualDurableSchedule(), client)
	second.now = func() time.Time { return secondNow }
	second.drain(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("reclaimed processing hits=%d", hits.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || !bytes.Equal(bodies[0], body) || ids[0] != ids[1] || ids[0] != "receiver-processed" {
		t.Fatalf("duplicate changed identity/body ids=%v bodies=%q", ids, bodies)
	}
}

func TestDurableWebhookTransientFailureSchedulesFutureRetry(t *testing.T) {
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	client, destination := webhookClientToTestServer(t, receiver, "retry-durable.example")
	f := newDurableHTTPFixture(t, destination, nil)
	current := time.Date(2026, 11, 8, 17, 30, 0, 0, time.UTC)
	f.admit(t, "retry-event", current, []byte(`{"id":"retry-event"}`))
	worker := newDurableWebhookWorkerWithClient(f.store, discardLogger(), newManualDurableSchedule(), client)
	worker.now = func() time.Time { return current }

	worker.drain(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("first attempt hits=%d", hits.Load())
	}
	worker.drain(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("retry ran before next_attempt_at: hits=%d", hits.Load())
	}
	current = current.Add(time.Second)
	worker.drain(context.Background())
	if hits.Load() != 2 {
		t.Fatalf("due retry hits=%d", hits.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type webhookDispatchStoreFunc struct {
	list  func(context.Context, int64) ([]store.WebhookRow, error)
	admit func(context.Context, store.AdmitDurableWebhookDeliveryInput) (string, error)
}

func (f webhookDispatchStoreFunc) ListWebhooksByProject(ctx context.Context, projectID int64) ([]store.WebhookRow, error) {
	return f.list(ctx, projectID)
}

func (f webhookDispatchStoreFunc) AdmitDurableWebhookDelivery(ctx context.Context, in store.AdmitDurableWebhookDeliveryInput) (string, error) {
	return f.admit(ctx, in)
}

func TestDurableWebhookUnauthorizedAndDeletedWorkMakesNoHTTPRequest(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("must not be called")
	})}
	f := newDurableHTTPFixture(t, "https://never-called.example/hook", nil)
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	service, err := f.store.CreateServiceAccount(f.ctx, f.owner.ID, "Durable Delivery Bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	serviceHook, err := f.store.CreateWebhook(f.ctx, f.owner.ID, store.CreateWebhookInput{
		ProjectID: f.project.ID, URL: "https://never-called.example/service", Events: []string{eventbus.TodoCreatedEventType},
		ServiceOwnerUserID: &service.ID, DeliveryMode: store.WebhookDeliveryDurable,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.ListWebhooksByProject(f.ctx, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var serviceRow store.WebhookRow
	for _, row := range rows {
		if row.ID == serviceHook.ID {
			serviceRow = row
		}
	}
	result, err := f.store.AdmitDurableWebhookDelivery(f.ctx, store.AdmitDurableWebhookDeliveryInput{
		WebhookID: serviceHook.ID, ProjectID: f.project.ID, AuthorizationGeneration: serviceRow.AuthorizationGeneration,
		EventID: "revoked-service", EventType: eventbus.TodoCreatedEventType, EventTimestamp: now.Add(-time.Second),
		Body: []byte(`{"id":"revoked-service"}`), AdmittedAt: now,
	})
	if err != nil || result != store.DurableWebhookAdmissionInserted {
		t.Fatalf("service admission=%q err=%v", result, err)
	}
	if err := f.store.RemoveProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	worker := newDurableWebhookWorkerWithClient(f.store, discardLogger(), newManualDurableSchedule(), client)
	worker.now = func() time.Time { return now }
	worker.drain(context.Background())
	if requests.Load() != 0 {
		t.Fatalf("revoked generation made %d requests", requests.Load())
	}

	f.admit(t, "revoked-before-send", now, []byte(`{"id":"revoked-before-send"}`))
	if err := f.store.UpdateProjectMemberRole(f.ctx, f.owner.ID, f.project.ID, f.owner.ID, store.RoleContributor); err == nil {
		// The sole-maintainer guard normally refuses this. A service-owner case is
		// covered by the store generation test, so delete the subscription here to
		// pin the stronger missing-owner boundary without bypassing membership rules.
		t.Fatal("unexpectedly downgraded sole maintainer")
	}
	if err := f.store.DeleteWebhook(f.ctx, f.owner.ID, f.hook.ID); err != nil {
		t.Fatal(err)
	}
	worker = newDurableWebhookWorkerWithClient(f.store, discardLogger(), newManualDurableSchedule(), client)
	worker.now = func() time.Time { return now }
	worker.drain(context.Background())
	if requests.Load() != 0 {
		t.Fatalf("deleted pending work made %d requests", requests.Load())
	}
}

func TestWebhookLogsNeverContainRawDestinationSecretOrPayload(t *testing.T) {
	const (
		rawURL  = "https://user:password@logs.example/private/path?token=url-secret"
		secret  = "hmac-secret-must-not-log"
		payload = `{"private":"payload-must-not-log"}`
	)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport failed")
	})}
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	queue := newWebhookQueue(logger)
	authorizer := webhookDeliveryAuthorizerFunc(func(context.Context, int64) (store.WebhookDeliveryTarget, bool, error) {
		return store.WebhookDeliveryTarget{URL: rawURL, Secret: stringPointerHTTP(secret)}, true, nil
	})
	worker := newWebhookWorkerWithClient(authorizer, queue, logger, client)
	queue.Enqueue(webhookDelivery{WebhookID: 7, URL: rawURL, Secret: stringPointerHTTP(secret), EventID: "safe-log-event", EventType: eventbus.TodoCreatedEventType, Body: []byte(payload)})
	worker.flush()
	got := logs.String()
	for _, forbidden := range []string{rawURL, "user:password", "url-secret", secret, "payload-must-not-log"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("logs disclosed %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "webhook_id=7 event=safe-log-event") {
		t.Fatalf("safe identifiers missing from logs: %s", got)
	}
}

func TestWebhookDispatcherSplitsBestEffortAndDurableWithoutFallback(t *testing.T) {
	st := newTestStore(t)
	ctx, owner, project := setupAuthenticatedProject(t, st)
	if _, err := st.CreateWebhook(ctx, owner.ID, store.CreateWebhookInput{
		ProjectID: project.ID, URL: "https://example.com/best", Events: []string{eventbus.TodoCreatedEventType},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWebhook(ctx, owner.ID, store.CreateWebhookInput{
		ProjectID: project.ID, URL: "https://example.com/durable", Events: []string{eventbus.TodoCreatedEventType}, DeliveryMode: store.WebhookDeliveryDurable,
	}); err != nil {
		t.Fatal(err)
	}
	queue := newWebhookQueue(discardLogger())
	dispatcher := newWebhookDispatcher(st, queue, discardLogger())
	now := time.Date(2026, 11, 8, 19, 0, 0, 0, time.UTC)
	event := eventbus.Event{ID: "split-event", Type: eventbus.TodoCreatedEventType, Time: now, ProjectID: project.ID, Payload: []byte(`{"todoId":1}`)}
	dispatcher.enqueueWork(event)
	queued := queue.Drain()
	if len(queued) != 1 || queued[0].EventID != event.ID {
		t.Fatalf("best-effort queue=%+v", queued)
	}
	claims, err := st.ClaimDueDurableWebhookDeliveries(ctx, time.Now().UTC().Add(time.Hour), 10, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].EventID != event.ID {
		t.Fatalf("durable claims=%+v err=%v", claims, err)
	}
}

func TestWebhookDispatcherDoesNotFallBackAfterDurablePersistenceFailure(t *testing.T) {
	var admissions atomic.Int32
	dispatchStore := webhookDispatchStoreFunc{
		list: func(context.Context, int64) ([]store.WebhookRow, error) {
			return []store.WebhookRow{{
				ID: 99, Events: []string{eventbus.TodoCreatedEventType}, DeliveryMode: store.WebhookDeliveryDurable, AuthorizationGeneration: 3,
			}}, nil
		},
		admit: func(context.Context, store.AdmitDurableWebhookDeliveryInput) (string, error) {
			admissions.Add(1)
			return "", errors.New("sqlite unavailable")
		},
	}
	queue := newWebhookQueue(discardLogger())
	dispatcher := newWebhookDispatcher(dispatchStore, queue, discardLogger())
	dispatcher.enqueueWork(eventbus.Event{
		ID: "persistence-failure", Type: eventbus.TodoCreatedEventType, Time: time.Now().UTC(), ProjectID: 1, Payload: []byte(`{}`),
	})
	if admissions.Load() != 1 {
		t.Fatalf("admissions=%d", admissions.Load())
	}
	if queued := queue.Drain(); len(queued) != 0 {
		t.Fatalf("durable failure fell back to in-memory queue: %+v", queued)
	}
}
