package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type durableWebhookFixture struct {
	store   *Store
	db      *sql.DB
	ctx     context.Context
	owner   User
	project Project
}

func newDurableWebhookFixture(t *testing.T) (durableWebhookFixture, func()) {
	t.Helper()
	st, database, cleanup := newTestStoreWithSQL(t)
	owner, err := st.BootstrapUser(context.Background(), "durable-owner@example.com", "password123", "Owner")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	ctx := WithUserID(context.Background(), owner.ID)
	project, err := st.CreateProject(ctx, "Durable webhook")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return durableWebhookFixture{store: st, db: database, ctx: ctx, owner: owner, project: project}, cleanup
}

func (f durableWebhookFixture) createDurableWebhook(t *testing.T, owner *int64) (Webhook, WebhookRow) {
	t.Helper()
	hook, err := f.store.CreateWebhook(f.ctx, f.owner.ID, CreateWebhookInput{
		ProjectID:          f.project.ID,
		URL:                "https://hooks.example.com/private?credential=hidden",
		Events:             []string{TodoEventCreated},
		Secret:             stringPtrStore("signing-secret"),
		ServiceOwnerUserID: owner,
		DeliveryMode:       WebhookDeliveryDurable,
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.store.ListWebhooksByProject(f.ctx, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == hook.ID {
			return hook, row
		}
	}
	t.Fatalf("webhook %d missing from delivery listing", hook.ID)
	return Webhook{}, WebhookRow{}
}

func stringPtrStore(value string) *string { return &value }

func durableAdmission(row WebhookRow, projectID int64, eventID string, now time.Time, body []byte) AdmitDurableWebhookDeliveryInput {
	return AdmitDurableWebhookDeliveryInput{
		WebhookID:               row.ID,
		ProjectID:               projectID,
		AuthorizationGeneration: row.AuthorizationGeneration,
		EventID:                 eventID,
		EventType:               TodoEventCreated,
		EventTimestamp:          now.Add(-time.Second),
		Body:                    body,
		AdmittedAt:              now,
	}
}

func TestWebhookDeliveryModeDefaultsAndDurableValidation(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()

	legacy, err := f.store.CreateWebhook(f.ctx, f.owner.ID, CreateWebhookInput{
		ProjectID: f.project.ID,
		URL:       "https://example.com/legacy",
		Events:    []string{"*", "future.event"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.DeliveryMode != WebhookDeliveryBestEffort {
		t.Fatalf("default mode=%q", legacy.DeliveryMode)
	}
	listed, err := f.store.ListWebhooks(f.ctx, f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if listed[0].DeliveryMode != WebhookDeliveryBestEffort {
		t.Fatalf("listed default mode=%q", listed[0].DeliveryMode)
	}

	durable, _ := f.createDurableWebhook(t, nil)
	if durable.DeliveryMode != WebhookDeliveryDurable {
		t.Fatalf("durable mode=%q", durable.DeliveryMode)
	}
	for _, events := range [][]string{{"*"}, {"todo.updated"}, {TodoEventCreated, "todo.updated"}} {
		_, err := f.store.CreateWebhook(f.ctx, f.owner.ID, CreateWebhookInput{
			ProjectID:    f.project.ID,
			URL:          "https://example.com/rejected",
			Events:       events,
			DeliveryMode: WebhookDeliveryDurable,
		})
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "only todo.created") {
			t.Fatalf("events=%v err=%v", events, err)
		}
	}
	_, err = f.store.CreateWebhook(f.ctx, f.owner.ID, CreateWebhookInput{
		ProjectID: f.project.ID, URL: "https://example.com/rejected-mode", Events: []string{TodoEventCreated}, DeliveryMode: "sometimes",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid mode err=%v", err)
	}
}

func TestDurableWebhookAdmissionIsIdempotentAndStoresNoDestinationOrSecret(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"event-one","type":"todo.created","payload":{"todoId":9}}`)
	in := durableAdmission(row, f.project.ID, "event-one", now, body)
	got, err := f.store.AdmitDurableWebhookDelivery(f.ctx, in)
	if err != nil || got != DurableWebhookAdmissionInserted {
		t.Fatalf("first admission=%q err=%v", got, err)
	}
	got, err = f.store.AdmitDurableWebhookDelivery(f.ctx, in)
	if err != nil || got != DurableWebhookAdmissionDuplicate {
		t.Fatalf("second admission=%q err=%v", got, err)
	}
	var count int
	var stored []byte
	if err := f.db.QueryRow(`SELECT COUNT(*), body FROM webhook_deliveries WHERE webhook_id = ? AND event_id = ?`, row.ID, in.EventID).Scan(&count, &stored); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !bytes.Equal(stored, body) {
		t.Fatalf("count=%d body=%q", count, stored)
	}
	var sensitiveColumns int
	if err := f.db.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('webhook_deliveries')
WHERE lower(name) IN ('url', 'secret', 'destination')`).Scan(&sensitiveColumns); err != nil {
		t.Fatal(err)
	}
	if sensitiveColumns != 0 || bytes.Contains(stored, []byte("credential")) || bytes.Contains(stored, []byte("signing-secret")) {
		t.Fatalf("durable row leaked destination/signing material")
	}
}

func TestDurableWebhookExpiredLeaseIsReclaimedAndStaleWorkerCannotAcknowledge(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	_, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 13, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"lease-event"}`)
	if got, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "lease-event", now, body)); err != nil || got != DurableWebhookAdmissionInserted {
		t.Fatalf("admit=%q err=%v", got, err)
	}
	first, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now, 1, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim=%v err=%v", first, err)
	}
	if claims, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now.Add(59*time.Second), 1, time.Minute); err != nil || len(claims) != 0 {
		t.Fatalf("early reclaim=%v err=%v", claims, err)
	}
	second, err := f.store.ClaimDueDurableWebhookDeliveries(f.ctx, now.Add(time.Minute), 1, time.Minute)
	if err != nil || len(second) != 1 {
		t.Fatalf("expired reclaim=%v err=%v", second, err)
	}
	if first[0].LeaseToken == second[0].LeaseToken || first[0].EventID != second[0].EventID || !bytes.Equal(first[0].Body, second[0].Body) {
		t.Fatalf("reclaimed identity/body changed: first=%+v second=%+v", first[0], second[0])
	}
	if updated, err := f.store.CompleteDurableWebhookDelivery(f.ctx, first[0].ID, first[0].LeaseToken, now.Add(time.Minute)); err != nil || updated {
		t.Fatalf("stale completion updated=%v err=%v", updated, err)
	}
	if updated, err := f.store.CompleteDurableWebhookDelivery(f.ctx, second[0].ID, second[0].LeaseToken, now.Add(time.Minute)); err != nil || !updated {
		t.Fatalf("current completion updated=%v err=%v", updated, err)
	}
}

func TestDurableWebhookAuthorizationGenerationPreventsResurrection(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	service, err := f.store.CreateServiceAccount(f.ctx, f.owner.ID, "Delivery Bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	_, oldRow := f.createDurableWebhook(t, &service.ID)
	now := time.Date(2026, 11, 8, 14, 0, 0, 0, time.UTC)
	if got, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(oldRow, f.project.ID, "before-revoke", now, []byte(`{"id":"before-revoke"}`))); err != nil || got != DurableWebhookAdmissionInserted {
		t.Fatalf("admit=%q err=%v", got, err)
	}
	if _, eligible, err := f.store.GetDurableWebhookDeliveryTarget(f.ctx, oldRow.ID, oldRow.AuthorizationGeneration); err != nil || !eligible {
		t.Fatalf("service Viewer target eligible=%v err=%v", eligible, err)
	}
	if err := f.store.RemoveProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, eligible, err := f.store.GetDurableWebhookDeliveryTarget(f.ctx, oldRow.ID, oldRow.AuthorizationGeneration); err != nil || eligible {
		t.Fatalf("old generation resurrected eligible=%v err=%v", eligible, err)
	}
	if got, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(oldRow, f.project.ID, "crossed-revoke", now, []byte(`{"id":"crossed-revoke"}`))); err != nil || got != DurableWebhookAdmissionIneligible {
		t.Fatalf("old generation admission=%q err=%v", got, err)
	}
	rows, err := f.store.ListWebhooksByProject(f.ctx, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fresh WebhookRow
	for _, candidate := range rows {
		if candidate.ID == oldRow.ID {
			fresh = candidate
		}
	}
	if fresh.AuthorizationGeneration <= oldRow.AuthorizationGeneration {
		t.Fatalf("generation did not advance: old=%d new=%d", oldRow.AuthorizationGeneration, fresh.AuthorizationGeneration)
	}
	if got, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(fresh, f.project.ID, "after-readd", now, []byte(`{"id":"after-readd"}`))); err != nil || got != DurableWebhookAdmissionInserted {
		t.Fatalf("new generation admission=%q err=%v", got, err)
	}
}

func TestDurableWebhookPendingRowsCascadeWithWebhookAndProject(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	hook, row := f.createDurableWebhook(t, nil)
	now := time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)
	if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "cascade-hook", now, []byte(`{"id":"cascade-hook"}`))); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteWebhook(f.ctx, f.owner.ID, hook.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("after webhook delete count=%d err=%v", count, err)
	}
	if usage, err := f.store.DurableWebhookDeliveryUsage(f.ctx); err != nil || usage != (DurableWebhookUsage{}) {
		t.Fatalf("after webhook delete usage=%+v err=%v", usage, err)
	}
	_, row = f.createDurableWebhook(t, nil)
	if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "cascade-project", now, []byte(`{"id":"cascade-project"}`))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM projects WHERE id = ?`, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("after project delete count=%d err=%v", count, err)
	}
	if usage, err := f.store.DurableWebhookDeliveryUsage(context.Background()); err != nil || usage != (DurableWebhookUsage{}) {
		t.Fatalf("after project delete usage=%+v err=%v", usage, err)
	}
}

func TestDurableWebhookPendingRowsCascadeWithOwner(t *testing.T) {
	f, cleanup := newDurableWebhookFixture(t)
	defer cleanup()
	service, err := f.store.CreateServiceAccount(f.ctx, f.owner.ID, "Cascade Bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(f.ctx, f.owner.ID, f.project.ID, service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	_, row := f.createDurableWebhook(t, &service.ID)
	now := time.Date(2026, 11, 8, 15, 30, 0, 0, time.UTC)
	if _, err := f.store.AdmitDurableWebhookDelivery(f.ctx, durableAdmission(row, f.project.ID, "cascade-owner", now, []byte(`{"id":"cascade-owner"}`))); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM users WHERE id = ?`, service.ID); err != nil {
		t.Fatal(err)
	}
	var deliveries, hooks int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries`).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE id = ?`, row.ID).Scan(&hooks); err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 || hooks != 0 {
		t.Fatalf("owner delete left hooks=%d deliveries=%d", hooks, deliveries)
	}
	if usage, err := f.store.DurableWebhookDeliveryUsage(context.Background()); err != nil || usage != (DurableWebhookUsage{}) {
		t.Fatalf("after owner delete usage=%+v err=%v", usage, err)
	}
}
