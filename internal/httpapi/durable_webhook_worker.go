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
	defaultDurableWebhookPollInterval         = 30 * time.Second
	defaultDurableWebhookLease                = 2 * time.Minute
	defaultDurableWebhookClaimLimit           = 4
	defaultDurableWebhookConcurrency          = 4
	defaultDurableWebhookAuthorizationTimeout = 5 * time.Second
	defaultDurableWebhookAcknowledgeTimeout   = 5 * time.Second
	defaultDurableWebhookMaintenanceInterval  = 5 * time.Minute
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
	ReserveDurableWebhookAttempt(context.Context, int64, string, time.Time) (store.DurableWebhookAttemptReservation, error)
	CompleteDurableWebhookDelivery(context.Context, int64, string, time.Time) (bool, error)
	RetryDurableWebhookDelivery(context.Context, int64, string, string, *int, time.Time, time.Time) (bool, error)
	ReleaseDurableWebhookDelivery(context.Context, int64, string, time.Time) (bool, error)
	FailDurableWebhookDelivery(context.Context, int64, string, string, *int, time.Time) (bool, error)
	SkipDurableWebhookDelivery(context.Context, int64, string, string, time.Time) (bool, error)
	MaintainDurableWebhookDeliveries(context.Context, time.Time, int) (store.DurableWebhookMaintenance, error)
}

type durableWebhookWorker struct {
	store               durableWebhookStore
	client              *http.Client
	logger              *log.Logger
	now                 func() time.Time
	schedule            durableWebhookSchedule
	leaseDuration       time.Duration
	claimLimit          int
	concurrency         int
	maintenanceInterval time.Duration
	nextMaintenance     time.Time
	done                chan struct{}
}

func newDurableWebhookWorker(st durableWebhookStore, logger *log.Logger, schedule durableWebhookSchedule) *durableWebhookWorker {
	return newDurableWebhookWorkerWithClient(st, logger, schedule, newWebhookHTTPClient())
}

func newDurableWebhookWorkerWithClient(st durableWebhookStore, logger *log.Logger, schedule durableWebhookSchedule, client *http.Client) *durableWebhookWorker {
	return &durableWebhookWorker{
		store:               st,
		client:              client,
		logger:              logger,
		now:                 time.Now,
		schedule:            schedule,
		leaseDuration:       defaultDurableWebhookLease,
		claimLimit:          defaultDurableWebhookClaimLimit,
		concurrency:         defaultDurableWebhookConcurrency,
		maintenanceInterval: defaultDurableWebhookMaintenanceInterval,
		done:                make(chan struct{}),
	}
}

func (w *durableWebhookWorker) Done() <-chan struct{} { return w.done }

func (w *durableWebhookWorker) Run(ctx context.Context) {
	defer close(w.done)
	defer w.schedule.Close()
	w.drain(ctx)
	for w.schedule.Wait(ctx) {
		w.drain(ctx)
	}
}

