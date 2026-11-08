package httpapi

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"scrumboy/internal/safehttp"
	"scrumboy/internal/store"
)

const (
	defaultDurableWebhookPollInterval = time.Second
	defaultDurableWebhookLease        = 30 * time.Second
	defaultDurableWebhookClaimLimit   = 16
	defaultDurableWebhookConcurrency  = 4
	defaultDurableWebhookMaxAttempts  = 8
)

type durableWebhookNotifier interface {
	Notify()
}

type durableWebhookSchedule interface {
	durableWebhookNotifier
	Wait(context.Context) bool
	Close()
}

type pollingDurableWebhookSchedule struct {
	ticker *time.Ticker
	wake   chan struct{}
}

func newPollingDurableWebhookSchedule(interval time.Duration) *pollingDurableWebhookSchedule {
	if interval <= 0 {
		interval = defaultDurableWebhookPollInterval
	}
	return &pollingDurableWebhookSchedule{
		ticker: time.NewTicker(interval),
		wake:   make(chan struct{}, 1),
	}
}

func (s *pollingDurableWebhookSchedule) Notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *pollingDurableWebhookSchedule) Wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.ticker.C:
		return true
	case <-s.wake:
		return true
	}
}

func (s *pollingDurableWebhookSchedule) Close() { s.ticker.Stop() }

type durableWebhookStore interface {
	ClaimDueDurableWebhookDeliveries(context.Context, time.Time, int, time.Duration) ([]store.DurableWebhookDelivery, error)
	GetDurableWebhookDeliveryTarget(context.Context, int64, int64) (store.WebhookDeliveryTarget, bool, error)
	CompleteDurableWebhookDelivery(context.Context, int64, string, time.Time) (bool, error)
	RetryDurableWebhookDelivery(context.Context, int64, string, string, *int, time.Time) (bool, error)
	FailDurableWebhookDelivery(context.Context, int64, string, string, *int, time.Time) (bool, error)
	SkipDurableWebhookDelivery(context.Context, int64, string, string, time.Time) (bool, error)
}

type durableWebhookWorker struct {
	store         durableWebhookStore
	client        *http.Client
	logger        *log.Logger
	now           func() time.Time
	schedule      durableWebhookSchedule
	leaseDuration time.Duration
	claimLimit    int
	concurrency   int
	maxAttempts   int
	done          chan struct{}
}

func newDurableWebhookWorker(st durableWebhookStore, logger *log.Logger, schedule durableWebhookSchedule) *durableWebhookWorker {
	return newDurableWebhookWorkerWithClient(st, logger, schedule, newWebhookHTTPClient())
}

func newDurableWebhookWorkerWithClient(st durableWebhookStore, logger *log.Logger, schedule durableWebhookSchedule, client *http.Client) *durableWebhookWorker {
	return &durableWebhookWorker{
		store:         st,
		client:        client,
		logger:        logger,
		now:           time.Now,
		schedule:      schedule,
		leaseDuration: defaultDurableWebhookLease,
		claimLimit:    defaultDurableWebhookClaimLimit,
		concurrency:   defaultDurableWebhookConcurrency,
		maxAttempts:   defaultDurableWebhookMaxAttempts,
		done:          make(chan struct{}),
	}
}

func (w *durableWebhookWorker) Done() <-chan struct{} { return w.done }

func (w *durableWebhookWorker) Run(ctx context.Context) {
	defer close(w.done)
	defer w.schedule.Close()
	w.drain(ctx) // Resume committed pending work immediately after startup.
	for w.schedule.Wait(ctx) {
		w.drain(ctx)
	}
}

func (w *durableWebhookWorker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		claims, err := w.store.ClaimDueDurableWebhookDeliveries(ctx, w.now(), w.claimLimit, w.leaseDuration)
		if err != nil {
			if ctx.Err() == nil {
				w.logger.Printf("durable webhook claim failed: %v", err)
			}
			return
		}
		if len(claims) == 0 {
			return
		}
		w.deliverClaims(ctx, claims)
		if len(claims) < w.claimLimit {
			return
		}
	}
}

