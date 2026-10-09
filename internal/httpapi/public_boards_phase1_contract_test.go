package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestPhase1FlagsDoNotChangeRootRouting(t *testing.T) {
	st := newTestStore(t)
	for _, mode := range []string{"full", "anonymous"} {
		t.Run(mode, func(t *testing.T) {
			baseline := NewServer(st, Options{ScrumboyMode: mode})
			defer baseline.Close(context.Background())
			flagged := NewServer(st, Options{
				ScrumboyMode:          mode,
				PublicProjectsEnabled: true,
				LandingPageEnabled:    true,
			})
			defer flagged.Close(context.Background())

			serveRoot := func(server *Server) *httptest.ResponseRecorder {
				recorder := httptest.NewRecorder()
				server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
				return recorder
			}
			want := serveRoot(baseline)
			got := serveRoot(flagged)
			if got.Code != want.Code || got.Header().Get("Location") != want.Header().Get("Location") || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") || !bytes.Equal(got.Body.Bytes(), want.Body.Bytes()) {
				t.Fatalf("flagged root differs from baseline: got status=%d location=%q type=%q bytes=%d; want status=%d location=%q type=%q bytes=%d", got.Code, got.Header().Get("Location"), got.Header().Get("Content-Type"), got.Body.Len(), want.Code, want.Header().Get("Location"), want.Header().Get("Content-Type"), want.Body.Len())
			}
		})
	}
}

func TestPublishedStateAddsOnlyTheIsolatedPublicReadEndpoint(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		ScrumboyMode:          "full",
		PublicProjectsEnabled: true,
		LandingPageEnabled:    true,
	})
	defer cleanup()
	client := newCookieClient(t)
	userJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "phase1-public@example.com", "password123")
	userID := int64(userJSON["id"].(float64))
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), userID), "Published Phase One")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.UpdateProjectPublicViewing(context.Background(), project.ID, userID, true); err != nil {
		t.Fatalf("UpdateProjectPublicViewing: %v", err)
	}

	memberResp, err := client.Get(ts.URL + "/api/board/" + project.Slug)
	if err != nil {
		t.Fatalf("member board GET: %v", err)
	}
	memberBody, err := io.ReadAll(memberResp.Body)
	_ = memberResp.Body.Close()
	if err != nil {
		t.Fatalf("read member board body: %v", err)
	}
	if memberResp.StatusCode != http.StatusOK {
		t.Fatalf("member board status=%d body=%s", memberResp.StatusCode, memberBody)
	}
	lowerBody := strings.ToLower(string(memberBody))
	if strings.Contains(lowerBody, "publicview") || strings.Contains(lowerBody, "public_view") {
		t.Fatalf("existing board response exposed publication state: %s", memberBody)
	}

	publicResp, err := http.Get(ts.URL + "/api/public/board/" + project.Slug)
	if err != nil {
		t.Fatalf("public board GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, publicResp.Body)
	_ = publicResp.Body.Close()
	if publicResp.StatusCode != http.StatusOK {
		t.Fatalf("public board status=%d, want 200", publicResp.StatusCode)
	}
	eventsResp, err := http.Get(ts.URL + "/api/public/board/" + project.Slug + "/events")
	if err != nil {
		t.Fatalf("public events GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, eventsResp.Body)
	_ = eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusNotFound {
		t.Fatalf("public events status=%d, want 404", eventsResp.StatusCode)
	}

	for _, path := range []string{
		"/api/board/" + project.Slug,
		"/api/projects/" + stringID(project.ID) + "/board",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("anonymous GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("publication unexpectedly authorized existing route %s", path)
		}
	}
}

func TestPhase1FlagsPreserveAnonymousBoardLinkBehavior(t *testing.T) {
	ts, _, cleanup := newTestHTTPServerWithOptions(t, Options{
		ScrumboyMode:          "anonymous",
		PublicProjectsEnabled: true,
		LandingPageEnabled:    true,
	})
	defer cleanup()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/anon")
	if err != nil {
		t.Fatalf("GET /anon: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /anon status=%d, want 302", resp.StatusCode)
	}
	slugPath := resp.Header.Get("Location")
	if slugPath == "" || !strings.HasPrefix(slugPath, "/") {
		t.Fatalf("GET /anon location=%q", slugPath)
	}
	boardResp, err := client.Get(ts.URL + "/api/board" + slugPath)
	if err != nil {
		t.Fatalf("anonymous board GET: %v", err)
	}
	boardBody, err := io.ReadAll(boardResp.Body)
	_ = boardResp.Body.Close()
	if err != nil {
		t.Fatalf("read anonymous board: %v", err)
	}
	if boardResp.StatusCode != http.StatusOK {
		t.Fatalf("anonymous board status=%d body=%s", boardResp.StatusCode, boardBody)
	}
	publicResp, err := client.Get(ts.URL + "/api/public/board" + slugPath)
	if err != nil {
		t.Fatalf("public namespace GET: %v", err)
	}
	_ = publicResp.Body.Close()
	if publicResp.StatusCode != http.StatusNotFound {
		t.Fatalf("public namespace status=%d, want 404", publicResp.StatusCode)
	}
}

func stringID(id int64) string {
	return strconv.FormatInt(id, 10)
}
