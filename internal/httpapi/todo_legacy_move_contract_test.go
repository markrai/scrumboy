package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func legacyTodoMoveDurableSetup(t *testing.T, name string) (*legacyTodoMutationFixture, store.User, context.Context, *http.Client, store.Project) {
	t.Helper()
	fixture := newLegacyTodoMutationFixture(t, "full")
	owner, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
	legacyTodoSeedGlobalIdentity(t, fixture, ownerCtx, store.ModeFull)
	project := legacyTodoCreateProject(t, fixture, ownerCtx, name)
	return fixture, owner, ownerCtx, client, project
}

func legacyTodoMoveRequest(t *testing.T, fixture *legacyTodoMutationFixture, client *http.Client, todoID int64, payload map[string]any, out any) (*http.Response, []byte) {
	t.Helper()
	return doJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/todos/%d/move", fixture.ts.URL, todoID), payload, out)
}

func legacyTodoAssertMoveFailureUnchanged(t *testing.T, fixture *legacyTodoMutationFixture, projectID int64, before store.Todo) {
	t.Helper()
	after := legacyTodoRead(t, fixture, before.ID)
	if after.ColumnKey != before.ColumnKey || after.Rank != before.Rank {
		t.Fatalf("failed move changed Todo from column=%q rank=%d to column=%q rank=%d", before.ColumnKey, before.Rank, after.ColumnKey, after.Rank)
	}
	if len(legacyTodoAudits(t, fixture, "todo_moved", before.ID)) != 0 {
		t.Fatal("failed move wrote todo_moved audit")
	}
	legacyTodoAssertNoEvents(t, fixture, projectID)
}

func TestLegacyTodoMoveAccessAndModeContracts(t *testing.T) {
	t.Run("maintainer_success", func(t *testing.T) {
		fixture, owner, ownerCtx, _, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE maintainer")
		maintainer := legacyTodoCreateUser(t, fixture, "move-maintainer@example.com")
		legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, maintainer.ID, store.RoleMaintainer)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Move maintainer"})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoMoveRequest(t, fixture, legacyTodoClientForUser(t, fixture, maintainer.ID), todo.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("maintainer MOVE status=%d body=%s", response.StatusCode, body)
		}
		if got := legacyTodoRead(t, fixture, todo.ID); got.ColumnKey != store.DefaultColumnDoing {
			t.Fatalf("maintainer MOVE persisted column=%q", got.ColumnKey)
		}
	})

	cases := []struct {
		name string
		role store.ProjectRole
		kind string
	}{
		{name: "contributor_hidden", role: store.RoleContributor},
		{name: "viewer_hidden", role: store.RoleViewer},
		{name: "non_member_hidden", kind: "nonmember"},
		{name: "no_session_hidden", kind: "anonymous"},
		{name: "missing_todo", kind: "missing"},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, owner, ownerCtx, ownerClient, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE hidden")
			todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Hidden move"})
			client := ownerClient
			todoID := todo.ID
			switch tc.kind {
			case "anonymous":
				client = legacyTodoAnonymousClient(t, fixture)
			case "missing":
				todoID += 100000
			default:
				actor := legacyTodoCreateUser(t, fixture, fmt.Sprintf("move-hidden-%d@example.com", index))
				if tc.kind != "nonmember" {
					legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, actor.ID, tc.role)
				}
				client = legacyTodoClientForUser(t, fixture, actor.ID)
			}
			legacyTodoResetEvents(fixture)
			response, body := legacyTodoMoveRequest(t, fixture, client, todoID, map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
			legacyTodoAssertError(t, response, body, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
			legacyTodoAssertMoveFailureUnchanged(t, fixture, project.ID, todo)
		})
	}

	t.Run("temporary_link_holder_success", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateTemporaryProject(t, fixture, ownerCtx)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Temporary move"})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoMoveRequest(t, fixture, legacyTodoAnonymousClient(t, fixture), todo.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("temporary MOVE status=%d body=%s", response.StatusCode, body)
		}
		legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_moved", todo.ID)
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_moved", 0)
	})

	t.Run("expired_temporary_currently_succeeds", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateTemporaryProject(t, fixture, ownerCtx)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Expired temporary move"})
		if _, err := fixture.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), project.ID); err != nil {
			t.Fatalf("expire temporary project: %v", err)
		}
		legacyTodoResetEvents(fixture)

		// This freezes existing compatibility behavior for refactor safety. It is
		// deliberately not a statement that mutating an expired board is desirable.
		response, body := legacyTodoMoveRequest(t, fixture, legacyTodoAnonymousClient(t, fixture), todo.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("expired temporary MOVE status=%d body=%s, want current 200", response.StatusCode, body)
		}
		if got := legacyTodoRead(t, fixture, todo.ID); got.ColumnKey != store.DefaultColumnDoing {
			t.Fatalf("expired temporary MOVE did not persist: %+v", got)
		}
		legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_moved", todo.ID)
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_moved", 0)
	})
}

