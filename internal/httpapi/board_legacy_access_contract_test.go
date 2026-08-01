package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func legacyBoardAccessURL(baseURL string, projectID int64) string {
	return baseURL + "/api/projects/" + strconv.FormatInt(projectID, 10) + "/board"
}

func assertLegacyBoardAccessSuccess(
	t *testing.T,
	client *http.Client,
	baseURL string,
	projectID int64,
	query string,
) {
	t.Helper()

	var board boardLegacyReadContractResponse
	resp, body := doJSON(
		t,
		client,
		http.MethodGet,
		legacyBoardAccessURL(baseURL, projectID)+query,
		nil,
		&board,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET legacy board: status=%d body=%s", resp.StatusCode, string(body))
	}
	if board.Project.ID != projectID {
		t.Fatalf("project ID = %d, want %d", board.Project.ID, projectID)
	}
}

func assertLegacyBoardAccessNotFound(
	t *testing.T,
	client *http.Client,
	baseURL string,
	projectID int64,
	query string,
) {
	t.Helper()

	var got apiErrorEnvelope
	resp, body := doJSON(
		t,
		client,
		http.MethodGet,
		legacyBoardAccessURL(baseURL, projectID)+query,
		nil,
		&got,
	)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET legacy board: status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, got, "NOT_FOUND", "")
	if got.Error.Message != "not found" {
		t.Fatalf("error message = %q, want %q", got.Error.Message, "not found")
	}
	if reason, ok := got.Error.Details["reason"]; ok {
		t.Fatalf("not-found response exposed validation reason %v: %+v", reason, got.Error)
	}
	if field, ok := got.Error.Details["field"]; ok {
		t.Fatalf("not-found response exposed validation field %v: %+v", field, got.Error)
	}
}

func assertLegacyBoardInvalidAssignee(
	t *testing.T,
	client *http.Client,
	baseURL string,
	projectID int64,
) {
	t.Helper()

	var got apiErrorEnvelope
	resp, body := doJSON(
		t,
		client,
		http.MethodGet,
		legacyBoardAccessURL(baseURL, projectID)+"?assignee=abc",
		nil,
		&got,
	)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET legacy board: status=%d body=%s", resp.StatusCode, string(body))
	}
	assertAPIError(t, got, "VALIDATION_ERROR", "assignee", "invalid_assignee")
	if got.Error.Message != "invalid assignee" {
		t.Fatalf("error message = %q, want %q", got.Error.Message, "invalid assignee")
	}
}

