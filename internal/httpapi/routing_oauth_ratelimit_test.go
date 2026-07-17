package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/httpapi/ratelimit"
)

func newOAuthRateLimitTestServer(t *testing.T, opts Options) (*Server, *httptest.Server) {
	t.Helper()
	srv := newTestOAuthServer(t, opts)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return srv, ts
}

func oauthDCRStatus(t *testing.T, baseURL string) int {
	t.Helper()
	var out map[string]any
	resp, _ := doJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/oauth/register", map[string]any{
		"client_name":   "Limiter Test Client",
		"redirect_uris": []string{"https://client.example.com/callback"},
	}, &out)
	return resp.StatusCode
}

func oauthTokenProbeStatus(t *testing.T, baseURL string) int {
	t.Helper()
	status, _ := exchangeToken(t, baseURL, url.Values{"grant_type": {"unsupported"}})
	return status
}

func oauthPasswordLoginStatus(t *testing.T, baseURL, email, password string) int {
	t.Helper()
	var out map[string]any
	resp, _ := doJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/api/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, &out)
	return resp.StatusCode
}

func bootstrapRateLimitUser(t *testing.T, baseURL string) {
	t.Helper()
	bootstrapUserClient(t, newCookieClient(t), baseURL, "Limiter Owner", "limiter@example.com", "password123")
}

func TestOAuthRateLimiterIsolation(t *testing.T) {
	t.Run("DCR exhaustion does not starve login", func(t *testing.T) {
		_, ts := newOAuthRateLimitTestServer(t, Options{
			AuthRateLimit:       ratelimit.New(1, time.Minute),
			OAuthDCRRateLimit:   ratelimit.New(1, time.Minute),
			OAuthTokenRateLimit: ratelimit.New(1, time.Minute),
		})
		bootstrapRateLimitUser(t, ts.URL)

		if got := oauthDCRStatus(t, ts.URL); got != http.StatusCreated {
			t.Fatalf("first DCR status=%d, want 201", got)
		}
		if got := oauthDCRStatus(t, ts.URL); got != http.StatusTooManyRequests {
			t.Fatalf("exhausted DCR status=%d, want 429", got)
		}
		if got := oauthPasswordLoginStatus(t, ts.URL, "limiter@example.com", "password123"); got != http.StatusOK {
			t.Fatalf("DCR exhaustion affected login: status=%d", got)
		}
	})

	t.Run("token exhaustion does not starve login", func(t *testing.T) {
		_, ts := newOAuthRateLimitTestServer(t, Options{
			AuthRateLimit:       ratelimit.New(1, time.Minute),
			OAuthDCRRateLimit:   ratelimit.New(1, time.Minute),
			OAuthTokenRateLimit: ratelimit.New(1, time.Minute),
		})
		bootstrapRateLimitUser(t, ts.URL)

		if got := oauthTokenProbeStatus(t, ts.URL); got != http.StatusBadRequest {
			t.Fatalf("first token probe status=%d, want 400", got)
		}
		if got := oauthTokenProbeStatus(t, ts.URL); got != http.StatusTooManyRequests {
			t.Fatalf("exhausted token limiter status=%d, want 429", got)
		}
		if got := oauthPasswordLoginStatus(t, ts.URL, "limiter@example.com", "password123"); got != http.StatusOK {
			t.Fatalf("token exhaustion affected login: status=%d", got)
		}
	})

	t.Run("authentication exhaustion does not starve OAuth", func(t *testing.T) {
		_, ts := newOAuthRateLimitTestServer(t, Options{
			AuthRateLimit:       ratelimit.New(1, time.Minute),
			OAuthDCRRateLimit:   ratelimit.New(1, time.Minute),
			OAuthTokenRateLimit: ratelimit.New(1, time.Minute),
		})
		bootstrapRateLimitUser(t, ts.URL)

		if got := oauthPasswordLoginStatus(t, ts.URL, "limiter@example.com", "wrong-password"); got != http.StatusUnauthorized {
			t.Fatalf("first login probe status=%d, want 401", got)
		}
		if got := oauthPasswordLoginStatus(t, ts.URL, "limiter@example.com", "password123"); got != http.StatusTooManyRequests {
			t.Fatalf("exhausted auth limiter status=%d, want 429", got)
		}
		if got := oauthDCRStatus(t, ts.URL); got != http.StatusCreated {
			t.Fatalf("auth exhaustion affected DCR: status=%d", got)
		}
		if got := oauthTokenProbeStatus(t, ts.URL); got != http.StatusBadRequest {
			t.Fatalf("auth exhaustion affected token endpoint: status=%d", got)
		}
	})
}