func TestLegacyTodoMoveGlobalAnchorAndValidationContracts(t *testing.T) {
	t.Run("to_column_key", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE column")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Column target"})
		response, body := legacyTodoMoveRequest(t, fixture, client, todo.ID, map[string]any{"toColumnKey": store.DefaultColumnTesting}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("MOVE status=%d body=%s", response.StatusCode, body)
		}
		if got := legacyTodoRead(t, fixture, todo.ID); got.ColumnKey != store.DefaultColumnTesting {
			t.Fatalf("column=%q, want %q", got.ColumnKey, store.DefaultColumnTesting)
		}
	})

	t.Run("legacy_to_status", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE status")
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Status target"})
		var got todoJSON
		response, body := legacyTodoMoveRequest(t, fixture, client, todo.ID, map[string]any{"toStatus": "IN_PROGRESS"}, &got)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("MOVE toStatus status=%d body=%s", response.StatusCode, body)
		}
		if got.ColumnKey != store.DefaultColumnDoing || got.Status != "DOING" {
			t.Fatalf("legacy toStatus projection=%+v", got)
		}
	})

	t.Run("missing_destination_precedes_missing_todo", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE destination precedence")
		control := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Destination control"})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoMoveRequest(t, fixture, client, control.ID+100000, map[string]any{}, nil)
		legacyTodoAssertError(t, response, body, http.StatusBadRequest, "VALIDATION_ERROR", "missing toColumnKey", "missing_to_column_key", "toColumnKey")
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})

	t.Run("after_id_is_global", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE global after")
		target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "After target"})
		anchor := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "After anchor", ColumnKey: store.DefaultColumnDoing})
		if anchor.ID == anchor.LocalID {
			t.Fatalf("anchor identity did not diverge: %+v", anchor)
		}
		response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "afterId": anchor.ID}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("global afterId MOVE status=%d body=%s", response.StatusCode, body)
		}
		moved := legacyTodoRead(t, fixture, target.ID)
		if moved.ColumnKey != store.DefaultColumnDoing || moved.Rank <= legacyTodoRead(t, fixture, anchor.ID).Rank {
			t.Fatalf("moved=%+v anchor=%+v", moved, legacyTodoRead(t, fixture, anchor.ID))
		}
	})

	t.Run("before_id_is_global", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE global before")
		target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Before target"})
		anchor := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Before anchor", ColumnKey: store.DefaultColumnDoing})
		if anchor.ID == anchor.LocalID {
			t.Fatalf("anchor identity did not diverge: %+v", anchor)
		}
		response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "beforeId": anchor.ID}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("global beforeId MOVE status=%d body=%s", response.StatusCode, body)
		}
		moved := legacyTodoRead(t, fixture, target.ID)
		if moved.ColumnKey != store.DefaultColumnDoing || moved.Rank >= legacyTodoRead(t, fixture, anchor.ID).Rank {
			t.Fatalf("moved=%+v anchor=%+v", moved, legacyTodoRead(t, fixture, anchor.ID))
		}
	})

	t.Run("both_anchors_supported", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE both anchors")
		target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Both target"})
		after := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Both after", ColumnKey: store.DefaultColumnDoing})
		before := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Both before", ColumnKey: store.DefaultColumnDoing})
		response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "afterId": after.ID, "beforeId": before.ID}, nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("both-anchor MOVE status=%d body=%s", response.StatusCode, body)
		}
		moved := legacyTodoRead(t, fixture, target.ID)
		afterRow := legacyTodoRead(t, fixture, after.ID)
		beforeRow := legacyTodoRead(t, fixture, before.ID)
		if !(afterRow.Rank < moved.Rank && moved.Rank < beforeRow.Rank) {
			t.Fatalf("ranks after=%d moved=%d before=%d", afterRow.Rank, moved.Rank, beforeRow.Rank)
		}
	})

	t.Run("reversed_anchors_conflict", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE reversed anchors")
		target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Reversed target"})
		before := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Earlier", ColumnKey: store.DefaultColumnDoing})
		after := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Later", ColumnKey: store.DefaultColumnDoing})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "afterId": after.ID, "beforeId": before.ID}, nil)
		if response.StatusCode != http.StatusConflict {
			t.Fatalf("reversed anchors status=%d body=%s, want 409", response.StatusCode, body)
		}
		got := legacyTodoDecodeError(t, body)
		if got.Error.Code != "CONFLICT" || !strings.Contains(got.Error.Message, "afterId must come before beforeId") {
			t.Fatalf("reversed anchor error=%+v", got.Error)
		}
		legacyTodoAssertMoveFailureUnchanged(t, fixture, project.ID, target)
	})

	t.Run("self_after_rejected", func(t *testing.T) {
		fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE self anchor")
		target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Self target"})
		legacyTodoResetEvents(fixture)
		response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "afterId": target.ID}, nil)
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("self anchor status=%d body=%s, want 400", response.StatusCode, body)
		}
		got := legacyTodoDecodeError(t, body)
		if got.Error.Code != "VALIDATION_ERROR" || !strings.Contains(got.Error.Message, "afterId cannot equal todoId") {
			t.Fatalf("self anchor error=%+v", got.Error)
		}
		legacyTodoAssertMoveFailureUnchanged(t, fixture, project.ID, target)
	})

	failureCases := []struct {
		name        string
		anchorSetup func(*testing.T, *legacyTodoMutationFixture, context.Context, store.Project) int64
	}{
		{
			name: "missing_anchor_hidden",
			anchorSetup: func(_ *testing.T, _ *legacyTodoMutationFixture, _ context.Context, _ store.Project) int64 {
				return 999999
			},
		},
		{
			name: "cross_project_anchor_hidden",
			anchorSetup: func(t *testing.T, fixture *legacyTodoMutationFixture, ownerCtx context.Context, _ store.Project) int64 {
				other := legacyTodoCreateProject(t, fixture, ownerCtx, "Other anchor project")
				return legacyTodoCreateTodo(t, fixture, ownerCtx, other.ID, store.ModeFull, store.CreateTodoInput{Title: "Cross anchor", ColumnKey: store.DefaultColumnDoing}).ID
			},
		},
		{
			name: "destination_column_mismatch_hidden",
			anchorSetup: func(t *testing.T, fixture *legacyTodoMutationFixture, ownerCtx context.Context, project store.Project) int64 {
				return legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Wrong lane anchor", ColumnKey: store.DefaultColumnBacklog}).ID
			},
		},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, _, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE hidden anchor")
			target := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Anchor failure target"})
			anchorID := tc.anchorSetup(t, fixture, ownerCtx, project)
			legacyTodoResetEvents(fixture)
			response, body := legacyTodoMoveRequest(t, fixture, client, target.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing, "afterId": anchorID}, nil)
			legacyTodoAssertError(t, response, body, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
			legacyTodoAssertMoveFailureUnchanged(t, fixture, project.ID, target)
		})
	}
}

