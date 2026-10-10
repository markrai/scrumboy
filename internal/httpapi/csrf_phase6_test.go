package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhase6CSRFGateCoversEveryBrowserMutationMethod(t *testing.T) {
	srv := NewServer(newTestStore(t), Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
	})

	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/user/preferences", strings.NewReader(`{"key":"phase6","value":"checked"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected missing CSRF header to return 403 for %s, got %d: %s", method, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "missing X-Scrumboy header") {
				t.Fatalf("expected CSRF error for %s, got %s", method, rec.Body.String())
			}
		})
	}
}

func TestPhase6CSRFGatePreservesNarrowAuthExemptions(t *testing.T) {
	srv := NewServer(newTestStore(t), Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
	})

	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
	}{
		{
			name:        "form logout",
			method:      http.MethodPost,
			path:        "/api/auth/logout",
			contentType: "application/x-www-form-urlencoded",
		},
		{
			name:        "multipart logout",
			method:      http.MethodPost,
			path:        "/api/auth/logout",
			contentType: "multipart/form-data; boundary=phase6",
			body:        "--phase6--\r\n",
		},
		{
			name:        "token authenticated password reset",
			method:      http.MethodPost,
			path:        "/api/auth/reset-password",
			contentType: "application/json",
			body:        `{}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()

			srv.ServeHTTP(rec, req)

			if rec.Code == http.StatusForbidden && strings.Contains(rec.Body.String(), "missing X-Scrumboy header") {
				t.Fatalf("narrow exemption was rejected by CSRF gate: %s", rec.Body.String())
			}
		})
	}
}
