package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func serveFor(t *testing.T, server *Server, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder
}

// TestPhase5RootRoutingMatrix freezes the routing matrix: only Full Mode with
// the landing flag serves marketing at /. The public-projects flag never
// changes root routing, and Anonymous Mode is unchanged by either flag.
func TestPhase5RootRoutingMatrix(t *testing.T) {
	st := newTestStore(t)
	baseline := map[string][]byte{}
	for _, mode := range []string{"full", "anonymous"} {
		server := NewServer(st, Options{ScrumboyMode: mode})
		baseline[mode] = serveFor(t, server, http.MethodGet, "/").Body.Bytes()
		server.Close(context.Background())
	}
	fullSPA := baseline["full"]
	if !bytes.Contains(fullSPA, []byte("app.js")) {
		t.Fatalf("full-mode baseline root is not the SPA shell")
	}

	for _, mode := range []string{"full", "anonymous"} {
		for _, public := range []bool{false, true} {
			for _, landing := range []bool{false, true} {
				name := mode
				if public {
					name += "/public"
				}
				if landing {
					name += "/landing"
				}
				t.Run(name, func(t *testing.T) {
					server := NewServer(st, Options{ScrumboyMode: mode, PublicProjectsEnabled: public, LandingPageEnabled: landing})
					defer server.Close(context.Background())
					root := serveFor(t, server, http.MethodGet, "/")
					switch {
					case mode == "anonymous":
						if root.Code != http.StatusOK || !bytes.Equal(root.Body.Bytes(), baseline["anonymous"]) {
							t.Fatalf("anonymous root changed: status=%d", root.Code)
						}
					case landing:
						body := root.Body.String()
						if root.Code != http.StatusOK || !strings.Contains(body, `<a class="brand" href="/_app" aria-label="Open app" data-workspace-entry>`) || strings.Contains(body, "nav-workspace-link") || strings.Count(body, "data-workspace-entry") != 1 {
							t.Fatalf("landing root status=%d, workspace link enabled=%v", root.Code, strings.Contains(body, `data-workspace-entry>`))
						}
						if root.Header().Get("Cache-Control") != "no-cache" || !strings.HasPrefix(root.Header().Get("Content-Type"), "text/html") {
							t.Fatalf("landing headers: %v", root.Header())
						}
						if head := serveFor(t, server, http.MethodHead, "/"); head.Code != http.StatusOK {
							t.Fatalf("HEAD / status=%d", head.Code)
						}
					default:
						if root.Code != http.StatusOK || !bytes.Equal(root.Body.Bytes(), fullSPA) {
							t.Fatalf("full root without landing is not the existing workspace shell: status=%d", root.Code)
						}
					}

					if mode != "full" {
						return
					}
					// Workspace entry and board URLs are the SPA shell in every Full combination.
					for _, path := range []string{"/_app", "/_app/", "/ignite", "/ignite/t/3", "/ignite?tag=api", "/fr/", "/en", "/de"} {
						got := serveFor(t, server, http.MethodGet, path)
						if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), fullSPA) {
							t.Fatalf("%s status=%d, served SPA=%v", path, got.Code, bytes.Equal(got.Body.Bytes(), fullSPA))
						}
					}
				})
			}
		}
	}
}

func TestPhase5AuthStatusReportsEffectiveFlags(t *testing.T) {
	st := newTestStore(t)
	for _, tc := range []struct {
		mode            string
		public, landing bool
		wantPublic      bool
		wantLanding     bool
	}{
		{"full", false, false, false, false},
		{"full", true, false, true, false},
		{"full", false, true, false, true},
		{"full", true, true, true, true},
		{"anonymous", true, true, false, false},
	} {
		server := NewServer(st, Options{ScrumboyMode: tc.mode, PublicProjectsEnabled: tc.public, LandingPageEnabled: tc.landing})
		got := serveFor(t, server, http.MethodGet, "/api/auth/status")
		server.Close(context.Background())
		var body map[string]any
		if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["publicProjectsEnabled"] != tc.wantPublic || body["landingPageEnabled"] != tc.wantLanding {
			t.Fatalf("%+v: status body flags public=%v landing=%v", tc, body["publicProjectsEnabled"], body["landingPageEnabled"])
		}
	}
}

func TestPhase5LogoutReturnToIsSanitized(t *testing.T) {
	st := newTestStore(t)
	server := NewServer(st, Options{ScrumboyMode: "full"})
	defer server.Close(context.Background())
	for _, tc := range []struct {
		returnTo string
		wantURL  string
	}{
		{"", "/"},
		{"/ignite/t/3?tag=api&priority=high", "/ignite/t/3?tag=api&amp;priority=high"},
		{"/_app", "/_app"},
		{"//evil.example/x", "/"},
		{"https://evil.example/", "/"},
		{`/\evil.example`, "/"},
		{"javascript:alert(1)", "/"},
		{"/%2F%2Fevil.example", "/"},
		// A same-origin path survives sanitization but is HTML-escaped, never markup.
		{"/ok\"><script>", "/ok&#34;&gt;&lt;script&gt;"},
	} {
		form := url.Values{}
		if tc.returnTo != "" {
			form.Set("return_to", tc.returnTo)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, req)
		want := `content="0;url=` + tc.wantURL + `"`
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("return_to %q: status=%d body=%s want %s", tc.returnTo, recorder.Code, recorder.Body.String(), want)
		}
		if strings.Contains(recorder.Body.String(), "<script>") {
			t.Fatalf("return_to %q injected markup", tc.returnTo)
		}
	}
}

// TestLandingLogoIsWorkspaceEntry: the landing header has no separate "Open
// app" button. The Full Mode landing makes the logo itself the /_app link;
// Anonymous Mode keeps the logo linking to / with no workspace entry.
func TestLandingLogoIsWorkspaceEntry(t *testing.T) {
	st := newTestStore(t)
	anon := NewServer(st, Options{ScrumboyMode: "anonymous"})
	defer anon.Close(context.Background())
	anonBody := serveFor(t, anon, http.MethodGet, "/").Body.String()
	if !strings.Contains(anonBody, `<a class="brand" href="/" aria-label="Scrumboy home"`) || strings.Contains(anonBody, "data-workspace-entry") {
		t.Fatalf("anonymous landing logo changed")
	}

	full := NewServer(st, Options{ScrumboyMode: "full", LandingPageEnabled: true})
	defer full.Close(context.Background())
	body := serveFor(t, full, http.MethodGet, "/").Body.String()
	if strings.Count(body, `href="/_app"`) != 1 || !strings.Contains(body, `<a class="brand" href="/_app" aria-label="Open app" data-workspace-entry>`) {
		t.Fatalf("full-mode landing logo is not the single /_app workspace entry")
	}
	if strings.Contains(body, "nav-workspace-link") || strings.Contains(body, ">Open app</a>") {
		t.Fatalf("separate Open app button still rendered")
	}
}
