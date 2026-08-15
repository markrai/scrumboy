package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/eventbus"
	"scrumboy/internal/store"
)

func TestLegacyTodoPatchAccessAndValidationContracts(t *testing.T) {
	t.Run("maintainer_success", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		legacyTodoSeedGlobalIdentity(t, fixture, ownerCtx, store.ModeFull)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH maintainer")
		maintainer := legacyTodoCreateUser(t, fixture, "patch-maintainer@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, maintainer.ID, store.RoleMaintainer)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Before", Body: "before"})
		legacyTodoResetEvents(fixture)

		payload := legacyTodoFullPatch(todo)
		payload["title"] = "After"
		payload["body"] = "after"
		var got todoJSON
		response, body := doJSON(t, legacyTodoClientForUser(t, fixture, maintainer.ID), http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, &got)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", response.StatusCode, body)
		}
		legacyTodoAssertProjectionIdentity(t, got, todo)
		if persisted := legacyTodoRead(t, fixture, todo.ID); persisted.Title != "After" || persisted.Body != "after" {
			t.Fatalf("persisted Todo=%+v", persisted)
		}
	})

	t.Run("assigned_contributor_body_only", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH assigned contributor")
		contributor := legacyTodoCreateUser(t, fixture, "patch-assigned-contributor@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, contributor.ID, store.RoleContributor)
		points := int64(3)
		priority := "medium"
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{
			Title:            "Original title",
			Body:             "Original body",
			Tags:             []string{"original"},
			EstimationPoints: &points,
			AssigneeUserID:   &contributor.ID,
			PriorityKey:      &priority,
		})
		ledgerBefore := legacyTodoAssignmentCount(t, fixture, todo.ID)
		legacyTodoResetEvents(fixture)
		changedPoints := int64(8)
		payload := map[string]any{
			"title":            "Attempted title",
			"body":             "Contributor body",
			"tags":             []string{"attempted"},
			"estimationPoints": changedPoints,
			"assigneeUserId":   contributor.ID,
			"priorityKey":      "high",
		}
		response, body := doJSON(t, legacyTodoClientForUser(t, fixture, contributor.ID), http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("assigned contributor PATCH status=%d body=%s", response.StatusCode, body)
		}
		persisted := legacyTodoRead(t, fixture, todo.ID)
		if persisted.Body != "Contributor body" || persisted.Title != todo.Title || !reflect.DeepEqual(persisted.Tags, []string{"original"}) || persisted.EstimationPoints == nil || *persisted.EstimationPoints != points || persisted.PriorityKey == nil || *persisted.PriorityKey != priority {
			t.Fatalf("body-only scope drifted, persisted=%+v", persisted)
		}
		if got := len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)); got != 0 {
			t.Fatalf("body-only update audits=%d, want 0", got)
		}
		if got := legacyTodoAssignmentCount(t, fixture, todo.ID); got != ledgerBefore {
			t.Fatalf("assignment rows=%d, want unchanged %d", got, ledgerBefore)
		}
		request := legacyTodoAssertRefreshAndCreatorRequest(t, fixture, project.ID, "todo_updated", contributor.ID)
		if request.ProjectID != project.ID || request.ProjectSlug != project.Slug || request.TodoID != todo.ID || request.LocalID != todo.LocalID || request.Title != todo.Title || request.ActivityReason != "todo_updated" || request.CreatedByUserID != owner.ID || request.ActorUserID != contributor.ID {
			t.Fatalf("creator request payload=%+v", request)
		}
	})

	failureCases := []struct {
		name string
		role store.ProjectRole
		kind string
	}{
		{name: "unassigned_contributor_hidden", role: store.RoleContributor},
		{name: "viewer_hidden", role: store.RoleViewer},
		{name: "non_member_hidden", kind: "nonmember"},
		{name: "no_session_hidden", kind: "anonymous"},
		{name: "missing_todo", kind: "missing"},
	}
	for index, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLegacyTodoMutationFixture(t, "full")
			owner, ownerCtx, ownerClient := legacyTodoBootstrapOwner(t, fixture)
			project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH hidden")
			todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Hidden target", Body: "before"})
			client := ownerClient
			todoID := todo.ID
			switch tc.kind {
			case "anonymous":
				client = legacyTodoAnonymousClient(t, fixture)
			case "missing":
				todoID += 100000
			default:
				actor := legacyTodoCreateUser(t, fixture, fmt.Sprintf("patch-hidden-%d@example.com", index))
				if tc.kind != "nonmember" {
					legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, actor.ID, tc.role)
				}
				client = legacyTodoClientForUser(t, fixture, actor.ID)
			}
			legacyTodoResetEvents(fixture)
			payload := legacyTodoFullPatch(todo)
			payload["body"] = "forbidden change"
			response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todoID), payload, nil)
			legacyTodoAssertError(t, response, body, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
			if persisted := legacyTodoRead(t, fixture, todo.ID); persisted.Body != "before" {
				t.Fatalf("failed PATCH changed body to %q", persisted.Body)
			}
			if len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)) != 0 {
				t.Fatal("failed PATCH wrote update audit")
			}
			legacyTodoAssertNoEvents(t, fixture, project.ID)
		})
	}

	t.Run("malformed_json_precedes_required_field", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH malformed")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Malformed target"})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoDoRawJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), `{"title":`)
		legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json", "invalid_json", "")
		if legacyTodoRead(t, fixture, todo.ID).Title != todo.Title {
			t.Fatal("malformed JSON changed Todo")
		}
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})

	t.Run("missing_assignee_precedes_missing_todo", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH presence")
		control := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Presence control"})
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, control.ID+100000), map[string]any{"title": "Complete enough to parse"}, nil)
		legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "missing assigneeUserId", "missing_assignee_user_id", "assigneeUserId")
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})

	t.Run("typed_decode_failure_after_presence", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH typed decode")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Typed target"})
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), map[string]any{"assigneeUserId": nil, "title": []string{"wrong"}}, nil)
		legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "invalid json payload", "invalid_json", "")
		if legacyTodoRead(t, fixture, todo.ID).Title != todo.Title {
			t.Fatal("typed decode failure changed Todo")
		}
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})
}

