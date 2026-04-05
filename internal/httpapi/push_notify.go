package httpapi

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"scrumboy/internal/eventbus"
)

// pushNotifier sends Web Push notifications on todo.assigned (async; does not block SSE).
type pushNotifier struct {
	store           storeAPI
	logger          *log.Logger
	vapidPublicKey  string
	vapidPrivateKey string
	subscriber      string // VAPID JWT sub claim (e.g. mailto:ops@example.com)
	debug           bool
}

func newPushNotifier(st storeAPI, logger *log.Logger, vapidPublic, vapidPrivate, subscriber string, debug bool) *pushNotifier {
	if subscriber == "" {
		subscriber = "mailto:scrumboy@localhost"
	}
	return &pushNotifier{
		store:           st,
		logger:          logger,
		vapidPublicKey:  vapidPublic,
		vapidPrivateKey: vapidPrivate,
		subscriber:      subscriber,
		debug:           debug,
	}
}

func (p *pushNotifier) OnEvent(ctx context.Context, e eventbus.Event) {
	if e.Type != "todo.assigned" {
		return
	}
	if p.vapidPublicKey == "" || p.vapidPrivateKey == "" {
		return
	}
	// Same pattern as webhook dispatcher: never block fanout / SSE.
	go p.handle(context.Background(), e)
}

func (p *pushNotifier) handle(ctx context.Context, e eventbus.Event) {
	var domain eventbus.TodoAssignedPayload
	if err := json.Unmarshal(e.Payload, &domain); err != nil {
		return
	}
	if domain.ToAssigneeUID == nil {
		return
	}
	assigneeID := *domain.ToAssigneeUID
	// Match realtime.ts: no push for self-assignment.
	if domain.ActorUserID != 0 && domain.ActorUserID == assigneeID {
		if p.debug {
			p.logger.Printf("push: skip self-assign event=%s todo=%d", e.ID, domain.TodoID)
		}
		return
	}

	proj, err := p.store.GetProject(ctx, e.ProjectID)
	if err != nil {
		if p.debug {
			p.logger.Printf("push: GetProject %d: %v", e.ProjectID, err)
		}
		return
	}
	slug := proj.Slug

	subs, err := p.store.ListPushSubscriptionsByUser(ctx, assigneeID)
	if err != nil {
		p.logger.Printf("push: list subscriptions user=%d: %v", assigneeID, err)
		return
	}
	if len(subs) == 0 {
		return
	}

	// projectSlug/todoId are for a future notification center; the service worker does not route taps on them yet.
	payload := map[string]any{
		"type":         "todo_assigned",
		"title":        "Assigned to you",
		"body":         domain.Title,
		"projectSlug":  slug,
		"todoId":       domain.TodoID,
		"scrumboyPush": true,
	}
	if p.debug {
		payload["debug"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	opts := &webpush.Options{
		Subscriber:      p.subscriber,
		VAPIDPublicKey:  p.vapidPublicKey,
		VAPIDPrivateKey: p.vapidPrivateKey,
		TTL:             86400,
	}

	for _, sub := range subs {
		wsub := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys: webpush.Keys{
				Auth:   sub.Auth,
				P256dh: sub.P256dh,
			},
		}
		resp, err := webpush.SendNotificationWithContext(ctx, body, wsub, opts)
		if err != nil {
			p.logger.Printf("push: send user=%d endpoint=%s err=%v", assigneeID, truncateEndpoint(sub.Endpoint), err)
			_ = p.store.DeletePushSubscriptionByEndpoint(ctx, sub.Endpoint)
			continue
		}
		if resp != nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
				if p.debug {
					p.logger.Printf("push: prune subscription status=%d endpoint=%s", resp.StatusCode, truncateEndpoint(sub.Endpoint))
				}
				_ = p.store.DeletePushSubscriptionByEndpoint(ctx, sub.Endpoint)
			} else if p.debug {
				p.logger.Printf("push: sent user=%d todo=%d status=%d", assigneeID, domain.TodoID, resp.StatusCode)
			}
		}
	}
}

func truncateEndpoint(ep string) string {
	if len(ep) <= 64 {
		return ep
	}
	return ep[:32] + "…" + ep[len(ep)-16:]
}
