package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

const testVapidPub, testVapidPriv = "dGVzdC1wdWJsaWMta2V5LXBhZGRlZA", "dGVzdC1wcml2YXRlLWtleS1wYWRkZWQ"

func newPushTestServer(t *testing.T, mode string, withVAPID bool) (*httptest.Server, *store.Store, func()) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	if mode == "" {
		mode = "full"
	}
	opts := Options{MaxRequestBody: 1 << 20, ScrumboyMode: mode}
	if withVAPID {
		opts.VAPIDPublicKey = testVapidPub
		opts.VAPIDPrivateKey = testVapidPriv
	}
	srv := NewServer(st, opts)
	ts := httptest.NewServer(srv)
	return ts, st, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func TestPushRoutes_VapidPublicKey503WhenUnconfigured(t *testing.T) {
	ts, _, cleanup := newPushTestServer(t, "full", false)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/push/vapid-public-key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
}

func TestPushRoutes_Subscribe503WhenUnconfigured(t *testing.T) {
	ts, st, cleanup := newPushTestServer(t, "full", false)
	defer cleanup()

	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "U")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"endpoint":"https://example.com/ep","keys":{"p256dh":"x","auth":"y"}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/push/subscribe", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: tok})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", resp.StatusCode)
	}
}

func TestPushRoutes_VapidPublicKeyAndSubscribeWhenConfigured(t *testing.T) {
	ts, st, cleanup := newPushTestServer(t, "full", true)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/push/vapid-public-key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("key status=%d", resp.StatusCode)
	}
	var keyBody struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keyBody); err != nil {
		t.Fatal(err)
	}
	if keyBody.PublicKey != testVapidPub {
		t.Fatalf("publicKey=%q", keyBody.PublicKey)
	}

	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "U")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := st.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	ep := "https://push.example.com/subscription/abc"
	subBody := `{"endpoint":"` + ep + `","keys":{"p256dh":"p256","auth":"authbytes"}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/push/subscribe", strings.NewReader(subBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: tok})

	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("subscribe status=%d body=%s", resp2.StatusCode, string(b))
	}

	subs, err := st.ListPushSubscriptionsByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Endpoint != ep {
		t.Fatalf("subs=%+v", subs)
	}
}

func TestPushRoutes_UnsubscribeDeletesOnlyMatchingUserAndEndpoint(t *testing.T) {
	ts, st, cleanup := newPushTestServer(t, "full", true)
	defer cleanup()
	ctx := context.Background()

	u1, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "U1")
	if err != nil {
		t.Fatal(err)
	}
	u2, err := st.CreateUser(ctx, "b@b.com", "pass1234A!", "U2")
	if err != nil {
		t.Fatal(err)
	}

	ep1 := "https://push.example.com/e1"
	ep2 := "https://push.example.com/e2"
	if err := st.UpsertPushSubscription(ctx, u1.ID, ep1, "p1", "a1", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPushSubscription(ctx, u2.ID, ep2, "p2", "a2", nil); err != nil {
		t.Fatal(err)
	}

	tok1, _, err := st.CreateSession(ctx, u1.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// u1 cannot delete u2's endpoint (0 rows affected; still 204).
	delWrong := `{"endpoint":"` + ep2 + `"}`
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/push/unsubscribe", strings.NewReader(delWrong))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: tok1})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete wrong user status=%d", resp.StatusCode)
	}
	s2, err := st.ListPushSubscriptionsByUser(ctx, u2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2) != 1 {
		t.Fatalf("expected u2 subscription preserved, got %d rows", len(s2))
	}

	// u1 deletes own endpoint
	del1 := `{"endpoint":"` + ep1 + `"}`
	req2, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/push/unsubscribe", strings.NewReader(del1))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Scrumboy", "1")
	req2.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: tok1})
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("delete self status=%d", resp2.StatusCode)
	}
	n1, err := st.ListPushSubscriptionsByUser(ctx, u1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(n1) != 0 {
		t.Fatalf("u1 subs: %d", len(n1))
	}
	n2, err := st.ListPushSubscriptionsByUser(ctx, u2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(n2) != 1 {
		t.Fatalf("u2 subs after u1 delete: %d", len(n2))
	}
}

func TestPushRoutes_AnonymousMode404(t *testing.T) {
	ts, _, cleanup := newPushTestServer(t, "anonymous", true)
	defer cleanup()

	resp, err := http.Get(ts.URL + "/api/push/vapid-public-key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
}

func TestPushRoutes_Unsubscribe401WithoutSession(t *testing.T) {
	ts, _, cleanup := newPushTestServer(t, "full", true)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/push/unsubscribe", strings.NewReader(`{"endpoint":"https://x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Scrumboy", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", resp.StatusCode)
	}
}
