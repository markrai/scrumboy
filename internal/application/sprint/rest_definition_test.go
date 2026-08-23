package sprint

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type restDefinitionContextKey struct{}

type restDefinitionRoleCall struct {
	ctx       context.Context
	projectID int64
	userID    int64
}

type restDefinitionCreateCall struct {
	ctx            context.Context
	projectID      int64
	name           string
	plannedStartAt time.Time
	plannedEndAt   time.Time
}

type restDefinitionUpdateCall struct {
	ctx      context.Context
	sprintID int64
	input    store.UpdateSprintInput
}

type restDefinitionPublishCall struct {
	kind      string
	ctx       context.Context
	projectID int64
	name      string
}

type restDefinitionFake struct {
	trace []string

	role      store.ProjectRole
	roleErr   error
	roleCalls []restDefinitionRoleCall

	createResult          store.Sprint
	createErr             error
	useCreateContextError bool
	createCalls           []restDefinitionCreateCall

	updateErr             error
	useUpdateContextError bool
	updateCalls           []restDefinitionUpdateCall

	publishCalls []restDefinitionPublishCall
}

var _ RoleStore = (*restDefinitionFake)(nil)
var _ DefinitionStore = (*restDefinitionFake)(nil)
var _ RESTDefinitionPublisher = (*restDefinitionFake)(nil)

func (f *restDefinitionFake) GetProjectRole(
	ctx context.Context,
	projectID int64,
	userID int64,
) (store.ProjectRole, error) {
	f.trace = append(f.trace, "role")
	f.roleCalls = append(f.roleCalls, restDefinitionRoleCall{
		ctx:       ctx,
		projectID: projectID,
		userID:    userID,
	})
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *restDefinitionFake) CreateSprint(
	ctx context.Context,
	projectID int64,
	name string,
	plannedStartAt time.Time,
	plannedEndAt time.Time,
) (store.Sprint, error) {
	f.trace = append(f.trace, "create")
	f.createCalls = append(f.createCalls, restDefinitionCreateCall{
		ctx:            ctx,
		projectID:      projectID,
		name:           name,
		plannedStartAt: plannedStartAt,
		plannedEndAt:   plannedEndAt,
	})
	if f.useCreateContextError && ctx.Err() != nil {
		return store.Sprint{}, ctx.Err()
	}
	return f.createResult, f.createErr
}

func (f *restDefinitionFake) UpdateSprint(
	ctx context.Context,
	sprintID int64,
	input store.UpdateSprintInput,
) error {
	f.trace = append(f.trace, "update")
	f.updateCalls = append(f.updateCalls, restDefinitionUpdateCall{
		ctx:      ctx,
		sprintID: sprintID,
		input:    input,
	})
	if f.useUpdateContextError && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.updateErr
}

func (f *restDefinitionFake) PublishSprintCreated(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish-created")
	f.publishCalls = append(f.publishCalls, restDefinitionPublishCall{
		kind:      "created",
		ctx:       ctx,
		projectID: projectID,
		name:      name,
	})
}

func (f *restDefinitionFake) PublishSprintUpdated(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish-updated")
	f.publishCalls = append(f.publishCalls, restDefinitionPublishCall{
		kind:      "updated",
		ctx:       ctx,
		projectID: projectID,
		name:      name,
	})
}

func newRESTDefinitionService(fake *restDefinitionFake) *RESTDefinitionService {
	return NewRESTDefinitionService(RESTDefinitionServiceDependencies{
		Roles:       fake,
		Definitions: fake,
		Publisher:   fake,
	})
}

func restDefinitionActorContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), restDefinitionContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func TestRESTDefinitionPrepareAuthorization(t *testing.T) {
	configuredRoleErr := errors.New("configured private role failure")
	operations := []struct {
		name    string
		prepare func(*RESTDefinitionService, context.Context) (bool, error)
	}{
		{
			name: "create",
			prepare: func(service *RESTDefinitionService, ctx context.Context) (bool, error) {
				prepared, err := service.PrepareCreate(ctx, ResolvedRESTProjectTarget{ProjectID: 41})
				return prepared != nil, err
			},
		},
		{
			name: "update",
			prepare: func(service *RESTDefinitionService, ctx context.Context) (bool, error) {
				prepared, err := service.PrepareUpdate(ctx, ResolvedRESTSprintTarget{
					ProjectID: 41,
					SprintID:  907,
				})
				return prepared != nil, err
			},
		},
	}
	tests := []struct {
		name          string
		ctx           context.Context
		role          store.ProjectRole
		roleErr       error
		wantPrepared  bool
		wantErr       error
		wantRoleCalls int
	}{
		{
			name:          "missing actor",
			ctx:           context.WithValue(context.Background(), restDefinitionContextKey{}, "missing"),
			wantErr:       ErrActorRequired,
			wantRoleCalls: 0,
		},
		{
			name:          "owner",
			ctx:           restDefinitionActorContext(73, "owner"),
			role:          store.RoleOwner,
			wantPrepared:  true,
			wantRoleCalls: 1,
		},
		{
			name:          "maintainer",
			ctx:           restDefinitionActorContext(73, "maintainer"),
			role:          store.RoleMaintainer,
			wantPrepared:  true,
			wantRoleCalls: 1,
		},
		{
			name:          "contributor",
			ctx:           restDefinitionActorContext(73, "contributor"),
			role:          store.RoleContributor,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "viewer",
			ctx:           restDefinitionActorContext(73, "viewer"),
			role:          store.RoleViewer,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "non-member",
			ctx:           restDefinitionActorContext(73, "non-member"),
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "role lookup failure discards cause",
			ctx:           restDefinitionActorContext(73, "role-error"),
			roleErr:       configuredRoleErr,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
		{
			name:          "role cancellation discards cause",
			ctx:           restDefinitionActorContext(73, "role-cancellation"),
			roleErr:       context.Canceled,
			wantErr:       ErrMaintainerRequired,
			wantRoleCalls: 1,
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					fake := &restDefinitionFake{role: tt.role, roleErr: tt.roleErr}
					prepared, err := operation.prepare(newRESTDefinitionService(fake), tt.ctx)

					if prepared != tt.wantPrepared {
						t.Fatalf("prepared = %v, want %v", prepared, tt.wantPrepared)
					}
					if err != tt.wantErr {
						t.Fatalf("error = %v, want exact %v", err, tt.wantErr)
					}
					if tt.roleErr != nil && errors.Is(err, tt.roleErr) {
						t.Fatalf("error %v unexpectedly preserves discarded role cause %v", err, tt.roleErr)
					}
					if len(fake.roleCalls) != tt.wantRoleCalls {
						t.Fatalf("role calls = %d, want %d", len(fake.roleCalls), tt.wantRoleCalls)
					}
					if tt.wantRoleCalls == 1 {
						call := fake.roleCalls[0]
						if call.ctx != tt.ctx || call.ctx.Value(restDefinitionContextKey{}) != tt.ctx.Value(restDefinitionContextKey{}) {
							t.Fatal("role lookup did not receive the exact marked context")
						}
						if call.projectID != 41 || call.userID != 73 {
							t.Fatalf("role args = project %d user %d, want project 41 user 73", call.projectID, call.userID)
						}
					}
					if len(fake.createCalls) != 0 || len(fake.updateCalls) != 0 || len(fake.publishCalls) != 0 {
						t.Fatalf("preparation performed later work: create=%d update=%d publish=%d", len(fake.createCalls), len(fake.updateCalls), len(fake.publishCalls))
					}
					var wantTrace []string
					if tt.wantRoleCalls == 1 {
						wantTrace = []string{"role"}
					}
					if !reflect.DeepEqual(fake.trace, wantTrace) {
						t.Fatalf("trace = %v, want %v", fake.trace, wantTrace)
					}
				})
			}
		})
	}
}

func TestRESTDefinitionPrepareBindsDistinctTargetsByValue(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		ctx := restDefinitionActorContext(73, "create-copy")
		target := ResolvedRESTProjectTarget{ProjectID: 41}
		prepared, err := newRESTDefinitionService(fake).PrepareCreate(ctx, target)
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}
		target.ProjectID = 999

		if _, err := prepared.Create(CreateCommand{Name: "copy"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got := fake.createCalls[0].projectID; got != 41 {
			t.Fatalf("create project = %d, want bound 41", got)
		}
		if got := fake.publishCalls[0].projectID; got != 41 {
			t.Fatalf("publish project = %d, want bound 41", got)
		}
	})

	t.Run("update", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		ctx := restDefinitionActorContext(73, "update-copy")
		target := ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907}
		prepared, err := newRESTDefinitionService(fake).PrepareUpdate(ctx, target)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		target.ProjectID = 999
		target.SprintID = 888

		if err := prepared.Update(UpdateCommand{}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := fake.updateCalls[0].sprintID; got != 907 {
			t.Fatalf("update sprint = %d, want bound stored ID 907", got)
		}
		if got := fake.publishCalls[0].projectID; got != 41 {
			t.Fatalf("publish project = %d, want bound 41", got)
		}
	})
}

