package httpapi

import (
	"context"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/safehttp"
	"scrumboy/internal/store"
)

type pushTestTransport func(*http.Request) (*http.Response, error)

func (fn pushTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func pushTestKeys(t *testing.T) (string, string) {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y)), base64.RawURLEncoding.EncodeToString(auth)
}

func TestPushHTTPClientRejectsInternalAddressesAndRedirects(t *testing.T) {
	client := newPushHTTPClient()
	if client.Timeout == 0 || client.CheckRedirect == nil {
		t.Fatal("push HTTP client missing timeout or redirect policy")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.DialContext == nil {
		t.Fatal("push HTTP client not using direct vetted dialing")
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "127.0.0.1:443")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("internal connection was allowed")
	}
	if !errors.Is(err, safehttp.ErrForbidden) {
		t.Fatalf("internal dial error = %v", err)
	}
	if err := client.CheckRedirect(&http.Request{}, nil); !errors.Is(err, errPushRedirect) {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestPushNotifierRetainsFailedAndPrunesExpiredSubscriptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		status int
		transportError bool
		preserve bool
	}{
		{name: "temporary transport failure", transportError: true, preserve: true},
		{name: "rate limited", status: http.StatusTooManyRequests, preserve: true},
		{name: "server error", status: http.StatusServiceUnavailable, preserve: true},
		{name: "not found", status: http.StatusNotFound},
		{name: "gone", status: http.StatusGone},
		{name: "created", status: http.StatusCreated, preserve: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			ctx := context.Background()
			user, err := st.BootstrapUser(ctx, "push-pruning@example.com", "pass1234A!", "Push")
			if err != nil { t.Fatal(err) }
			project, err := st.CreateProject(store.WithUserID(ctx, user.ID), "Push Pruning")
			if err != nil { t.Fatal(err) }
			p256dh, auth := pushTestKeys(t)
			endpoint := "https://push.example.com/private-capability?token=secret"
			if err := st.UpsertPushSubscription(ctx, user.ID, endpoint, p256dh, auth, nil); err != nil { t.Fatal(err) }
			var logs strings.Builder
			notifier := newPushNotifier(st, log.New(&logs, "", 0), testVapidPub, testVapidPriv, "mailto:ops@example.com", true, true)
			calls := 0
			notifier.httpClient = &http.Client{Transport: pushTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transportError {
					return nil, errors.New("transport failure for "+endpoint)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
			})}
			payload, err := json.Marshal(eventbus.TodoAssignedPayload{
				ProjectID: project.ID, TodoID: 42, Title: "Assigned",
				ActorUserID: user.ID + 1, ToAssigneeUID: &user.ID,
			})
			if err != nil { t.Fatal(err) }
			notifier.handle(ctx, eventbus.Event{Type: "todo.assigned", ProjectID: project.ID, Payload: payload})
			if calls != 1 { t.Fatalf("delivery attempts = %d", calls) }
			subs, err := st.ListPushSubscriptionsByUser(ctx, user.ID)
			if err != nil { t.Fatal(err) }
			if (len(subs) == 1) != tc.preserve {
				t.Fatalf("subscriptions after delivery: %+v; want preserve=%t", subs, tc.preserve)
			}
			if strings.Contains(logs.String(), endpoint) || strings.Contains(logs.String(), "token=secret") {
				t.Fatalf("endpoint leaked to logs: %q", logs.String())
			}
		})
	}
}

func TestPushNotifierDoesNotDialStoredUnsafeEndpoint(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	_ = ts
	ctx := context.Background()
	st := store.New(sqlDB, nil)
	user, err := st.BootstrapUser(ctx, "push-stored-unsafe@example.com", "pass1234A!", "Push")
	if err != nil { t.Fatal(err) }
	project, err := st.CreateProject(store.WithUserID(ctx, user.ID), "Push Existing Endpoint")
	if err != nil { t.Fatal(err) }
	unsafeTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("delivery reached unsafe destination")
	}))
	defer unsafeTarget.Close()
	endpoint := unsafeTarget.URL + "/private"
	now := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.ExecContext(ctx,
		"INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, created_at, updated_at) VALUES (?,?,?,?,?,?)",
		user.ID, endpoint, "bad", "bad", now, now); err != nil {
		t.Fatal(err)
	}
	assigneeID := user.ID
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID: project.ID, TodoID: 99, Title: "Assigned",
		ActorUserID: user.ID + 1, ToAssigneeUID: &assigneeID,
	})
	if err != nil { t.Fatal(err) }
	notifier := newPushNotifier(st, log.New(io.Discard, "", 0), testVapidPub, testVapidPriv, "mailto:ops@example.com", true, false)
	notifier.handle(ctx, eventbus.Event{Type: "todo.assigned", ProjectID: project.ID, Payload: payload})
	subs, err := st.ListPushSubscriptionsByUser(ctx, user.ID)
	if err != nil || len(subs) != 1 {
		t.Fatalf("unsafe existing subscription should be retained but not contacted: %+v %v", subs, err)
	}
}

func TestPushHTTPClientDoesNotUseAmbientProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8999")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8999")
	client := newPushHTTPClient()
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("HTTP proxy inherited from environment")
	}
}
