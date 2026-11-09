package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/migrate"
	"scrumboy/internal/safehttp"
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

func TestDurableWebhookWorkerReopensDatabaseForPendingAndExpiredLease(t *testing.T) {
	const secret = "reopen-signing-secret"
	for _, testCase := range []struct {
		name       string
		leaseFirst bool
	}{
		{name: "pending"},
		{name: "expired_lease", leaseFirst: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var gotBody []byte
			var gotID, gotSignature string
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				gotID = r.Header.Get("X-Scrumboy-Delivery")
				gotSignature = r.Header.Get("X-Scrumboy-Signature")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			client, destination := webhookClientToTestServer(t, receiver, "reopen-receiver.example")
			path := filepath.Join(t.TempDir(), "durable-reopen.db")
			open := func() *sql.DB {
				database, err := db.Open(path, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
				if err != nil {
					t.Fatal(err)
				}
				return database
			}
			database := open()
			if err := migrate.Apply(context.Background(), database); err != nil {
				t.Fatal(err)
			}
			st := store.New(database, nil)
			owner, err := st.BootstrapUser(context.Background(), "reopen@example.com", "password123", "Reopen")
			if err != nil {
				t.Fatal(err)
			}
			ctx := store.WithUserID(context.Background(), owner.ID)
			project, err := st.CreateProject(ctx, "Reopen durable webhook")
			if err != nil {
				t.Fatal(err)
			}
			hook, err := st.CreateWebhook(ctx, owner.ID, store.CreateWebhookInput{
				ProjectID: project.ID, URL: destination, Events: []string{eventbus.TodoCreatedEventType},
				Secret: stringPointerHTTP(secret), DeliveryMode: store.WebhookDeliveryDurable,
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := st.ListWebhooksByProject(ctx, project.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%v err=%v", rows, err)
			}
			now := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
			eventID := "reopen-" + testCase.name
			body := []byte(`{"id":"` + eventID + `","type":"todo.created"}`)
			result, err := st.AdmitDurableWebhookDelivery(ctx, store.AdmitDurableWebhookDeliveryInput{
				WebhookID: hook.ID, ProjectID: project.ID, AuthorizationGeneration: rows[0].AuthorizationGeneration,
				EventID: eventID, EventType: eventbus.TodoCreatedEventType, EventTimestamp: now,
				Body: body, AdmittedAt: now,
			})
			if err != nil || result != store.DurableWebhookAdmissionInserted {
				t.Fatalf("admit=%q err=%v", result, err)
			}
			workerNow := now
			if testCase.leaseFirst {
				claims, err := st.ClaimDueDurableWebhookDeliveries(ctx, now, 1, time.Minute)
				if err != nil || len(claims) != 1 {
					t.Fatalf("persist lease claims=%v err=%v", claims, err)
				}
				workerNow = now.Add(time.Minute)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			reopened := open()
			defer reopened.Close()
			restartedStore := store.New(reopened, nil)
			worker := newDurableWebhookWorkerWithClient(restartedStore, discardLogger(), &stopDurableSchedule{}, client)
			worker.now = func() time.Time { return workerNow }
			worker.Run(context.Background())
			if !bytes.Equal(gotBody, body) || gotID != eventID {
				t.Fatalf("body=%q id=%q", gotBody, gotID)
			}
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write(body)
			wantSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
			if gotSignature != wantSignature {
				t.Fatalf("signature=%q want=%q", gotSignature, wantSignature)
			}
			var status string
			if err := reopened.QueryRow(`SELECT status FROM webhook_deliveries WHERE event_id = ?`, eventID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != "delivered" {
				t.Fatalf("status=%q", status)
			}
		})
	}
}

type failFirstCompletionStore struct {
	*store.Store
	mu     sync.Mutex
	failed bool
}

type failEveryCompletionStore struct {
	*store.Store
}

func (s *failEveryCompletionStore) CompleteDurableWebhookDelivery(context.Context, int64, string, time.Time) (bool, error) {
	return false, errors.New("simulated acknowledgement failure")
}

func TestDurableWebhookAcknowledgementFailuresCannotExceedEightRequests(t *testing.T) {
	var hits atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	client, destination := webhookClientToTestServer(t, receiver, "attempt-limit-receiver.example")
	f := newDurableHTTPFixture(t, destination, nil)
	current := time.Date(2026, 11, 9, 2, 0, 0, 0, time.UTC)
	f.admit(t, "attempt-limit-ack", current, []byte(`{"id":"attempt-limit-ack"}`))
	worker := newDurableWebhookWorkerWithClient(&failEveryCompletionStore{Store: f.store}, discardLogger(), newManualDurableSchedule(), client)
	worker.now = func() time.Time { return current }
	for i := 0; i < store.DurableWebhookMaxHTTPAttempts; i++ {
		worker.drain(context.Background())
		if hits.Load() != int32(i+1) {
			t.Fatalf("attempt %d hits=%d", i+1, hits.Load())
		}
		current = current.Add(defaultDurableWebhookLease)
	}
	worker.drain(context.Background())
	if hits.Load() != store.DurableWebhookMaxHTTPAttempts {
		t.Fatalf("ninth processing hits=%d", hits.Load())
	}
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), current.Add(defaultDurableWebhookLease), 1, defaultDurableWebhookLease)
	if err != nil || len(claims) != 0 {
		t.Fatalf("exhausted delivery claims=%v err=%v", claims, err)
	}
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

type authorizationErrorStore struct {
	*store.Store
	reservations atomic.Int32
}

func (s *authorizationErrorStore) GetDurableWebhookDeliveryTarget(context.Context, int64, int64) (store.WebhookDeliveryTarget, bool, error) {
	return store.WebhookDeliveryTarget{}, false, errors.New("temporary authorization database failure")
}

func (s *authorizationErrorStore) ReserveDurableWebhookAttempt(ctx context.Context, id int64, token string, at time.Time) (store.DurableWebhookAttemptReservation, error) {
	s.reservations.Add(1)
	return s.Store.ReserveDurableWebhookAttempt(ctx, id, token, at)
}

type reservationErrorStore struct {
	*store.Store
}

type blockingAuthorizationStore struct {
	*store.Store
	entered      chan struct{}
	once         sync.Once
	reservations atomic.Int32
}

func (s *blockingAuthorizationStore) GetDurableWebhookDeliveryTarget(ctx context.Context, _ int64, _ int64) (store.WebhookDeliveryTarget, bool, error) {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return store.WebhookDeliveryTarget{}, false, ctx.Err()
}

func (s *blockingAuthorizationStore) ReserveDurableWebhookAttempt(ctx context.Context, id int64, token string, at time.Time) (store.DurableWebhookAttemptReservation, error) {
	s.reservations.Add(1)
	return s.Store.ReserveDurableWebhookAttempt(ctx, id, token, at)
}

func (s *reservationErrorStore) ReserveDurableWebhookAttempt(context.Context, int64, string, time.Time) (store.DurableWebhookAttemptReservation, error) {
	return store.DurableWebhookAttemptReservation{}, errors.New("simulated reservation failure")
}

func TestDurableWebhookAuthorizationAndReservationErrorsMakeNoRequestOrAttempt(t *testing.T) {
	for _, testCase := range []struct {
		name string
		wrap func(*store.Store) durableWebhookStore
	}{
		{name: "authorization", wrap: func(st *store.Store) durableWebhookStore { return &authorizationErrorStore{Store: st} }},
		{name: "reservation", wrap: func(st *store.Store) durableWebhookStore { return &reservationErrorStore{Store: st} }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var requests atomic.Int32
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests.Add(1)
				return nil, errors.New("must not be called")
			})}
			f := newDurableHTTPFixture(t, "https://never-called.example/hook", nil)
			current := time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC)
			f.admit(t, "no-attempt-"+testCase.name, current, []byte(`{}`))
			worker := newDurableWebhookWorkerWithClient(testCase.wrap(f.store), discardLogger(), newManualDurableSchedule(), client)
			worker.now = func() time.Time { return current }
			worker.drain(context.Background())
			if requests.Load() != 0 {
				t.Fatalf("requests=%d", requests.Load())
			}
			claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), current.Add(time.Second), 1, time.Minute)
			if err != nil || len(claims) != 1 {
				t.Fatalf("claims=%v err=%v", claims, err)
			}
			if claims[0].HTTPAttemptCount != 0 {
				t.Fatalf("http attempts=%d", claims[0].HTTPAttemptCount)
			}
		})
	}
}

