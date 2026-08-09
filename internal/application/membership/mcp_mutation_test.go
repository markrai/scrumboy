package membership

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

var _ MCPMutationAccessStore = (*store.Store)(nil)

type mcpMutationContextKey struct{}

type mcpMembershipAccessCall struct {
	ctx  context.Context
	slug string
	mode store.Mode
}

type mcpMembershipMutationCall struct {
	operation   string
	ctx         context.Context
	requesterID int64
	projectID   int64
	targetID    int64
	role        store.ProjectRole
}

type mcpMembershipListCall struct {
	ctx         context.Context
	projectID   int64
	requesterID int64
}

type mcpMutationFake struct {
	trace []string

	projectContext store.ProjectContext
	accessErr      error
	addErr         error
	updateErr      error
	removeErr      error
	listErr        error
	members        []store.ProjectMember
	honorContext   bool
	afterMutation  func()

	accessCalls   []mcpMembershipAccessCall
	mutationCalls []mcpMembershipMutationCall
	listCalls     []mcpMembershipListCall
}

func (f *mcpMutationFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.accessCalls = append(f.accessCalls, mcpMembershipAccessCall{ctx: ctx, slug: slug, mode: mode})
	if f.honorContext && ctx.Err() != nil {
		return store.ProjectContext{}, ctx.Err()
	}
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpMutationFake) AddProjectMember(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
	role store.ProjectRole,
) error {
	f.trace = append(f.trace, "add")
	f.mutationCalls = append(f.mutationCalls, mcpMembershipMutationCall{
		operation: "add", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID, role: role,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.addErr != nil {
		return f.addErr
	}
	if f.afterMutation != nil {
		f.afterMutation()
	}
	return nil
}

func (f *mcpMutationFake) UpdateProjectMemberRole(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
	role store.ProjectRole,
) error {
	f.trace = append(f.trace, "update_role")
	f.mutationCalls = append(f.mutationCalls, mcpMembershipMutationCall{
		operation: "update_role", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID, role: role,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	if f.afterMutation != nil {
		f.afterMutation()
	}
	return nil
}

func (f *mcpMutationFake) RemoveProjectMember(
	ctx context.Context,
	requesterID int64,
	projectID int64,
	targetUserID int64,
) error {
	f.trace = append(f.trace, "remove")
	f.mutationCalls = append(f.mutationCalls, mcpMembershipMutationCall{
		operation: "remove", ctx: ctx, requesterID: requesterID, projectID: projectID,
		targetID: targetUserID,
	})
	if f.honorContext && ctx.Err() != nil {
		return ctx.Err()
	}
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.afterMutation != nil {
		f.afterMutation()
	}
	return nil
}

func (f *mcpMutationFake) ListProjectMembers(
	ctx context.Context,
	projectID int64,
	requesterID int64,
) ([]store.ProjectMember, error) {
	f.trace = append(f.trace, "list")
	f.listCalls = append(f.listCalls, mcpMembershipListCall{
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

func newMCPMutationTestService(fake *mcpMutationFake) *MCPMutationService {
	return NewMCPMutationService(MCPMutationServiceDependencies{
		Access:    fake,
		Mutations: fake,
		Members:   fake,
	})
}

func mcpMutationActorContext(userID int64, marker string) context.Context {
	ctx := context.WithValue(context.Background(), mcpMutationContextKey{}, marker)
	return store.WithUserID(ctx, userID)
}

func assertMCPMutationTrace(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call trace=%v want=%v", got, want)
	}
}

func preparedMCPMutationForTest(
	t *testing.T,
	fake *mcpMutationFake,
	ctx context.Context,
) *PreparedMCPMutation {
	t.Helper()
	prepared, err := newMCPMutationTestService(fake).Prepare(ctx, MCPMutationTarget{
		ProjectSlug: "bound-project",
		Mode:        store.ModeFull,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func TestMCPMutationPrepareOrderingAndBinding(t *testing.T) {
	t.Run("access failure", func(t *testing.T) {
		accessErr := errors.New("access failed")
		fake := &mcpMutationFake{accessErr: accessErr}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			mcpMutationActorContext(41, "access failure"),
			MCPMutationTarget{ProjectSlug: "missing-project", Mode: store.ModeAnonymous},
		)
		if prepared != nil || err != accessErr {
			t.Fatalf("Prepare=(%v, %v), want nil and exact %v", prepared, err, accessErr)
		}
		if len(fake.accessCalls) != 1 || len(fake.mutationCalls) != 0 || len(fake.listCalls) != 0 {
			t.Fatalf("calls: access=%+v mutations=%+v lists=%+v", fake.accessCalls, fake.mutationCalls, fake.listCalls)
		}
		call := fake.accessCalls[0]
		if call.slug != "missing-project" || call.mode != store.ModeAnonymous {
			t.Fatalf("access call=%+v", call)
		}
		assertMCPMutationTrace(t, fake.trace, "access")
	})

	for _, role := range []store.ProjectRole{"", store.RoleViewer, store.RoleContributor} {
		t.Run("insufficient role "+string(role), func(t *testing.T) {
			fake := &mcpMutationFake{projectContext: store.ProjectContext{
				Project: store.Project{ID: 51}, Role: role,
			}}
			prepared, err := newMCPMutationTestService(fake).Prepare(
				context.Background(),
				MCPMutationTarget{ProjectSlug: "role-project", Mode: store.ModeFull},
			)
			if prepared != nil || !errors.Is(err, ErrMaintainerRequired) || errors.Is(err, ErrActorRequired) {
				t.Fatalf("Prepare=(%v, %v), want nil and only %v", prepared, err, ErrMaintainerRequired)
			}
			assertMCPMutationTrace(t, fake.trace, "access")
		})
	}

	t.Run("actor required after authorized access", func(t *testing.T) {
		fake := &mcpMutationFake{projectContext: store.ProjectContext{
			Project: store.Project{ID: 52}, Role: store.RoleMaintainer,
		}}
		prepared, err := newMCPMutationTestService(fake).Prepare(
			context.Background(),
			MCPMutationTarget{ProjectSlug: "actor-project", Mode: store.ModeFull},
		)
		if prepared != nil || !errors.Is(err, ErrActorRequired) || errors.Is(err, ErrMaintainerRequired) {
			t.Fatalf("Prepare=(%v, %v), want nil and only %v", prepared, err, ErrActorRequired)
		}
		assertMCPMutationTrace(t, fake.trace, "access")
	})

	for _, role := range []store.ProjectRole{store.RoleMaintainer, store.RoleOwner} {
		t.Run("authorized role "+string(role), func(t *testing.T) {
			ctx := mcpMutationActorContext(61, "bound")
			fake := &mcpMutationFake{projectContext: store.ProjectContext{
				Project: store.Project{ID: 71}, Role: role,
			}}
			target := MCPMutationTarget{ProjectSlug: "bound-project", Mode: store.ModeFull}
			service := newMCPMutationTestService(fake)
			prepared, err := service.Prepare(ctx, target)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			target.ProjectSlug = "replacement-project"
			target.Mode = store.ModeAnonymous
			fake.projectContext.Project.ID = 99
			if prepared.ctx != ctx || prepared.service != service || prepared.requesterID != 61 || prepared.projectID != 71 {
				t.Fatalf("prepared binding: ctxSame=%v serviceSame=%v requester=%d project=%d", prepared.ctx == ctx, prepared.service == service, prepared.requesterID, prepared.projectID)
			}
			if got := prepared.ctx.Value(mcpMutationContextKey{}); got != "bound" {
				t.Fatalf("context marker=%v want=bound", got)
			}
			if len(fake.accessCalls) != 1 || fake.accessCalls[0].ctx != ctx ||
				fake.accessCalls[0].slug != "bound-project" || fake.accessCalls[0].mode != store.ModeFull {
				t.Fatalf("access calls=%+v", fake.accessCalls)
			}
			if len(fake.mutationCalls) != 0 || len(fake.listCalls) != 0 {
				t.Fatalf("preparation performed operation: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
			}
			assertMCPMutationTrace(t, fake.trace, "access")
		})
	}
}

func TestPreparedMCPMutationSuccessfulSequences(t *testing.T) {
	ctx := mcpMutationActorContext(81, "successful sequence")
	wantTarget := store.ProjectMember{
		UserID: 91, Email: "target@example.com", Name: "Target", Role: store.RoleViewer,
	}
	wantMembers := []store.ProjectMember{
		{UserID: 81, Name: "Requester", Role: store.RoleMaintainer},
		wantTarget,
		{UserID: 92, Name: "Other", Role: store.RoleContributor},
	}

	t.Run("add", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 101}, Role: store.RoleMaintainer},
			members:        wantMembers,
		}
		prepared := preparedMCPMutationForTest(t, fake, ctx)
		got, err := prepared.Add(AddCommand{TargetUserID: 91, Role: store.RoleViewer})
		if err != nil || !reflect.DeepEqual(got, wantTarget) {
			t.Fatalf("Add=(%+v, %v), want %+v", got, err, wantTarget)
		}
		assertMCPMutationCallAndList(t, fake, ctx, "add", 81, 101, 91, store.RoleViewer)
		assertMCPMutationTrace(t, fake.trace, "access", "add", "list")
	})

	t.Run("update semantic no-op still persists and lists", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 101}, Role: store.RoleMaintainer},
			members:        wantMembers,
		}
		prepared := preparedMCPMutationForTest(t, fake, ctx)
		got, err := prepared.UpdateRole(UpdateRoleCommand{TargetUserID: 91, Role: store.RoleViewer})
		if err != nil || !reflect.DeepEqual(got, wantTarget) {
			t.Fatalf("UpdateRole=(%+v, %v), want %+v", got, err, wantTarget)
		}
		assertMCPMutationCallAndList(t, fake, ctx, "update_role", 81, 101, 91, store.RoleViewer)
		assertMCPMutationTrace(t, fake.trace, "access", "update_role", "list")
	})

	t.Run("remove including self-removal performs no list", func(t *testing.T) {
		fake := &mcpMutationFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 101}, Role: store.RoleMaintainer},
			listErr:        errors.New("remove must not list"),
		}
		prepared := preparedMCPMutationForTest(t, fake, ctx)
		if err := prepared.Remove(RemoveCommand{TargetUserID: 81}); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 0 {
			t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
		}
		call := fake.mutationCalls[0]
		if call.operation != "remove" || call.ctx != ctx || call.requesterID != 81 ||
			call.projectID != 101 || call.targetID != 81 || call.role != "" {
			t.Fatalf("remove call=%+v", call)
		}
		assertMCPMutationTrace(t, fake.trace, "access", "remove")
	})
}

