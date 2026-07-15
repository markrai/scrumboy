package httpapi

import "log"

const defaultMailQueueCapacity = 1024

// mailDelivery is one queued outbound email.
type mailDelivery struct {
	To      string
	Subject string
	Body    string
	// LogRef is a non-sensitive identifier for log correlation (e.g.
	// "password-reset user=123"), never the email address or token.
	LogRef string
}

func (d mailDelivery) logRef() string { return d.LogRef }

type mailQueue = deliveryQueue[mailDelivery]

func newMailQueue(logger *log.Logger) *mailQueue {
	return newMailQueueWithCapacity(logger, defaultMailQueueCapacity)
}

func newMailQueueWithCapacity(logger *log.Logger, capacity int) *mailQueue {
	return newDeliveryQueue[mailDelivery](logger, capacity, "mail")
}
