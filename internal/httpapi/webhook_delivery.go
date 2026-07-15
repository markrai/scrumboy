package httpapi

import (
	"fmt"
	"time"
)

type webhookDelivery struct {
	WebhookID int64
	URL       string
	Secret    *string
	EventID   string
	EventType string
	Timestamp time.Time
	Body      []byte // pre-serialized JSON payload
}

func (d webhookDelivery) logRef() string {
	return fmt.Sprintf("webhook_id=%d event=%s url=%s", d.WebhookID, d.EventID, d.URL)
}
