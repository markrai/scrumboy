package membership

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restMutationContextKey struct{}

type restMembershipMutationCall struct {
	operation   string
	ctx         context.Context
	requesterID int64
	projectID   int64
	targetID    int64
	role        store.ProjectRole
}

type restMembershipListCall struct {
	ctx         context.Context
	projectID   int64
	requesterID int64
}

type restMembersUpdatedCall struct {
	ctx       context.Context
	projectID int64
}

type restMembershipChangedCall struct {
	ctx       context.Context
	projectID int64
	actorID   int64
	targetID  int64
	action    MembershipAction
}

type restMutationFake struct {
	trace []string

	mutationCalls []restMembershipMutationCall
	listCalls     []restMembershipListCall
	members       []store.ProjectMember
	addErr        error
	updateErr     error
	removeErr     error
	listErr       error
	honorContext  bool

	membersUpdatedCalls    []restMembersUpdatedCall
	membershipChangedCalls []restMembershipChangedCall
}

func (f *restMutationFake) AddProjectMember(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
	role store.ProjectRole,
) error {
	f.trace = append(f.trace, "add")
	f.mutationCalls = append(f.mutationCalls, restMembershipMutationCall{
		operation: "add", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID, role: role,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.addErr
}

func (f *restMutationFake) UpdateProjectMemberRole(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
	role store.ProjectRole,
) error {
	f.trace = append(f.trace, "update_role")
	f.mutationCalls = append(f.mutationCalls, restMembershipMutationCall{
		operation: "update_role", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID, role: role,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.updateErr
}

func (f *restMutationFake) RemoveProjectMember(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
) error {
	f.trace = append(f.trace, "remove")
	f.mutationCalls = append(f.mutationCalls, restMembershipMutationCall{
		operation: "remove", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	return f.removeErr
}

func (f *restMutationFake) ListProjectMembers(
	ctx context.Context,
	projectID int64,
	requesterID int64,
) ([]store.ProjectMember, error) {
	f.trace = append(f.trace, "list")
	f.listCalls = append(f.listCalls, restMembershipListCall{
		ctx: ctx, projectID: projectID, requesterID: requesterID,
	})
	if f.honorContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.members, nil
}

func (f *restMutationFake) PublishMembersUpdated(ctx context.Context, projectID int64) {
	f.trace = append(f.trace, "publish_members_updated")
	f.membersUpdatedCalls = append(f.membersUpdatedCalls, restMembersUpdatedCall{
		ctx: ctx, projectID: projectID,
	})
}

func (f *restMutationFake) PublishMembershipChanged(
	ctx context.Context,
	projectID int64,
	actorUserID int64,
	targetUserID int64,
	action MembershipAction,
) {
	f.trace = append(f.trace, "publish_membership_changed")
	f.membershipChangedCalls = append(f.membershipChangedCalls, restMembershipChangedCall{
		ctx: ctx, projectID: projectID, actorID: actorUserID,
		targetID: targetUserID, action: action,
	})
}

func newRESTMutationTestService(fake *restMutationFake) *RESTMutationService {
	return NewRESTMutationService(RESTMutationServiceDependencies{
		Mutations: fake,
		Members:   fake,
		Publisher: fake,
	})
}

func restMutationActorContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), restMutationContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func assertRESTMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

type restMutationOperation struct {
	name       string
	traceName  string
	targetID   int64
	role       store.ProjectRole
	wantAction MembershipAction
	execute    func(*PreparedRESTMutation) ([]store.ProjectMember, error)
	setError   func(*restMutationFake, error)
}

func restMutationOperations() []restMutationOperation {
	return []restMutationOperation{
		{
			name: "add", traceName: "add", targetID: 301, role: store.RoleContributor,
			wantAction: MembershipAction("added"),
			execute: func(p *PreparedRESTMutation) ([]store.ProjectMember, error) {
				return p.Add(AddCommand{TargetUserID: 301, Role: store.RoleContributor})
			},
			setError: func(f *restMutationFake, err error) { f.addErr = err },
		},
		{
			name: "update role including semantic no-op", traceName: "update_role",
			targetID: 302, role: store.RoleViewer,
			wantAction: MembershipAction("role_changed"),
			execute: func(p *PreparedRESTMutation) ([]store.ProjectMember, error) {
				return p.UpdateRole(UpdateRoleCommand{TargetUserID: 302, Role: store.RoleViewer})
			},
			setError: func(f *restMutationFake, err error) { f.updateErr = err },
		},
		{
			name: "remove", traceName: "remove", targetID: 303,
			wantAction: MembershipAction("removed"),
			execute: func(p *PreparedRESTMutation) ([]store.ProjectMember, error) {
				return p.Remove(RemoveCommand{TargetUserID: 303})
			},
			setError: func(f *restMutationFake, err error) { f.removeErr = err },
		},
	}
}

func TestRESTMutationPrepareBindsActorContextAndProject(t *testing.T) {
	fake := &restMutationFake{}
	service := newRESTMutationTestService(fake)

	prepared, err := service.Prepare(context.Background(), ResolvedRESTMutationTarget{ProjectID: 51})
	if !errors.Is(err, ErrActorRequired) || prepared != nil {
		t.Fatalf("missing-actor Prepare=(%v, %v), want nil and %v", prepared, err, ErrActorRequired)
	}
	assertRESTMutationTrace(t, fake.trace)

	ctx := restMutationActorContext(41, "bound")
	target := ResolvedRESTMutationTarget{ProjectID: 61}
	prepared, err = service.Prepare(ctx, target)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	target.ProjectID = 99
	if prepared.ctx != ctx || prepared.requesterID != 41 || prepared.projectID != 61 || prepared.service != service {
		t.Fatalf("prepared binding: ctxSame=%v requester=%d project=%d serviceSame=%v", prepared.ctx == ctx, prepared.requesterID, prepared.projectID, prepared.service == service)
	}
	if got := prepared.ctx.Value(restMutationContextKey{}); got != "bound" {
		t.Fatalf("bound context marker=%v want=bound", got)
	}
	assertRESTMutationTrace(t, fake.trace)
}

func TestPreparedRESTMutationSuccessfulSequences(t *testing.T) {
	wantMembers := []store.ProjectMember{
		{UserID: 41, Name: "Requester", Role: store.RoleMaintainer},
		{UserID: 302, Name: "Target", Role: store.RoleViewer},
	}

	for _, operation := range restMutationOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fake := &restMutationFake{members: wantMembers}
			service := newRESTMutationTestService(fake)
			ctx := restMutationActorContext(41, operation.traceName)
			prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 71})
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}

			got, err := operation.execute(prepared)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !reflect.DeepEqual(got, wantMembers) {
				t.Fatalf("members=%+v want=%+v", got, wantMembers)
			}
			if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 ||
				len(fake.membersUpdatedCalls) != 1 || len(fake.membershipChangedCalls) != 1 {
				t.Fatalf("call cardinality: mutations=%d lists=%d membersUpdated=%d membershipChanged=%d", len(fake.mutationCalls), len(fake.listCalls), len(fake.membersUpdatedCalls), len(fake.membershipChangedCalls))
			}
			mutation := fake.mutationCalls[0]
			if mutation.operation != operation.traceName || mutation.ctx != ctx ||
				mutation.requesterID != 41 || mutation.projectID != 71 ||
				mutation.targetID != operation.targetID || mutation.role != operation.role {
				t.Fatalf("mutation call=%+v", mutation)
			}
			list := fake.listCalls[0]
			if list.ctx != ctx || list.projectID != 71 || list.requesterID != 41 {
				t.Fatalf("list call=%+v", list)
			}
			membersUpdated := fake.membersUpdatedCalls[0]
			if membersUpdated.ctx != ctx || membersUpdated.projectID != 71 {
				t.Fatalf("members-updated publication=%+v", membersUpdated)
			}
			membershipChanged := fake.membershipChangedCalls[0]
			if membershipChanged.ctx != ctx || membershipChanged.projectID != 71 ||
				membershipChanged.actorID != 41 || membershipChanged.targetID != operation.targetID ||
				membershipChanged.action != operation.wantAction {
				t.Fatalf("membership-changed publication=%+v", membershipChanged)
			}
			if got := mutation.ctx.Value(restMutationContextKey{}); got != operation.traceName {
				t.Fatalf("mutation context marker=%v want=%s", got, operation.traceName)
			}
			assertRESTMutationTrace(t, fake.trace,
				operation.traceName,
				"list",
				"publish_members_updated",
				"publish_membership_changed",
			)
		})
	}
}

