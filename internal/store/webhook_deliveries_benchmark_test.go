package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
)

func benchmarkWebhookAdmissionFixture(b *testing.B, mode string) (*Store, context.Context, Project, WebhookRow) {
	b.Helper()
	database, err := db.Open(filepath.Join(b.TempDir(), "benchmark.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := migrate.Apply(ctx, database); err != nil {
		b.Fatal(err)
	}
	st := New(database, nil)
	owner, err := st.BootstrapUser(ctx, "benchmark-webhook@example.com", "password123", "Benchmark")
	if err != nil {
		b.Fatal(err)
	}
	ctx = WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctx, "Webhook benchmark")
	if err != nil {
		b.Fatal(err)
	}
	hook, err := st.CreateWebhook(ctx, owner.ID, CreateWebhookInput{
		ProjectID:    project.ID,
		URL:          "https://benchmark.example.com/hook",
		Events:       []string{TodoEventCreated},
		DeliveryMode: mode,
	})
	if err != nil {
		b.Fatal(err)
	}
	rows, err := st.ListWebhooksByProject(ctx, project.ID)
	if err != nil {
		b.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == hook.ID {
			return st, ctx, project, row
		}
	}
	b.Fatal("webhook missing")
	return nil, nil, Project{}, WebhookRow{}
}

func BenchmarkWebhookEventAdmission(b *testing.B) {
	b.Run("best_effort", func(b *testing.B) {
		st, ctx, project, _ := benchmarkWebhookAdmissionFixture(b, WebhookDeliveryBestEffort)
		var before, after int64
		if err := st.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for range b.N {
			if _, err := st.ListWebhooksByProject(ctx, project.ID); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		if err := st.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(after-before)/float64(b.N), "db_changes/op")
	})

	b.Run("durable", func(b *testing.B) {
		st, ctx, project, row := benchmarkWebhookAdmissionFixture(b, WebhookDeliveryDurable)
		body := []byte(`{"id":"benchmark","type":"todo.created"}`)
		at := time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)
		var before, after int64
		if err := st.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		for i := range b.N {
			rows, err := st.ListWebhooksByProject(ctx, project.ID)
			if err != nil || len(rows) != 1 {
				b.Fatalf("list rows=%d err=%v", len(rows), err)
			}
			result, err := st.AdmitDurableWebhookDelivery(ctx, AdmitDurableWebhookDeliveryInput{
				WebhookID: row.ID, ProjectID: project.ID, AuthorizationGeneration: row.AuthorizationGeneration,
				EventID: fmt.Sprintf("benchmark-%d", i), EventType: TodoEventCreated,
				EventTimestamp: at, Body: body, AdmittedAt: at,
			})
			if err != nil || result != DurableWebhookAdmissionInserted {
				b.Fatalf("admission=%q err=%v", result, err)
			}
		}
		b.StopTimer()
		if err := st.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(after-before)/float64(b.N), "db_changes/op")
	})
}