func TestDurableWebhookShutdownDuringAuthorizationReleasesClaimsWithoutAttempts(t *testing.T) {
	f := newDurableHTTPFixture(t, "https://never-called.example/hook", nil)
	now := time.Date(2026, 11, 9, 4, 30, 0, 0, time.UTC)
	f.admit(t, "shutdown-auth-one", now, []byte(`{}`))
	f.admit(t, "shutdown-auth-two", now, []byte(`{}`))
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now, 2, time.Minute)
	if err != nil || len(claims) != 2 {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	blocking := &blockingAuthorizationStore{Store: f.store, entered: make(chan struct{})}
	worker := newDurableWebhookWorkerWithClient(blocking, discardLogger(), newManualDurableSchedule(), &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("must not be called")
		}),
	})
	worker.now = func() time.Time { return now }
	worker.concurrency = 1
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.deliverClaims(runCtx, claims)
		close(done)
	}()
	<-blocking.entered
	cancel()
	<-done
	if blocking.reservations.Load() != 0 {
		t.Fatalf("reservations=%d", blocking.reservations.Load())
	}
	reclaimed, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now, 2, time.Minute)
	if err != nil || len(reclaimed) != 2 {
		t.Fatalf("reclaimed=%v err=%v", reclaimed, err)
	}
	for _, claim := range reclaimed {
		if claim.HTTPAttemptCount != 0 {
			t.Fatalf("claim %s attempts=%d", claim.EventID, claim.HTTPAttemptCount)
		}
	}
}

