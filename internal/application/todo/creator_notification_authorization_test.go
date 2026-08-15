package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type creatorNotificationAuthorizationStoreFake struct {
	project          store.Project
	projectErr       error
	role             store.ProjectRole
	roleErr          error
	honorContext     bool
	projectCalls     []int64
	roleCalls        [][2]int64
	trace            []string
	afterProjectRead func()
}

func (f *creatorNotificationAuthorizationStoreFake) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	f.projectCalls = append(f.projectCalls, projectID)
	f.trace = append(f.trace, "project")
	if f.honorContext && ctx.Err() != nil {
		return store.Project{}, ctx.Err()
	}
	project, err := f.project, f.projectErr
	if f.afterProjectRead != nil {
		f.afterProjectRead()
	}
	return project, err
}

func (f *creatorNotificationAuthorizationStoreFake) GetProjectRole(_ context.Context, projectID int64, userID int64) (store.ProjectRole, error) {
	f.roleCalls = append(f.roleCalls, [2]int64{projectID, userID})
	f.trace = append(f.trace, "role")
	return f.role, f.roleErr
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

func TestCreatorNotificationAuthorizationUsesFreshCurrentProjectAccess(t *testing.T) {
	for _, role := range []store.ProjectRole{store.RoleViewer, store.RoleContributor, store.RoleMaintainer} {
		t.Run(role.String(), func(t *testing.T) {
			access := &creatorNotificationAuthorizationStoreFake{
				project: store.Project{ID: 7, Slug: "current-slug"},
				role:    role,
			}
			got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(context.Background(), creatorAuthorizationRequest())
			if err != nil || !ok {
				t.Fatalf("Authorize = (%+v, %v, %v), want authorized", got, ok, err)
			}
			want := AuthorizedCreatorNotification{
				ProjectID: 7, ProjectSlug: "current-slug", TodoID: 81, LocalID: 5,
				Title: "Committed title", ActivityReason: RefreshReasonTodoUpdated,
				RecipientUserID: 11, ActorUserID: 22,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("authorized = %+v, want %+v", got, want)
			}
			if !reflect.DeepEqual(access.trace, []string{"project", "role"}) ||
				!reflect.DeepEqual(access.projectCalls, []int64{7}) ||
				!reflect.DeepEqual(access.roleCalls, [][2]int64{{7, 11}}) {
				t.Fatalf("access trace=%v projectCalls=%v roleCalls=%v", access.trace, access.projectCalls, access.roleCalls)
			}
		})
	}
}

func TestCreatorNotificationAuthorizationFailsClosed(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	lookupErr := errors.New("lookup failed")
	tests := []struct {
		name            string
		mutateRequest   func(*CreatorNotificationRequest)
		access          *creatorNotificationAuthorizationStoreFake
		wantErr         error
		wantProjectRead bool
		wantRoleRead    bool
	}{
		{name: "nil store"},
		{name: "nonpositive project id", mutateRequest: func(r *CreatorNotificationRequest) { r.ProjectID = 0 }},
		{name: "nonpositive todo id", mutateRequest: func(r *CreatorNotificationRequest) { r.TodoID = 0 }},
		{name: "nonpositive local id", mutateRequest: func(r *CreatorNotificationRequest) { r.LocalID = 0 }},
		{name: "nonpositive creator", mutateRequest: func(r *CreatorNotificationRequest) { r.CreatedByUserID = 0 }},
		{name: "nonpositive actor", mutateRequest: func(r *CreatorNotificationRequest) { r.ActorUserID = 0 }},
		{name: "self request", mutateRequest: func(r *CreatorNotificationRequest) { r.ActorUserID = r.CreatedByUserID }},
		{name: "unknown activity", mutateRequest: func(r *CreatorNotificationRequest) { r.ActivityReason = "unknown" }},
		{
			name:    "project deleted or unavailable",
			access:  &creatorNotificationAuthorizationStoreFake{projectErr: lookupErr},
			wantErr: lookupErr, wantProjectRead: true,
		},
		{
			name:            "project identity mismatch",
			access:          &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 8, Slug: "other"}, role: store.RoleViewer},
			wantProjectRead: true,
		},
		{
			name:            "temporary board",
			access:          &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "temporary", ExpiresAt: &expires}, role: store.RoleViewer},
			wantProjectRead: true,
		},
		{
			name:            "missing current slug",
			access:          &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7}, role: store.RoleViewer},
			wantProjectRead: true,
		},
		{
			name:            "removed nonexistent or deleted creator",
			access:          &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}},
			wantProjectRead: true, wantRoleRead: true,
		},
		{
			name:            "malformed membership role",
			access:          &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.ProjectRole("unexpected")},
			wantProjectRead: true, wantRoleRead: true,
		},
		{
			name:    "membership lookup error",
			access:  &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}, roleErr: lookupErr},
			wantErr: lookupErr, wantProjectRead: true, wantRoleRead: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := creatorAuthorizationRequest()
			if tt.mutateRequest != nil {
				tt.mutateRequest(&request)
			}
			var service *CreatorNotificationAuthorizationService
			if tt.access == nil {
				service = NewCreatorNotificationAuthorizationService(nil)
			} else {
				service = NewCreatorNotificationAuthorizationService(tt.access)
			}
			got, ok, err := service.Authorize(context.Background(), request)
			if ok || got != (AuthorizedCreatorNotification{}) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authorize = (%+v, %v, %v), want zero/false/%v", got, ok, err, tt.wantErr)
			}
			if tt.access == nil {
				return
			}
			if (len(tt.access.projectCalls) > 0) != tt.wantProjectRead || (len(tt.access.roleCalls) > 0) != tt.wantRoleRead {
				t.Fatalf("projectReads=%d roleReads=%d, want project=%v role=%v", len(tt.access.projectCalls), len(tt.access.roleCalls), tt.wantProjectRead, tt.wantRoleRead)
			}
		})
	}
}

