package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"time"
)

type webhookWorker struct {
	*retryWorker[webhookDelivery]
}

func newWebhookWorker(queue *webhookQueue, logger *log.Logger) *webhookWorker {
	client := &http.Client{Timeout: 10 * time.Second}
	send := func(d webhookDelivery) error {
		return sendWebhook(client, d)
	}
	return &webhookWorker{retryWorker: newRetryWorker(queue, logger, "webhook", send)}
}

func sendWebhook(client *http.Client, d webhookDelivery) error {
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

	resp, err := client.Do(req)
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
