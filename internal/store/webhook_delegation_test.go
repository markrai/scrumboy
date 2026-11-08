package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type webhookDelegationFixture struct {
	store       *Store
	owner       User
	maintainer  User
	viewer      User
	contributor User
	service     User
	project     Project
}

func newWebhookDelegationFixture(t *testing.T) *webhookDelegationFixture {
	t.Helper()
	store, cleanup := newTestStore(t)
	t.Cleanup(cleanup)
	ctx := context.Background()
	owner, err := store.BootstrapUser(ctx, "webhook-owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	maintainer, err := store.CreateUser(ctx, "webhook-maintainer@example.com", "password123", "Maintainer")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := store.CreateUser(ctx, "webhook-viewer@example.com", "password123", "Viewer")
	if err != nil {
		t.Fatal(err)
	}
	contributor, err := store.CreateUser(ctx, "webhook-contributor@example.com", "password123", "Contributor")
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(WithUserID(ctx, maintainer.ID), "Webhook Delegation")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddProjectMember(WithUserID(ctx, maintainer.ID), maintainer.ID, project.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if err := store.AddProjectMember(WithUserID(ctx, maintainer.ID), maintainer.ID, project.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateServiceAccount(ctx, owner.ID, "Webhook Bot")
	if err != nil {
		t.Fatal(err)
	}
	return &webhookDelegationFixture{store: store, owner: owner, maintainer: maintainer, viewer: viewer, contributor: contributor, service: service, project: project}
}

func (f *webhookDelegationFixture) create(t *testing.T, actorID int64, ownerID *int64) (Webhook, error) {
	t.Helper()
	return f.store.CreateWebhook(WithUserID(context.Background(), actorID), actorID, CreateWebhookInput{
		ProjectID:          f.project.ID,
		URL:                "https://example.com/webhook",
		Events:             []string{"todo.created"},
		Secret:             stringPointer("test-signing-secret"),
		ServiceOwnerUserID: ownerID,
	})
}

func stringPointer(value string) *string {
	return &value
}

func TestWebhookDelegatedRegistrationAuthorization(t *testing.T) {
	roles := []ProjectRole{RoleViewer, RoleContributor, RoleMaintainer}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			f := newWebhookDelegationFixture(t)
			if err := f.store.AddProjectMember(WithUserID(context.Background(), f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, role); err != nil {
				t.Fatal(err)
			}
			hook, err := f.create(t, f.maintainer.ID, &f.service.ID)
			if err != nil {
				t.Fatalf("delegated create: %v", err)
			}
			if hook.UserID != f.service.ID || !hook.OwnerIsService || hook.RegisteredByUserID == nil || *hook.RegisteredByUserID != f.maintainer.ID {
				t.Fatalf("unsafe or incorrect ownership projection: owner=%d service=%v registrar=%v", hook.UserID, hook.OwnerIsService, hook.RegisteredByUserID)
			}
		})
	}

	t.Run("service maintainer self-registers", func(t *testing.T) {
		f := newWebhookDelegationFixture(t)
		if err := f.store.AddProjectMember(WithUserID(context.Background(), f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleMaintainer); err != nil {
			t.Fatal(err)
		}
		hook, err := f.create(t, f.service.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if hook.UserID != f.service.ID || hook.RegisteredByUserID == nil || *hook.RegisteredByUserID != f.service.ID {
			t.Fatalf("service self-registration projection is incorrect")
		}
	})

	tests := []struct {
		name    string
		prepare func(*testing.T, *webhookDelegationFixture) (int64, *int64)
	}{
		{"human viewer", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) { return f.viewer.ID, nil }},
		{"human contributor", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) { return f.contributor.ID, nil }},
		{"system owner without membership", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) { return f.owner.ID, nil }},
		{"delegate to human", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) { return f.maintainer.ID, &f.viewer.ID }},
		{"delegate to nonmember service", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) { return f.maintainer.ID, &f.service.ID }},
		{"service viewer self-registers", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) {
			if err := f.store.AddProjectMember(WithUserID(context.Background(), f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
				t.Fatal(err)
			}
			return f.service.ID, nil
		}},
		{"service contributor self-registers", func(t *testing.T, f *webhookDelegationFixture) (int64, *int64) {
			if err := f.store.AddProjectMember(WithUserID(context.Background(), f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleContributor); err != nil {
				t.Fatal(err)
			}
			return f.service.ID, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newWebhookDelegationFixture(t)
			actorID, ownerID := test.prepare(t, f)
			if _, err := f.create(t, actorID, ownerID); err == nil {
				t.Fatal("unauthorized registration succeeded")
			}
			var count int
			if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE project_id = ?`, f.project.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed registration left rows: count=%d err=%v", count, err)
			}
		})
	}
}

func TestWebhookLimitIsTransactionalUnderConcurrency(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	const attempts = 30
	errorsByAttempt := make([]error, attempts)
	var wait sync.WaitGroup
	for index := 0; index < attempts; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsByAttempt[index] = f.store.CreateWebhook(WithUserID(context.Background(), f.maintainer.ID), f.maintainer.ID, CreateWebhookInput{
				ProjectID: f.project.ID,
				URL:       fmt.Sprintf("https://example.com/webhook/%d", index),
				Events:    []string{"*"},
			})
		}(index)
	}
	wait.Wait()
	var succeeded int
	for _, err := range errorsByAttempt {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("unexpected concurrent registration error: %v", err)
		}
	}
	if succeeded != maxWebhooksPerProject {
		t.Fatalf("successful registrations=%d want=%d", succeeded, maxWebhooksPerProject)
	}
	var count int
	if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE project_id = ?`, f.project.ID).Scan(&count); err != nil || count != maxWebhooksPerProject {
		t.Fatalf("stored registrations=%d err=%v", count, err)
	}
}

func TestWebhookManagementVisibilityDeletionAndProvenanceLifecycle(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	ctx := context.Background()
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	registrar, err := f.store.CreateUser(ctx, "webhook-registrar@example.com", "password123", "Registrar")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, registrar.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	serviceHook, err := f.store.CreateWebhook(WithUserID(ctx, registrar.ID), registrar.ID, CreateWebhookInput{
		ProjectID: f.project.ID, URL: "https://example.com/service", Events: []string{"*"}, ServiceOwnerUserID: &f.service.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	humanHook, err := f.create(t, registrar.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	items, _, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 20, 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("maintainer list: len=%d err=%v", len(items), err)
	}
	serviceItems, _, err := f.store.ListProjectWebhooks(ctx, f.service.ID, f.project.ID, 20, 0)
	if err != nil || len(serviceItems) != 1 || serviceItems[0].ID != serviceHook.ID {
		t.Fatalf("owner list: items=%v err=%v", serviceItems, err)
	}
	if err := f.store.RemoveProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID); err != nil {
		t.Fatal(err)
	}
	serviceItems, _, err = f.store.ListProjectWebhooks(ctx, f.service.ID, f.project.ID, 20, 0)
	if err != nil || len(serviceItems) != 1 || serviceItems[0].ID != serviceHook.ID {
		t.Fatalf("removed owner list: items=%v err=%v", serviceItems, err)
	}
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.ListProjectWebhooks(ctx, f.owner.ID, f.project.ID, 20, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("system owner without membership list err=%v", err)
	}
	if err := f.store.DeleteWebhook(ctx, f.maintainer.ID, serviceHook.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner-only deletion accepted a project Maintainer: %v", err)
	}
	if err := f.store.DeleteProjectWebhook(ctx, f.maintainer.ID, f.project.ID, humanHook.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("maintainer deleted another human webhook: %v", err)
	}

	if err := f.store.DeleteUser(ctx, f.owner.ID, registrar.ID); err != nil {
		t.Fatalf("delete registrar: %v", err)
	}
	var registeredBy sql.NullInt64
	if err := f.store.db.QueryRow(`SELECT registered_by_user_id FROM webhooks WHERE id = ?`, serviceHook.ID).Scan(&registeredBy); err != nil {
		t.Fatal(err)
	}
	if registeredBy.Valid {
		t.Fatalf("registrar reference was not cleared")
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, serviceHook.ID); err != nil || !eligible {
		t.Fatalf("registrar deletion changed delivery: eligible=%v err=%v", eligible, err)
	}
	if err := f.store.DeleteProjectWebhook(ctx, f.maintainer.ID, f.project.ID, serviceHook.ID); err != nil {
		t.Fatalf("maintainer delete delegated webhook: %v", err)
	}
	if err := f.store.DeleteProjectWebhook(ctx, f.maintainer.ID, f.project.ID, serviceHook.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated deletion err=%v", err)
	}
}

func TestWebhookOwnerDeletionSurvivesMembershipRemoval(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	ctx := context.Background()
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	hook, err := f.create(t, f.maintainer.ID, &f.service.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RemoveProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteWebhook(ctx, f.service.ID, hook.ID); err != nil {
		t.Fatalf("removed owner could not delete its subscription: %v", err)
	}
	if err := f.store.DeleteWebhook(ctx, f.service.ID, hook.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeated owner deletion err=%v", err)
	}
}

func TestWebhookManagementRedactsCrossOwnerDestinations(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	ctx := context.Background()
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	const serviceDestination = "https://userinfo-service:password-service@hooks.example.com/path-service/inbox?token=query-service#fragment-service"
	serviceHook, err := f.store.CreateWebhook(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, CreateWebhookInput{
		ProjectID: f.project.ID, URL: serviceDestination, Events: []string{"todo.created"}, ServiceOwnerUserID: &f.service.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if serviceHook.URL != serviceDestination || serviceHook.DestinationRedacted {
		t.Fatal("immediate creation response did not retain the supplied destination")
	}

	humanOwner, err := f.store.CreateUser(ctx, "webhook-human-owner@example.com", "password123", "Human Owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, humanOwner.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	const humanDestination = "https://userinfo-human:password-human@human-hooks.example.com/path-human/inbox?token=query-human#fragment-human"
	humanHook, err := f.store.CreateWebhook(WithUserID(ctx, humanOwner.ID), humanOwner.ID, CreateWebhookInput{
		ProjectID: f.project.ID, URL: humanDestination, Events: []string{"todo.updated"},
	})
	if err != nil {
		t.Fatal(err)
	}

	maintainerItems, _, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 20, 0)
	if err != nil || len(maintainerItems) != 2 {
		t.Fatalf("maintainer listing failed: count=%d err=%v", len(maintainerItems), err)
	}
	for _, item := range maintainerItems {
		if !item.DestinationRedacted || item.URL != "" || item.DestinationPreview == "" {
			t.Fatal("cross-owner destination was not explicitly redacted")
		}
		for _, credential := range []string{"userinfo-service", "password-service", "path-service", "query-service", "fragment-service", "userinfo-human", "password-human", "path-human", "query-human", "fragment-human"} {
			if strings.Contains(item.DestinationPreview, credential) {
				t.Fatal("cross-owner destination preview exposed caller-supplied credential material")
			}
		}
	}

	serviceItems, _, err := f.store.ListProjectWebhooks(ctx, f.service.ID, f.project.ID, 20, 0)
	if err != nil || len(serviceItems) != 1 || serviceItems[0].ID != serviceHook.ID {
		t.Fatalf("service owner listing failed: count=%d err=%v", len(serviceItems), err)
	}
	if serviceItems[0].DestinationRedacted || serviceItems[0].URL != serviceDestination || serviceItems[0].DestinationPreview != "" {
		t.Fatal("service owner did not retain its complete destination")
	}
	humanItems, _, err := f.store.ListProjectWebhooks(ctx, humanOwner.ID, f.project.ID, 20, 0)
	if err != nil || len(humanItems) != 2 {
		t.Fatalf("human owner listing failed: count=%d err=%v", len(humanItems), err)
	}
	var foundHumanOwner, foundCrossOwner bool
	for _, item := range humanItems {
		switch item.ID {
		case humanHook.ID:
			foundHumanOwner = true
			if item.DestinationRedacted || item.URL != humanDestination || item.DestinationPreview != "" {
				t.Fatal("human owner did not retain its complete destination")
			}
		case serviceHook.ID:
			foundCrossOwner = true
			if !item.DestinationRedacted || item.URL != "" || item.DestinationPreview == "" {
				t.Fatal("human Maintainer received another owner's complete destination")
			}
		}
	}
	if !foundHumanOwner || !foundCrossOwner {
		t.Fatal("human Maintainer management view omitted an expected subscription")
	}
}

func TestWebhookDeliveryEligibilityDistinguishesHumanAndServiceOwners(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	ctx := context.Background()
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
		t.Fatal(err)
	}
	serviceHook, err := f.create(t, f.maintainer.ID, &f.service.ID)
	if err != nil {
		t.Fatal(err)
	}
	humanHook, err := f.create(t, f.maintainer.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, serviceHook.ID); err != nil || !eligible {
		t.Fatalf("service viewer not eligible: %v %v", eligible, err)
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, humanHook.ID); err != nil || !eligible {
		t.Fatalf("human maintainer not eligible: %v %v", eligible, err)
	}
	if err := f.store.UpdateProjectMemberRole(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleContributor); err != nil {
		t.Fatal(err)
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, serviceHook.ID); err != nil || !eligible {
		t.Fatalf("service contributor not eligible: %v %v", eligible, err)
	}
	if err := f.store.RemoveProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID); err != nil {
		t.Fatal(err)
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, serviceHook.ID); err != nil || eligible {
		t.Fatalf("removed service still eligible: %v %v", eligible, err)
	}
	if err := f.store.UpdateProjectMemberRole(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.maintainer.ID, RoleContributor); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected sole-maintainer protection, got %v", err)
	}
	if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.owner.ID, RoleMaintainer); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpdateProjectMemberRole(WithUserID(ctx, f.owner.ID), f.owner.ID, f.project.ID, f.maintainer.ID, RoleContributor); err != nil {
		t.Fatal(err)
	}
	if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, humanHook.ID); err != nil || eligible {
		t.Fatalf("demoted human still eligible: %v %v", eligible, err)
	}
}