func TestPreparedRESTCreateDelegatesAndPublishes(t *testing.T) {
	start := time.Date(2026, time.September, 1, 2, 3, 4, 5, time.UTC)
	end := start.Add(14 * 24 * time.Hour)
	wantSprint := store.Sprint{
		ID:             907,
		ProjectID:      812,
		Number:         12,
		Name:           "Returned sprint",
		PlannedStartAt: start,
		PlannedEndAt:   end,
		State:          "PLANNED",
	}
	fake := &restDefinitionFake{
		role:         store.RoleMaintainer,
		createResult: wantSprint,
	}
	ctx := restDefinitionActorContext(73, "create-success")
	prepared, err := newRESTDefinitionService(fake).PrepareCreate(ctx, ResolvedRESTProjectTarget{ProjectID: 41})
	if err != nil {
		t.Fatalf("PrepareCreate: %v", err)
	}

	gotSprint, err := prepared.Create(CreateCommand{
		Name:           "Input sprint",
		PlannedStartAt: start,
		PlannedEndAt:   end,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !reflect.DeepEqual(gotSprint, wantSprint) {
		t.Fatalf("sprint = %#v, want exact %#v", gotSprint, wantSprint)
	}
	if !reflect.DeepEqual(fake.trace, []string{"role", "create", "publish-created"}) {
		t.Fatalf("trace = %v", fake.trace)
	}
	if len(fake.roleCalls) != 1 || len(fake.createCalls) != 1 || len(fake.updateCalls) != 0 || len(fake.publishCalls) != 1 {
		t.Fatalf("calls: role=%d create=%d update=%d publish=%d", len(fake.roleCalls), len(fake.createCalls), len(fake.updateCalls), len(fake.publishCalls))
	}
	createCall := fake.createCalls[0]
	if createCall.ctx != ctx || createCall.ctx.Value(restDefinitionContextKey{}) != "create-success" {
		t.Fatal("create did not receive exact bound context")
	}
	if createCall.projectID != 41 || createCall.name != "Input sprint" || !createCall.plannedStartAt.Equal(start) || !createCall.plannedEndAt.Equal(end) {
		t.Fatalf("create args = %#v", createCall)
	}
	publishCall := fake.publishCalls[0]
	if publishCall.kind != "created" || publishCall.ctx != ctx || publishCall.projectID != 41 || publishCall.name != "Returned sprint" {
		t.Fatalf("publish = %#v, want created with bound context/project and create-result name", publishCall)
	}
}

func TestPreparedRESTUpdateDelegatesAndPublishes(t *testing.T) {
	start := time.Date(2026, time.October, 2, 3, 4, 5, 6, time.UTC)
	end := start.Add(21 * 24 * time.Hour)
	emptyName := ""
	zeroTime := time.Time{}
	tests := []struct {
		name    string
		command UpdateCommand
		assert  func(*testing.T, store.UpdateSprintInput)
	}{
		{
			name: "all fields",
			command: UpdateCommand{
				Name:           stringPointer("Renamed"),
				PlannedStartAt: timePointer(start),
				PlannedEndAt:   timePointer(end),
			},
			assert: func(t *testing.T, input store.UpdateSprintInput) {
				t.Helper()
				if input.Name == nil || *input.Name != "Renamed" || input.PlannedStartAt == nil || !input.PlannedStartAt.Equal(start) || input.PlannedEndAt == nil || !input.PlannedEndAt.Equal(end) {
					t.Fatalf("materialized input = %#v", input)
				}
			},
		},
		{
			name:    "all fields omitted still writes",
			command: UpdateCommand{},
			assert: func(t *testing.T, input store.UpdateSprintInput) {
				t.Helper()
				if input.Name != nil || input.PlannedStartAt != nil || input.PlannedEndAt != nil {
					t.Fatalf("empty input = %#v, want all nil", input)
				}
			},
		},
		{
			name: "explicit empty and zero remain supplied",
			command: UpdateCommand{
				Name:           &emptyName,
				PlannedStartAt: &zeroTime,
			},
			assert: func(t *testing.T, input store.UpdateSprintInput) {
				t.Helper()
				if input.Name == nil || *input.Name != "" || input.PlannedStartAt == nil || !input.PlannedStartAt.IsZero() || input.PlannedEndAt != nil {
					t.Fatalf("explicit empty input = %#v", input)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restDefinitionFake{role: store.RoleOwner}
			ctx := restDefinitionActorContext(73, "update-success")
			prepared, err := newRESTDefinitionService(fake).PrepareUpdate(ctx, ResolvedRESTSprintTarget{
				ProjectID: 41,
				SprintID:  907,
				Name:      "Existing",
			})
			if err != nil {
				t.Fatalf("PrepareUpdate: %v", err)
			}

			if err := prepared.Update(tt.command); err != nil {
				t.Fatalf("Update: %v", err)
			}
			if !reflect.DeepEqual(fake.trace, []string{"role", "update", "publish-updated"}) {
				t.Fatalf("trace = %v", fake.trace)
			}
			if len(fake.roleCalls) != 1 || len(fake.createCalls) != 0 || len(fake.updateCalls) != 1 || len(fake.publishCalls) != 1 {
				t.Fatalf("calls: role=%d create=%d update=%d publish=%d", len(fake.roleCalls), len(fake.createCalls), len(fake.updateCalls), len(fake.publishCalls))
			}
			call := fake.updateCalls[0]
			if call.ctx != ctx || call.ctx.Value(restDefinitionContextKey{}) != "update-success" || call.sprintID != 907 {
				t.Fatalf("update binding = ctx marker %v sprint %d", call.ctx.Value(restDefinitionContextKey{}), call.sprintID)
			}
			tt.assert(t, call.input)
			if tt.command.Name != nil && call.input.Name == tt.command.Name {
				t.Fatal("materialized name pointer aliases command")
			}
			if tt.command.PlannedStartAt != nil && call.input.PlannedStartAt == tt.command.PlannedStartAt {
				t.Fatal("materialized start pointer aliases command")
			}
			if tt.command.PlannedEndAt != nil && call.input.PlannedEndAt == tt.command.PlannedEndAt {
				t.Fatal("materialized end pointer aliases command")
			}
			publishCall := fake.publishCalls[0]
			wantName := "Existing"
			if tt.command.Name != nil {
				wantName = strings.TrimSpace(*tt.command.Name)
			}
			if publishCall.kind != "updated" || publishCall.ctx != ctx || publishCall.projectID != 41 || publishCall.name != wantName {
				t.Fatalf("publish = %#v, want updated with bound context/project and name %q", publishCall, wantName)
			}
		})
	}
}

func TestPreparedRESTDefinitionFailuresSuppressPublication(t *testing.T) {
	t.Run("create returns no partial result", func(t *testing.T) {
		createErr := errors.New("configured create failure")
		fake := &restDefinitionFake{
			role:         store.RoleMaintainer,
			createResult: store.Sprint{ID: 907, ProjectID: 41, Name: "must not escape"},
			createErr:    createErr,
		}
		prepared, err := newRESTDefinitionService(fake).PrepareCreate(
			restDefinitionActorContext(73, "create-failure"),
			ResolvedRESTProjectTarget{ProjectID: 41},
		)
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}

		got, err := prepared.Create(CreateCommand{Name: "failure"})
		if err != createErr {
			t.Fatalf("error = %v, want exact %v", err, createErr)
		}
		if got != (store.Sprint{}) {
			t.Fatalf("partial sprint escaped: %#v", got)
		}
		if !reflect.DeepEqual(fake.trace, []string{"role", "create"}) || len(fake.createCalls) != 1 || len(fake.updateCalls) != 0 || len(fake.publishCalls) != 0 {
			t.Fatalf("trace/calls = %v create=%d update=%d publish=%d", fake.trace, len(fake.createCalls), len(fake.updateCalls), len(fake.publishCalls))
		}
	})

	t.Run("update returns raw error", func(t *testing.T) {
		updateErr := errors.New("configured update failure")
		fake := &restDefinitionFake{role: store.RoleMaintainer, updateErr: updateErr}
		prepared, err := newRESTDefinitionService(fake).PrepareUpdate(
			restDefinitionActorContext(73, "update-failure"),
			ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907},
		)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}

		err = prepared.Update(UpdateCommand{})
		if err != updateErr {
			t.Fatalf("error = %v, want exact %v", err, updateErr)
		}
		if !reflect.DeepEqual(fake.trace, []string{"role", "update"}) || len(fake.createCalls) != 0 || len(fake.updateCalls) != 1 || len(fake.publishCalls) != 0 {
			t.Fatalf("trace/calls = %v create=%d update=%d publish=%d", fake.trace, len(fake.createCalls), len(fake.updateCalls), len(fake.publishCalls))
		}
	})
}