func assertMCPMutationCallAndList(
	t *testing.T,
	fake *mcpMutationFake,
	ctx context.Context,
	operation string,
	requesterID int64,
	projectID int64,
	targetID int64,
	role store.ProjectRole,
) {
	t.Helper()
	if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 {
		t.Fatalf("call cardinality: mutations=%d lists=%d", len(fake.mutationCalls), len(fake.listCalls))
	}
	mutation := fake.mutationCalls[0]
	if mutation.operation != operation || mutation.ctx != ctx || mutation.requesterID != requesterID ||
		mutation.projectID != projectID || mutation.targetID != targetID || mutation.role != role {
		t.Fatalf("mutation call=%+v", mutation)
	}
	list := fake.listCalls[0]
	if list.ctx != ctx || list.projectID != projectID || list.requesterID != requesterID {
		t.Fatalf("list call=%+v", list)
	}
}

type mcpMutationOperation struct {
	name      string
	traceName string
	setError  func(*mcpMutationFake, error)
	execute   func(*PreparedMCPMutation) (store.ProjectMember, error)
}

func mcpMutationOperations() []mcpMutationOperation {
	return []mcpMutationOperation{
		{
			name: "add", traceName: "add",
			setError: func(f *mcpMutationFake, err error) { f.addErr = err },
			execute: func(p *PreparedMCPMutation) (store.ProjectMember, error) {
				return p.Add(AddCommand{TargetUserID: 111, Role: store.RoleContributor})
			},
		},
		{
			name: "update role", traceName: "update_role",
			setError: func(f *mcpMutationFake, err error) { f.updateErr = err },
			execute: func(p *PreparedMCPMutation) (store.ProjectMember, error) {
				return p.UpdateRole(UpdateRoleCommand{TargetUserID: 111, Role: store.RoleViewer})
			},
		},
		{
			name: "remove", traceName: "remove",
			setError: func(f *mcpMutationFake, err error) { f.removeErr = err },
			execute: func(p *PreparedMCPMutation) (store.ProjectMember, error) {
				return store.ProjectMember{}, p.Remove(RemoveCommand{TargetUserID: 111})
			},
		},
	}
}

