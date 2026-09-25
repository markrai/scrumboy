package useradmin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"scrumboy/internal/store"
)

type mcpDeletionContextKey struct{}

type mcpDeletionReadFake struct {
	trace  *[]string
	user   store.User
	err    error
	calls  int
	userID int64
	marker any
}

func (f *mcpDeletionReadFake) GetUser(ctx context.Context, userID int64) (store.User, error) {
	*f.trace = append(*f.trace, "requester-read")
	f.calls++
	f.userID = userID
	f.marker = ctx.Value(mcpDeletionContextKey{})
	return f.user, f.err
}

type mcpDeletionFake struct {
	trace       *[]string
	err         error
	calls       int
	requesterID int64
	targetID    int64
	marker      any
}

func (f *mcpDeletionFake) DeleteUser(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
) error {
	*f.trace = append(*f.trace, "delete")
	f.calls++
	f.requesterID = requesterID
	f.targetID = targetUserID
	f.marker = ctx.Value(mcpDeletionContextKey{})
	return f.err
}

func newMCPDeletionTestService(
	trace *[]string,
) (*MCPDeletionService, *mcpDeletionReadFake, *mcpDeletionFake) {
	requester := &mcpDeletionReadFake{
		trace: trace,
		user:  store.User{ID: 23, SystemRole: store.SystemRoleOwner},
	}
	deletions := &mcpDeletionFake{trace: trace}
	service := NewMCPDeletionService(MCPDeletionServiceDependencies{
		RequesterRead: requester,
		Deletions:     deletions,
	})
	return service, requester, deletions
}

func TestMCPDeletionServicePrepareRequiresActor(t *testing.T) {
	trace := []string{}
	service, requester, deletions := newMCPDeletionTestService(&trace)

	prepared, err := service.Prepare(context.Background(), DeleteCommand{TargetUserID: 61})
	if prepared != nil || !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
	}
	if requester.calls != 0 || deletions.calls != 0 || len(trace) != 0 {
		t.Fatalf("missing actor reached dependencies: trace=%v", trace)
	}
}

func TestMCPDeletionServiceRequesterReadFailureStopsDeletion(t *testing.T) {
	trace := []string{}
	service, requester, deletions := newMCPDeletionTestService(&trace)
	wantErr := errors.New("requester read failed")
	requester.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 23),
		DeleteCommand{TargetUserID: 61},
	)
	if prepared != nil || err != wantErr {
		t.Fatalf("Prepare() = (%v, %v), want (nil, original error)", prepared, err)
	}
	if requester.calls != 1 || deletions.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read"}) {
		t.Fatalf("requester failure calls=%d/%d trace=%v", requester.calls, deletions.calls, trace)
	}
}

func TestMCPDeletionServiceRequiresExactOwner(t *testing.T) {
	for _, role := range []store.SystemRole{store.SystemRoleUser, store.SystemRoleAdmin} {
		t.Run(role.String(), func(t *testing.T) {
			trace := []string{}
			service, requester, deletions := newMCPDeletionTestService(&trace)
			requester.user.SystemRole = role

			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 23),
				DeleteCommand{TargetUserID: 61},
			)
			if prepared != nil || !errors.Is(err, ErrOwnerRequired) {
				t.Fatalf("Prepare() = (%v, %v), want (nil, ErrOwnerRequired)", prepared, err)
			}
			if requester.calls != 1 || deletions.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read"}) {
				t.Fatalf("role gate calls=%d/%d trace=%v", requester.calls, deletions.calls, trace)
			}
		})
	}
}

func TestMCPDeletionServiceSuccessOrderAndArguments(t *testing.T) {
	trace := []string{}
	service, requester, deletions := newMCPDeletionTestService(&trace)
	ctx := store.WithUserID(
		context.WithValue(context.Background(), mcpDeletionContextKey{}, "mcp-delete-context"),
		23,
	)

	prepared, err := service.Prepare(ctx, DeleteCommand{TargetUserID: 61})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	wantTrace := []string{"requester-read", "delete"}
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("trace = %v, want %v", trace, wantTrace)
	}
	if requester.calls != 1 || deletions.calls != 1 || requester.userID != 23 ||
		deletions.requesterID != 23 || deletions.targetID != 61 {
		t.Fatalf(
			"calls/IDs = requester:%d delete:%d read-id:%d delete-requester:%d target:%d",
			requester.calls,
			deletions.calls,
			requester.userID,
			deletions.requesterID,
			deletions.targetID,
		)
	}
	if requester.marker != "mcp-delete-context" || deletions.marker != "mcp-delete-context" {
		t.Fatalf("context markers = requester:%v delete:%v", requester.marker, deletions.marker)
	}
}

