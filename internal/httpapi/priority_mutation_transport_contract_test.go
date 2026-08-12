package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/store"
)

type priorityRESTFixture struct {
	ts      *httptest.Server
	st      *store.Store
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
}

func newPriorityRESTFixture(t *testing.T, name string) *priorityRESTFixture {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServer(t, "full")
	t.Cleanup(cleanup)
	client := newCookieClient(t)
	owner := bootstrapUserClient(t, client, ts.URL, "Priority Owner", name+"@example.com", "password123")
	ownerID := int64(owner["id"].(float64))
	ctx := store.WithUserID(context.Background(), ownerID)
	st := store.New(sqlDB, nil)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return &priorityRESTFixture{ts: ts, st: st, client: client, ownerID: ownerID, ctx: ctx, project: project}
}

func priorityTierByKey(t *testing.T, tiers []store.PriorityTier, key string) (store.PriorityTier, bool) {
	t.Helper()
	for _, tier := range tiers {
		if tier.Key == key {
			return tier, true
		}
	}
	return store.PriorityTier{}, false
}

func assertPriorityRESTRefresh(t *testing.T, events []todoUpdateWireEvent, projectID int64, reason string) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("priority event count=%d want=1; events=%+v", len(events), events)
	}
	event := events[0]
	if event.Type != "refresh_needed" || event.ProjectID != projectID || event.Reason != reason {
		t.Fatalf("priority refresh mismatch: want project=%d reason=%q; event=%+v", projectID, reason, event)
	}
}

func TestPriorityMutationRESTSuccessRefreshContracts(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-create")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var created priorityTierJSON
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities", map[string]any{
			"name": "  Critical  ",
		}, &created)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create status=%d body=%s", resp.StatusCode, body)
		}
		if created.Key != "critical" || created.Name != "Critical" || created.Position != 4 {
			t.Fatalf("created tier=%+v", created)
		}
		tiers, err := fx.st.GetProjectPriorities(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get priorities: %v", err)
		}
		persisted, ok := priorityTierByKey(t, tiers, created.Key)
		if !ok || persisted.Name != created.Name {
			t.Fatalf("created tier not persisted as expected: response=%+v tiers=%+v", created, tiers)
		}
		assertPriorityRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "priority_tier_added")
	})

	t.Run("update", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-update")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodPatch, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities/low", map[string]any{
			"name":  "  Chill  ",
			"color": "  #123456  ",
		}, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("update status=%d body=%s", resp.StatusCode, body)
		}
		tiers, err := fx.st.GetProjectPriorities(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get priorities: %v", err)
		}
		persisted, ok := priorityTierByKey(t, tiers, "low")
		if !ok || persisted.Name != "Chill" || persisted.Color != "#123456" {
			t.Fatalf("updated tier=%+v found=%v", persisted, ok)
		}
		assertPriorityRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "priority_tier_updated")
	})

	t.Run("delete", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-delete")
		tier, err := fx.st.AddPriorityTier(fx.ctx, fx.project.ID, "Disposable")
		if err != nil {
			t.Fatalf("add delete fixture: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities/"+tier.Key, nil, nil)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete status=%d body=%s", resp.StatusCode, body)
		}
		tiers, err := fx.st.GetProjectPriorities(fx.ctx, fx.project.ID)
		if err != nil {
			t.Fatalf("get priorities: %v", err)
		}
		if _, ok := priorityTierByKey(t, tiers, tier.Key); ok {
			t.Fatalf("deleted tier %q still present: %+v", tier.Key, tiers)
		}
		assertPriorityRESTRefresh(t, collectTodoUpdateEvents(t, stream), fx.project.ID, "priority_tier_deleted")
	})
}

func TestPriorityMutationRESTFailureSilence(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-validation-silence")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var out map[string]any
		resp, body := doJSON(t, fx.client, http.MethodPost, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities", map[string]any{"name": "   "}, &out)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("validation status=%d body=%s", resp.StatusCode, body)
		}
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "VALIDATION_ERROR" || errBody["details"].(map[string]any)["reason"] != "invalid_priority_tier_name" {
			t.Fatalf("validation response=%+v", out)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("validation emitted priority events: %+v", events)
		}
	})

	t.Run("authorization", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-auth-silence")
		contributor, err := fx.st.CreateUser(context.Background(), "priority-rest-contributor@example.com", "password123", "Contributor")
		if err != nil {
			t.Fatalf("create contributor: %v", err)
		}
		if err := fx.st.AddProjectMember(fx.ctx, fx.ownerID, fx.project.ID, contributor.ID, store.RoleContributor); err != nil {
			t.Fatalf("add contributor: %v", err)
		}
		contributorClient := newCookieClient(t)
		loginUserClient(t, contributorClient, fx.ts.URL, contributor.Email, "password123")
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		resp, body := doJSON(t, contributorClient, http.MethodPatch, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities/low", map[string]any{
			"name":  "Forbidden",
			"color": "#123456",
		}, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("authorization status=%d body=%s", resp.StatusCode, body)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("authorization failure emitted priority events: %+v", events)
		}
	})

	t.Run("store rejection", func(t *testing.T) {
		fx := newPriorityRESTFixture(t, "priority-rest-store-silence")
		low := "low"
		if _, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{Title: "Blocks deletion", PriorityKey: &low}, store.ModeFull); err != nil {
			t.Fatalf("create todo with priority: %v", err)
		}
		stream := subscribeTodoUpdateEvents(t, fx.client, fx.ts.URL+"/api/board/"+fx.project.Slug+"/events")

		var out map[string]any
		resp, body := doJSON(t, fx.client, http.MethodDelete, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities/low", nil, &out)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("store rejection status=%d body=%s", resp.StatusCode, body)
		}
		errBody := out["error"].(map[string]any)
		if errBody["code"] != "CONFLICT" || errBody["details"].(map[string]any)["reason"] != "priority_tier_in_use" {
			t.Fatalf("conflict response=%+v", out)
		}
		if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
			t.Fatalf("store rejection emitted priority events: %+v", events)
		}
	})
}

func TestPriorityMutationRESTCountsEndpoint(t *testing.T) {
	fx := newPriorityRESTFixture(t, "priority-rest-counts")
	low := "low"
	if _, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{Title: "Low priority todo", PriorityKey: &low}, store.ModeFull); err != nil {
		t.Fatalf("create todo with priority: %v", err)
	}

	var counts priorityTierCountsJSON
	resp, body := doJSON(t, fx.client, http.MethodGet, fx.ts.URL+"/api/board/"+fx.project.Slug+"/priorities/counts", nil, &counts)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("counts status=%d body=%s", resp.StatusCode, body)
	}
	if counts.CountsByPriorityKey["low"] != 1 {
		t.Fatalf("counts=%+v want low=1", counts.CountsByPriorityKey)
	}
}