func TestDurableWebhookShutdownDuringHTTPPersistsRetryWithReservedAttempt(t *testing.T) {
	entered := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(entered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	f := newDurableHTTPFixture(t, "https://cancelled-http.example/hook", nil)
	now := time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC)
	f.admit(t, "shutdown-http", now, []byte(`{}`))
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now, 1, time.Minute)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	worker := newDurableWebhookWorkerWithClient(f.store, discardLogger(), newManualDurableSchedule(), client)
	worker.now = func() time.Time { return now }
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.deliverOne(runCtx, claims[0])
		close(done)
	}()
	<-entered
	cancel()
	<-done
	reclaimed, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now.Add(time.Second), 1, time.Minute)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaimed=%v err=%v", reclaimed, err)
	}
	if reclaimed[0].HTTPAttemptCount != 1 {
		t.Fatalf("attempts=%d", reclaimed[0].HTTPAttemptCount)
	}
}

func TestDurableWebhookHTTPClassification(t *testing.T) {
	for _, testCase := range []struct {
		status    int
		class     string
		permanent bool
	}{
		{status: http.StatusMovedPermanently, class: store.WebhookErrorHTTP3xx, permanent: true},
		{status: http.StatusRequestTimeout, class: store.WebhookErrorHTTP4xx},
		{status: http.StatusTooManyRequests, class: store.WebhookErrorHTTP4xx},
		{status: http.StatusBadRequest, class: store.WebhookErrorHTTP4xx, permanent: true},
		{status: http.StatusServiceUnavailable, class: store.WebhookErrorHTTP5xx},
	} {
		class, status, permanent := classifyDurableWebhookError(&webhookHTTPError{StatusCode: testCase.status})
		if class != testCase.class || status == nil || *status != testCase.status || permanent != testCase.permanent {
			t.Fatalf("status %d class=%q stored=%v permanent=%v", testCase.status, class, status, permanent)
		}
	}
	class, status, permanent := classifyDurableWebhookError(safehttp.ErrForbidden)
	if class != store.WebhookErrorSSRF || status != nil || !permanent {
		t.Fatalf("ssrf class=%q status=%v permanent=%v", class, status, permanent)
	}
}

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