func (w *durableWebhookWorker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		now := w.now().UTC()
		if w.nextMaintenance.IsZero() || !now.Before(w.nextMaintenance) {
			if _, err := w.store.MaintainDurableWebhookDeliveries(ctx, now, store.DurableWebhookJanitorBatch); err != nil {
				if ctx.Err() == nil {
					w.logger.Printf("durable webhook maintenance failed: %v", err)
				}
			}
			w.nextMaintenance = now.Add(w.maintenanceInterval)
		}
		claimLimit := w.claimLimit
		if claimLimit > w.concurrency {
			claimLimit = w.concurrency
		}
		if claimLimit < 1 {
			claimLimit = 1
		}
		claims, err := w.store.ClaimDueDurableWebhookDeliveries(ctx, now, claimLimit, w.leaseDuration)
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
		if len(claims) < claimLimit {
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
	for i, claim := range claims {
		select {
		case <-ctx.Done():
			for _, unstarted := range claims[i:] {
				w.release(unstarted)
			}
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
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
	if runCtx.Err() != nil {
		w.release(claim)
		return
	}
	authCtx, cancel := context.WithTimeout(runCtx, defaultDurableWebhookAuthorizationTimeout)
	target, eligible, err := w.store.GetDurableWebhookDeliveryTarget(authCtx, claim.WebhookID, claim.AuthorizationGeneration)
	cancel()
	if err != nil {
		if runCtx.Err() != nil {
			w.release(claim)
			return
		}
		w.retry(claim, store.WebhookErrorInternal, nil, claim.AttemptCount)
		return
	}
	if !eligible {
		ctx, ackCancel := w.acknowledgementContext()
		updated, updateErr := w.store.SkipDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, store.WebhookErrorUnauthorized, w.now())
		ackCancel()
		w.transitionLog(updated, updateErr, claim, "skip")
		return
	}
	if runCtx.Err() != nil {
		w.release(claim)
		return
	}
	reserveCtx, reserveCancel := context.WithTimeout(runCtx, defaultDurableWebhookAcknowledgeTimeout)
	reservation, err := w.store.ReserveDurableWebhookAttempt(reserveCtx, claim.ID, claim.LeaseToken, w.now())
	reserveCancel()
	if err != nil {
		if runCtx.Err() != nil {
			w.release(claim)
			return
		}
		w.retry(claim, store.WebhookErrorInternal, nil, claim.AttemptCount)
		return
	}
	if reservation.Status != store.DurableWebhookAttemptReserved {
		if reservation.Status == store.DurableWebhookAttemptStale {
			w.transitionLog(false, nil, claim, "reserve")
		}
		return
	}
	if runCtx.Err() != nil {
		w.retry(claim, store.WebhookErrorInternal, nil, reservation.HTTPAttemptCount)
		return
	}
	err = sendWebhookContext(runCtx, w.client, webhookDelivery{
		WebhookID: claim.WebhookID,
		URL:       target.URL,
		Secret:    target.Secret,
		EventID:   claim.EventID,
		EventType: claim.EventType,
		Timestamp: claim.EventTimestamp,
		Body:      claim.Body,
	})
	if err == nil {
		ctx, ackCancel := w.acknowledgementContext()
		updated, updateErr := w.store.CompleteDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, w.now())
		ackCancel()
		w.transitionLog(updated, updateErr, claim, "complete")
		return
	}
	class, httpStatus, permanent := classifyDurableWebhookError(err)
	if permanent {
		ctx, ackCancel := w.acknowledgementContext()
		updated, updateErr := w.store.FailDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, class, httpStatus, w.now())
		ackCancel()
		w.transitionLog(updated, updateErr, claim, "fail")
		return
	}
	w.retry(claim, class, httpStatus, reservation.HTTPAttemptCount)
}

func (w *durableWebhookWorker) acknowledgementContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), defaultDurableWebhookAcknowledgeTimeout)
}

func (w *durableWebhookWorker) retry(claim store.DurableWebhookDelivery, class string, httpStatus *int, backoffAttempt int) {
	now := w.now().UTC()
	next := now.Add(durableWebhookBackoff(backoffAttempt))
	ctx, cancel := w.acknowledgementContext()
	updated, err := w.store.RetryDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, class, httpStatus, next, now)
	cancel()
	w.transitionLog(updated, err, claim, "retry")
}

func (w *durableWebhookWorker) release(claim store.DurableWebhookDelivery) {
	ctx, cancel := w.acknowledgementContext()
	updated, err := w.store.ReleaseDurableWebhookDelivery(ctx, claim.ID, claim.LeaseToken, w.now())
	cancel()
	w.transitionLog(updated, err, claim, "release")
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
		case status >= 300 && status < 400:
			return store.WebhookErrorHTTP3xx, &status, true
		case status == http.StatusRequestTimeout || status == http.StatusTooManyRequests:
			return store.WebhookErrorHTTP4xx, &status, false
		case status >= 400 && status < 500:
			return store.WebhookErrorHTTP4xx, &status, true
		case status >= 500 && status < 600:
			return store.WebhookErrorHTTP5xx, &status, false
		default:
			return store.WebhookErrorHTTPOther, &status, true
		}
	}
	return store.WebhookErrorNetwork, nil, false
}
