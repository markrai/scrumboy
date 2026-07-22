package httpapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/golang-jwt/jwt/v5"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func TestPushNotifier_OnEvent_IgnoresNonTodoAssigned(t *testing.T) {
	st := newTestStore(t)
	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "pub", "priv", "t@t", true, false)
	ctx := context.Background()
	p.OnEvent(ctx, eventbus.Event{Type: "todo.created", Payload: []byte(`{}`)})
	// No crash; synchronous return (no vapid check reached for wrong type).
}

func TestPushNotifier_OnEvent_NoVapidDoesNotPanic(t *testing.T) {
	st := newTestStore(t)
	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "", "", "t@t", false, false)
	ctx := context.Background()
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:     1,
		TodoID:        1,
		ActorUserID:   2,
		ToAssigneeUID: ptrInt64(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	p.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", Payload: payload})
}

func TestPushNotifier_OnEvent_SelfAssignLeavesSubscriptions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	u, err := st.BootstrapUser(ctx, "a@b.com", "pass1234A!", "U")
	if err != nil {
		t.Fatal(err)
	}
	ep := "https://push.example.com/sub"
	if err := st.UpsertPushSubscription(ctx, u.ID, ep, "p256", "auth", nil); err != nil {
		t.Fatal(err)
	}

	p := newPushNotifier(st, log.New(os.Stderr, "", 0), "pub", "priv", "t@t", true, false)
	assignee := u.ID
	payload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:     1,
		TodoID:        1,
		Title:         "t",
		ActorUserID:   assignee,
		ToAssigneeUID: &assignee,
	})
	if err != nil {
		t.Fatal(err)
	}
	p.OnEvent(ctx, eventbus.Event{Type: "todo.assigned", ID: "e1", Payload: payload})
	time.Sleep(150 * time.Millisecond)

	subs, err := st.ListPushSubscriptionsByUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 1 || subs[0].Endpoint != ep {
		t.Fatalf("self-assign must not remove subscription; got %+v", subs)
	}
}

func TestPushNotifier_GeneratesExpectedVAPIDRequest(t *testing.T) {
	type capturedRequest struct {
		method          string
		authorization   string
		ttl             string
		contentEncoding string
		contentType     string
		bodyLength      int64
	}
	captured := make(chan capturedRequest, 1)
	pushService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured <- capturedRequest{
			method:          r.Method,
			authorization:   r.Header.Get("Authorization"),
			ttl:             r.Header.Get("TTL"),
			contentEncoding: r.Header.Get("Content-Encoding"),
			contentType:     r.Header.Get("Content-Type"),
			bodyLength:      r.ContentLength,
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer pushService.Close()

	vapidPrivate, vapidPublic, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatalf("generate VAPID keys: %v", err)
	}
	receiverPrivate, receiverX, receiverY, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate subscription key: %v", err)
	}
	_ = receiverPrivate
	receiverPublic := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), receiverX, receiverY))
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}

	st := newTestStore(t)
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "push@example.com", "pass1234A!", "Push User")
	if err != nil {
		t.Fatalf("bootstrap user: %v", err)
	}
	project, err := st.CreateProject(store.WithUserID(ctx, user.ID), "Push Test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.UpsertPushSubscription(ctx, user.ID, pushService.URL, receiverPublic, base64.RawURLEncoding.EncodeToString(authSecret), nil); err != nil {
		t.Fatalf("store subscription: %v", err)
	}

	prepared := prepareWebPushConfiguration("full", " \t"+vapidPublic+"\r\n", "\n"+vapidPrivate+"  ", "MailTo:ops@example.com")
	if prepared.status.State != pushStateEnabled {
		t.Fatalf("prepare whitespace-padded VAPID configuration: %+v", prepared.status)
	}
	notifier := newPushNotifier(st, log.New(os.Stderr, "", 0), prepared.publicKey, prepared.privateKey, prepared.subscriber, true, false)
	assignee := user.ID
	eventPayload, err := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:     project.ID,
		TodoID:        42,
		Title:         "Captured request",
		ActorUserID:   user.ID + 1,
		ToAssigneeUID: &assignee,
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	before := time.Now()
	sendCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go notifier.handle(sendCtx, eventbus.Event{Type: "todo.assigned", ProjectID: project.ID, Payload: eventPayload})

	var request capturedRequest
	select {
	case request = <-captured:
	case <-sendCtx.Done():
		t.Fatal("timed out waiting for intercepted Web Push request")
	}
	after := time.Now()
	if request.method != http.MethodPost || request.ttl != "86400" || request.contentEncoding != "aes128gcm" || request.contentType != "application/octet-stream" || request.bodyLength <= 0 {
		t.Fatalf("unexpected Web Push request: %+v", request)
	}
	if !strings.HasPrefix(request.authorization, "vapid t=") {
		t.Fatalf("unexpected Authorization header: %q", request.authorization)
	}
	authorizationParts := strings.Split(strings.TrimPrefix(request.authorization, "vapid "), ", ")
	if len(authorizationParts) != 2 || !strings.HasPrefix(authorizationParts[0], "t=") || !strings.HasPrefix(authorizationParts[1], "k=") {
		t.Fatalf("unexpected VAPID Authorization fields: %q", request.authorization)
	}
	jwtText := strings.TrimPrefix(authorizationParts[0], "t=")
	if gotKey := strings.TrimPrefix(authorizationParts[1], "k="); gotKey != vapidPublic {
		t.Fatalf("Authorization VAPID public key = %q, want %q", gotKey, vapidPublic)
	}
	decodedPublic, err := base64.RawURLEncoding.DecodeString(vapidPublic)
	if err != nil {
		t.Fatalf("decode VAPID public key: %v", err)
	}
	publicX, publicY := elliptic.Unmarshal(elliptic.P256(), decodedPublic)
	if publicX == nil || publicY == nil {
		t.Fatal("generated VAPID public key is not a P-256 point")
	}
	parsedToken, err := jwt.Parse(jwtText, func(token *jwt.Token) (any, error) {
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: publicX, Y: publicY}, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil || !parsedToken.Valid {
		t.Fatalf("parse and verify VAPID JWT: valid=%v err=%v", parsedToken != nil && parsedToken.Valid, err)
	}
	if parsedToken.Method.Alg() != "ES256" {
		t.Fatalf("VAPID JWT alg = %q, want ES256", parsedToken.Method.Alg())
	}
	claims, ok := parsedToken.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("unexpected VAPID claims type %T", parsedToken.Claims)
	}
	if got := claims["sub"]; got != "mailto:ops@example.com" {
		t.Fatalf("VAPID sub = %#v, want mailto:ops@example.com", got)
	}
	if strings.Contains(claims["sub"].(string), "mailto:mailto:") {
		t.Fatalf("VAPID sub contains a nested mailto prefix: %q", claims["sub"])
	}
	if got := claims["aud"]; got != pushService.URL {
		t.Fatalf("VAPID aud = %#v, want %q", got, pushService.URL)
	}
	exp, ok := claims["exp"].(float64)
	if !ok {
		t.Fatalf("VAPID exp has type %T, want number", claims["exp"])
	}
	wantMin := before.Add(12*time.Hour - time.Minute).Unix()
	wantMax := after.Add(12*time.Hour + time.Minute).Unix()
	if int64(math.Round(exp)) < wantMin || int64(math.Round(exp)) > wantMax {
		t.Fatalf("VAPID exp=%v outside [%d,%d]", exp, wantMin, wantMax)
	}
}

func ptrInt64(v int64) *int64 { return &v }
