package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestToolCallsNeverFollowRedirects drives real redirects through the client a
// command uses. Go keeps the Authorization header on a redirect to the same
// hostname whatever the port or scheme (so also https -> http), and re-sends
// the POST body on 307/308, which would replay a mutation. The CLI must stop
// at the first response for every code, destination and transport.
func TestToolCallsNeverFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		io.WriteString(w, `{"ok":true,"data":{}}`)
	}))
	defer target.Close()
	port := target.URL[strings.LastIndex(target.URL, ":")+1:]

	transports := []struct {
		name, token, path string
		rpc               bool
	}{
		{"legacy sb_ token", "sb_secret", "/mcp", false},
		{"rpc OAuth access token", "oauth-access-secret", "/mcp/rpc", true},
	}
	dests := map[string]string{
		"same hostname, other port": "http://127.0.0.1:" + port,
		"other hostname":            "http://localhost:" + port,
	}
	for _, tr := range transports {
		for destName, dest := range dests {
			for _, code := range []int{301, 302, 303, 307, 308} {
				t.Run(fmt.Sprintf("%s/%s/%d", tr.name, destName, code), func(t *testing.T) {
					var originHits atomic.Int32
					origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						originHits.Add(1)
						http.Redirect(w, r, dest+tr.path, code)
					}))
					defer origin.Close()
					before := targetHits.Load()

					cl := NewClient(origin.URL, tr.token, "test")
					cl.RPC = tr.rpc
					_, err := cl.Call(context.Background(), "todos_delete", map[string]any{"projectSlug": "p", "localId": 1})
					assertRedirectRefused(t, err, code, dest+tr.path)
					if got := targetHits.Load() - before; got != 0 {
						t.Fatalf("redirect target was contacted %d time(s)", got)
					}
					if got := originHits.Load(); got != 1 {
						t.Fatalf("origin received %d requests, want exactly 1 (no replay)", got)
					}
				})
			}
		}
	}

	// https -> http on the same hostname would put the bearer on the wire in cleartext.
	for _, code := range []int{302, 307, 308} {
		t.Run(fmt.Sprintf("https to http same hostname/%d", code), func(t *testing.T) {
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "http://127.0.0.1:"+port+"/mcp/rpc", code)
			}))
			defer origin.Close()
			before := targetHits.Load()

			cl := NewClient(origin.URL, "oauth-access-secret", "test")
			cl.HTTP.Transport = origin.Client().Transport // trust the test cert; keep the CLI's redirect policy
			cl.RPC = true
			_, err := cl.Call(context.Background(), "me_get", nil)
			assertRedirectRefused(t, err, code, "http://127.0.0.1:"+port+"/mcp/rpc")
			if got := targetHits.Load() - before; got != 0 {
				t.Fatalf("cleartext redirect target was contacted %d time(s)", got)
			}
		})
	}
}

func assertRedirectRefused(t *testing.T, err error, code int, location string) {
	t.Helper()
	if err == nil {
		t.Fatal("call succeeded through a redirect")
	}
	msg := err.Error()
	if !strings.Contains(msg, fmt.Sprintf("HTTP %d", code)) || !strings.Contains(msg, location) || !strings.Contains(msg, "--server") {
		t.Fatalf("error does not explain the refused redirect: %v", err)
	}
	if strings.Contains(msg, "secret") {
		t.Fatalf("error leaks the credential: %v", err)
	}
}

// TestRedirectedServerIsAUsableError covers the realistic case: Scrumboy's
// own TLS listener answers plain http with a 308 to https.
func TestRedirectedServerIsAUsableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://scrumboy.example"+r.URL.Path, http.StatusPermanentRedirect)
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, nil)
	if code := h.run("project", "list"); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(h.err.String(), "https://scrumboy.example/mcp") || strings.Contains(h.err.String(), "sb_test") {
		t.Fatalf("stderr = %q", h.err)
	}
}