func TestWebhookOwnerAndProjectDeletionCascadeDelegatedSubscriptions(t *testing.T) {
	t.Run("service owner", func(t *testing.T) {
		f := newWebhookDelegationFixture(t)
		ctx := context.Background()
		if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
			t.Fatal(err)
		}
		hook, err := f.create(t, f.maintainer.ID, &f.service.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteServiceAccount(ctx, f.owner.ID, f.service.ID); err != nil {
			t.Fatal(err)
		}
		if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, hook.ID); err != nil || eligible {
			t.Fatalf("deleted service owner left an eligible subscription: eligible=%v err=%v", eligible, err)
		}
		var count int
		if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE id = ?`, hook.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deleted service owner left webhook rows: count=%d err=%v", count, err)
		}
	})

	t.Run("project", func(t *testing.T) {
		f := newWebhookDelegationFixture(t)
		ctx := context.Background()
		if err := f.store.AddProjectMember(WithUserID(ctx, f.maintainer.ID), f.maintainer.ID, f.project.ID, f.service.ID, RoleViewer); err != nil {
			t.Fatal(err)
		}
		hook, err := f.create(t, f.maintainer.ID, &f.service.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.DeleteProject(WithUserID(ctx, f.maintainer.ID), f.project.ID, f.maintainer.ID); err != nil {
			t.Fatal(err)
		}
		if _, eligible, err := f.store.GetWebhookDeliveryTarget(ctx, hook.ID); err != nil || eligible {
			t.Fatalf("deleted project left an eligible subscription: eligible=%v err=%v", eligible, err)
		}
		var count int
		if err := f.store.db.QueryRow(`SELECT COUNT(*) FROM webhooks WHERE id = ?`, hook.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deleted project left webhook rows: count=%d err=%v", count, err)
		}
	})
}

func TestListProjectWebhooksPaginationBounds(t *testing.T) {
	f := newWebhookDelegationFixture(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if _, err := f.create(t, f.maintainer.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{0, -2, 101, math.MaxInt} {
		if _, _, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, limit, 0); !errors.Is(err, ErrValidation) {
			t.Fatalf("limit %d err=%v", limit, err)
		}
	}
	if _, _, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 20, -1); !errors.Is(err, ErrValidation) {
		t.Fatalf("beforeID -1 err=%v", err)
	}
	if _, _, err := f.store.ListProjectWebhooks(ctx, 0, f.project.ID, 20, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("actor 0 err=%v", err)
	}
	var beforeID int64
	seen := map[int64]bool{}
	var previous int64
	for round := 0; ; round++ {
		page, next, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 3, beforeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 || len(page) > 3 {
			t.Fatalf("round %d page size = %d", round, len(page))
		}
		for _, hook := range page {
			if seen[hook.ID] {
				t.Fatalf("duplicate webhook %d", hook.ID)
			}
			seen[hook.ID] = true
			if previous != 0 && hook.ID >= previous {
				t.Fatalf("order broke at %d after %d", hook.ID, previous)
			}
			previous = hook.ID
		}
		if next == nil {
			break
		}
		beforeID = *next
		if round > 10 {
			t.Fatal("cursor did not terminate")
		}
	}
	if len(seen) != 7 {
		t.Fatalf("walked %d of 7 webhooks", len(seen))
	}
	exact, exactCursor, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 7, 0)
	if err != nil || len(exact) != 7 || exactCursor != nil {
		t.Fatalf("exact page = %d cursor=%v err=%v", len(exact), exactCursor, err)
	}
	full, fullCursor, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, f.project.ID, 100, 0)
	if err != nil || len(full) != 7 || fullCursor != nil {
		t.Fatalf("limit 100 = %d cursor=%v err=%v", len(full), fullCursor, err)
	}
	quiet, err := f.store.CreateProject(WithUserID(ctx, f.maintainer.ID), "Quiet webhooks")
	if err != nil {
		t.Fatal(err)
	}
	none, noneCursor, err := f.store.ListProjectWebhooks(ctx, f.maintainer.ID, quiet.ID, 20, 0)
	if err != nil || len(none) != 0 || noneCursor != nil {
		t.Fatalf("empty project list = %d cursor=%v err=%v", len(none), noneCursor, err)
	}
}