func TestPreparedRESTMutationFailuresShortCircuit(t *testing.T) {
	for _, operation := range restMutationOperations() {
		t.Run(operation.name+" mutation failure", func(t *testing.T) {
			mutationErr := errors.New(operation.traceName + " failed")
			fake := &restMutationFake{}
			operation.setError(fake, mutationErr)
			prepared, err := newRESTMutationTestService(fake).Prepare(
				restMutationActorContext(51, operation.traceName+" failure"),
				ResolvedRESTMutationTarget{ProjectID: 81},
			)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}

			got, err := operation.execute(prepared)
			if err != mutationErr || got != nil {
				t.Fatalf("execute=(%+v, %v), want nil and exact %v", got, err, mutationErr)
			}
			if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 0 ||
				len(fake.membersUpdatedCalls) != 0 || len(fake.membershipChangedCalls) != 0 {
				t.Fatalf("call cardinality: mutations=%d lists=%d membersUpdated=%d membershipChanged=%d", len(fake.mutationCalls), len(fake.listCalls), len(fake.membersUpdatedCalls), len(fake.membershipChangedCalls))
			}
			assertRESTMutationTrace(t, fake.trace, operation.traceName)
		})

		t.Run(operation.name+" post-read failure", func(t *testing.T) {
			readErr := errors.New(operation.traceName + " list failed")
			if operation.traceName == "remove" {
				readErr = store.ErrNotFound
			}
			fake := &restMutationFake{listErr: readErr}
			prepared, err := newRESTMutationTestService(fake).Prepare(
				restMutationActorContext(52, operation.traceName+" read failure"),
				ResolvedRESTMutationTarget{ProjectID: 82},
			)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}

			got, err := operation.execute(prepared)
			if err != readErr || got != nil {
				t.Fatalf("execute=(%+v, %v), want nil and exact %v", got, err, readErr)
			}
			if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 ||
				len(fake.membersUpdatedCalls) != 0 || len(fake.membershipChangedCalls) != 0 {
				t.Fatalf("call cardinality: mutations=%d lists=%d membersUpdated=%d membershipChanged=%d", len(fake.mutationCalls), len(fake.listCalls), len(fake.membersUpdatedCalls), len(fake.membershipChangedCalls))
			}
			assertRESTMutationTrace(t, fake.trace, operation.traceName, "list")
		})
	}
}

