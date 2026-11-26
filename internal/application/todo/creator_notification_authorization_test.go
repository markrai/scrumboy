package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type creatorNotificationAuthorizationStoreFake struct {
	facts        store.TodoCreatorNotificationFacts
	found        bool
	err          error
	honorContext bool
	calls        [][3]int64
}

func (f *creatorNotificationAuthorizationStoreFake) GetTodoCreatorNotificationFacts(
	ctx context.Context,
	projectID, todoID, recipientUserID int64,
) (store.TodoCreatorNotificationFacts, bool, error) {
	f.calls = append(f.calls, [3]int64{projectID, todoID, recipientUserID})
	if f.honorContext && ctx.Err() != nil {
		return store.TodoCreatorNotificationFacts{}, false, ctx.Err()
	}
	return f.facts, f.found, f.err
}

func creatorAuthorizationFacts(role store.ProjectRole, creatorID int64) store.TodoCreatorNotificationFacts {
	return store.TodoCreatorNotificationFacts{
		ProjectID:       7,
		ProjectName:     "Roadmap",
		ProjectSlug:     "current-slug",
		DurableProject:  true,
		RecipientRole:   role,
		TodoID:          81,
		TodoProjectID:   7,
		TodoLocalID:     5,
		CreatedByUserID: &creatorID,
	}
}

func creatorAuthorizationAccess(role store.ProjectRole) *creatorNotificationAuthorizationStoreFake {
	return &creatorNotificationAuthorizationStoreFake{
		facts: creatorAuthorizationFacts(role, 11),
		found: true,
	}
}

func creatorAuthorizationRequest() CreatorNotificationRequest {
	return CreatorNotificationRequest{
		ProjectID:       7,
		ProjectSlug:     "mutation-time-slug",
		TodoID:          81,
		LocalID:         5,
		Title:           "Committed title",
		ActivityReason:  RefreshReasonTodoUpdated,
		CreatedByUserID: 11,
		ActorUserID:     22,
	}
}

func authorizedCreatorNotification() AuthorizedCreatorNotification {
	return AuthorizedCreatorNotification{
		ProjectID:       7,
		ProjectSlug:     "authorization-time-slug",
		TodoID:          81,
		LocalID:         5,
		Title:           "Committed title",
		ActivityReason:  RefreshReasonTodoUpdated,
		RecipientUserID: 11,
		ActorUserID:     22,
	}
}