func (w *durableWebhookWorker) deliverClaims(ctx context.Context, claims []store.DurableWebhookDelivery) {
	limit := w.concurrency
	if limit < 1 {
		limit = 1
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, claim := range claims {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(delivery store.DurableWebhookDelivery) {
			defer wg.Done()
			defer func() { <-sem }()
			w.deliverOne(ctx, delivery)
		}(claim)
	}
	wg.Wait()
}

func (w *durableWebhookWorker) deliverOne(runCtx context.Context, claim store.DurableWebhookDelivery) {
	ctx, cancel := context.WithTimeout(runCtx, 5*time.Second)
	defer cancel()
	target, eligible, err := w.store.GetDurableWebhookDeliveryTarget(ctx, claim.WebhookID, claim.AuthorizationGeneration)
	if err != nil {
		w.retryOrFail(runCtx, claim, store.WebhookErrorInternal, nil)
		return
	}
	if !eligible {
		updated, updateErr := w.store.SkipDurableWebhookDelivery(runCtx, claim.ID, claim.LeaseToken, store.WebhookErrorUnauthorized, w.now())
		w.transitionLog(updated, updateErr, claim, "skip")
		return
	}
	err = sendWebhook(w.client, webhookDelivery{
		WebhookID: claim.WebhookID,
		URL:       target.URL,
		Secret:    target.Secret,
		EventID:   claim.EventID,
		EventType: claim.EventType,
		Timestamp: claim.EventTimestamp,
		Body:      claim.Body,
	})
	if err == nil {
		updated, updateErr := w.store.CompleteDurableWebhookDelivery(runCtx, claim.ID, claim.LeaseToken, w.now())
		w.transitionLog(updated, updateErr, claim, "complete")
		return
	}
	class, httpStatus, permanent := classifyDurableWebhookError(err)
	if permanent {
		updated, updateErr := w.store.FailDurableWebhookDelivery(runCtx, claim.ID, claim.LeaseToken, class, httpStatus, w.now())
		w.transitionLog(updated, updateErr, claim, "fail")
		return
	}
	w.retryOrFail(runCtx, claim, class, httpStatus)
}

func (w *durableWebhookWorker) retryOrFail(ctx context.Context, claim store.DurableWebhookDelivery, class string, httpStatus *int) {
	if claim.AttemptCount >= w.maxAttempts {
		updated, err := w.store.FailDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, store.WebhookErrorAttemptsExhausted, httpStatus, w.now())
		w.transitionLog(updated, err, claim, "exhaust")
		return
	}
	next := w.now().Add(durableWebhookBackoff(claim.AttemptCount))
	updated, err := w.store.RetryDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, class, httpStatus, next)
	w.transitionLog(updated, err, claim, "retry")
}

func (w *durableWebhookWorker) transitionLog(updated bool, err error, claim store.DurableWebhookDelivery, action string) {
	if err != nil {
		w.logger.Printf("durable webhook %s update failed delivery_id=%d webhook_id=%d event=%s: %v", action, claim.ID, claim.WebhookID, claim.EventID, err)
		return
	}
	if !updated {
		w.logger.Printf("durable webhook %s ignored stale lease delivery_id=%d webhook_id=%d event=%s", action, claim.ID, claim.WebhookID, claim.EventID)
	}
}

func durableWebhookBackoff(attempt int) time.Duration {
	switch attempt {
	case 0, 1:
		return time.Second
	case 2:
		return 5 * time.Second
	case 3:
		return 30 * time.Second
	case 4:
		return 2 * time.Minute
	case 5:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}

func classifyDurableWebhookError(err error) (string, *int, bool) {
	if errors.Is(err, safehttp.ErrForbidden) {
		return store.WebhookErrorSSRF, nil, true
	}
	if errors.Is(err, errWebhookRedirect) {
		return store.WebhookErrorRedirect, nil, true
	}
	var httpErr *webhookHTTPError
	if errors.As(err, &httpErr) {
		status := httpErr.StatusCode
		switch {
		case status >= 400 && status < 500:
			return store.WebhookErrorHTTP4xx, &status, false
		case status >= 500:
			return store.WebhookErrorHTTP5xx, &status, false
		default:
			return store.WebhookErrorHTTPOther, &status, false
		}
	}
	return store.WebhookErrorNetwork, nil, false
}
