package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/store"
)

func publicationURL(f *publicSSEFixture, slug string) string {
	return f.ts.URL + "/api/board/" + slug + "/publication"
}

func publicationCall(t *testing.T, client *http.Client, method, url string, body any, csrf bool) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if raw, ok := body.(string); ok {
			buf.WriteString(raw)
		} else if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-Scrumboy", "1")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func publicationEnabled(t *testing.T, f *publicSSEFixture, projectID int64) bool {
	t.Helper()
	var enabled int
	if err := f.db.QueryRow(`SELECT public_view_enabled FROM projects WHERE id = ?`, projectID).Scan(&enabled); err != nil {
		t.Fatalf("read publication: %v", err)
	}
	return enabled == 1
}

func publicationAuditCount(t *testing.T, f *publicSSEFixture, projectID int64) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE project_id = ? AND action LIKE 'project_public_viewing_%'`, projectID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

func TestPublicationPhase5MaintainerPublishUnpublishRevokesAndIsIdempotent(t *testing.T) {
	f := newPublicSSEFixture(t)
	url := publicationURL(f, f.project.Slug)

	status, body := publicationCall(t, f.ownerClient, http.MethodGet, url, nil, false)
	if status != http.StatusOK || body["enabled"] != true || body["publishable"] != true || len(body) != 2 {
		t.Fatalf("GET status=%d body=%v", status, body)
	}

	first, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	second, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	auditBefore := publicationAuditCount(t, f, f.project.ID)

	status, body = publicationCall(t, f.ownerClient, http.MethodPatch, url, map[string]any{"enabled": false}, true)
	if status != http.StatusOK || body["enabled"] != false || body["changed"] != true {
		t.Fatalf("unpublish status=%d body=%v", status, body)
	}
	if publicationEnabled(t, f, f.project.ID) {
		t.Fatal("unpublish was not persisted")
	}
	for _, stream := range []*publicSSEStream{first, second} {
		stream.expectLine(t, publicSSERevokedLine)
		stream.expectEOF(t)
	}
	if resp, _ := publicHTTP(t, f.ts.Client(), http.MethodGet, f.ts.URL+"/api/public/board/"+f.project.Slug, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("public read after unpublish status=%d", resp.StatusCode)
	}
	if got := publicationAuditCount(t, f, f.project.ID); got != auditBefore+1 {
		t.Fatalf("audit count=%d want %d", got, auditBefore+1)
	}

	// Idempotent no-op: no audit, no change.
	status, body = publicationCall(t, f.ownerClient, http.MethodPatch, url, map[string]any{"enabled": false}, true)
	if status != http.StatusOK || body["changed"] != false || body["enabled"] != false {
		t.Fatalf("no-op status=%d body=%v", status, body)
	}
	if got := publicationAuditCount(t, f, f.project.ID); got != auditBefore+1 {
		t.Fatalf("no-op audited: %d", got)
	}

	// Republish: old streams stay closed; only a new subscription is admitted.
	status, body = publicationCall(t, f.ownerClient, http.MethodPatch, url, map[string]any{"enabled": true}, true)
	if status != http.StatusOK || body["enabled"] != true || body["changed"] != true {
		t.Fatalf("publish status=%d body=%v", status, body)
	}
	if counts := f.server.publicHub.counts(); counts.ByProject[f.project.ID] != 0 {
		t.Fatalf("republish resurrected subscriptions: %+v", counts)
	}
	fresh, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if fresh.resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh stream status=%d", fresh.resp.StatusCode)
	}
}

func TestPublicationPhase5NonMaintainersCannotReadOrChange(t *testing.T) {
	f := newPublicSSEFixture(t)
	ctx := store.WithUserID(context.Background(), f.owner.ID)
	url := publicationURL(f, f.project.Slug)
	contributor, contributorClient := f.createUser(t, "pub-contributor@example.com")
	viewer, viewerClient := f.createUser(t, "pub-viewer@example.com")
	_, outsiderClient := f.createUser(t, "pub-outsider@example.com")
	if err := f.st.AddProjectMember(ctx, f.owner.ID, f.project.ID, contributor.ID, store.RoleContributor); err != nil {
		t.Fatalf("add contributor: %v", err)
	}
	if err := f.st.AddProjectMember(ctx, f.owner.ID, f.project.ID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("add viewer: %v", err)
	}
	auditBefore := publicationAuditCount(t, f, f.project.ID)

	for _, tc := range []struct {
		name   string
		client *http.Client
		want   int
	}{
		{"contributor", contributorClient, http.StatusForbidden},
		{"viewer", viewerClient, http.StatusForbidden},
		{"unrelated user", outsiderClient, http.StatusNotFound},
		// Board sub-routes hide signed-out access as 404 (existing convention).
		{"signed out", f.ts.Client(), http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if status, body := publicationCall(t, tc.client, http.MethodGet, url, nil, false); status != tc.want {
				t.Fatalf("GET status=%d body=%v want %d", status, body, tc.want)
			}
			if status, body := publicationCall(t, tc.client, http.MethodPatch, url, map[string]any{"enabled": false}, true); status != tc.want {
				t.Fatalf("PATCH status=%d body=%v want %d", status, body, tc.want)
			}
		})
	}
	if !publicationEnabled(t, f, f.project.ID) || publicationAuditCount(t, f, f.project.ID) != auditBefore {
		t.Fatal("unauthorized callers changed publication state or audit")
	}
}

func TestPublicationPhase5InputAndEligibility(t *testing.T) {
	f := newPublicSSEFixture(t)
	url := publicationURL(f, f.project.Slug)

	for _, tc := range []struct {
		name   string
		body   string
		reason string
	}{
		{"missing enabled", `{}`, "publication_enabled_required"},
		{"unknown field", `{"enabled":false,"slug":"x"}`, "invalid_json"},
		{"non boolean", `{"enabled":"no"}`, "invalid_json"},
		{"trailing data", `{"enabled":false}{}`, "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body := publicationCall(t, f.ownerClient, http.MethodPatch, url, tc.body, true)
			details, _ := body["error"].(map[string]any)["details"].(map[string]any)
			if status != http.StatusBadRequest || details["reason"] != tc.reason {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
	if status, _ := publicationCall(t, f.ownerClient, http.MethodPatch, url, map[string]any{"enabled": false}, false); status != http.StatusForbidden {
		t.Fatalf("PATCH without CSRF header status=%d", status)
	}
	if status, _ := publicationCall(t, f.ownerClient, http.MethodPut, url, map[string]any{"enabled": false}, true); status != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status=%d", status)
	}
	if !publicationEnabled(t, f, f.project.ID) {
		t.Fatal("invalid requests changed publication")
	}

	// Reserved legacy slug: readable, but cannot be published.
	reserved, err := f.st.CreateProject(store.WithUserID(context.Background(), f.owner.ID), "Reserved Legacy")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE projects SET slug = 'dashboard' WHERE id = ?`, reserved.ID); err != nil {
		t.Fatalf("set reserved slug: %v", err)
	}
	reservedURL := publicationURL(f, "dashboard")
	if status, body := publicationCall(t, f.ownerClient, http.MethodGet, reservedURL, nil, false); status != http.StatusOK || body["publishable"] != false || body["enabled"] != false {
		t.Fatalf("reserved GET status=%d body=%v", status, body)
	}
	status, body := publicationCall(t, f.ownerClient, http.MethodPatch, reservedURL, map[string]any{"enabled": true}, true)
	details, _ := body["error"].(map[string]any)["details"].(map[string]any)
	if status != http.StatusBadRequest || details["reason"] != "publication_slug_reserved" || publicationEnabled(t, f, reserved.ID) {
		t.Fatalf("reserved PATCH status=%d body=%v", status, body)
	}

	// Temporary boards cannot be published even by their creator.
	temporary, err := f.st.CreateAnonymousBoard(store.WithUserID(context.Background(), f.owner.ID))
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if status, _ := publicationCall(t, f.ownerClient, http.MethodPatch, publicationURL(f, temporary.Slug), map[string]any{"enabled": true}, true); status != http.StatusNotFound {
		t.Fatalf("temporary PATCH status=%d", status)
	}
	if publicationEnabled(t, f, temporary.ID) {
		t.Fatal("temporary board became public")
	}
}

