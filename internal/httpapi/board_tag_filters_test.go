package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestParseBoardTagFilters(t *testing.T) {
	want := []string{"Bug", "feature", "Needs QA"}
	got, err := parseBoardTagFilters([]string{" Bug ", "feature", "bug", "", "  ", "Needs QA"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBoardTagFilters()=%v err=%v want=%v", got, err, want)
	}

	twenty := make([]string, maxBoardTagFilters)
	for i := range twenty {
		twenty[i] = fmt.Sprintf("tag-%d", i)
	}
	if got, err := parseBoardTagFilters(append(append([]string{}, twenty...), "TAG-0")); err != nil || len(got) != maxBoardTagFilters {
		t.Fatalf("duplicate after cap should be accepted: len=%d err=%v", len(got), err)
	}
	if _, err := parseBoardTagFilters(append(twenty, "tag-20")); err == nil {
		t.Fatal("twenty-one unique tag filters should be rejected")
	}
}

func TestBoardReadRoutesRejectMoreThanTwentyUniqueTagFilters(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	client := newCookieClient(t)
	ownerJSON := bootstrapUserClient(t, client, ts.URL, "Owner", "tag-cap@example.com", "password123")
	ownerID := int64(ownerJSON["id"].(float64))
	ownerCtx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ownerCtx, "Tag Cap")
	if err != nil {
		t.Fatal(err)
	}

	query := url.Values{}
	for i := 0; i <= maxBoardTagFilters; i++ {
		query.Add("tag", fmt.Sprintf("tag-%d", i))
	}
	paths := []string{
		"/api/board/" + project.Slug,
		"/api/board/" + project.Slug + "/lanes/" + store.DefaultColumnBacklog,
		fmt.Sprintf("/api/projects/%d/board", project.ID),
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			var payload map[string]any
			resp, body := doJSON(t, client, http.MethodGet, ts.URL+path+"?"+query.Encode(), nil, &payload)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), `"reason":"too_many_tag_filters"`) {
				t.Fatalf("missing stable reason in body: %s", body)
			}
		})
	}
}