func TestPreparedMCPMutationFailuresPreserveOrderAndIdentity(t *testing.T) {
	for _, operation := range mcpMutationOperations() {
		t.Run(operation.name+" mutation failure", func(t *testing.T) {
			mutationErr := errors.New(operation.traceName + " failed")
			fake := &mcpMutationFake{
				projectContext: store.ProjectContext{Project: store.Project{ID: 121}, Role: store.RoleMaintainer},
			}
			operation.setError(fake, mutationErr)
			prepared := preparedMCPMutationForTest(t, fake, mcpMutationActorContext(122, operation.traceName))
			got, err := operation.execute(prepared)
			if err != mutationErr || got != (store.ProjectMember{}) {
				t.Fatalf("execute=(%+v, %v), want zero member and exact %v", got, err, mutationErr)
			}
			if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 0 {
				t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
			}
			assertMCPMutationTrace(t, fake.trace, "access", operation.traceName)
		})
	}

	readErrors := []struct {
		name string
		err  error
	}{
		{name: "generic", err: errors.New("list failed")},
		{name: "wrapped sentinel", err: fmt.Errorf("wrapped list failure: %w", store.ErrNotFound)},
	}
	for _, operation := range mcpMutationOperations()[:2] {
		for _, readError := range readErrors {
			t.Run(operation.name+" "+readError.name+" post-read failure", func(t *testing.T) {
				fake := &mcpMutationFake{
					projectContext: store.ProjectContext{Project: store.Project{ID: 131}, Role: store.RoleMaintainer},
					listErr:        readError.err,
				}
				prepared := preparedMCPMutationForTest(t, fake, mcpMutationActorContext(132, operation.traceName+" list"))
				got, err := operation.execute(prepared)
				if err != readError.err || got != (store.ProjectMember{}) {
					t.Fatalf("execute=(%+v, %v), want zero member and exact %v", got, err, readError.err)
				}
				if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 {
					t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
				}
				assertMCPMutationTrace(t, fake.trace, "access", operation.traceName, "list")
			})
		}
	}
}

