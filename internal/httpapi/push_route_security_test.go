package httpapi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPushRoutesRejectUnsafeEndpoints(t *testing.T) {
	ts, st, cleanup := newPushTestServer(t, "full", true)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "push-validation@example.com", "pass1234A!", "Push")
	if err != nil { t.Fatal(err) }
	token, _, err := st.CreateSession(ctx, user.ID, time.Hour)
	if err != nil { t.Fatal(err) }
	for _, endpoint := range []string{
		"http://127.0.0.1:39999/private", "https://169.254.169.254/latest",
		"https://127.0.0.1/send", "https://[::ffff:10.0.0.1]/send",
		"https://push.example.com/path#fragment", "https://user:pw@push.example.com/send",
	} {
		body := `{"endpoint":"` + endpoint + `","keys":{"p256dh":"key","auth":"auth"}}`
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/push/subscribe", strings.NewReader(body))
		if err != nil { t.Fatal(err) }
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Scrumboy", "1")
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
		resp, err := ts.Client().Do(req)
		if err != nil { t.Fatal(err) }
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil { t.Fatal(err) }
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "invalid_push_endpoint") {
			t.Fatalf("endpoint %q: status=%d body=%s", endpoint, resp.StatusCode, string(b))
		}
	}
	subs, err := st.ListPushSubscriptionsByUser(ctx, user.ID)
	if err != nil || len(subs) != 0 {
		t.Fatalf("unsafe subscriptions persisted: %+v %v", subs, err)
	}
}