func TestLegacyTodoPatchRealtimeAssignmentAndNoOpContracts(t *testing.T) {
	t.Run("non_assignment_change", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH refresh")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Before", Body: "same"})
		legacyTodoResetEvents(fixture)
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")
		payload := legacyTodoFullPatch(todo)
		payload["title"] = "After"
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("PATCH status=%d body=%s", response.StatusCode, body)
		}
		if got := len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)); got != 1 {
			t.Fatalf("update audit count=%d, want 1", got)
		}
		if got := legacyTodoAssignmentCount(t, fixture, todo.ID); got != 0 {
			t.Fatalf("assignment rows=%d, want 0", got)
		}
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_updated", owner.ID)
		assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_updated", 0)
	})

	t.Run("semantic_no_op", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH no-op")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Same", Body: "same"})
		if _, err := fixture.db.Exec(`UPDATE todos SET updated_at = 1 WHERE id = ?`, todo.ID); err != nil {
			t.Fatalf("seed updated_at sentinel: %v", err)
		}
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), legacyTodoFullPatch(todo), nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("no-op PATCH status=%d body=%s", response.StatusCode, body)
		}
		if persisted := legacyTodoRead(t, fixture, todo.ID); persisted.UpdatedAt.UnixMilli() <= 1 {
			t.Fatalf("no-op did not execute update path: updatedAt=%v", persisted.UpdatedAt)
		}
		if len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)) != 0 || legacyTodoAssignmentCount(t, fixture, todo.ID) != 0 {
			t.Fatal("semantic no-op wrote audit or assignee ledger")
		}
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_updated", owner.ID)
	})

	t.Run("assignment_set", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH assignment set")
		assignee := legacyTodoCreateUser(t, fixture, "patch-set-assignee@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, assignee.ID, store.RoleContributor)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Assign me", Body: "same"})
		legacyTodoResetEvents(fixture)
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")
		payload := legacyTodoFullPatch(todo)
		payload["assigneeUserId"] = assignee.ID
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("assignment PATCH status=%d body=%s", response.StatusCode, body)
		}
		persisted := legacyTodoRead(t, fixture, todo.ID)
		if persisted.AssigneeUserID == nil || *persisted.AssigneeUserID != assignee.ID || legacyTodoAssignmentCount(t, fixture, todo.ID) != 1 {
			t.Fatalf("assignment persistence=%+v ledger=%d", persisted, legacyTodoAssignmentCount(t, fixture, todo.ID))
		}
		if len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)) != 0 {
			t.Fatal("assignment-only PATCH wrote todo_updated audit")
		}
		events := legacyTodoEventsForProject(fixture, project.ID)
		if len(events) != 1 || events[0].Type != "todo.assigned" {
			t.Fatalf("internal events=%+v, want one todo.assigned and no direct refresh", events)
		}
		var domain eventbus.TodoAssignedPayload
		if err := json.Unmarshal(events[0].Payload, &domain); err != nil {
			t.Fatalf("decode todo.assigned: %v", err)
		}
		if domain.TodoID != todo.ID || domain.LocalID != todo.LocalID || domain.ProjectSlug != project.Slug || domain.ActorUserID != owner.ID || domain.ToAssigneeUID == nil || *domain.ToAssigneeUID != assignee.ID {
			t.Fatalf("todo.assigned payload=%+v", domain)
		}
		assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_assigned", 1)
	})

	t.Run("assignment_clear", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH assignment clear")
		assignee := legacyTodoCreateUser(t, fixture, "patch-clear-assignee@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, assignee.ID, store.RoleContributor)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Clear me", Body: "same", AssigneeUserID: &assignee.ID})
		ledgerBefore := legacyTodoAssignmentCount(t, fixture, todo.ID)
		legacyTodoResetEvents(fixture)
		stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")
		payload := legacyTodoFullPatch(todo)
		payload["assigneeUserId"] = nil
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("clear PATCH status=%d body=%s", response.StatusCode, body)
		}
		if persisted := legacyTodoRead(t, fixture, todo.ID); persisted.AssigneeUserID != nil {
			t.Fatalf("assignment was not cleared: %+v", persisted)
		}
		if got := legacyTodoAssignmentCount(t, fixture, todo.ID); got != ledgerBefore+1 {
			t.Fatalf("assignment rows=%d, want %d", got, ledgerBefore+1)
		}
		events := legacyTodoEventsForProject(fixture, project.ID)
		if len(events) != 1 || events[0].Type != "todo.assigned" {
			t.Fatalf("internal events=%+v, want one todo.assigned", events)
		}
		assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_assigned", 0)
	})

	t.Run("assignment_unchanged", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH assignment unchanged")
		assignee := legacyTodoCreateUser(t, fixture, "patch-unchanged-assignee@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, assignee.ID, store.RoleContributor)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Same assignment", Body: "same", AssigneeUserID: &assignee.ID})
		ledgerBefore := legacyTodoAssignmentCount(t, fixture, todo.ID)
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), legacyTodoFullPatch(todo), nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("unchanged assignment PATCH status=%d body=%s", response.StatusCode, body)
		}
		if legacyTodoAssignmentCount(t, fixture, todo.ID) != ledgerBefore || len(legacyTodoAudits(t, fixture, "todo_updated", todo.ID)) != 0 {
			t.Fatal("unchanged assignment wrote ledger or update audit")
		}
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_updated", owner.ID)
	})
}

