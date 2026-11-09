package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
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

func benchmarkPercentile(samples []time.Duration, percentile float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	index := int(float64(len(samples)-1) * percentile)
	return samples[index]
}

func BenchmarkWebhookConcurrentAdmission(b *testing.B) {
	for _, mode := range []string{WebhookDeliveryBestEffort, WebhookDeliveryDurable} {
		for _, subscriptions := range []int{1, 5, 20} {
			for _, concurrency := range []int{1, 4, 16, 64} {
				name := fmt.Sprintf("%s/subscriptions_%d/concurrency_%d", mode, subscriptions, concurrency)
				b.Run(name, func(b *testing.B) {
					workload := b.N
					if workload < concurrency {
						workload = concurrency
					}
					if mode == WebhookDeliveryDurable && workload*subscriptions >= DurableWebhookMaxActiveRows {
						b.Skip("run with -benchtime=20x so retained rows stay below the production quota")
					}
					path := filepath.Join(b.TempDir(), "concurrent-admission.db")
					database, err := db.Open(path, db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
					if err != nil {
						b.Fatal(err)
					}
					b.Cleanup(func() { _ = database.Close() })
					ctx := context.Background()
					if err := migrate.Apply(ctx, database); err != nil {
						b.Fatal(err)
					}
					st := New(database, nil)
					owner, err := st.BootstrapUser(ctx, fmt.Sprintf("concurrent-%s-%d-%d@example.com", mode, subscriptions, concurrency), "password123", "Benchmark")
					if err != nil {
						b.Fatal(err)
					}
					ctx = WithUserID(ctx, owner.ID)
					project, err := st.CreateProject(ctx, "Concurrent webhook benchmark")
					if err != nil {
						b.Fatal(err)
					}
					for i := 0; i < subscriptions; i++ {
						if _, err := st.CreateWebhook(ctx, owner.ID, CreateWebhookInput{
							ProjectID: project.ID,
							URL:       fmt.Sprintf("https://benchmark-%d.example.com/hook", i),
							Events:    []string{TodoEventCreated}, DeliveryMode: mode,
						}); err != nil {
							b.Fatal(err)
						}
					}
					rows, err := st.ListWebhooksByProject(ctx, project.ID)
					if err != nil || len(rows) != subscriptions {
						b.Fatalf("rows=%d err=%v", len(rows), err)
					}
					jobs := make(chan int, workload)
					for i := 0; i < workload; i++ {
						jobs <- i
					}
					close(jobs)
					ready := make(chan struct{}, concurrency)
					operationsStart := make(chan struct{})
					admissionLatencies := make([]time.Duration, workload)
					rowLatencies := make([]time.Duration, workload*subscriptions)
					todoLatencies := make([]time.Duration, workload)
					var admissionErrors, admissionTimeouts atomic.Int64
					var nextTodo atomic.Int64
					var peakGoroutines, inFlight, peakInFlight, insertedRows atomic.Int64
					var workers sync.WaitGroup
					workerCount := concurrency
					for i := 0; i < workerCount; i++ {
						workers.Add(1)
						go func() {
							defer workers.Done()
							ready <- struct{}{}
							<-operationsStart
							for job := range jobs {
								currentGoroutines := int64(runtime.NumGoroutine())
								for {
									previous := peakGoroutines.Load()
									if currentGoroutines <= previous || peakGoroutines.CompareAndSwap(previous, currentGoroutines) {
										break
									}
								}
								currentInFlight := inFlight.Add(1)
								for {
									previous := peakInFlight.Load()
									if currentInFlight <= previous || peakInFlight.CompareAndSwap(previous, currentInFlight) {
										break
									}
								}
								started := time.Now()
								operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
								eventID := fmt.Sprintf("concurrent-%d", job)
								if mode == WebhookDeliveryBestEffort {
									_, operationErr := st.ListWebhooksByProject(operationCtx, project.ID)
									if operationErr != nil {
										admissionErrors.Add(1)
										if errors.Is(operationErr, context.DeadlineExceeded) {
											admissionTimeouts.Add(1)
										}
									}
								} else {
									body := []byte(fmt.Sprintf(`{"id":%q,"type":"todo.created"}`, eventID))
									for rowIndex, row := range rows {
										rowStarted := time.Now()
										_, operationErr := st.AdmitDurableWebhookDelivery(operationCtx, AdmitDurableWebhookDeliveryInput{
											WebhookID: row.ID, ProjectID: project.ID, AuthorizationGeneration: row.AuthorizationGeneration,
											EventID: eventID, EventType: TodoEventCreated, EventTimestamp: started,
											Body: body, AdmittedAt: started,
										})
										rowLatencies[job*subscriptions+rowIndex] = time.Since(rowStarted)
										if operationErr != nil {
											admissionErrors.Add(1)
											if errors.Is(operationErr, context.DeadlineExceeded) {
												admissionTimeouts.Add(1)
											}
											break
										}
										insertedRows.Add(1)
									}
								}
								cancel()
								admissionLatencies[job] = time.Since(started)
								inFlight.Add(-1)
							}
						}()
					}
					var todoWorker sync.WaitGroup
					todoWorker.Add(1)
					go func() {
						defer todoWorker.Done()
						<-operationsStart
						for {
							index := int(nextTodo.Add(1) - 1)
							if index >= workload {
								return
							}
							started := time.Now()
							if _, createErr := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: fmt.Sprintf("Unrelated write %d", index)}, ModeFull); createErr != nil {
								b.Errorf("unrelated todo write: %v", createErr)
							}
							todoLatencies[index] = time.Since(started)
						}
					}()
					for i := 0; i < workerCount; i++ {
						<-ready
					}
					b.ResetTimer()
					started := time.Now()
					close(operationsStart)
					workers.Wait()
					todoWorker.Wait()
					elapsed := time.Since(started)
					b.StopTimer()
					usage := DurableWebhookUsage{}
					if mode == WebhookDeliveryDurable {
						usage, err = st.DurableWebhookDeliveryUsage(ctx)
						if err != nil {
							b.Fatal(err)
						}
					}
					var walBytes int64
					if info, statErr := os.Stat(path + "-wal"); statErr == nil {
						walBytes = info.Size()
					}
					var databaseBytes int64
					if info, statErr := os.Stat(path); statErr == nil {
						databaseBytes = info.Size()
					}
					b.ReportMetric(0, "ns/op")
					b.ReportMetric(float64(workload)/elapsed.Seconds(), "event_batches/s")
					b.ReportMetric(float64(insertedRows.Load())/elapsed.Seconds(), "delivery_rows/s")
					b.ReportMetric(float64(benchmarkPercentile(admissionLatencies, 0.50).Microseconds()), "event_batch_p50_us")
					b.ReportMetric(float64(benchmarkPercentile(admissionLatencies, 0.95).Microseconds()), "event_batch_p95_us")
					b.ReportMetric(float64(benchmarkPercentile(admissionLatencies, 0.99).Microseconds()), "event_batch_p99_us")
					b.ReportMetric(float64(benchmarkPercentile(rowLatencies, 0.50).Microseconds()), "delivery_row_p50_us")
					b.ReportMetric(float64(benchmarkPercentile(rowLatencies, 0.95).Microseconds()), "delivery_row_p95_us")
					b.ReportMetric(float64(benchmarkPercentile(rowLatencies, 0.99).Microseconds()), "delivery_row_p99_us")
					b.ReportMetric(float64(concurrency), "requested_concurrency")
					b.ReportMetric(float64(peakInFlight.Load()), "actual_concurrency")
					b.ReportMetric(float64(workload), "event_batches")
					b.ReportMetric(float64(insertedRows.Load()), "delivery_rows")
					b.ReportMetric(float64(admissionErrors.Load()), "admission_errors")
					b.ReportMetric(float64(admissionTimeouts.Load()), "admission_timeouts")
					b.ReportMetric(float64(peakGoroutines.Load()), "peak_goroutines")
					b.ReportMetric(float64(usage.ActivePayloadBytes), "active_payload_bytes")
					b.ReportMetric(float64(usage.TotalPayloadBytes), "retained_payload_bytes")
					b.ReportMetric(float64(databaseBytes), "database_bytes")
					b.ReportMetric(float64(walBytes), "wal_bytes")
					b.ReportMetric(float64(benchmarkPercentile(todoLatencies, 0.95).Microseconds()), "unrelated_todo_p95_us")
				})
			}
		}
	}
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
