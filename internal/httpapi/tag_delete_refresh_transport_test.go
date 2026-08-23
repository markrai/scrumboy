package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"scrumboy/internal/eventbus"
)

func TestTagDeleteRoutesPublishChosenEntity(t *testing.T) {
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	defer cleanup()
	srv := ts.Config.Handler.(*Server)
	collector := &capturingEventConsumer{}
	srv.fanout = eventbus.NewFanout(collector)

	client := newCookieClient(t)
	bootstrapUserClient(t, client, ts.URL, "Owner", "tag-entity-owner@example.com", "password123")

	t.Run("name route publishes tag name", func(t *testing.T) {
		pid, slug := createProjectAPI(t, client, ts.URL, "NameRoute")
		createTodoAPI(t, client, ts.URL, slug, "card", "blocked")
		collector.events = nil

		resp, body := doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/blocked", nil, &map[string]any{})
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("name delete status=%d body=%s", resp.StatusCode, body)
		}
		deleted := tagDeletedEvents(t, collector.events)
		if len(deleted) == 0 {
			t.Fatalf("expected tag_deleted refresh, got %+v", collector.events)
		}
		for _, e := range deleted {
			assertRefreshNeededName(t, e.Payload, "blocked")
		}
	})

	t.Run("id route publishes zero entity", func(t *testing.T) {
		pid, slug := createProjectAPI(t, client, ts.URL, "IDRoute")
		createTodoAPI(t, client, ts.URL, slug, "card", "blocked")
		var tagID int64
		if err := sqlDB.QueryRow(`SELECT id FROM tags WHERE name='blocked' AND user_id IS NOT NULL ORDER BY id DESC LIMIT 1`).Scan(&tagID); err != nil {
			t.Fatalf("lookup tag id: %v", err)
		}
		collector.events = nil

		resp, body := doJSON(t, client, http.MethodDelete, ts.URL+"/api/projects/"+strconv.FormatInt(pid, 10)+"/tags/id/"+strconv.FormatInt(tagID, 10), nil, &map[string]any{})
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("id delete status=%d body=%s", resp.StatusCode, body)
		}
		deleted := tagDeletedEvents(t, collector.events)
		if len(deleted) == 0 {
			t.Fatalf("expected tag_deleted refresh, got %+v", collector.events)
		}
		for _, e := range deleted {
			assertRefreshNeededEntityOmitted(t, e.Payload)
		}
	})
}

func tagDeletedEvents(t *testing.T, events []eventbus.Event) []eventbus.Event {
	t.Helper()
	var out []eventbus.Event
	for _, e := range events {
		if e.Type != "board.refresh_needed" {
			continue
		}
		var p refreshNeededPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if p.Reason == "tag_deleted" {
			out = append(out, e)
		}
	}
	return out
}
