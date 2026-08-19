package httpapi

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestLegacyTodoDeleteSuccessAndRealtimeContract(t *testing.T) {
	fixture := newLegacyTodoMutationFixture(t, "full")
	owner, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
	legacyTodoSeedGlobalIdentity(t, fixture, ownerCtx, store.ModeFull)
	project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy DELETE success")
	maintainer := legacyTodoCreateUser(t, fixture, "delete-maintainer@example.com")
	legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, maintainer.ID, store.RoleMaintainer)
	todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Delete target", ColumnKey: store.DefaultColumnTesting})
	if todo.ID == todo.LocalID {
		t.Fatalf("fixture identity did not diverge: %+v", todo)
	}
	client := legacyTodoClientForUser(t, fixture, maintainer.ID)
	legacyTodoResetEvents(fixture)
	stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

	response, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), nil, nil)
	if response.StatusCode != http.StatusNoContent || len(body) != 0 {
		t.Fatalf("DELETE status=%d body=%q, want empty 204", response.StatusCode, body)
	}
	if legacyTodoRowCount(t, fixture, todo.ID) != 0 {
		t.Fatal("successful DELETE retained Todo")
	}
	audits := legacyTodoAudits(t, fixture, "todo_deleted", todo.ID)
	if len(audits) != 1 || audits[0].ProjectID != project.ID || !audits[0].ActorID.Valid || audits[0].ActorID.Int64 != maintainer.ID || audits[0].TargetType != "todo" || !audits[0].TargetID.Valid || audits[0].TargetID.Int64 != todo.ID {
		t.Fatalf("delete audits=%+v", audits)
	}
	if int64(audits[0].Metadata["local_id"].(float64)) != todo.LocalID || audits[0].Metadata["column_key"] != store.DefaultColumnTesting {
		t.Fatalf("delete audit metadata=%+v", audits[0].Metadata)
	}
	legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_deleted", maintainer.ID)
	if gotAssigned := legacyTodoCountEvents(t, fixture, project.ID, "todo.assigned"); gotAssigned != 0 {
		t.Fatalf("delete assignment events=%d, want 0", gotAssigned)
	}
	assertTodoUpdateRefreshes(t, collectTodoUpdateEvents(t, stream), project.ID, "todo_deleted", 0)
}