func TestPreparedRESTMutationUsesCancelledBoundContext(t *testing.T) {
	fake := &restMutationFake{honorContext: true}
	service := newRESTMutationTestService(fake)
	ctx, cancel := context.WithCancel(restMutationActorContext(61, "cancelled"))
	prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 91})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	cancel()

	got, err := prepared.Add(AddCommand{TargetUserID: 401, Role: store.RoleViewer})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("Add=(%+v, %v), want nil and %v", got, err, context.Canceled)
	}
	if len(fake.mutationCalls) != 1 || fake.mutationCalls[0].ctx != ctx || len(fake.listCalls) != 0 ||
		len(fake.membersUpdatedCalls) != 0 || len(fake.membershipChangedCalls) != 0 {
		t.Fatalf("cancelled calls: mutations=%+v lists=%+v membersUpdated=%+v membershipChanged=%+v", fake.mutationCalls, fake.listCalls, fake.membersUpdatedCalls, fake.membershipChangedCalls)
	}
	assertRESTMutationTrace(t, fake.trace, "add")
}

func TestRESTMutationServiceNilPublisherIsNoop(t *testing.T) {
	wantMembers := []store.ProjectMember{{UserID: 501, Role: store.RoleContributor}}
	fake := &restMutationFake{members: wantMembers}
	service := NewRESTMutationService(RESTMutationServiceDependencies{
		Mutations: fake,
		Members:   fake,
	})
	prepared, err := service.Prepare(
		restMutationActorContext(71, "nil publisher"),
		ResolvedRESTMutationTarget{ProjectID: 101},
	)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	got, err := prepared.Add(AddCommand{TargetUserID: 501, Role: store.RoleContributor})
	if err != nil || !reflect.DeepEqual(got, wantMembers) {
		t.Fatalf("Add=(%+v, %v), want %+v", got, err, wantMembers)
	}
	if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 {
		t.Fatalf("call cardinality: mutations=%d lists=%d", len(fake.mutationCalls), len(fake.listCalls))
	}
	assertRESTMutationTrace(t, fake.trace, "add", "list")
}
