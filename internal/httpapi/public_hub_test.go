package httpapi

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func receivePublicEvent(t *testing.T, events <-chan PublicEvent) (PublicEvent, bool) {
	t.Helper()
	select {
	case event, open := <-events:
		return event, open
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for public hub event")
		return 0, false
	}
}

func TestPublicHubRefreshIsProjectScopedAndTyped(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
	first, err := hub.Subscribe(1, "ip:first")
	if err != nil {
		t.Fatalf("Subscribe first: %v", err)
	}
	second, err := hub.Subscribe(2, "ip:second")
	if err != nil {
		t.Fatalf("Subscribe second: %v", err)
	}

	hub.RefreshPublicProject(1)
	if event, open := receivePublicEvent(t, first.Events); !open || event != PublicEventRefreshNeeded {
		t.Fatalf("first event/open = %v/%v", event, open)
	}
	select {
	case event := <-second.Events:
		t.Fatalf("different project received event %v", event)
	default:
	}
}

func TestPublicHubEnforcesIndependentConcurrencyLimitsAndReleasesCounters(t *testing.T) {
	t.Run("per IP", func(t *testing.T) {
		hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 1, PerProject: 10, Buffer: 1})
		first, err := hub.Subscribe(1, "ip:one")
		if err != nil {
			t.Fatalf("Subscribe first: %v", err)
		}
		if _, err := hub.Subscribe(2, "ip:one"); !errors.Is(err, ErrPublicStreamLimit) {
			t.Fatalf("second Subscribe error = %v", err)
		}
		first.Unsubscribe()
		if _, err := hub.Subscribe(2, "ip:one"); err != nil {
			t.Fatalf("Subscribe after release: %v", err)
		}
	})

	t.Run("per project", func(t *testing.T) {
		hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 1, Buffer: 1})
		if _, err := hub.Subscribe(1, "ip:one"); err != nil {
			t.Fatalf("Subscribe first: %v", err)
		}
		if _, err := hub.Subscribe(1, "ip:two"); !errors.Is(err, ErrPublicStreamLimit) {
			t.Fatalf("second Subscribe error = %v", err)
		}
	})

	t.Run("global", func(t *testing.T) {
		hub := NewPublicHub(PublicHubLimits{Global: 1, PerIP: 10, PerProject: 10, Buffer: 1})
		if _, err := hub.Subscribe(1, "ip:one"); err != nil {
			t.Fatalf("Subscribe first: %v", err)
		}
		if _, err := hub.Subscribe(2, "ip:two"); !errors.Is(err, ErrPublicStreamLimit) {
			t.Fatalf("second Subscribe error = %v", err)
		}
	})
}

func TestPublicHubOverflowEvictsSlowSubscriber(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 1, PerIP: 1, PerProject: 1, Buffer: 1})
	subscription, err := hub.Subscribe(7, "ip:slow")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	hub.RefreshPublicProject(7)
	hub.RefreshPublicProject(7)

	if event, open := receivePublicEvent(t, subscription.Events); !open || event != PublicEventRefreshNeeded {
		t.Fatalf("buffered event/open = %v/%v", event, open)
	}
	if _, open := receivePublicEvent(t, subscription.Events); open {
		t.Fatal("overflowed subscription remained open")
	}
	if _, err := hub.Subscribe(8, "ip:slow"); err != nil {
		t.Fatalf("overflow did not release counters: %v", err)
	}
}

func TestPublicHubRevokeDropsRefreshAndClosesOnlyTargetProject(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
	revoked, _ := hub.Subscribe(7, "ip:one")
	other, _ := hub.Subscribe(8, "ip:two")
	hub.RefreshPublicProject(7)
	hub.RevokePublicProject(7)

	if event, open := receivePublicEvent(t, revoked.Events); !open || event != PublicEventAccessRevoked {
		t.Fatalf("terminal event/open = %v/%v", event, open)
	}
	if _, open := receivePublicEvent(t, revoked.Events); open {
		t.Fatal("revoked subscription remained open")
	}
	if !other.Active() {
		t.Fatal("other project subscription was revoked")
	}
}