func TestPreparedRESTDefinitionUsesCancelledBoundContext(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer, useCreateContextError: true}
		ctx, cancel := context.WithCancel(restDefinitionActorContext(73, "cancel-create"))
		prepared, err := newRESTDefinitionService(fake).PrepareCreate(ctx, ResolvedRESTProjectTarget{ProjectID: 41})
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}
		cancel()

		_, err = prepared.Create(CreateCommand{Name: "cancelled"})
		if err != context.Canceled {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if fake.createCalls[0].ctx != ctx || !reflect.DeepEqual(fake.trace, []string{"role", "create"}) || len(fake.createCalls) != 1 || len(fake.publishCalls) != 0 {
			t.Fatalf("trace/calls = %v create=%d publish=%d", fake.trace, len(fake.createCalls), len(fake.publishCalls))
		}
	})

	t.Run("update", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer, useUpdateContextError: true}
		ctx, cancel := context.WithCancel(restDefinitionActorContext(73, "cancel-update"))
		prepared, err := newRESTDefinitionService(fake).PrepareUpdate(ctx, ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907})
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		cancel()

		err = prepared.Update(UpdateCommand{})
		if err != context.Canceled {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if fake.updateCalls[0].ctx != ctx || !reflect.DeepEqual(fake.trace, []string{"role", "update"}) || len(fake.updateCalls) != 1 || len(fake.publishCalls) != 0 {
			t.Fatalf("trace/calls = %v update=%d publish=%d", fake.trace, len(fake.updateCalls), len(fake.publishCalls))
		}
	})
}