func TestCreatorNotificationAuthorizationUsesMembershipAtConsumptionTime(t *testing.T) {
	t.Run("membership added after mutation but before authorization allows current member", func(t *testing.T) {
		access := &creatorNotificationAuthorizationStoreFake{
			project: store.Project{ID: 7, Slug: "durable"},
			role:    store.RoleViewer,
		}
		_, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(context.Background(), creatorAuthorizationRequest())
		if err != nil || !ok {
			t.Fatalf("Authorize current member = (%v, %v), want true", ok, err)
		}
	})

	t.Run("membership removed before authorization denies", func(t *testing.T) {
		access := &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}}
		got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(context.Background(), creatorAuthorizationRequest())
		if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
			t.Fatalf("Authorize removed member = (%+v, %v, %v), want denied", got, ok, err)
		}
	})

	t.Run("membership removed between project and role reads denies", func(t *testing.T) {
		access := &creatorNotificationAuthorizationStoreFake{
			project: store.Project{ID: 7, Slug: "durable"},
			role:    store.RoleViewer,
		}
		access.afterProjectRead = func() { access.role = "" }
		got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(context.Background(), creatorAuthorizationRequest())
		if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
			t.Fatalf("Authorize concurrently removed member = (%+v, %v, %v), want denied", got, ok, err)
		}
	})
}

func TestCreatorNotificationAuthorizationCancelledLookupFailsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	access := &creatorNotificationAuthorizationStoreFake{
		project:      store.Project{ID: 7, Slug: "durable"},
		role:         store.RoleViewer,
		honorContext: true,
	}
	got, ok, err := NewCreatorNotificationAuthorizationService(access).Authorize(ctx, creatorAuthorizationRequest())
	if ok || got != (AuthorizedCreatorNotification{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Authorize cancelled context = (%+v, %v, %v), want fail-closed cancellation", got, ok, err)
	}
	if len(access.projectCalls) != 1 || len(access.roleCalls) != 0 {
		t.Fatalf("cancelled access projectReads=%d roleReads=%d, want 1/0", len(access.projectCalls), len(access.roleCalls))
	}
}

func TestCreatorNotificationDeliveryReauthorizationUsesFreshCurrentAccess(t *testing.T) {
	access := &creatorNotificationAuthorizationStoreFake{
		project: store.Project{ID: 7, Slug: "delivery-time-slug"},
		role:    store.RoleViewer,
	}
	got, ok, err := NewCreatorNotificationAuthorizationService(access).ReauthorizeRecipient(
		context.Background(),
		authorizedCreatorNotification(),
	)
	if err != nil || !ok {
		t.Fatalf("ReauthorizeRecipient = (%+v, %v, %v), want authorized", got, ok, err)
	}
	want := authorizedCreatorNotification()
	want.ProjectSlug = "delivery-time-slug"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reauthorized = %+v, want %+v", got, want)
	}
}

