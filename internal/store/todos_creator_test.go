package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func creatorPatch(id *int64) UpdateTodoInput {
	return UpdateTodoInput{
		KeepTitle:              true,
		KeepBody:               true,
		KeepTags:               true,
		KeepEstimationPoints:   true,
		KeepAssigneeUserID:     true,
		CreatedByUserID:        id,
		CreatedByUserIDPresent: true,
	}
}

func creatorAuditCount(t *testing.T, st *Store, ctx context.Context, todoID int64) int {
	t.Helper()
	var count int
	if err := st.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM audit_events
		WHERE action = 'todo_creator_changed' AND target_id = ?
	`, todoID).Scan(&count); err != nil {
		t.Fatalf("count todo_creator_changed audits: %v", err)
	}
	return count
}

func TestUpdateTodo_ReassignCreator(t *testing.T) {
	st, cleanup, ctx, project, maintainer, _, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)

	nonMember, err := st.CreateUser(ctx, "creator-target@example.com", "password", "Historical creator")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{
		Title: "Moved ticket", ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	assertTodoCreator(t, todo, &maintainer.ID)

	var events []TodoLifecycleEvent
	st.SetTodoLifecyclePublisher(func(_ context.Context, event TodoLifecycleEvent) {
		events = append(events, event)
	})

	t.Run("authorized same value is a no-op", func(t *testing.T) {
		events = nil
		got, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(maintainer.ID)), ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertTodoCreator(t, got, &maintainer.ID)
		if got.MaterialChanged || got.CreatorChanged || got.OtherMaterialChanged || got.PublicProjectionChanged {
			t.Fatalf("same-value mutation facts = material:%v creator:%v other:%v public:%v", got.MaterialChanged, got.CreatorChanged, got.OtherMaterialChanged, got.PublicProjectionChanged)
		}
		if count := creatorAuditCount(t, st, ctx, todo.ID); count != 0 {
			t.Fatalf("todo_creator_changed audits = %d, want 0", count)
		}
		if len(events) != 0 {
			t.Fatalf("same-value creator patch published lifecycle events: %+v", events)
		}
	})

	for _, tc := range []struct {
		name string
		id   *int64
	}{
		{name: "null", id: nil},
		{name: "zero", id: ptrInt64(0)},
		{name: "negative", id: ptrInt64(-1)},
		{name: "nonexistent", id: ptrInt64(999999)},
	} {
		t.Run(tc.name+" target is invalid", func(t *testing.T) {
			if _, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(tc.id), ModeFull); !errors.Is(err, ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}

	t.Run("maintainer assigns an existing non-member", func(t *testing.T) {
		events = nil
		got, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(nonMember.ID)), ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertTodoCreator(t, got, &nonMember.ID)
		if !got.MaterialChanged || !got.CreatorChanged || got.OtherMaterialChanged || got.PublicProjectionChanged {
			t.Fatalf("creator-only mutation facts = material:%v creator:%v other:%v public:%v", got.MaterialChanged, got.CreatorChanged, got.OtherMaterialChanged, got.PublicProjectionChanged)
		}

		persisted, err := st.GetTodoByLocalID(ctxM, project.ID, todo.LocalID, ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID: %v", err)
		}
		assertTodoCreator(t, persisted, &nonMember.ID)
		if role, err := st.GetProjectRole(ctx, project.ID, nonMember.ID); err != nil || role != "" {
			t.Fatalf("creator role = %q, err = %v; attribution must not grant membership", role, err)
		}
		if _, err := st.UpdateTodo(WithUserID(ctx, nonMember.ID), todo.ID, UpdateTodoInput{
			KeepTitle: true, KeepBody: true, KeepTags: true,
			KeepEstimationPoints: true, KeepAssigneeUserID: true,
		}, ModeFull); !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("attributed non-member update err = %v, want access denial", err)
		}
		if count := creatorAuditCount(t, st, ctx, todo.ID); count != 1 {
			t.Fatalf("todo_creator_changed audits = %d, want 1", count)
		}
		if len(events) != 1 || events[0].Type != TodoEventUpdated || !containsString(events[0].ChangedFields, "creator") {
			t.Fatalf("creator reassignment lifecycle events = %+v", events)
		}
		if events[0].ProjectSlug != project.Slug || events[0].Todo.ID != todo.ID || events[0].Todo.CreatedByUserID == nil || *events[0].Todo.CreatedByUserID != nonMember.ID || events[0].ActorUserID == nil || *events[0].ActorUserID != maintainer.ID {
			t.Fatalf("creator reassignment lifecycle identity = %+v", events[0])
		}
		var actorID int64
		var projectID int64
		var metadataJSON string
		if err := st.db.QueryRowContext(ctx, `
			SELECT actor_user_id, project_id, metadata
			FROM audit_events
			WHERE action = 'todo_creator_changed' AND target_id = ?
		`, todo.ID).Scan(&actorID, &projectID, &metadataJSON); err != nil {
			t.Fatalf("read creator audit: %v", err)
		}
		var metadata struct {
			Before *int64 `json:"before"`
			After  int64  `json:"after"`
		}
		if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
			t.Fatalf("decode creator audit: %v", err)
		}
		if actorID != maintainer.ID || projectID != project.ID || metadata.Before == nil || *metadata.Before != maintainer.ID || metadata.After != nonMember.ID {
			t.Fatalf("creator audit actor=%d project=%d metadata=%+v", actorID, projectID, metadata)
		}
	})

	t.Run("omitted creator preserves attribution", func(t *testing.T) {
		got, err := st.UpdateTodo(ctxM, todo.ID, UpdateTodoInput{
			Title: "Renamed", KeepBody: true, KeepTags: true,
			KeepEstimationPoints: true, KeepAssigneeUserID: true,
		}, ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertTodoCreator(t, got, &nonMember.ID)
		if !got.MaterialChanged || got.CreatorChanged || !got.OtherMaterialChanged || !got.PublicProjectionChanged {
			t.Fatalf("ordinary mutation facts = material:%v creator:%v other:%v public:%v", got.MaterialChanged, got.CreatorChanged, got.OtherMaterialChanged, got.PublicProjectionChanged)
		}
		if count := creatorAuditCount(t, st, ctx, todo.ID); count != 1 {
			t.Fatalf("creator audit count changed after omitted field: %d", count)
		}
	})
}

func TestUpdateTodo_CreatorMaterialClassification(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)

	newTodo := func(title string) Todo {
		t.Helper()
		todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{Title: title, ColumnKey: DefaultColumnBacklog}, ModeFull)
		if err != nil {
			t.Fatalf("CreateTodo %q: %v", title, err)
		}
		return todo
	}
	assertFacts := func(t *testing.T, got Todo, material, creator, other, public bool) {
		t.Helper()
		if got.MaterialChanged != material || got.CreatorChanged != creator || got.OtherMaterialChanged != other || got.PublicProjectionChanged != public {
			t.Fatalf("mutation facts = material:%v creator:%v other:%v public:%v; want %v/%v/%v/%v", got.MaterialChanged, got.CreatorChanged, got.OtherMaterialChanged, got.PublicProjectionChanged, material, creator, other, public)
		}
		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("Marshal Todo: %v", err)
		}
		var projection map[string]any
		if err := json.Unmarshal(encoded, &projection); err != nil {
			t.Fatalf("Unmarshal Todo projection: %v", err)
		}
		for _, field := range []string{"AssignmentChanged", "MaterialChanged", "CreatorChanged", "OtherMaterialChanged", "PublicProjectionChanged"} {
			if _, ok := projection[field]; ok {
				t.Fatalf("transient field %q serialized in %s", field, encoded)
			}
		}
	}

	t.Run("creator only", func(t *testing.T) {
		todo := newTodo("creator only")
		got, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(contributor.ID)), ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertFacts(t, got, true, true, false, false)
	})

	t.Run("same value", func(t *testing.T) {
		todo := newTodo("same value")
		got, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(maintainer.ID)), ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertFacts(t, got, false, false, false, false)
	})

	t.Run("creator plus public field", func(t *testing.T) {
		todo := newTodo("combined public")
		patch := creatorPatch(ptrInt64(contributor.ID))
		patch.KeepTitle = false
		patch.Title = "combined public changed"
		got, err := st.UpdateTodo(ctxM, todo.ID, patch, ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertFacts(t, got, true, true, true, true)
	})

	t.Run("creator plus private field", func(t *testing.T) {
		todo := newTodo("combined private")
		patch := creatorPatch(ptrInt64(contributor.ID))
		patch.DueAtPresent = true
		dueAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Millisecond)
		patch.DueAt = &dueAt
		got, err := st.UpdateTodo(ctxM, todo.ID, patch, ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertFacts(t, got, true, true, true, false)
	})

	t.Run("creator plus assignment publishes committed creator", func(t *testing.T) {
		todo := newTodo("combined assignment")
		var published bool
		var publishedTo *int64
		var publishedFacts TodoAssignedMutationFacts
		st.SetTodoAssignedPublisher(func(_ context.Context, _, _, _ int64, _, _, _ string, _, to *int64, _ int64, facts TodoAssignedMutationFacts) {
			published = true
			publishedTo = cloneInt64Ptr(to)
			publishedFacts = facts
		})
		patch := creatorPatch(ptrInt64(contributor.ID))
		patch.KeepAssigneeUserID = false
		patch.AssigneeUserID = ptrInt64(contributor.ID)
		got, err := st.UpdateTodo(ctxM, todo.ID, patch, ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		assertFacts(t, got, true, true, true, false)
		if !got.AssignmentChanged || !published || publishedTo == nil || *publishedTo != contributor.ID ||
			publishedFacts.CreatedByUserID == nil || *publishedFacts.CreatedByUserID != contributor.ID ||
			!publishedFacts.DurableProject || publishedFacts.PublicProjectionChanged {
			t.Fatalf("assignment publication to=%v facts=%+v todo=%+v", publishedTo, publishedFacts, got)
		}
	})
}

func TestGetTodoCreatorNotificationFacts(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{Title: "authorization facts", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	facts, found, err := st.GetTodoCreatorNotificationFacts(ctx, project.ID, todo.ID, maintainer.ID)
	if err != nil || !found {
		t.Fatalf("GetTodoCreatorNotificationFacts = (%+v, %v, %v)", facts, found, err)
	}
	if facts.ProjectID != project.ID || facts.ProjectName != project.Name || facts.ProjectSlug != project.Slug || !facts.DurableProject || facts.RecipientRole != RoleMaintainer || facts.TodoID != todo.ID || facts.TodoProjectID != project.ID || facts.TodoLocalID != todo.LocalID || facts.CreatedByUserID == nil || *facts.CreatedByUserID != maintainer.ID {
		t.Fatalf("facts = %+v", facts)
	}

	if _, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(contributor.ID)), ModeFull); err != nil {
		t.Fatalf("UpdateTodo creator: %v", err)
	}
	staleFacts, found, err := st.GetTodoCreatorNotificationFacts(ctx, project.ID, todo.ID, maintainer.ID)
	if err != nil || !found || staleFacts.CreatedByUserID == nil || *staleFacts.CreatedByUserID != contributor.ID {
		t.Fatalf("stale recipient facts = (%+v, %v, %v)", staleFacts, found, err)
	}
	currentFacts, found, err := st.GetTodoCreatorNotificationFacts(ctx, project.ID, todo.ID, contributor.ID)
	if err != nil || !found || currentFacts.RecipientRole != RoleContributor || currentFacts.CreatedByUserID == nil || *currentFacts.CreatedByUserID != contributor.ID {
		t.Fatalf("current recipient facts = (%+v, %v, %v)", currentFacts, found, err)
	}

	if _, found, err := st.GetTodoCreatorNotificationFacts(ctx, project.ID, todo.ID+999999, contributor.ID); err != nil || found {
		t.Fatalf("missing todo = (%v, %v), want false/nil", found, err)
	}

	otherProject, err := st.CreateProject(ctxM, "Other creator facts project")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	otherTodo, err := st.CreateTodo(ctxM, otherProject.ID, CreateTodoInput{Title: "other project todo", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo other: %v", err)
	}
	if _, found, err := st.GetTodoCreatorNotificationFacts(ctx, project.ID, otherTodo.ID, maintainer.ID); err != nil || found {
		t.Fatalf("cross-project todo = (%v, %v), want false/nil", found, err)
	}
}

func TestUpdateTodo_CreatorAuthorizationBoundaries(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, viewer := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	ctxC := WithUserID(ctx, contributor.ID)

	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{
		Title: "Authorization", Body: "original", ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	assigned, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{
		Title: "Assigned", Body: "original assigned", ColumnKey: DefaultColumnBacklog,
		AssigneeUserID: ptrInt64(contributor.ID),
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo assigned: %v", err)
	}

	var events []TodoLifecycleEvent
	st.SetTodoLifecyclePublisher(func(_ context.Context, event TodoLifecycleEvent) {
		events = append(events, event)
	})

	for _, tc := range []struct {
		name    string
		actor   context.Context
		todoID  int64
		target  int64
		wantErr error
	}{
		{name: "contributor different value", actor: ctxC, todoID: todo.ID, target: contributor.ID, wantErr: ErrUnauthorized},
		{name: "contributor same value", actor: ctxC, todoID: todo.ID, target: maintainer.ID, wantErr: ErrUnauthorized},
		{name: "contributor nonexistent target is authorized first", actor: ctxC, todoID: todo.ID, target: 999999, wantErr: ErrUnauthorized},
		{name: "viewer", actor: WithUserID(ctx, viewer.ID), todoID: todo.ID, target: maintainer.ID, wantErr: ErrNotFound},
		{name: "unauthenticated durable caller", actor: ctx, todoID: todo.ID, target: maintainer.ID, wantErr: ErrUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.UpdateTodo(tc.actor, tc.todoID, creatorPatch(ptrInt64(tc.target)), ModeFull); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}

	t.Run("assigned contributor combined mutation rolls back", func(t *testing.T) {
		combined := creatorPatch(ptrInt64(maintainer.ID))
		combined.KeepBody = false
		combined.Body = "unauthorized body"
		if _, err := st.UpdateTodo(ctxC, assigned.ID, combined, ModeFull); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
		persisted, err := st.GetTodoByLocalID(ctxM, project.ID, assigned.LocalID, ModeFull)
		if err != nil {
			t.Fatalf("GetTodoByLocalID: %v", err)
		}
		if persisted.Body != "original assigned" {
			t.Fatalf("body = %q, want rollback to original assigned", persisted.Body)
		}
		assertTodoCreator(t, persisted, &maintainer.ID)
	})

	if creatorAuditCount(t, st, ctx, todo.ID) != 0 || creatorAuditCount(t, st, ctx, assigned.ID) != 0 {
		t.Fatal("rejected creator patches generated todo_creator_changed audits")
	}
	if len(events) != 0 {
		t.Fatalf("rejected creator patches published lifecycle events: %+v", events)
	}

	t.Run("omitted creator preserves body-only contributor behavior", func(t *testing.T) {
		got, err := st.UpdateTodo(ctxC, assigned.ID, UpdateTodoInput{
			KeepTitle: true, Body: "allowed body", KeepTags: true,
			KeepEstimationPoints: true, KeepAssigneeUserID: true,
		}, ModeFull)
		if err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		if got.Body != "allowed body" {
			t.Fatalf("body = %q, want allowed body", got.Body)
		}
		assertTodoCreator(t, got, &maintainer.ID)
	})
}

func TestUpdateTodo_CreatorTemporaryAnonymousAndNoAuthBoundaries(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "temporary-creator-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)

	temporary, err := st.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard temporary: %v", err)
	}
	temporaryTodo, err := st.CreateTodo(ownerCtx, temporary.ID, CreateTodoInput{Title: "temporary"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo temporary: %v", err)
	}
	for _, tc := range []struct {
		name  string
		actor context.Context
		id    *int64
	}{
		{name: "unauthenticated link holder same value", actor: ctx, id: ptrInt64(owner.ID)},
		{name: "temporary creator without project role same value", actor: ownerCtx, id: ptrInt64(owner.ID)},
		{name: "unauthenticated invalid value is still unauthorized", actor: ctx, id: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.UpdateTodo(tc.actor, temporaryTodo.ID, creatorPatch(tc.id), ModeFull); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("err = %v, want ErrUnauthorized", err)
			}
		})
	}
	assertTodoCreator(t, temporaryTodo, &owner.ID)
	if count := creatorAuditCount(t, st, ctx, temporaryTodo.ID); count != 0 {
		t.Fatalf("temporary rejected creator audits = %d, want 0", count)
	}

	anonymous, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard anonymous: %v", err)
	}
	anonymousTodo, err := st.CreateTodo(ctx, anonymous.ID, CreateTodoInput{Title: "anonymous"}, ModeAnonymous)
	if err != nil {
		t.Fatalf("CreateTodo anonymous: %v", err)
	}
	if err := st.EnsureMaintainerMembership(ctx, anonymous.ID, owner.ID); err != nil {
		t.Fatalf("EnsureMaintainerMembership: %v", err)
	}
	if _, err := st.UpdateTodo(ownerCtx, anonymousTodo.ID, creatorPatch(ptrInt64(owner.ID)), ModeAnonymous); !errors.Is(err, ErrValidation) {
		t.Fatalf("anonymous creator patch err = %v, want ErrValidation", err)
	}

	preBootstrap, preBootstrapCleanup := newTestStore(t)
	defer preBootstrapCleanup()
	project, err := preBootstrap.CreateProject(ctx, "Pre-bootstrap creator")
	if err != nil {
		t.Fatalf("CreateProject pre-bootstrap: %v", err)
	}
	preBootstrapTodo, err := preBootstrap.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "pre-bootstrap"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo pre-bootstrap: %v", err)
	}
	if _, err := preBootstrap.UpdateTodo(ctx, preBootstrapTodo.ID, creatorPatch(ptrInt64(1)), ModeFull); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pre-bootstrap creator patch err = %v, want ErrUnauthorized", err)
	}
}

func TestUpdateTodo_CreatorGuestSystemAndServiceBoundaries(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	if project.OwnerUserID == nil {
		t.Fatal("fixture project has no owner")
	}
	ownerID := *project.OwnerUserID
	ownerCtx := WithUserID(ctx, ownerID)
	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{Title: "identity boundaries"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	guest, err := st.CreateUser(ctx, "creator-guest@example.com", "password", "Guest")
	if err != nil {
		t.Fatalf("CreateUser guest: %v", err)
	}
	if err := st.UpdateUserRole(ownerCtx, ownerID, guest.ID, SystemRoleGuest); err != nil {
		t.Fatalf("UpdateUserRole guest: %v", err)
	}
	if err := st.AddProjectMember(ctxM, maintainer.ID, project.ID, guest.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember guest: %v", err)
	}
	if _, err := st.UpdateTodo(WithUserID(ctx, guest.ID), todo.ID, creatorPatch(ptrInt64(maintainer.ID)), ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("guest creator patch err = %v, want ErrNotFound", err)
	}

	admin, err := st.CreateUser(ctx, "creator-admin@example.com", "password", "Admin outsider")
	if err != nil {
		t.Fatalf("CreateUser admin: %v", err)
	}
	if err := st.UpdateUserRole(ownerCtx, ownerID, admin.ID, SystemRoleAdmin); err != nil {
		t.Fatalf("UpdateUserRole admin: %v", err)
	}
	if _, err := st.UpdateTodo(WithUserID(ctx, admin.ID), todo.ID, creatorPatch(ptrInt64(maintainer.ID)), ModeFull); !errors.Is(err, ErrNotFound) {
		t.Fatalf("system admin creator patch err = %v, want ErrNotFound", err)
	}

	service, err := st.CreateServiceAccount(ownerCtx, ownerID, "Creator service")
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	if err := st.AddProjectMember(ctxM, maintainer.ID, project.ID, service.ID, RoleContributor); err != nil {
		t.Fatalf("AddProjectMember service contributor: %v", err)
	}
	serviceCtx := WithServiceIdentity(WithUserID(ctx, service.ID), service.ID, true)
	if _, err := st.UpdateTodo(serviceCtx, todo.ID, creatorPatch(ptrInt64(contributor.ID)), ModeFull); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("service contributor creator patch err = %v, want ErrUnauthorized", err)
	}
	if err := st.UpdateProjectMemberRole(ctxM, maintainer.ID, project.ID, service.ID, RoleMaintainer); err != nil {
		t.Fatalf("promote service to Maintainer: %v", err)
	}
	got, err := st.UpdateTodo(serviceCtx, todo.ID, creatorPatch(ptrInt64(contributor.ID)), ModeFull)
	if err != nil {
		t.Fatalf("service Maintainer creator patch: %v", err)
	}
	assertTodoCreator(t, got, &contributor.ID)
}

func TestUpdateTodo_CreatorPatchPreservesArchivedTodoProtection(t *testing.T) {
	st, cleanup, ctx, project, maintainer, contributor, _ := setupAssigneeTestProject(t)
	defer cleanup()
	ctxM := WithUserID(ctx, maintainer.ID)
	todo, err := st.CreateTodo(ctxM, project.ID, CreateTodoInput{Title: "archived creator"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	if _, err := st.ArchiveTodoByLocalID(ctxM, project.ID, todo.LocalID, ModeFull); err != nil {
		t.Fatalf("ArchiveTodoByLocalID: %v", err)
	}

	var events []TodoLifecycleEvent
	st.SetTodoLifecyclePublisher(func(_ context.Context, event TodoLifecycleEvent) {
		events = append(events, event)
	})
	if _, err := st.UpdateTodo(ctxM, todo.ID, creatorPatch(ptrInt64(contributor.ID)), ModeFull); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived creator patch err = %v, want ErrConflict", err)
	}
	persisted, err := st.GetTodoByLocalID(ctxM, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	assertTodoCreator(t, persisted, &maintainer.ID)
	if count := creatorAuditCount(t, st, ctx, todo.ID); count != 0 {
		t.Fatalf("archived creator audits = %d, want 0", count)
	}
	if len(events) != 0 {
		t.Fatalf("rejected archived patch published lifecycle events: %+v", events)
	}
}