func TestRESTDefinitionServiceNilPublisher(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		service := NewRESTDefinitionService(RESTDefinitionServiceDependencies{
			Roles:       fake,
			Definitions: fake,
			Publisher:   nil,
		})
		prepared, err := service.PrepareCreate(restDefinitionActorContext(73, "nil-create"), ResolvedRESTProjectTarget{ProjectID: 41})
		if err != nil {
			t.Fatalf("PrepareCreate: %v", err)
		}
		if _, err := prepared.Create(CreateCommand{Name: "nil publisher"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !reflect.DeepEqual(fake.trace, []string{"role", "create"}) || len(fake.createCalls) != 1 {
			t.Fatalf("trace = %v, create calls = %d", fake.trace, len(fake.createCalls))
		}
	})

	t.Run("update", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		service := NewRESTDefinitionService(RESTDefinitionServiceDependencies{
			Roles:       fake,
			Definitions: fake,
			Publisher:   nil,
		})
		prepared, err := service.PrepareUpdate(restDefinitionActorContext(73, "nil-update"), ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907})
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		if err := prepared.Update(UpdateCommand{}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if !reflect.DeepEqual(fake.trace, []string{"role", "update"}) || len(fake.updateCalls) != 1 {
			t.Fatalf("trace = %v, update calls = %d", fake.trace, len(fake.updateCalls))
		}
	})
}

func TestPreparedRESTUpdatePublishesRenameCorrectName(t *testing.T) {
	t.Run("omitted name keeps retained existing name", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		prepared, err := newRESTDefinitionService(fake).PrepareUpdate(
			restDefinitionActorContext(73, "retain"),
			ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907, Name: "Sprint 12"},
		)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		if err := prepared.Update(UpdateCommand{}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := fake.publishCalls[0].name; got != "Sprint 12" {
			t.Fatalf("published name = %q, want retained Sprint 12", got)
		}
	})

	t.Run("supplied name publishes trimmed new name", func(t *testing.T) {
		fake := &restDefinitionFake{role: store.RoleMaintainer}
		prepared, err := newRESTDefinitionService(fake).PrepareUpdate(
			restDefinitionActorContext(73, "rename"),
			ResolvedRESTSprintTarget{ProjectID: 41, SprintID: 907, Name: "Sprint 12"},
		)
		if err != nil {
			t.Fatalf("PrepareUpdate: %v", err)
		}
		renamed := "  Sprint 13  "
		if err := prepared.Update(UpdateCommand{Name: &renamed}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := fake.publishCalls[0].name; got != "Sprint 13" {
			t.Fatalf("published name = %q, want trimmed Sprint 13", got)
		}
	})
}

func stringPointer(value string) *string {
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}
