package httpapi

import (
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

func TestAPI_BackupImport_Responds(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// Enable auth + authenticate (ImportProjects requires a user when auth is enabled).
	var u map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Alice",
		"email":    "admin@example.com",
		"password": "password123",
	}, &u)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", resp.StatusCode, string(body))
	}

	now := time.Now().UTC()
	data := store.ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []store.ProjectExport{
			{
				Slug:      "p1",
				Name:      "Project 1",
				ExpiresAt: nil,
				CreatedAt: now,
				UpdatedAt: now,
				Todos:     nil,
				Tags:      nil,
			},
		},
	}

	t.Run("merge", func(t *testing.T) {
		var out importResultJSON
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", map[string]any{
			"data":       data,
			"importMode": "merge",
		}, &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("import status=%d body=%s", resp.StatusCode, string(body))
		}
		if out.Imported != 1 {
			t.Fatalf("expected imported=1, got %d", out.Imported)
		}

		var n int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects WHERE name = ?`, "Project 1").Scan(&n); err != nil {
			t.Fatalf("count projects: %v", err)
		}
		if n != 1 {
			t.Fatalf("expected 1 project row, got %d", n)
		}
	})

	t.Run("replace", func(t *testing.T) {
		var out importResultJSON
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", map[string]any{
			"data":         data,
			"importMode":   "replace",
			"confirmation": "REPLACE",
		}, &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("import status=%d body=%s", resp.StatusCode, string(body))
		}
	})
}

// TestAPI_BackupImport_MissingDataIsValidationErrorNotPanic is a regression test for a
// nil dereference in the import handler: it logged len(in.Data.Projects) before checking
// in.Data == nil, so a body that omits "data" panicked instead of failing validation.
// The malformed request must come back through the ordinary validation contract
// (400 VALIDATION_ERROR / reason "missing_data") and must not import anything.
func TestAPI_BackupImport_MissingDataIsValidationErrorNotPanic(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// The panic was reachable before any user existed: the import route carries no auth
	// gate of its own (unlike export), and the dereference ran immediately after
	// decoding. Pin that pre-bootstrap surface here, before the user is created below.
	t.Run("pre-bootstrap", func(t *testing.T) {
		var out apiErrorEnvelope
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", map[string]any{
			"importMode": "copy",
		}, &out)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("pre-bootstrap missing-data status=%d want 400; body=%s", resp.StatusCode, string(body))
		}
		if out.Error.Code != "VALIDATION_ERROR" || out.Error.Message != "missing data" {
			t.Fatalf("pre-bootstrap error envelope=%+v body=%s", out.Error, string(body))
		}
		if reason, _ := out.Error.Details["reason"].(string); reason != "missing_data" {
			t.Fatalf("pre-bootstrap details=%+v want reason missing_data", out.Error.Details)
		}
	})

	var u map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/bootstrap", map[string]any{
		"name":     "Alice",
		"email":    "admin@example.com",
		"password": "password123",
	}, &u)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", resp.StatusCode, string(body))
	}

	projectsBefore := backupProjectCount(t, sqlDB)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{name: "copy", body: map[string]any{"importMode": "copy"}},
		{name: "empty object", body: map[string]any{}},
		{name: "merge", body: map[string]any{"importMode": "merge"}},
		{name: "replace", body: map[string]any{"importMode": "replace", "confirmation": "REPLACE"}},
		{name: "explicit null data", body: map[string]any{"data": nil, "importMode": "copy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out apiErrorEnvelope
			// A panic in the handler closes the connection, so doJSON failing to get a
			// response at all is itself the regression signal.
			resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/backup/import", tc.body, &out)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d want 400; body=%s", resp.StatusCode, string(body))
			}
			if out.Error.Code != "VALIDATION_ERROR" || out.Error.Message != "missing data" {
				t.Fatalf("error envelope=%+v body=%s", out.Error, string(body))
			}
			if reason, _ := out.Error.Details["reason"].(string); reason != "missing_data" {
				t.Fatalf("details=%+v want reason missing_data", out.Error.Details)
			}
		})
	}

	if after := backupProjectCount(t, sqlDB); after != projectsBefore {
		t.Fatalf("malformed imports mutated projects: %d -> %d", projectsBefore, after)
	}
}

func backupProjectCount(t *testing.T, sqlDB *sql.DB) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		t.Fatalf("count projects: %v", err)
	}
	return n
}