func TestLegacyTodoDeleteAccessAndModeContracts(t *testing.T) {
	cases := []struct {
		name string
		role store.ProjectRole
		kind string
	}{
		{name: "viewer_hidden", role: store.RoleViewer},
		{name: "non_member_hidden", kind: "nonmember"},
		{name: "no_session_hidden", kind: "anonymous"},
		{name: "missing_todo", kind: "missing"},
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLegacyTodoMutationFixture(t, "full")
			owner, ownerCtx, ownerClient := legacyTodoBootstrapOwner(t, fixture)
			project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy DELETE hidden")
			todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Hidden delete"})
			client := ownerClient
			todoID := todo.ID
			switch tc.kind {
			case "anonymous":
				client = legacyTodoAnonymousClient(t, fixture)
			case "missing":
				todoID += 100000
			default:
				actor := legacyTodoCreateUser(t, fixture, fmt.Sprintf("delete-hidden-%d@example.com", index))
				if tc.kind != "nonmember" {
					legacyTodoAddMember(t, fixture, owner, ownerCtx, project.ID, actor.ID, tc.role)
				}
				client = legacyTodoClientForUser(t, fixture, actor.ID)
			}
			legacyTodoResetEvents(fixture)
			response, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todoID), nil, nil)
			legacyTodoAssertError(t, response, body, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
			if legacyTodoRowCount(t, fixture, todo.ID) != 1 || len(legacyTodoAudits(t, fixture, "todo_deleted", todo.ID)) != 0 {
				t.Fatal("failed DELETE removed Todo or wrote delete audit")
			}
			legacyTodoAssertNoEvents(t, fixture, project.ID)
		})
	}

	t.Run("temporary_link_holder_success", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateTemporaryProject(t, fixture, ownerCtx)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Temporary delete"})
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, legacyTodoAnonymousClient(t, fixture), http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), nil, nil)
		if response.StatusCode != http.StatusNoContent || len(body) != 0 {
			t.Fatalf("temporary DELETE status=%d body=%q, want empty 204", response.StatusCode, body)
		}
		if legacyTodoRowCount(t, fixture, todo.ID) != 0 {
			t.Fatal("temporary DELETE retained Todo")
		}
		legacyTodoAssertAnonymousAuditActor(t, fixture, "todo_deleted", todo.ID)
		legacyTodoAssertOneRefresh(t, fixture, project.ID, "todo_deleted", 0)
	})

	t.Run("expired_temporary_rejected", func(t *testing.T) {
		fixture := newLegacyTodoMutationFixture(t, "full")
		_, ownerCtx, _ := legacyTodoBootstrapOwner(t, fixture)
		project := legacyTodoCreateTemporaryProject(t, fixture, ownerCtx)
		todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Expired delete"})
		if _, err := fixture.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), project.ID); err != nil {
			t.Fatalf("expire temporary project: %v", err)
		}
		legacyTodoResetEvents(fixture)
		response, body := doJSON(t, legacyTodoAnonymousClient(t, fixture), http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), nil, nil)
		legacyTodoAssertError(t, response, body, http.StatusNotFound, "NOT_FOUND", "not found", "", "")
		if legacyTodoRowCount(t, fixture, todo.ID) != 1 || len(legacyTodoAudits(t, fixture, "todo_deleted", todo.ID)) != 0 {
			t.Fatal("expired temporary DELETE changed persistence")
		}
		legacyTodoAssertNoEvents(t, fixture, project.ID)
	})
}

func TestLegacyTodoDeleteFailureIsRealtimeSilent(t *testing.T) {
	fixture := newLegacyTodoMutationFixture(t, "full")
	_, ownerCtx, client := legacyTodoBootstrapOwner(t, fixture)
	project := legacyTodoCreateProject(t, fixture, ownerCtx, "Legacy DELETE failure")
	todo := legacyTodoCreateTodo(t, fixture, ownerCtx, project.ID, store.ModeFull, store.CreateTodoInput{Title: "Retained after failure"})
	if _, err := fixture.db.Exec(`
		CREATE TRIGGER phase22_legacy_abort_todo_delete
		BEFORE DELETE ON todos
		WHEN OLD.id = ` + fmt.Sprintf("%d", todo.ID) + `
		BEGIN
			SELECT RAISE(ABORT, 'forced legacy todo delete failure');
		END`); err != nil {
		t.Fatalf("create delete failure trigger: %v", err)
	}
	defer func() {
		if _, err := fixture.db.Exec(`DROP TRIGGER IF EXISTS phase22_legacy_abort_todo_delete`); err != nil {
			t.Errorf("drop delete failure trigger: %v", err)
		}
	}()
	legacyTodoResetEvents(fixture)
	stream := subscribeTodoUpdateEvents(t, client, fixture.ts.URL+"/api/board/"+project.Slug+"/events")

	response, body := doJSON(t, client, http.MethodDelete, fmt.Sprintf("%s/api/todos/%d", fixture.ts.URL, todo.ID), nil, nil)
	legacyTodoAssertError(t, response, body, http.StatusInternalServerError, "INTERNAL", "internal error", "", "")
	if legacyTodoRowCount(t, fixture, todo.ID) != 1 {
		t.Fatal("failed DELETE removed Todo")
	}
	if len(legacyTodoAudits(t, fixture, "todo_deleted", todo.ID)) != 0 {
		t.Fatal("failed DELETE committed delete audit")
	}
	legacyTodoAssertNoEvents(t, fixture, project.ID)
	if events := collectTodoUpdateEvents(t, stream); len(events) != 0 {
		t.Fatalf("failed DELETE emitted SSE events: %+v", events)
	}
}