func TestMCPDeletionServiceSelfTargetOrdering(t *testing.T) {
	t.Run("non-owner stops before deletion", func(t *testing.T) {
		trace := []string{}
		service, requester, deletions := newMCPDeletionTestService(&trace)
		requester.user.SystemRole = store.SystemRoleAdmin

		prepared, err := service.Prepare(
			store.WithUserID(context.Background(), 23),
			DeleteCommand{TargetUserID: 23},
		)
		if prepared != nil || !errors.Is(err, ErrOwnerRequired) {
			t.Fatalf("Prepare() = (%v, %v), want (nil, ErrOwnerRequired)", prepared, err)
		}
		if deletions.calls != 0 || !reflect.DeepEqual(trace, []string{"requester-read"}) {
			t.Fatalf("non-owner self-target reached delete: calls=%d trace=%v", deletions.calls, trace)
		}
	})

	t.Run("owner reaches deletion port", func(t *testing.T) {
		trace := []string{}
		service, _, deletions := newMCPDeletionTestService(&trace)
		wantErr := fmt.Errorf("%w: cannot delete yourself", store.ErrValidation)
		deletions.err = wantErr

		prepared, err := service.Prepare(
			store.WithUserID(context.Background(), 23),
			DeleteCommand{TargetUserID: 23},
		)
		if err != nil {
			t.Fatalf("Prepare(): %v", err)
		}
		if err := prepared.Delete(); err != wantErr {
			t.Fatalf("Delete() error = %v, want original %v", err, wantErr)
		}
		if deletions.calls != 1 || deletions.requesterID != 23 || deletions.targetID != 23 ||
			!reflect.DeepEqual(trace, []string{"requester-read", "delete"}) {
			t.Fatalf(
				"owner self-target = calls:%d requester:%d target:%d trace:%v",
				deletions.calls,
				deletions.requesterID,
				deletions.targetID,
				trace,
			)
		}
	})
}

func TestMCPDeletionServicePreservesMutationErrors(t *testing.T) {
	for _, wantErr := range []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		store.ErrValidation,
		store.ErrConflict,
		errors.New("delete failed"),
	} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			trace := []string{}
			service, _, deletions := newMCPDeletionTestService(&trace)
			deletions.err = wantErr
			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 23),
				DeleteCommand{TargetUserID: 61},
			)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}
			if err := prepared.Delete(); err != wantErr {
				t.Fatalf("Delete() error = %v, want exact %v", err, wantErr)
			}
			if deletions.calls != 1 {
				t.Fatalf("delete calls = %d, want 1", deletions.calls)
			}
		})
	}
}

func TestMCPDeletionServicePreparedDeleteExecutesMutationAtMostOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		firstErr error
	}{
		{name: "successful deletion"},
		{name: "deletion failure", firstErr: errors.New("first deletion failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := []string{}
			service, requester, deletions := newMCPDeletionTestService(&trace)
			deletions.err = tc.firstErr
			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 23),
				DeleteCommand{TargetUserID: 61},
			)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}

			if err := prepared.Delete(); err != tc.firstErr {
				t.Fatalf("first Delete() error = %v, want %v", err, tc.firstErr)
			}
			traceAfterFirst := append([]string(nil), trace...)
			if err := prepared.Delete(); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("second Delete() error = %v, want ErrPreparedMutationAlreadyExecuted", err)
			}
			if requester.calls != 1 || deletions.calls != 1 || !reflect.DeepEqual(trace, traceAfterFirst) {
				t.Fatalf(
					"repeat calls/trace = requester:%d delete:%d trace:%v",
					requester.calls,
					deletions.calls,
					trace,
				)
			}
		})
	}
}

func TestMCPDeletionServicePreparedValuesAndContextAreOwned(t *testing.T) {
	for _, targetID := range []int64{72, 0, -73} {
		t.Run(strconv.FormatInt(targetID, 10), func(t *testing.T) {
			trace := []string{}
			service, requester, deletions := newMCPDeletionTestService(&trace)
			ctx := store.WithUserID(
				context.WithValue(context.Background(), mcpDeletionContextKey{}, targetID),
				23,
			)
			command := DeleteCommand{TargetUserID: targetID}

			prepared, err := service.Prepare(ctx, command)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}
			command.TargetUserID = 999
			ctx = context.Background()

			if err := prepared.Delete(); err != nil {
				t.Fatalf("Delete(): %v", err)
			}
			if requester.userID != 23 || deletions.requesterID != 23 || deletions.targetID != targetID ||
				requester.marker != targetID || deletions.marker != targetID {
				t.Fatalf(
					"bound values = read-id:%d requester:%d target:%d read-marker:%v delete-marker:%v",
					requester.userID,
					deletions.requesterID,
					deletions.targetID,
					requester.marker,
					deletions.marker,
				)
			}
		})
	}
}
