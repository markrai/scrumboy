package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"scrumboy/internal/safehttp"
)

func TestSendWebhookBlocksStoredLoopbackURL(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer ts.Close()

	err := sendWebhook(newWebhookHTTPClient(), webhookDelivery{
		WebhookID: 1,
		URL:       ts.URL + "/hook",
		EventID:   "evt-1",
		EventType: "todo.assigned",
		Body:      []byte(`{"id":"evt-1"}`),
	})
	if !errors.Is(err, safehttp.ErrForbidden) {
		t.Fatalf("err=%v, want ErrForbidden", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback listener was contacted %d times", hits.Load())
	}
}

func TestSendWebhookDialsVettedIPAndPreservesHMAC(t *testing.T) {
	var method, ctype, event, delivery, sig string
	var body []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		ctype = r.Header.Get("Content-Type")
		event = r.Header.Get("X-Scrumboy-Event")
		delivery = r.Header.Get("X-Scrumboy-Delivery")
		sig = r.Header.Get("X-Scrumboy-Signature")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	client, publicURL := webhookClientToTestServer(t, ts, "hooks.example")
	secret := "shared-secret"
	payload := []byte(`{"id":"evt-9","type":"todo.created"}`)
	if err := sendWebhook(client, webhookDelivery{
		WebhookID: 9,
		URL:       publicURL + "/path?q=1",
		Secret:    &secret,
		EventID:   "evt-9",
		EventType: "todo.created",
		Body:      payload,
	}); err != nil {
		t.Fatalf("sendWebhook: %v", err)
	}
	if method != http.MethodPost {
		t.Fatalf("method=%q", method)
	}
	if ctype != "application/json; charset=utf-8" {
		t.Fatalf("content-type=%q", ctype)
	}
	if event != "todo.created" || delivery != "evt-9" {
		t.Fatalf("event=%q delivery=%q", event, delivery)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if sig != wantSig {
		t.Fatalf("sig=%q want %q", sig, wantSig)
	}
	if string(body) != string(payload) {
		t.Fatalf("body=%q", body)
	}
}

func TestSendWebhookDoesNotFollowRedirectToLoopback(t *testing.T) {
	var privateHits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		privateHits.Add(1)
	}))
	defer private.Close()

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL+"/internal", http.StatusTemporaryRedirect)
	}))
	defer public.Close()

	client, publicURL := webhookClientToTestServer(t, public, "hooks.example")
	err := sendWebhook(client, webhookDelivery{
		URL:       publicURL + "/start",
		EventID:   "evt-307",
		EventType: "todo.assigned",
		Body:      []byte(`{"id":"evt-307"}`),
	})
	if !errors.Is(err, errWebhookRedirect) {
		t.Fatalf("err=%v, want errWebhookRedirect", err)
	}
	if privateHits.Load() != 0 {
		t.Fatalf("private server hits=%d", privateHits.Load())
	}
}

func TestSendWebhookDoesNotFollow302(t *testing.T) {
	var privateHits atomic.Int32
	private := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		privateHits.Add(1)
	}))
	defer private.Close()

	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, private.URL+"/internal", http.StatusFound)
	}))
	defer public.Close()

	client, publicURL := webhookClientToTestServer(t, public, "hooks.example")
	err := sendWebhook(client, webhookDelivery{
		URL:       publicURL + "/start",
		EventID:   "evt-302",
		EventType: "todo.assigned",
		Body:      []byte(`{"id":"evt-302"}`),
	})
	if !errors.Is(err, errWebhookRedirect) {
		t.Fatalf("err=%v, want errWebhookRedirect", err)
	}
	if privateHits.Load() != 0 {
		t.Fatalf("private server hits=%d", privateHits.Load())
	}
}

func TestSendWebhookHostnameResolvingPrivateIsForbidden(t *testing.T) {
	dialed := 0
	client := newWebhookHTTPClientWithDialer(safehttp.Dialer{
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.1.2.3")}, nil
		},
		Dial: func(context.Context, string, string) (net.Conn, error) {
			dialed++
			return nil, errors.New("should not dial")
		},
	})
	err := sendWebhook(client, webhookDelivery{
		URL:       "https://hooks.example/hook",
		EventID:   "evt-priv",
		EventType: "todo.assigned",
		Body:      []byte(`{}`),
	})
	if !errors.Is(err, safehttp.ErrForbidden) {
		t.Fatalf("err=%v, want ErrForbidden", err)
	}
	if dialed != 0 {
		t.Fatalf("dialed %d times", dialed)
	}
}

func TestWebhookForbiddenErrorIsPermanent(t *testing.T) {
	var sendCalls atomic.Int32
	q := newWebhookQueue(discardLogger())
	send := func(d webhookDelivery) error {
		sendCalls.Add(1)
		return sendWebhook(newWebhookHTTPClient(), d)
	}
	inner := newRetryWorker(q, discardLogger(), "webhook", send)
	inner.isPermanent = isPermanentWebhookError
	w := &webhookWorker{retryWorker: inner}

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(runCtx)

	q.Enqueue(webhookDelivery{
		WebhookID: 1,
		URL:       "http://127.0.0.1/hook",
		EventID:   "stored-unsafe",
		EventType: "todo.assigned",
		Body:      []byte(`{}`),
	})

	deadline := time.After(2 * time.Second)
	for sendCalls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("send was never called")
		case <-time.After(10 * time.Millisecond):
		}
	}
	time.Sleep(250 * time.Millisecond)
	if got := sendCalls.Load(); got != 1 {
		t.Fatalf("attempts=%d, want 1 (permanent forbidden)", got)
	}
}

func TestWebhookHTTPClientDisablesProxyAndRedirects(t *testing.T) {
	client := newWebhookHTTPClient()
	if client.CheckRedirect == nil {
		t.Fatal("CheckRedirect must reject redirects")
	}
	if err := client.CheckRedirect(&http.Request{}, nil); !errors.Is(err, errWebhookRedirect) {
		t.Fatalf("CheckRedirect err=%v", err)
	}
	rt, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Transport is not *http.Transport")
	}
	if rt.Proxy != nil {
		t.Fatal("Proxy must be nil")
	}
	if !rt.DisableKeepAlives {
		t.Fatal("keep-alives must be disabled")
	}
	if rt.DialContext == nil {
		t.Fatal("DialContext must be the vetted dialer")
	}
	if !rt.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 must be true so HTTP/2 compatibility is preserved")
	}
	if rt.TLSClientConfig == nil || rt.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("TLS min version=%v, want TLS1.2", rt.TLSClientConfig)
	}
}

func webhookClientToTestServer(t *testing.T, ts *httptest.Server, hostname string) (*http.Client, string) {
	t.Helper()
	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	target := ts.Listener.Addr().String()
	client := newWebhookHTTPClientWithDialer(safehttp.Dialer{
		LookupIP: func(_ context.Context, host string) ([]net.IP, error) {
			if host != hostname {
				return nil, errors.New("unexpected host " + host)
			}
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			want := net.JoinHostPort("8.8.8.8", port)
			if address != want {
				return nil, errors.New("dialed " + address + ", want " + want)
			}
			var n net.Dialer
			return n.DialContext(ctx, "tcp", target)
		},
	})
	return client, "http://" + hostname + ":" + port
}