func TestBoardLegacyAccess_RESTContract(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	st := store.New(sqlDB, nil)
	signedOut := newCookieClient(t)

	preBootstrapProject, err := st.CreateProject(context.Background(), "Pre-bootstrap Legacy Access")
	if err != nil {
		t.Fatalf("CreateProject before bootstrap: %v", err)
	}
	t.Run("pre-bootstrap durable project", func(t *testing.T) {
		assertLegacyBoardAccessSuccess(t, signedOut, ts.URL, preBootstrapProject.ID, "")
	})

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		ownerClient,
		ts.URL,
		"Owner",
		"board-legacy-access-owner@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)

	durableProject, err := st.CreateProject(ctxOwner, "Durable Legacy Access")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	viewer, err := st.CreateUser(
		context.Background(),
		"board-legacy-access-viewer@example.com",
		"password123",
		"Viewer",
	)
	if err != nil {
		t.Fatalf("CreateUser(viewer): %v", err)
	}
	if err := st.AddProjectMember(
		ctxOwner,
		ownerID,
		durableProject.ID,
		viewer.ID,
		store.RoleViewer,
	); err != nil {
		t.Fatalf("AddProjectMember(viewer): %v", err)
	}
	viewerClient := newCookieClient(t)
	loginUserClient(
		t,
		viewerClient,
		ts.URL,
		"board-legacy-access-viewer@example.com",
		"password123",
	)

	_, err = st.CreateUser(
		context.Background(),
		"board-legacy-access-outsider@example.com",
		"password123",
		"Outsider",
	)
	if err != nil {
		t.Fatalf("CreateUser(outsider): %v", err)
	}
	outsiderClient := newCookieClient(t)
	loginUserClient(
		t,
		outsiderClient,
		ts.URL,
		"board-legacy-access-outsider@example.com",
		"password123",
	)

	activeExpiringProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(active): %v", err)
	}
	expiredProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(expired): %v", err)
	}
	if _, err := sqlDB.Exec(
		`UPDATE projects SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).UnixMilli(),
		expiredProject.ID,
	); err != nil {
		t.Fatalf("expire project: %v", err)
	}
	missingProjectID := expiredProject.ID + 1_000_000

	t.Run("authenticated owner", func(t *testing.T) {
		assertLegacyBoardAccessSuccess(t, ownerClient, ts.URL, durableProject.ID, "")
	})
	t.Run("authenticated viewer", func(t *testing.T) {
		assertLegacyBoardAccessSuccess(t, viewerClient, ts.URL, durableProject.ID, "")
	})
	t.Run("signed-out durable project", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(t, signedOut, ts.URL, durableProject.ID, "")
	})
	t.Run("authenticated non-member", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(t, outsiderClient, ts.URL, durableProject.ID, "")
	})
	t.Run("active expiring project is link-readable", func(t *testing.T) {
		assertLegacyBoardAccessSuccess(t, signedOut, ts.URL, activeExpiringProject.ID, "")
	})
	t.Run("expired expiring project", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(t, signedOut, ts.URL, expiredProject.ID, "")
	})
	t.Run("nonexistent project", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(t, signedOut, ts.URL, missingProjectID, "")
	})
}

func TestBoardLegacyAccess_RESTPrecedesQueryValidation(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		ownerClient,
		ts.URL,
		"Owner",
		"board-legacy-access-order-owner@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)

	durableProject, err := st.CreateProject(ctxOwner, "Legacy Access Order")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err = st.CreateUser(
		context.Background(),
		"board-legacy-access-order-outsider@example.com",
		"password123",
		"Outsider",
	)
	if err != nil {
		t.Fatalf("CreateUser(outsider): %v", err)
	}
	outsiderClient := newCookieClient(t)
	loginUserClient(
		t,
		outsiderClient,
		ts.URL,
		"board-legacy-access-order-outsider@example.com",
		"password123",
	)
	signedOut := newCookieClient(t)

	activeExpiringProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(active): %v", err)
	}
	expiredProject, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard(expired): %v", err)
	}
	if _, err := sqlDB.Exec(
		`UPDATE projects SET expires_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Hour).UnixMilli(),
		expiredProject.ID,
	); err != nil {
		t.Fatalf("expire project: %v", err)
	}
	missingProjectID := expiredProject.ID + 1_000_000

	t.Run("authorized durable project reaches validation", func(t *testing.T) {
		assertLegacyBoardInvalidAssignee(t, ownerClient, ts.URL, durableProject.ID)
	})
	t.Run("active expiring project reaches validation", func(t *testing.T) {
		assertLegacyBoardInvalidAssignee(t, signedOut, ts.URL, activeExpiringProject.ID)
	})
	t.Run("signed-out durable project hides before validation", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(
			t,
			signedOut,
			ts.URL,
			durableProject.ID,
			"?assignee=abc",
		)
	})
	t.Run("authenticated non-member hides before validation", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(
			t,
			outsiderClient,
			ts.URL,
			durableProject.ID,
			"?assignee=abc",
		)
	})
	t.Run("expired project hides before validation", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(
			t,
			signedOut,
			ts.URL,
			expiredProject.ID,
			"?assignee=abc",
		)
	})
	t.Run("nonexistent project hides before validation", func(t *testing.T) {
		assertLegacyBoardAccessNotFound(
			t,
			signedOut,
			ts.URL,
			missingProjectID,
			"?assignee=abc",
		)
	})
}
