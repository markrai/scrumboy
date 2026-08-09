package sprint

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

func TestMCPDeletionServiceConstructionIsInertAndPublicationFree(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStatePlanned)
	service := newMCPDeletionTestService(fake)
	if service == nil {
		t.Fatal("NewMCPDeletionService returned nil")
	}
	if len(fake.trace) != 0 {
		t.Fatalf("construction performed dependency work: %v", fake.trace)
	}

	depsType := reflect.TypeOf(MCPDeletionServiceDependencies{})
	wantFields := []string{"Access", "Roles", "Sprints", "Deletions"}
	gotFields := make([]string, 0, depsType.NumField())
	for i := 0; i < depsType.NumField(); i++ {
		gotFields = append(gotFields, depsType.Field(i).Name)
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("dependency fields = %v, want %v", gotFields, wantFields)
	}
	for _, field := range gotFields {
		name := strings.ToLower(field)
		if strings.Contains(name, "publish") || strings.Contains(name, "event") || strings.Contains(name, "fanout") {
			t.Fatalf("unexpected realtime dependency %q", field)
		}
	}
}

func TestMCPDeletionPreparationAcceptsEveryLifecycleState(t *testing.T) {
	for _, state := range []string{
		store.SprintStatePlanned,
		store.SprintStateActive,
		store.SprintStateClosed,
	} {
		t.Run(state, func(t *testing.T) {
			fake := newMCPLifecycleFake(state)
			prepared, err := newMCPDeletionTestService(fake).PrepareDelete(
				mcpLifecycleContext(149, state),
				MCPDeletionTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull},
			)
			if err != nil || prepared == nil {
				t.Fatalf("PrepareDelete() = %v, %v; want capability, nil", prepared, err)
			}
			if fake.nowCalls != 0 {
				t.Fatalf("deletion consulted transition clock %d times", fake.nowCalls)
			}
			assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target")
		})
	}
}

func TestMCPDeletionSuccessBindsIdentityAndHasNoPostRead(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStateActive)
	ctx := mcpLifecycleContext(151, "delete success")
	target := MCPDeletionTarget{ProjectSlug: "original", SprintID: 907, Mode: store.ModeFull}
	prepared, err := newMCPDeletionTestService(fake).PrepareDelete(ctx, target)
	if err != nil {
		t.Fatalf("PrepareDelete: %v", err)
	}
	target.ProjectSlug = "replacement"
	target.SprintID = 999
	target.Mode = store.ModeAnonymous

	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(fake.deleteCalls) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(fake.deleteCalls))
	}
	call := fake.deleteCalls[0]
	if call.ctx != ctx || call.projectID != 71 || call.sprintID != 907 {
		t.Fatalf("delete call = %+v, want exact bound context/resolved project/requested sprint", call)
	}
	if !fake.deleteCommitted {
		t.Fatal("delete did not commit")
	}
	if len(fake.readCalls) != 1 {
		t.Fatalf("read calls = %d, want target only", len(fake.readCalls))
	}
	assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target", "delete")
}

func TestMCPDeletionMutationFailurePassesThroughWithoutRetry(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStateClosed)
	deleteErr := errors.New("private deletion failure")
	fake.deleteErr = deleteErr
	prepared, err := newMCPDeletionTestService(fake).PrepareDelete(
		mcpLifecycleContext(157, "delete failure"),
		MCPDeletionTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareDelete: %v", err)
	}
	if err := prepared.Delete(); err != deleteErr {
		t.Fatalf("error = %v, want exact %v", err, deleteErr)
	}
	if len(fake.deleteCalls) != 1 || len(fake.readCalls) != 1 {
		t.Fatalf("delete/read calls = %d/%d, want 1/1", len(fake.deleteCalls), len(fake.readCalls))
	}
	if fake.deleteCommitted {
		t.Fatal("failed deletion marked committed")
	}
	assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target", "delete")
}

func TestMCPDeletionCancellationAfterPreparationUsesBoundContext(t *testing.T) {
	fake := newMCPLifecycleFake(store.SprintStatePlanned)
	fake.honorContext = true
	ctx, cancel := context.WithCancel(mcpLifecycleContext(163, "delete cancellation"))
	prepared, err := newMCPDeletionTestService(fake).PrepareDelete(
		ctx,
		MCPDeletionTarget{ProjectSlug: "board", SprintID: 907, Mode: store.ModeFull},
	)
	if err != nil {
		t.Fatalf("PrepareDelete: %v", err)
	}
	cancel()

	if err := prepared.Delete(); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if len(fake.deleteCalls) != 1 || fake.deleteCalls[0].ctx != ctx {
		t.Fatalf("delete calls = %+v, want exact cancelled bound context", fake.deleteCalls)
	}
	if len(fake.readCalls) != 1 {
		t.Fatalf("read calls = %d, want no post-delete read", len(fake.readCalls))
	}
	if fake.deleteCommitted {
		t.Fatal("cancelled deletion marked committed")
	}
	assertMCPLifecycleTrace(t, fake.trace, "access", "role", "target", "delete")
}