func TestCreatorNotificationDeliveryReauthorizationFailsClosed(t *testing.T) {
	expires := time.Now().UTC().Add(time.Hour)
	lookupErr := errors.New("lookup failed")
	tests := []struct {
		name          string
		mutate        func(*AuthorizedCreatorNotification)
		access        *creatorNotificationAuthorizationStoreFake
		wantErr       error
		wantRoleRead  bool
		cancelContext bool
	}{
		{name: "nil store"},
		{name: "invalid project", mutate: func(a *AuthorizedCreatorNotification) { a.ProjectID = 0 }},
		{name: "invalid todo", mutate: func(a *AuthorizedCreatorNotification) { a.TodoID = 0 }},
		{name: "invalid local id", mutate: func(a *AuthorizedCreatorNotification) { a.LocalID = 0 }},
		{name: "invalid recipient", mutate: func(a *AuthorizedCreatorNotification) { a.RecipientUserID = 0 }},
		{name: "invalid actor", mutate: func(a *AuthorizedCreatorNotification) { a.ActorUserID = 0 }},
		{name: "self recipient", mutate: func(a *AuthorizedCreatorNotification) { a.ActorUserID = a.RecipientUserID }},
		{name: "unknown activity", mutate: func(a *AuthorizedCreatorNotification) { a.ActivityReason = "unknown" }},
		{name: "project unavailable", access: &creatorNotificationAuthorizationStoreFake{projectErr: lookupErr}, wantErr: lookupErr},
		{name: "project identity mismatch", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 8, Slug: "other"}, role: store.RoleViewer}},
		{name: "temporary project", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "temporary", ExpiresAt: &expires}, role: store.RoleViewer}},
		{name: "missing project slug", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7}, role: store.RoleViewer}},
		{name: "removed deleted or nonexistent recipient", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}}, wantRoleRead: true},
		{name: "membership lookup failure", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}, roleErr: lookupErr}, wantErr: lookupErr, wantRoleRead: true},
		{name: "cancelled lookup", access: &creatorNotificationAuthorizationStoreFake{project: store.Project{ID: 7, Slug: "durable"}, role: store.RoleViewer, honorContext: true}, wantErr: context.Canceled, cancelContext: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authorized := authorizedCreatorNotification()
			if tt.mutate != nil {
				tt.mutate(&authorized)
			}
			var service *CreatorNotificationAuthorizationService
			if tt.access == nil {
				service = NewCreatorNotificationAuthorizationService(nil)
			} else {
				service = NewCreatorNotificationAuthorizationService(tt.access)
			}
			ctx := context.Background()
			if tt.cancelContext {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			got, ok, err := service.ReauthorizeRecipient(ctx, authorized)
			if ok || got != (AuthorizedCreatorNotification{}) || !errors.Is(err, tt.wantErr) {
				t.Fatalf("ReauthorizeRecipient = (%+v, %v, %v), want zero/false/%v", got, ok, err, tt.wantErr)
			}
			if tt.access != nil && (len(tt.access.roleCalls) > 0) != tt.wantRoleRead {
				t.Fatalf("roleReads=%d, want roleRead=%v", len(tt.access.roleCalls), tt.wantRoleRead)
			}
		})
	}
}

func TestCreatorNotificationDeliveryReauthorizationDeniesRemovalAfterPhaseThree(t *testing.T) {
	access := &creatorNotificationAuthorizationStoreFake{
		project: store.Project{ID: 7, Slug: "durable"},
		role:    store.RoleViewer,
	}
	service := NewCreatorNotificationAuthorizationService(access)
	authorized, ok, err := service.Authorize(context.Background(), creatorAuthorizationRequest())
	if err != nil || !ok {
		t.Fatalf("Phase 3 Authorize = (%+v, %v, %v), want authorized", authorized, ok, err)
	}

	access.role = ""
	got, ok, err := service.ReauthorizeRecipient(context.Background(), authorized)
	if err != nil || ok || got != (AuthorizedCreatorNotification{}) {
		t.Fatalf("delivery ReauthorizeRecipient = (%+v, %v, %v), want denied", got, ok, err)
	}
	if !reflect.DeepEqual(access.trace, []string{"project", "role", "project", "role"}) {
		t.Fatalf("access trace=%v, want independent Phase 3 and delivery-time reads", access.trace)
	}
}
