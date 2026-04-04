package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

type webhookWorker struct {
	queue  *webhookQueue
	logger *log.Logger
	client *http.Client
}

func newWebhookWorker(queue *webhookQueue, logger *log.Logger) *webhookWorker {
	return &webhookWorker{
		queue:  queue,
		logger: logger,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (w *webhookWorker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			w.flush()
			return
		case <-w.queue.Wait():
			w.flush()
		}
	}
}

func (w *webhookWorker) flush() {
	for {
		batch := w.queue.Drain()
		if len(batch) == 0 {
			return
		}
		for _, d := range batch {
			w.deliver(d)
		}
	}
}

func (w *webhookWorker) deliver(d webhookDelivery) {
	backoff := [3]time.Duration{0, 100 * time.Millisecond, 400 * time.Millisecond}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if backoff[attempt] > 0 {
			time.Sleep(backoff[attempt])
		}
		lastErr = w.send(d)
		if lastErr == nil {
			return
		}
	}
	w.logger.Printf("webhook delivery failed after 3 attempts: webhook_id=%d event=%s url=%s err=%v",
		d.WebhookID, d.EventID, d.URL, lastErr)
}

func (w *webhookWorker) send(d webhookDelivery) error {
	req, err := http.NewRequest(http.MethodPost, d.URL, bytes.NewReader(d.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Scrumboy-Event", d.EventType)
	req.Header.Set("X-Scrumboy-Delivery", d.EventID)

	if d.Secret != nil && *d.Secret != "" {
		mac := hmac.New(sha256.New, []byte(*d.Secret))
		mac.Write(d.Body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Scrumboy-Signature", sig)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &webhookHTTPError{StatusCode: resp.StatusCode}
}

type webhookHTTPError struct {
	StatusCode int
}

func (e *webhookHTTPError) Error() string {
	return "webhook endpoint returned " + http.StatusText(e.StatusCode)
}
