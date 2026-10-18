package httpapi

import (
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

func TestTagJSONCarriesServerCanonicalWriteName(t *testing.T) {
	items := tagCountsToJSON([]store.TagCount{{Name: "İ"}, {Name: "cafe\u0301"}, {Name: "make space"}, {Name: "bad!"}})
	want := []string{"i", "café", "make-space", ""}
	for i := range want {
		if items[i].CanonicalName != want[i] {
			t.Fatalf("canonicalName[%d] = %q, want %q", i, items[i].CanonicalName, want[i])
		}
	}
}

func TestRESTTodoUnicodeTagsCreateUpdateFilterAndRollback(t *testing.T) {
	ts, _, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Unicode", "unicode-rest@example.com", "password123")

	var project struct {
		Slug string `json:"slug"`
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/projects", map[string]any{"name": "Unicode Tags"}, &project)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", resp.StatusCode, body)
	}
	var todo struct {
		LocalID int64    `json:"localId"`
		Title   string   `json:"title"`
		Tags    []string `json:"tags"`
	}
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/board/"+project.Slug+"/todos", map[string]any{
		"title": "Unicode", "body": "", "status": "BACKLOG",
		"tags": []string{"erp系统流程", "つながるひろが", "Café", "cafe\u0301"},
	}, &todo)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create Unicode todo status=%d body=%s", resp.StatusCode, body)
	}
	wantCreated := []string{"café", "erp系统流程", "つながるひろが"}
	var fetched struct {
		Title string   `json:"title"`
		Tags  []string `json:"tags"`
	}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), nil, &fetched)
	if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(fetched.Tags, wantCreated) {
		t.Fatalf("retrieved Unicode tags status=%d tags=%#v body=%s", resp.StatusCode, fetched.Tags, body)
	}

	var board struct {
		Columns map[string][]struct {
			LocalID int64    `json:"localId"`
			Tags    []string `json:"tags"`
		} `json:"columns"`
	}
	filterURL := ts.URL + "/api/board/" + project.Slug + "?tag=" + url.QueryEscape("Cafe\u0301") + "&tag=" + url.QueryEscape("ERP系统流程")
	resp, body = doJSON(t, client, http.MethodGet, filterURL, nil, &board)
	if resp.StatusCode != http.StatusOK || len(board.Columns["backlog"]) != 1 {
		t.Fatalf("Unicode filtered board status=%d body=%s board=%+v", resp.StatusCode, body, board)
	}

	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), map[string]any{
		"title": "Unicode updated", "body": "", "assigneeUserId": nil,
		"tags": []string{"개발 작업", "إصلاح-خطأ", "नमस्ते दुनिया"},
	}, &todo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update Unicode todo status=%d body=%s", resp.StatusCode, body)
	}
	wantUpdated := []string{"إصلاح-خطأ", "नमस्ते-दुनिया", "개발-작업"}
	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), nil, &fetched)
	if resp.StatusCode != http.StatusOK || !reflect.DeepEqual(fetched.Tags, wantUpdated) {
		t.Fatalf("updated Unicode tags status=%d tags=%#v body=%s", resp.StatusCode, fetched.Tags, body)
	}

	var apiErr apiErrorEnvelope
	resp, body = doJSON(t, client, http.MethodPatch, ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), map[string]any{
		"title": "Must not persist", "body": "", "assigneeUserId": nil,
		"tags": []string{"개발 작업", "bad!"},
	}, &apiErr)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid Unicode update status=%d body=%s", resp.StatusCode, body)
	}
	assertAPIError(t, apiErr, "VALIDATION_ERROR", "", "invalid_tag")

	resp, body = doJSON(t, client, http.MethodGet, ts.URL+"/api/board/"+project.Slug+"/todos/"+strconv.FormatInt(todo.LocalID, 10), nil, &fetched)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get Unicode todo status=%d body=%s", resp.StatusCode, body)
	}
	if fetched.Title != "Unicode updated" || !reflect.DeepEqual(fetched.Tags, wantUpdated) {
		t.Fatalf("failed update partially persisted: %+v", fetched)
	}
}