func TestPublicHubUnsubscribeAndShutdownAreIdempotent(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 2})
	subscription, _ := hub.Subscribe(7, "ip:one")
	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			subscription.Unsubscribe()
		}()
	}
	wait.Wait()
	if _, open := receivePublicEvent(t, subscription.Events); open {
		t.Fatal("unsubscribed channel remained open")
	}

	remaining, _ := hub.Subscribe(8, "ip:two")
	hub.Shutdown()
	hub.Shutdown()
	if _, open := receivePublicEvent(t, remaining.Events); open {
		t.Fatal("shutdown channel remained open")
	}
	if _, err := hub.Subscribe(9, "ip:three"); !errors.Is(err, ErrPublicHubClosed) {
		t.Fatalf("Subscribe after Shutdown error = %v", err)
	}
}

func TestPublicHubConcurrentRefreshRevokeAndUnsubscribe(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 100, PerIP: 100, PerProject: 100, Buffer: 4})
	subscriptions := make([]*PublicSubscription, 0, 64)
	for index := range 64 {
		subscription, err := hub.Subscribe(42, "ip:"+string(rune(index+1)))
		if err != nil {
			t.Fatalf("Subscribe %d: %v", index, err)
		}
		subscriptions = append(subscriptions, subscription)
	}

	start := make(chan struct{})
	var wait sync.WaitGroup
	for _, subscription := range subscriptions {
		wait.Add(1)
		go func(subscription *PublicSubscription) {
			defer wait.Done()
			<-start
			subscription.Unsubscribe()
		}(subscription)
	}
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for range 100 {
			hub.RefreshPublicProject(42)
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		hub.RevokePublicProject(42)
	}()
	close(start)
	wait.Wait()
	hub.Shutdown()
}

func TestDrainQueuedPublicRefreshesCoalescesAndReportsTerminalStates(t *testing.T) {
	events := make(chan PublicEvent, 4)
	events <- PublicEventRefreshNeeded
	events <- PublicEventRefreshNeeded
	if state := drainQueuedPublicRefreshes(events); state != publicQueueOpen || len(events) != 0 {
		t.Fatalf("open drain state/len = %v/%d", state, len(events))
	}
	events <- PublicEventRefreshNeeded
	events <- PublicEventAccessRevoked
	close(events)
	if state := drainQueuedPublicRefreshes(events); state != publicQueueRevoked {
		t.Fatalf("revoked drain state = %v", state)
	}
	if state := drainQueuedPublicRefreshes(events); state != publicQueueClosed {
		t.Fatalf("closed drain state = %v", state)
	}
}

func TestPublicHubRevokedSubscriptionNeverReactivatesAndCountersRelease(t *testing.T) {
	hub := NewPublicHub(PublicHubLimits{Global: 10, PerIP: 10, PerProject: 10, Buffer: 8})
	subscription, _ := hub.Subscribe(7, "ip:one")
	for range 8 {
		hub.RefreshPublicProject(7)
	}
	hub.RevokePublicProject(7)
	if subscription.Active() {
		t.Fatal("revoked subscription still active")
	}
	// Queued invalidations were replaced by exactly one terminal signal.
	if event, open := receivePublicEvent(t, subscription.Events); !open || event != PublicEventAccessRevoked {
		t.Fatalf("terminal event/open = %v/%v", event, open)
	}
	if _, open := receivePublicEvent(t, subscription.Events); open {
		t.Fatal("revoked channel remained open")
	}
	// A later refresh (for example after republish) cannot reach it.
	hub.RefreshPublicProject(7)
	hub.RevokePublicProject(7)
	subscription.Unsubscribe()
	if counts := hub.counts(); counts.Global != 0 || len(counts.ByIP) != 0 || len(counts.ByProject) != 0 {
		t.Fatalf("counters not released: %+v", counts)
	}
}
