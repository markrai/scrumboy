package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func TestDurableWebhookAdmissionEnforcesPayloadAndPerWebhookCapacity(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 21, 0, 0, 0, time.UTC)
	tooLarge := bytes.Repeat([]byte("x"), DurableWebhookMaxBodyBytes+1)
	if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "too-large", now, tooLarge)); !errors.Is(err, ErrDurableWebhookPayloadTooLarge) {
		t.Fatalf("oversized admission err=%v", err)
	}

	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(f.ctx, `
INSERT INTO webhook_deliveries(
  webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, http_attempt_count,
  next_attempt_at, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', 0, 0, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < DurableWebhookMaxActivePerWebhook-1; i++ {
		if _, err := stmt.ExecContext(f.ctx, row.ID, f.project.ID, fmt.Sprintf("quota-%03d", i), TodoEventCreated,
			now.UnixMilli(), []byte(`{}`), row.AuthorizationGeneration, now.UnixMilli(), now.UnixMilli(), now.Add(DurableWebhookActiveLifetime).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := f.store.AdmitDurableWebhookDelivery(context.Background(), durableAdmission(
				row, f.project.ID, fmt.Sprintf("boundary-%d", index), now, []byte(`{}`),
			))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	var accepted, rejected int
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrDurableWebhookCapacity) {
			rejected++
		} else {
			t.Fatalf("unexpected boundary error: %v", err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	usage, err := f.store.DurableWebhookDeliveryUsage(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveRows != DurableWebhookMaxActivePerWebhook || usage.TotalRows != DurableWebhookMaxActivePerWebhook {
		t.Fatalf("usage=%+v", usage)
	}

	_, err = f.db.ExecContext(f.ctx, `
INSERT INTO webhook_deliveries(
  webhook_id, project_id, event_id, event_type, event_timestamp, body,
  authorization_generation, status, attempt_count, http_attempt_count,
  next_attempt_at, created_at, expires_at
) VALUES (?, ?, 'direct-too-large', ?, ?, ?, ?, 'pending', 0, 0, ?, ?, ?)`,
		row.ID, f.project.ID, TodoEventCreated, now.UnixMilli(), tooLarge, row.AuthorizationGeneration,
		now.UnixMilli(), now.UnixMilli(), now.Add(DurableWebhookActiveLifetime).UnixMilli())
	if err == nil {
		t.Fatal("database accepted oversized durable payload")
	}
}

func TestDurableWebhookGlobalCapacityExactBoundariesAndCleanupRecovery(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 9, 7, 0, 0, 0, time.UTC)
	body := bytes.Repeat([]byte("x"), 32)
	tests := []struct {
		name        string
		activeRows  int64
		activeBytes int64
		totalRows   int64
		totalBytes  int64
		retained    bool
		atBoundary  func(DurableWebhookUsage) bool
	}{
		{
			name: "active_rows", activeRows: DurableWebhookMaxActiveRows - 1,
			atBoundary: func(usage DurableWebhookUsage) bool { return usage.ActiveRows == DurableWebhookMaxActiveRows },
		},
		{
			name: "active_payload", activeBytes: DurableWebhookMaxActivePayloadBytes - int64(len(body)),
			atBoundary: func(usage DurableWebhookUsage) bool {
				return usage.ActivePayloadBytes == DurableWebhookMaxActivePayloadBytes
			},
		},
		{
			name: "retained_rows", totalRows: DurableWebhookMaxRetainedRows - 1, retained: true,
			atBoundary: func(usage DurableWebhookUsage) bool { return usage.TotalRows == DurableWebhookMaxRetainedRows },
		},
		{
			name: "retained_payload", totalBytes: DurableWebhookMaxRetainedPayloadBytes - int64(len(body)), retained: true,
			atBoundary: func(usage DurableWebhookUsage) bool {
				return usage.TotalPayloadBytes == DurableWebhookMaxRetainedPayloadBytes
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := f.db.ExecContext(f.ctx, `DELETE FROM webhook_deliveries`); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(f.ctx, `
UPDATE webhook_delivery_usage
SET active_rows = ?, active_payload_bytes = ?, total_rows = ?, total_payload_bytes = ?
WHERE singleton = 1`, testCase.activeRows, testCase.activeBytes, testCase.totalRows, testCase.totalBytes); err != nil {
				t.Fatal(err)
			}
			eventID := "boundary-" + testCase.name
			result, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, eventID, now, body))
			if err != nil || result != DurableWebhookAdmissionInserted {
				t.Fatalf("boundary admission=%q err=%v", result, err)
			}
			usage, err := f.store.DurableWebhookDeliveryUsage(f.ctx)
			if err != nil || !testCase.atBoundary(usage) {
				t.Fatalf("boundary usage=%+v err=%v", usage, err)
			}
			if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, eventID+"-rejected", now, body)); !errors.Is(err, ErrDurableWebhookCapacity) {
				t.Fatalf("over-boundary err=%v", err)
			}
			if testCase.retained {
				claims, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now, 1, time.Minute)
				if err != nil || len(claims) != 1 {
					t.Fatalf("claims=%v err=%v", claims, err)
				}
				if updated, err := f.store.CompleteDurableWebhookDelivery(f.ctx, claims[0].ID, claims[0].LeaseToken, now); err != nil || !updated {
					t.Fatalf("complete=%v err=%v", updated, err)
				}
			}
			if _, err := f.db.ExecContext(f.ctx, `UPDATE webhook_deliveries SET expires_at = ? WHERE event_id = ?`, now.Add(-time.Millisecond).UnixMilli(), eventID); err != nil {
				t.Fatal(err)
			}
			maintenance, err := f.store.MaintainDurableWebhookDeliveries(f.ctx, now, DurableWebhookJanitorBatch)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.retained && maintenance.Deleted != 1 {
				t.Fatalf("maintenance=%+v", maintenance)
			}
			if !testCase.retained && maintenance.Terminalized != 1 {
				t.Fatalf("maintenance=%+v", maintenance)
			}
			result, err = f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, eventID+"-recovered", now, body))
			if err != nil || result != DurableWebhookAdmissionInserted {
				t.Fatalf("recovered admission=%q err=%v", result, err)
			}
		})
	}
}

func TestDurableWebhookFileBackedWALUsageSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable-quota.db")
	open := func() *sql.DB {
		database, err := db.Open(path, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
		if err != nil {
			t.Fatal(err)
		}
		return database
	}
	database := open()
	if err := migrate.Apply(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	st := New(database, nil)
	owner, err := st.BootstrapUser(context.Background(), "file-backed@example.com", "password123", "File Backed")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ctx, "File-backed quota")
	if err != nil {
		t.Fatal(err)
	}
	hook, err := st.CreateWebhook(ctx, owner.ID, CreateWebhookInput{
		ProjectID: project.ID, URL: "https://file-backed.example/hook",
		Events: []string{TodoEventCreated}, DeliveryMode: WebhookDeliveryDurable,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListWebhooksByProject(ctx, project.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != hook.ID {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	now := time.Date(2026, 11, 9, 0, 30, 0, 0, time.UTC)
	body := bytes.Repeat([]byte("x"), DurableWebhookMaxBodyBytes)
	if result, err := st.AdmitDurableWebhookDelivery(ctx, durableAdmission(rows[0], project.ID, "file-backed", now, body)); err != nil || result != DurableWebhookAdmissionInserted {
		t.Fatalf("admit=%q err=%v", result, err)
	}
	var journalMode string
	if err := database.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode=%q", journalMode)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open()
	defer reopened.Close()
	usage, err := New(reopened, nil).DurableWebhookDeliveryUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveRows != 1 || usage.TotalRows != 1 || usage.ActivePayloadBytes != DurableWebhookMaxBodyBytes || usage.TotalPayloadBytes != DurableWebhookMaxBodyBytes {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestDurableWebhookAttemptReservationsAreStrictlyBounded(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 22, 0, 0, 0, time.UTC)
	if result, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "attempt-limit", now, []byte(`{}`))); err != nil || result != DurableWebhookAdmissionInserted {
		t.Fatalf("admit=%q err=%v", result, err)
	}
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now, 1, time.Hour)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%v err=%v", claims, err)
	}
	claim := claims[0]
	for i := 1; i <= DurableWebhookMaxHTTPAttempts; i++ {
		reservation, err := f.store.ReserveDurableWebhookAttempt(f.ctx, claim.ID, claim.LeaseToken, now)
		if err != nil || reservation.Status != DurableWebhookAttemptReserved || reservation.HTTPAttemptCount != i {
			t.Fatalf("reservation %d=%+v err=%v", i, reservation, err)
		}
	}
	reservation, err := f.store.ReserveDurableWebhookAttempt(f.ctx, claim.ID, claim.LeaseToken, now)
	if err != nil || reservation.Status != DurableWebhookAttemptExhausted || reservation.HTTPAttemptCount != DurableWebhookMaxHTTPAttempts {
		t.Fatalf("ninth reservation=%+v err=%v", reservation, err)
	}
	var status, class string
	var attempts int
	if err := f.db.QueryRowContext(f.ctx, `
SELECT status, last_error_class, http_attempt_count
FROM webhook_deliveries WHERE id = ?`, claim.ID).Scan(&status, &class, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || class != WebhookErrorAttemptsExhausted || attempts != DurableWebhookMaxHTTPAttempts {
		t.Fatalf("status=%q class=%q attempts=%d", status, class, attempts)
	}
}

func TestDurableWebhookClaimsRaceOnceAndPreReservationCrashesConsumeNoHTTPAttempts(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 23, 0, 0, 0, time.UTC)
	if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "claim-race", now, []byte(`{}`))); err != nil {
		t.Fatal(err)
	}
	type result struct {
		claims []DurableWebhookDelivery
		err    error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			claims, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now, 1, time.Minute)
			results <- result{claims: claims, err: err}
		}()
	}
	var claimed []DurableWebhookDelivery
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		claimed = append(claimed, got.claims...)
	}
	if len(claimed) != 1 {
		t.Fatalf("combined claims=%d", len(claimed))
	}
	for i := 1; i <= 3; i++ {
		reclaimed, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now.Add(time.Duration(i)*time.Minute), 1, time.Minute)
		if err != nil || len(reclaimed) != 1 {
			t.Fatalf("reclaim %d=%v err=%v", i, reclaimed, err)
		}
		claimed = reclaimed
	}
	if claimed[0].HTTPAttemptCount != 0 || claimed[0].AttemptCount != 4 {
		t.Fatalf("claim counters=%+v", claimed[0])
	}
}

func TestDurableWebhookMaintenanceHonorsLeaseAndRetention(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"delivered", "failed", "skipped", "active-expired", "leased-unexpired"} {
		if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, id, now, []byte(`{"id":"x"}`))); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now, 5, time.Hour)
	if err != nil || len(claims) != 5 {
		t.Fatalf("claims=%d err=%v", len(claims), err)
	}
	byEvent := make(map[string]DurableWebhookDelivery)
	for _, claim := range claims {
		byEvent[claim.EventID] = claim
	}
	if ok, err := f.store.CompleteDurableWebhookDelivery(f.ctx, byEvent["delivered"].ID, byEvent["delivered"].LeaseToken, now); err != nil || !ok {
		t.Fatalf("complete=%v err=%v", ok, err)
	}
	if ok, err := f.store.FailDurableWebhookDelivery(f.ctx, byEvent["failed"].ID, byEvent["failed"].LeaseToken, WebhookErrorHTTP4xx, nil, now); err != nil || !ok {
		t.Fatalf("fail=%v err=%v", ok, err)
	}
	if ok, err := f.store.SkipDurableWebhookDelivery(f.ctx, byEvent["skipped"].ID, byEvent["skipped"].LeaseToken, WebhookErrorUnauthorized, now); err != nil || !ok {
		t.Fatalf("skip=%v err=%v", ok, err)
	}
	if _, err := f.db.ExecContext(f.ctx, `
UPDATE webhook_deliveries SET expires_at = ?
WHERE event_id IN ('delivered', 'failed', 'skipped', 'active-expired')`, now.Add(-time.Millisecond).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `
UPDATE webhook_deliveries SET lease_expires_at = ? WHERE event_id = 'active-expired'`, now.Add(-time.Millisecond).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	maintenance, err := f.store.MaintainDurableWebhookDeliveries(f.ctx, now, DurableWebhookJanitorBatch)
	if err != nil {
		t.Fatal(err)
	}
	if maintenance.Terminalized != 1 || maintenance.Deleted != 3 {
		t.Fatalf("maintenance=%+v", maintenance)
	}
	var leasedStatus string
	if err := f.db.QueryRowContext(f.ctx, `SELECT status FROM webhook_deliveries WHERE event_id = 'leased-unexpired'`).Scan(&leasedStatus); err != nil {
		t.Fatal(err)
	}
	if leasedStatus != "leased" {
		t.Fatalf("unexpired lease status=%q", leasedStatus)
	}
	usage, err := f.store.DurableWebhookDeliveryUsage(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.ActiveRows != 1 || usage.TotalRows != 2 {
		t.Fatalf("usage=%+v", usage)
	}
}

func TestDurableWebhookCleanupAdmissionAndClaimKeepUsageConsistent(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	for _, eventID := range []string{"cleanup-old", "cleanup-pending-one", "cleanup-pending-two"} {
		if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, eventID, now, []byte(`{}`))); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now, 1, time.Hour)
	if err != nil || len(claims) != 1 {
		t.Fatalf("initial claims=%v err=%v", claims, err)
	}
	if ok, err := f.store.CompleteDurableWebhookDelivery(f.ctx, claims[0].ID, claims[0].LeaseToken, now); err != nil || !ok {
		t.Fatalf("complete=%v err=%v", ok, err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE webhook_deliveries SET expires_at = ? WHERE id = ?`, now.Add(-time.Millisecond).UnixMilli(), claims[0].ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errorsCh := make(chan error, 3)
	go func() {
		<-start
		_, err := f.store.MaintainDurableWebhookDeliveries(context.Background(), now, DurableWebhookJanitorBatch)
		errorsCh <- err
	}()
	go func() {
		<-start
		_, err := f.store.AdmitDurableWebhookDelivery(context.Background(), durableAdmission(row, f.project.ID, "cleanup-new", now, []byte(`{}`)))
		errorsCh <- err
	}()
	go func() {
		<-start
		_, err := f.store.ClaimDueDurableWebhookDeliveries(context.Background(), now, 1, time.Hour)
		errorsCh <- err
	}()
	close(start)
	for i := 0; i < 3; i++ {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	usage, err := f.store.DurableWebhookDeliveryUsage(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var activeRows, activeBytes, totalRows, totalBytes int64
	if err := f.db.QueryRowContext(f.ctx, `
SELECT
  COALESCE(SUM(CASE WHEN status IN ('pending', 'leased') THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status IN ('pending', 'leased') THEN length(body) ELSE 0 END), 0),
  COUNT(*), COALESCE(SUM(length(body)), 0)
FROM webhook_deliveries`).Scan(&activeRows, &activeBytes, &totalRows, &totalBytes); err != nil {
		t.Fatal(err)
	}
	if usage.ActiveRows != activeRows || usage.ActivePayloadBytes != activeBytes || usage.TotalRows != totalRows || usage.TotalPayloadBytes != totalBytes {
		t.Fatalf("usage=%+v actual active_rows=%d active_bytes=%d total_rows=%d total_bytes=%d", usage, activeRows, activeBytes, totalRows, totalBytes)
	}
}
