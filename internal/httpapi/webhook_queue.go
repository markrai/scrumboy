package httpapi

import "log"

const defaultQueueCapacity = 1024

type webhookQueue = deliveryQueue[webhookDelivery]

func newWebhookQueue(logger *log.Logger) *webhookQueue {
	return newDeliveryQueue[webhookDelivery](logger, defaultQueueCapacity, "webhook")
}