func TestLegacyTodoMoveRealtimeAuditAndProjectionContract(t *testing.T) {
	fixture, owner, ownerCtx, client, project := legacyTodoMoveDurableSetup(t, "Legacy MOVE side effects")
	now := time.Now().UTC()
	sprint, err := fixture.store.CreateSprint(ownerCtx, project.ID, "Move sprint", now, now.Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	points := int64(5)
	priority := "urgent"
	todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{
		Title:            "Move projection",
		Body:             "unchanged body",
		Tags:             []string{"move-tag"},
		EstimationPoints: &points,
		SprintID:         &sprint.ID,
		PriorityKey:      &priority,
	})
	if todo.ID == todo.LocalID {
		t.Fatalf("fixture identity did not diverge: %+v", todo)
	}
	legacyTodoResetEvents(fixture)
	stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")
	var got todoJSON
	response, body := legacyTodoMoveRequest(t, fixture, client, todo.ID, map[string]any{"toColumnKey": store.DefaultColumnDoing}, &got)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MOVE status=%d body=%s", response.StatusCode, body)
	}
	legacyTodoAssertProjectionIdentity(t, got, todo)
	if got.ColumnKey != store.DefaultColumnDoing || got.Status != "DOING" || got.Title != todo.Title || got.Body != todo.Body || got.EstimationPoints == nil || *got.EstimationPoints != points || got.PriorityKey == nil || *got.PriorityKey != priority || got.SprintId == nil || *got.SprintId != sprint.ID || !reflect.DeepEqual(got.Tags, []string{"move-tag"}) {
		t.Fatalf("legacy move projection=%+v", got)
	}
	audits := legacyTodoAudits(t, fixture, "todo_moved", todo.ID)
	if len(audits) != 1 || audits[0].ProjectID != project.ID || !audits[0].ActorID.Valid || audits[0].ActorID.Int64 != owner.ID || !audits[0].TargetID.Valid || audits[0].TargetID.Int64 != todo.ID {
		t.Fatalf("move audits=%+v", audits)
	}
	if audits[0].Metadata["from_column"] != store.DefaultColumnBacklog || audits[0].Metadata["to_column"] != store.DefaultColumnDoing || int64(audits[0].Metadata["local_id"].(float64)) != todo.LocalID {
		t.Fatalf("move audit metadata=%+v", audits[0].Metadata)
	}
	legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_moved", owner.ID)
	if gotAssigned := legacyTodoCountEvents(t, fixture, project.ID, "todo.assigned"); gotAssigned != 0 {
		t.Fatalf("move assignment events=%d, want 0", gotAssigned)
	}
	assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_moved", 0)
}
