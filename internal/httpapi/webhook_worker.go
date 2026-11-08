package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"time"

	"scrumboy/internal/safehttp"
	"scrumboy/internal/store"
)

var errWebhookRedirect = errors.New("webhook redirects are not allowed")
var errWebhookIneligible = errors.New("webhook subscription is no longer eligible for delivery")

type webhookDeliveryAuthorizer interface {
	GetWebhookDeliveryTarget(context.Context, int64) (store.WebhookDeliveryTarget, bool, error)
}

type webhookWorker struct {
	*retryWorker[webhookDelivery]
}

func newWebhookWorker(authorizer webhookDeliveryAuthorizer, queue *webhookQueue, logger *log.Logger) *webhookWorker {
	return newWebhookWorkerWithClient(authorizer, queue, logger, newWebhookHTTPClient())
}

func newWebhookWorkerWithClient(authorizer webhookDeliveryAuthorizer, queue *webhookQueue, logger *log.Logger, client *http.Client) *webhookWorker {
	send := func(d webhookDelivery) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		target, eligible, err := authorizer.GetWebhookDeliveryTarget(ctx, d.WebhookID)
		if err != nil {
			return err
		}
		if !eligible {
			return errWebhookIneligible
		}
		// URL and secret copied into the queue are only hints for logging. Always
		// send using the current authoritative subscription row.
		d.URL = target.URL
		d.Secret = target.Secret
		return sendWebhook(client, d)
	}
	inner := newRetryWorker(queue, logger, "webhook", send)
	inner.isPermanent = isPermanentWebhookError
	return &webhookWorker{retryWorker: inner}
}

func newWebhookHTTPClient() *http.Client {
	return newWebhookHTTPClientWithDialer(safehttp.Dialer{})
}

func newWebhookHTTPClientWithDialer(d safehttp.Dialer) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errWebhookRedirect
		},
		Transport: &http.Transport{
			Proxy:               nil,
			DisableKeepAlives:   true,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 5 * time.Second,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext:         safehttp.NewDialContext(d),
		},
	}
}

func isPermanentWebhookError(err error) bool {
	return errors.Is(err, safehttp.ErrForbidden) || errors.Is(err, errWebhookRedirect) || errors.Is(err, errWebhookIneligible)
}

func sendWebhook(client *http.Client, d webhookDelivery) error {
	req, err := http.NewRequest(http.MethodPost, d.URL, bytes.NewReader(d.Body))
	if err != nil {
		return &safeWebhookDeliveryError{cause: err}
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
		return &safeWebhookDeliveryError{cause: err}
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return &webhookHTTPError{StatusCode: resp.StatusCode}
}

// safeWebhookDeliveryError keeps request URLs (including userinfo, path and
// query credentials) out of worker logs while preserving errors.Is/As for the
// existing SSRF and redirect classifications.
type safeWebhookDeliveryError struct {
	cause error
}

func (e *safeWebhookDeliveryError) Error() string { return "webhook request failed" }
func (e *safeWebhookDeliveryError) Unwrap() error { return e.cause }

type webhookHTTPError struct {
	StatusCode int
}

func (e *webhookHTTPError) Error() string {
	return "webhook endpoint returned " + http.StatusText(e.StatusCode)
}
