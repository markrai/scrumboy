package httpapi

import (
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