func TestCreatorNotificationAuthorizationUsesCurrentTodoCreatorAndAccess(t *testing.T) {
	for _, role := range []store.ProjectRole{store.RoleViewer, store.RoleContributor, store.RoleMaintainer} {
		t.Run(role.String(), func(t *testing.T) {
			access := creatorAuthorizationAccess(role)
			got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(context.Background(), creatorAuthorizationRequest())
			if err != nil || !ok {
				t.Fatalf("Authorize = (%+v, %v, %v), want authorized", got, ok, err)
			}
			want := AuthorizedCreatorNotification{
				ProjectID: 7, ProjectSlug: "current-slug", ProjectName: "Roadmap", TodoID: 81, LocalID: 5,
				Title: "Committed title", ActivityReason: RefreshReasonTodoUpdated,
				RecipientUserID: 11, ActorUserID: 22,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("authorized = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(access.calls, [][3]int64{{7, 81, 11}}) {
				t.Fatalf("fact calls = %v", access.calls)
			}
		})
	}
}

func TestCreatorNotificationAuthorizationPreservesMoveTransitionSnapshot(t *testing.T) {
	access := creatorAuthorizationAccess(store.RoleViewer)
	request := creatorAuthorizationRequest()
	request.ActivityReason = RefreshReasonTodoMoved
	request.FromName = "Testing"
	request.ToName = "Done"
	service := NewCreatorNotificationAuthorizationService(access)

	authorized, ok, err := service.Authorize(context.Background(), request)
	if err != nil || !ok {
		t.Fatalf("Authorize = (%+v, %v, %v), want authorized", authorized, ok, err)
	}
	if authorized.FromName != "Testing" || authorized.ToName != "Done" {
		t.Fatalf("authorized move transition = %q -> %q", authorized.FromName, authorized.ToName)
	}

	reauthorized, ok, err := service.ReauthorizeRecipient(context.Background(), authorized)
	if err != nil || !ok {
		t.Fatalf("ReauthorizeRecipient = (%+v, %v, %v), want authorized", reauthorized, ok, err)
	}
	if reauthorized.FromName != "Testing" || reauthorized.ToName != "Done" {
		t.Fatalf("reauthorized move transition = %q -> %q", reauthorized.FromName, reauthorized.ToName)
	}
}

func TestCreatorNotificationAuthorizationFailsClosed(t *testing.T) {
	lookupErr := errors.New("lookup failed")
	tests := []struct {
		name          string
		mutateRequest func(*CreatorNotificationRequest)
		mutateFacts   func(*store.TodoCreatorNotificationFacts)
		access        *creatorNotificationAuthorizationStoreFake
		wantErr       error
		wantRead      bool
	}{
		{name: "nil store"},
		{name: "nonpositive project id", mutateRequest: func(r *CreatorNotificationRequest) { r.ProjectID = 0 }},
		{name: "nonpositive todo id", mutateRequest: func(r *CreatorNotificationRequest) { r.TodoID = 0 }},
		{name: "nonpositive local id", mutateRequest: func(r *CreatorNotificationRequest) { r.LocalID = 0 }},
		{name: "nonpositive creator", mutateRequest: func(r *CreatorNotificationRequest) { r.CreatedByUserID = 0 }},
		{name: "nonpositive actor", mutateRequest: func(r *CreatorNotificationRequest) { r.ActorUserID = 0 }},
		{name: "self request", mutateRequest: func(r *CreatorNotificationRequest) { r.ActorUserID = r.CreatedByUserID }},
		{name: "unknown activity", mutateRequest: func(r *CreatorNotificationRequest) { r.ActivityReason = "unknown" }},
		{name: "lookup failure", access: &creatorNotificationAuthorizationStoreFake{err: lookupErr}, wantErr: lookupErr, wantRead: true},
		{name: "project or todo missing", access: &creatorNotificationAuthorizationStoreFake{}, wantRead: true},
		{name: "project identity mismatch", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.ProjectID = 8 }, wantRead: true},
		{name: "temporary board", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.DurableProject = false }, wantRead: true},
		{name: "missing current slug", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.ProjectSlug = "" }, wantRead: true},
		{name: "todo identity mismatch", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.TodoID = 82 }, wantRead: true},
		{name: "todo project mismatch", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.TodoProjectID = 8 }, wantRead: true},
		{name: "todo local id mismatch", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.TodoLocalID = 6 }, wantRead: true},
		{name: "creator is null", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.CreatedByUserID = nil }, wantRead: true},
		{name: "creator changed", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { id := int64(33); f.CreatedByUserID = &id }, wantRead: true},
		{name: "removed membership", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.RecipientRole = "" }, wantRead: true},
		{name: "malformed membership", mutateFacts: func(f *store.TodoCreatorNotificationFacts) { f.RecipientRole = store.ProjectRole("unexpected") }, wantRead: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := creatorAuthorizationRequest()
			if tt.mutateRequest != nil {
				tt.mutateRequest(&request)
			}
			access := tt.access
			if access == nil && (tt.mutateFacts != nil || tt.wantRead) {
				access = creatorAuthorizationAccess(store.RoleViewer)
			}
			if tt.mutateFacts != nil {
				tt.mutateFacts(&access.facts)
			}
			var service *CreatorNotificationAuthorizationService
			if access == nil {
				service = NewCreatorNotificationAuthorizationService(nil)
			} else {
				service = NewCreatorNotificationAuthorizationService(access)
			}
			got, ok, err := service.Authorize(context.Background(), request)
			if ok || got != (AuthorizedCreatorNotification{}) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authorize = (%+v, %v, %v), want zero/false/%v", got, ok, err, tt.wantErr)
			}
			if access != nil && (len(access.calls) > 0) != tt.wantRead {
				t.Fatalf("fact reads=%d, want read=%v", len(access.calls), tt.wantRead)
			}
		})
	}
}

