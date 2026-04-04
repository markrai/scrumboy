package httpapi

import "time"

type webhookDelivery struct {
	WebhookID int64
	URL       string
	Secret    *string
	EventID   string
	EventType string
	Timestamp time.Time
	Body      []byte // pre-serialized JSON payload
}