func TestOAuthRateLimiterInjectionAndKeying(t *testing.T) {
	t.Run("distinct injected instances", func(t *testing.T) {
		authLimiter := ratelimit.New(1, time.Minute)
		dcrLimiter := ratelimit.New(1, time.Minute)
		tokenLimiter := ratelimit.New(1, time.Minute)
		srv, _ := newOAuthRateLimitTestServer(t, Options{
			AuthRateLimit:       authLimiter,
			OAuthDCRRateLimit:   dcrLimiter,
			OAuthTokenRateLimit: tokenLimiter,
		})
		if srv.authRateLimit != authLimiter || srv.oauthDCRRateLimit != dcrLimiter || srv.oauthTokenRateLimit != tokenLimiter {
			t.Fatal("NewServer did not retain the injected limiter instances")
		}
		if srv.authRateLimit == srv.oauthDCRRateLimit || srv.authRateLimit == srv.oauthTokenRateLimit || srv.oauthDCRRateLimit == srv.oauthTokenRateLimit {
			t.Fatal("authentication, DCR, and token limiters must be separate instances")
		}
	})

	t.Run("DCR uses IP-only key", func(t *testing.T) {
		dcrLimiter := ratelimit.New(1, time.Minute)
		if !dcrLimiter.Allow("ip:127.0.0.1", "") {
			t.Fatal("failed to prime DCR IP key")
		}
		_, ts := newOAuthRateLimitTestServer(t, Options{OAuthDCRRateLimit: dcrLimiter})
		if got := oauthDCRStatus(t, ts.URL); got != http.StatusTooManyRequests {
			t.Fatalf("DCR did not use ip:127.0.0.1 key: status=%d", got)
		}
	})

	t.Run("token uses IP-only key", func(t *testing.T) {
		tokenLimiter := ratelimit.New(1, time.Minute)
		if !tokenLimiter.Allow("ip:127.0.0.1", "") {
			t.Fatal("failed to prime token IP key")
		}
		_, ts := newOAuthRateLimitTestServer(t, Options{OAuthTokenRateLimit: tokenLimiter})
		if got := oauthTokenProbeStatus(t, ts.URL); got != http.StatusTooManyRequests {
			t.Fatalf("token endpoint did not use ip:127.0.0.1 key: status=%d", got)
		}
	})

	t.Run("authentication retains normalized email key", func(t *testing.T) {
		authLimiter := ratelimit.New(1, time.Minute)
		if !authLimiter.Allow("ip:203.0.113.1", "email:limiter@example.com") {
			t.Fatal("failed to prime authentication email key")
		}
		_, ts := newOAuthRateLimitTestServer(t, Options{AuthRateLimit: authLimiter})
		bootstrapRateLimitUser(t, ts.URL)
		if got := oauthPasswordLoginStatus(t, ts.URL, " LIMITER@EXAMPLE.COM ", "password123"); got != http.StatusTooManyRequests {
			t.Fatalf("authentication limiter did not retain normalized email key behavior: status=%d", got)
		}
	})
}

func TestOAuthDCRContentTypeRejectedBeforeLimiter(t *testing.T) {
	dcrLimiter := ratelimit.New(1, time.Minute)
	_, ts := newOAuthRateLimitTestServer(t, Options{OAuthDCRRateLimit: dcrLimiter})
	payload := `{"client_name":"Content Type Probe","redirect_uris":["https://client.example.com/callback"]}`

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/register", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new non-JSON DCR request: %v", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("non-JSON DCR request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("non-JSON DCR status=%d, want 400", resp.StatusCode)
	}

	if got := oauthDCRStatus(t, ts.URL); got != http.StatusCreated {
		t.Fatalf("non-JSON DCR consumed the only limiter slot: next status=%d", got)
	}
	if got := oauthDCRStatus(t, ts.URL); got != http.StatusTooManyRequests {
		t.Fatalf("second valid DCR status=%d, want 429", got)
	}
}