func TestCreatorNotificationAuthorizationRejectsStaleCreatorWithoutRedirect(t *testing.T) {
	access := creatorAuthorizationAccess(store.RoleViewer)
	service := NewCreatorNotificationAuthorizationService(access)
	authorized, ok, err := service.Authorize(context.Background(), creatorAuthorizationRequest())
	if err != nil || !ok {
		t.Fatalf("initial Authorize = (%+v, %v, %v)", authorized, ok, err)
	}

	newCreatorID := int64(33)
	access.facts.CreatedByUserID = &newCreatorID
	recipientAccess, accessOK, err := service.ReauthorizeRecipientAccess(context.Background(), authorized)
	if err != nil || !accessOK || recipientAccess.CurrentCreator || recipientAccess.Notification.RecipientUserID != 11 {
		t.Fatalf("stale recipient access = (%+v, %v, %v), want current member but not creator", recipientAccess, accessOK, err)
	}
	got, ok, err := service.ReauthorizeRecipient(context.Background(), authorized)
	if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
		t.Fatalf("stale ReauthorizeRecipient = (%+v, %v, %v), want discarded", got, ok, err)
	}

	staleRequest := creatorAuthorizationRequest()
	got, ok, err = service.Authorize(context.Background(), staleRequest)
	if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
		t.Fatalf("stale Authorize = (%+v, %v, %v), want discarded", got, ok, err)
	}

	access.facts.RecipientRole = store.RoleContributor
	newRequest := creatorAuthorizationRequest()
	newRequest.CreatedByUserID = newCreatorID
	got, ok, err = service.Authorize(context.Background(), newRequest)
	if err != nil || !ok || got.RecipientUserID != newCreatorID {
		t.Fatalf("current creator Authorize = (%+v, %v, %v), want creator %d", got, ok, err, newCreatorID)
	}
}

func TestCreatorNotificationAuthorizationCancelledLookupFailsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	access := creatorAuthorizationAccess(store.RoleViewer)
	access.honorContext = true
	got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(ctx, creatorAuthorizationRequest())
	if ok || got != (AuthorizedCreatorNotification{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize cancelled context = (%+v, %v, %v), want fail-closed cancellation", got, ok, err)
	}
}

func TestCreatorNotificationDeliveryReauthorizationUsesFreshCurrentFacts(t *testing.T) {
	access := creatorAuthorizationAccess(store.RoleViewer)
	access.facts.ProjectSlug = "delivery-time-slug"
	got, ok, err := NewCreatorNotificationAuthorizationService(access).ReauthorizeRecipient(
		context.Background(),
		authorizedCreatorNotification(),
	)
	if err != nil || !ok {
		t.Fatalf("ReauthorizeRecipient = (%+v, %v, %v), want authorized", got, ok, err)
	}
	want := authorizedCreatorNotification()
	want.ProjectSlug = "delivery-time-slug"
	want.ProjectName = "Roadmap"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reauthorized = %+v, want %+v", got, want)
	}
}

func TestCreatorNotificationDeliveryReauthorizationUsesIndependentRead(t *testing.T) {
	access := creatorAuthorizationAccess(store.RoleViewer)
	service := NewCreatorNotificationAuthorizationService(access)
	authorized, ok, err := service.Authorize(context.Background(), creatorAuthorizationRequest())
	if err != nil || !ok {
		t.Fatalf("Authorize = (%+v, %v, %v), want authorized", authorized, ok, err)
	}

	access.facts.RecipientRole = ""
	got, ok, err := service.ReauthorizeRecipient(context.Background(), authorized)
	if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
		t.Fatalf("ReauthorizeRecipient = (%+v, %v, %v), want denied", got, ok, err)
	}
	if len(access.calls) != 2 {
		t.Fatalf("fact reads=%d, want independent authorization and delivery reads", len(access.calls))
	}
}