func TestPreparedMCPMutationMissingTargetSentinelsAreDistinct(t *testing.T) {
	tests := []struct {
		name      string
		traceName string
		want      error
		notWant   error
		execute   func(*PreparedMCPMutation) (store.ProjectMember, error)
		forbidden []string
	}{
		{
			name: "add", traceName: "add", want: ErrAddedMemberMissing, notWant: ErrUpdatedMemberMissing,
			execute: func(p *PreparedMCPMutation) (store.ProjectMember, error) {
				return p.Add(AddCommand{TargetUserID: 141, Role: store.RoleViewer})
			},
			forbidden: []string{"bound-project", "141", "MCP", "INTERNAL"},
		},
		{
			name: "update", traceName: "update_role", want: ErrUpdatedMemberMissing, notWant: ErrAddedMemberMissing,
			execute: func(p *PreparedMCPMutation) (store.ProjectMember, error) {
				return p.UpdateRole(UpdateRoleCommand{TargetUserID: 142, Role: store.RoleContributor})
			},
			forbidden: []string{"bound-project", "142", "MCP", "NOT_FOUND"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpMutationFake{
				projectContext: store.ProjectContext{Project: store.Project{ID: 143}, Role: store.RoleMaintainer},
				members:        []store.ProjectMember{{UserID: 999, Role: store.RoleViewer}},
			}
			prepared := preparedMCPMutationForTest(t, fake, mcpMutationActorContext(144, tt.name+" missing"))
			got, err := tt.execute(prepared)
			if got != (store.ProjectMember{}) || !errors.Is(err, tt.want) || errors.Is(err, tt.notWant) {
				t.Fatalf("execute=(%+v, %v), want zero member, %v, and not %v", got, err, tt.want, tt.notWant)
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("stable sentinel leaked %q: %q", forbidden, err.Error())
				}
			}
			if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 {
				t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
			}
			assertMCPMutationTrace(t, fake.trace, "access", tt.traceName, "list")
		})
	}
}

func TestPreparedMCPMutationUsesCancelledBoundContext(t *testing.T) {
	for _, operation := range mcpMutationOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fake := &mcpMutationFake{
				projectContext: store.ProjectContext{Project: store.Project{ID: 151}, Role: store.RoleMaintainer},
				honorContext:   true,
			}
			ctx, cancel := context.WithCancel(mcpMutationActorContext(152, operation.traceName+" cancelled"))
			prepared := preparedMCPMutationForTest(t, fake, ctx)
			cancel()
			got, err := operation.execute(prepared)
			if got != (store.ProjectMember{}) || !errors.Is(err, context.Canceled) {
				t.Fatalf("execute=(%+v, %v), want zero member and %v", got, err, context.Canceled)
			}
			if len(fake.mutationCalls) != 1 || fake.mutationCalls[0].ctx != ctx || len(fake.listCalls) != 0 {
				t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
			}
			assertMCPMutationTrace(t, fake.trace, "access", operation.traceName)
		})
	}
}

func TestPreparedMCPMutationCancellationAfterMutationReachesList(t *testing.T) {
	ctx, cancel := context.WithCancel(mcpMutationActorContext(161, "cancel before list"))
	fake := &mcpMutationFake{
		projectContext: store.ProjectContext{Project: store.Project{ID: 162}, Role: store.RoleMaintainer},
		honorContext:   true,
		afterMutation:  cancel,
	}
	prepared := preparedMCPMutationForTest(t, fake, ctx)
	got, err := prepared.Add(AddCommand{TargetUserID: 163, Role: store.RoleViewer})
	if got != (store.ProjectMember{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Add=(%+v, %v), want zero member and %v", got, err, context.Canceled)
	}
	if len(fake.mutationCalls) != 1 || len(fake.listCalls) != 1 || fake.listCalls[0].ctx != ctx {
		t.Fatalf("calls: mutations=%+v lists=%+v", fake.mutationCalls, fake.listCalls)
	}
	assertMCPMutationTrace(t, fake.trace, "access", "add", "list")
}
