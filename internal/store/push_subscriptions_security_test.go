package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPushSubscriptionEndpointsAreValidatedInStore(t *testing.T) {
	st, sqlDB, cleanup := newTestStoreWithSQL(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "push-endpoint-validation@example.com", "pass1234A!", "Push")
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:8000/send",
		"https://10.0.0.1/send",
		"https://169.254.169.254/latest",
		"https://localhost/send",
		"https://push.example.com/send#fragment",
	} {
		if err := st.UpsertPushSubscription(ctx, user.ID, endpoint, "key", "auth", nil); !errors.Is(err, ErrValidation) {
			t.Fatalf("endpoint %q: got %v, want ErrValidation", endpoint, err)
		}
	}
	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM push_subscriptions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid endpoints persisted: count=%d err=%v", count, err)
	}
	endpoint := "https://push.example.com/valid?token=secret"
	if err := st.UpsertPushSubscription(ctx, user.ID, endpoint, "key", "auth", nil); err != nil {
		t.Fatalf("valid endpoint rejected: %v", err)
	}
	legacy := "http://127.0.0.1/private"
	now := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.ExecContext(ctx,
		"INSERT INTO push_subscriptions (user_id,endpoint,p256dh,auth,created_at,updated_at) VALUES (?,?,?,?,?,?)",
		user.ID, legacy, "key", "auth", now, now,
	); err != nil {
		t.Fatalf("seed legacy subscription: %v", err)
	}
	if err := st.DeletePushSubscription(ctx, user.ID, legacy); err != nil {
		t.Fatalf("legacy unsubscribe rejected: %v", err)
	}
	subs, err := st.ListPushSubscriptionsByUser(ctx, user.ID)
	if err != nil || len(subs) != 1 || subs[0].Endpoint != endpoint {
		t.Fatalf("unexpected subscriptions after legacy unsubscribe: %+v err=%v", subs, err)
	}
}