func TestLegacyTodoPatchCompatibilityPayloadAndProjectionContracts(t *testing.T) {
	t.Run("replacement_fields_are_not_sparse", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH replacement")
		points := int64(5)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Required title", Body: "before", Tags: []string{"stable"}, EstimationPoints: &points})
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), map[string]any{"body": "attempted", "assigneeUserId": nil}, nil)
		legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "validation: invalid title", "invalid_title", "")
		persisted := legacyTodoRead(t, fixture, todo.ID)
		if persisted.Title != todo.Title || persisted.Body != todo.Body || !reflect.DeepEqual(persisted.Tags, []string{"stable"}) || persisted.EstimationPoints == nil || *persisted.EstimationPoints != points {
			t.Fatalf("failed replacement PATCH changed Todo: %+v", persisted)
		}
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})

	t.Run("sprint_id_is_ignored", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		legacyTodoSeedGlobalIdentity(t, fixture, ownerCtx, store.ModeFull)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH ignored sprint")
		now := time.Now().UTC()
		originalSprint, err := fixture.store.CreateSprint(ownerCtx, project.ID, "Original sprint", now, now.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("create original sprint: %v", err)
		}
		otherSprint, err := fixture.store.CreateSprint(ownerCtx, project.ID, "Other sprint", now.Add(8*24*time.Hour), now.Add(15*24*time.Hour))
		if err != nil {
			t.Fatalf("create other sprint: %v", err)
		}
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Sprint target", Body: "same", SprintID: &originalSprint.ID})
		for _, supplied := range []any{nil, otherSprint.ID} {
			payload := legacyTodoFullPatch(legacyTodoRead(t, fixture, todo.ID))
			payload["sprintId"] = supplied
			var got todoJSON
			response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, &got)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("PATCH sprintId=%v status=%d body=%s", supplied, response.StatusCode, body)
			}
			persisted := legacyTodoRead(t, fixture, todo.ID)
			if persisted.SprintID == nil || *persisted.SprintID != originalSprint.ID || got.SprintId == nil || *got.SprintId != originalSprint.ID {
				t.Fatalf("supplied sprintId=%v changed/suppressed original: persisted=%+v response=%+v", supplied, persisted, got)
			}
		}
	})

	t.Run("legacy_success_projection", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
		legacyTodoSeedGlobalIdentity(t, fixture, ownerCtx, store.ModeFull)
		project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy PATCH projection")
		now := time.Now().UTC()
		sprint, err := fixture.store.CreateSprint(ownerCtx, project.ID, "Projection sprint", now, now.Add(7*24*time.Hour))
		if err != nil {
			t.Fatalf("create projection sprint: %v", err)
		}
		medium := "medium"
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Projection before", Body: "before", Tags: []string{"old"}, SprintID: &sprint.ID, PriorityKey: &medium})
		if todo.ID == todo.LocalID {
			t.Fatalf("fixture identity did not diverge: %+v", todo)
		}
		points := int64(8)
		payload := map[string]any{
			"title":            "Projection after",
			"body":             "after body",
			"tags":             []string{"alpha", "beta"},
			"estimationPoints": points,
			"assigneeUserId":   nil,
			"priorityKey":      "high",
		}
		var got todoJSON
		response, body := doJSON(t, client, http.MethodPatch, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), payload, &got)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("projection PATCH status=%d body=%s", response.StatusCode, body)
		}
		legacyTodoAssertProjectionIdentity(t, got, todo)
		if got.Title != "Projection after" || got.Body != "after body" || got.Status != "BACKLOG" || got.ColumnKey != store.DefaultColumnBacklog || got.EstimationPoints == nil || *got.EstimationPoints != points || got.PriorityKey == nil || *got.PriorityKey != "high" || got.SprintId == nil || *got.SprintId != sprint.ID || !reflect.DeepEqual(got.Tags, []string{"alpha", "beta"}) {
			t.Fatalf("legacy projection=%+v", got)
		}
	})
}
