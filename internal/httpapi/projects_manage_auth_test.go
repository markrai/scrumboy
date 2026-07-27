package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

func apiErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	errObj, ok := out["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing error object: %s", string(body))
	}
	code, _ := errObj["code"].(string)
	return code
}

func setupRESTTemporaryBoardOwner(t *testing.T, ts *httptest.Server, sqlDB *sql.DB) (ownerClient *http.Client, ownerID int64, tempBoard store.Project) {
	t.Helper()
	ownerClient = newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Temp Owner", "temp-owner@example.com", "password123")
	ownerID = int64(owner["id"].(float64))
	st := store.New(sqlDB, nil)
	ctx := store.WithUserID(context.Background(), ownerID)
	board, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	board, err = st.GetProject(ctx, board.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if board.CreatorUserID == nil {
		t.Fatal("expected Temporary Board owner (creator_user_id set)")
	}
	return ownerClient, ownerID, board
}

func TestRESTDeleteProject_durableViewerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "delete-viewer-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	st := store.New(sqlDB, nil)
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "Delete Viewer Forbidden")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	viewer, err := st.CreateUser(context.Background(), "delete-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.AddProjectMember(ctxOwner, ownerID, project.ID, viewer.ID, store.RoleViewer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}
	viewerClient := newCookieClient(t)
	loginUserClient(t, viewerClient, ts.URL, "delete-viewer@example.com", "password123")

	resp, body := doJSON(t, viewerClient, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %q", code)
	}
}

func TestRESTDeleteProject_temporaryBoardNonOwnerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	_, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-delete-other@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-delete-other@example.com", "password123")

	resp, body := doJSON(t, otherClient, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %q", code)
	}
}

func TestRESTDeleteProject_durableNonMemberNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	owner := bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "delete-nonmember-owner@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(store.WithUserID(context.Background(), ownerID), "Delete Non Member")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := st.CreateUser(context.Background(), "delete-nonmember@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "delete-nonmember@example.com", "password123")

	resp, body := doJSON(t, otherClient, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(project.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}

func TestRESTDeleteProject_temporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)

	resp, body := doJSON(t, ownerClient, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", resp.StatusCode, string(body))
	}
	st := store.New(sqlDB, nil)
	if _, err := st.GetProject(context.Background(), tempBoard.ID); err == nil {
		t.Fatal("expected temporary board to be deleted")
	}
}

func TestRESTDeleteProject_anonymousBoardNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "delete-anon-owner@example.com", "password123")
	st := store.New(sqlDB, nil)
	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(anonBoard.ID, 10), nil, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}

func TestRESTPatchProjectName_temporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), map[string]any{
		"name": "Temporary Board Renamed",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRESTPatchProjectName_temporaryBoardNonOwnerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	_, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-rename-other@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-rename-other@example.com", "password123")

	resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), map[string]any{
		"name": "Should Not Stick",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %q", code)
	}
}

func TestRESTPatchProjectImage_temporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), map[string]any{
		"image": "data:image/png;base64,aaaa",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRESTPatchProjectImage_temporaryBoardNonOwnerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	_, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-image-other@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-image-other@example.com", "password123")

	resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(tempBoard.ID, 10), map[string]any{
		"image": "data:image/png;base64,aaaa",
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %q", code)
	}
}

func TestRESTPatchBoardSettings_temporaryBoardOwnerSuccess(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug+"/settings", map[string]any{
		"defaultSprintWeeks": 1,
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestRESTPatchBoardSettings_temporaryBoardNonOwnerForbidden(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	_, _, tempBoard := setupRESTTemporaryBoardOwner(t, ts, sqlDB)
	st := store.New(sqlDB, nil)
	if _, err := st.CreateUser(context.Background(), "temp-settings-other@example.com", "password123", "Other"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	otherClient := newCookieClient(t)
	loginUserClient(t, otherClient, ts.URL, "temp-settings-other@example.com", "password123")

	resp, body := doJSON(t, otherClient, http.MethodPatch, ts.URL+"/api/board/"+tempBoard.Slug+"/settings", map[string]any{
		"defaultSprintWeeks": 1,
	}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "FORBIDDEN" {
		t.Fatalf("expected FORBIDDEN, got %q", code)
	}
}

func TestRESTPatchProjectImage_anonymousBoardNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "anon-image-owner@example.com", "password123")
	st := store.New(sqlDB, nil)
	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/projects/"+strconv.FormatInt(anonBoard.ID, 10), map[string]any{
		"image": "data:image/png;base64,aaaa",
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}

func TestRESTPatchBoardSettings_anonymousBoardNotFound(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	bootstrapUserClient(t, ownerClient, ts.URL, "Owner", "anon-settings-owner@example.com", "password123")
	st := store.New(sqlDB, nil)
	anonBoard, err := st.CreateAnonymousBoard(context.Background())
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}

	resp, body := doJSON(t, ownerClient, http.MethodPatch, ts.URL+"/api/board/"+anonBoard.Slug+"/settings", map[string]any{
		"defaultSprintWeeks": 1,
	}, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", resp.StatusCode, string(body))
	}
	if code := apiErrorCode(t, body); code != "NOT_FOUND" {
		t.Fatalf("expected NOT_FOUND, got %q", code)
	}
}