type delayedWebhookSnapshotStore struct {
	*store.Store
	captured chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (s *delayedWebhookSnapshotStore) ListWebhooksByProject(ctx context.Context, projectID int64) ([]store.WebhookRow, error) {
	rows, err := s.Store.ListWebhooksByProject(ctx, projectID)
	s.once.Do(func() { close(s.captured) })
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return rows, err
}

func TestWebhookDispatcherSnapshotPreventsRevocationReadditionResurrection(t *testing.T) {
	st := newTestStore(t)
	ctx, owner, project := setupAuthenticatedProject(t, st)
	service, err := st.CreateServiceAccount(ctx, owner.ID, "Snapshot Bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, project.ID, service.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateWebhook(ctx, owner.ID, store.CreateWebhookInput{
		ProjectID: project.ID, URL: "https://snapshot.example/hook", Events: []string{eventbus.TodoCreatedEventType},
		ServiceOwnerUserID: &service.ID, DeliveryMode: store.WebhookDeliveryDurable,
	}); err != nil {
		t.Fatal(err)
	}
	delayed := &delayedWebhookSnapshotStore{Store: st, captured: make(chan struct{}), release: make(chan struct{})}
	dispatcher := newWebhookDispatcher(delayed, newWebhookQueue(discardLogger()), discardLogger())
	done := make(chan struct{})
	go func() {
		dispatcher.enqueueWork(eventbus.Event{
			ID: "cross-generation-event", Type: eventbus.TodoCreatedEventType,
			Time: time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC), ProjectID: project.ID, Payload: []byte(`{}`),
		})
		close(done)
	}()
	<-delayed.captured
	if err := st.RemoveProjectMember(ctx, owner.ID, project.ID, service.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, project.ID, service.ID, store.RoleViewer); err != nil {
		t.Fatal(err)
	}
	close(delayed.release)
	<-done
	claims, err := st.ClaimDueDurableWebhookDeliveries(ctx, time.Date(2026, 11, 9, 3, 1, 0, 0, time.UTC), 1, time.Minute)
	if err != nil || len(claims) != 0 {
		t.Fatalf("old event crossed generation claims=%v err=%v", claims, err)
	}
}

func TestWebhookDispatcherBoundsDurableAdmissionSaturation(t *testing.T) {
	started := make(chan struct{}, defaultWebhookAdmissionSlots)
	release := make(chan struct{})
	var admissions atomic.Int32
	dispatchStore := webhookDispatchStoreFunc{
		list: func(context.Context, int64) ([]store.WebhookRow, error) {
			return []store.WebhookRow{{
				ID: 1, Events: []string{eventbus.TodoCreatedEventType},
				DeliveryMode: store.WebhookDeliveryDurable, AuthorizationGeneration: 1,
			}}, nil
		},
		admit: func(ctx context.Context, _ store.AdmitDurableWebhookDeliveryInput) (string, error) {
			admissions.Add(1)
			started <- struct{}{}
			select {
			case <-release:
				return store.DurableWebhookAdmissionInserted, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}
	var logs bytes.Buffer
	dispatcher := newWebhookDispatcher(dispatchStore, newWebhookQueue(discardLogger()), log.New(&logs, "", 0))
	for i := 0; i < defaultWebhookAdmissionSlots; i++ {
		dispatcher.OnEvent(context.Background(), eventbus.Event{
			ID: fmt.Sprintf("saturated-%d", i), Type: eventbus.TodoCreatedEventType,
			Time: time.Now().UTC(), ProjectID: 1, Payload: []byte(`{}`),
		})
	}
	for i := 0; i < defaultWebhookAdmissionSlots; i++ {
		<-started
	}
	dispatcher.OnEvent(context.Background(), eventbus.Event{
		ID: "saturated-rejected", Type: eventbus.TodoCreatedEventType,
		Time: time.Now().UTC(), ProjectID: 1, Payload: []byte(`{}`),
	})
	if !strings.Contains(logs.String(), "admission saturated event=saturated-rejected") {
		t.Fatalf("missing saturation signal: %s", logs.String())
	}
	close(release)
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !dispatcher.Close(closeCtx) {
		t.Fatal("dispatcher did not drain bounded admissions")
	}
	if admissions.Load() != defaultWebhookAdmissionSlots {
		t.Fatalf("admissions=%d", admissions.Load())
	}
}