func TestPublicationPhase5FailedTransactionDoesNotRevoke(t *testing.T) {
	f := newPublicSSEFixture(t)
	stream, _ := openPublicSSE(t, f.ts.Client(), f.eventsURL(f.project.Slug), nil)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_publication_audit BEFORE INSERT ON audit_events
WHEN NEW.action = 'project_public_viewing_disabled'
BEGIN SELECT RAISE(ABORT, 'injected audit failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	status, body := publicationCall(t, f.ownerClient, http.MethodPatch, publicationURL(f, f.project.Slug), map[string]any{"enabled": false}, true)
	if status != http.StatusInternalServerError {
		t.Fatalf("failed transaction status=%d body=%v", status, body)
	}
	if !publicationEnabled(t, f, f.project.ID) {
		t.Fatal("failed transaction persisted unpublication")
	}
	if counts := f.server.publicHub.counts(); counts.ByProject[f.project.ID] != 1 {
		t.Fatalf("failed transaction revoked streams: %+v", counts)
	}
	f.createTodo(t, f.project.Slug, "Still public")
	stream.expectLine(t, publicSSERefreshLine)
}

func TestPublicationPhase5CapabilityGates(t *testing.T) {
	f := newPublicSSEFixture(t, func(opts *Options) { opts.PublicProjectsEnabled = false })
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		status, _ := publicationCall(t, f.ownerClient, method, publicationURL(f, f.project.Slug), map[string]any{"enabled": false}, true)
		if status != http.StatusNotFound {
			t.Fatalf("%s with public projects disabled status=%d", method, status)
		}
	}
	if !publicationEnabled(t, f, f.project.ID) {
		t.Fatal("disabled capability changed stored preference")
	}

	// Anonymous Mode never exposes publication management.
	server := NewServer(f.st, Options{ScrumboyMode: "anonymous", PublicProjectsEnabled: true})
	defer server.Close(context.Background())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/board/%s/publication", f.project.Slug), nil)
	server.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusOK {
		t.Fatalf("anonymous mode exposed publication status: %d %s", recorder.Code, recorder.Body.String())
	}
}
