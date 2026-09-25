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

type restDeletionContextKey struct{}

type restDeletionFake struct {
	trace       []string
	err         error
	calls       int
	requesterID int64
	targetID    int64
	marker      any
}

func (f *restDeletionFake) DeleteUser(
	ctx context.Context,
	requesterID int64,
	targetUserID int64,
) error {
	f.trace = append(f.trace, "delete")
	f.calls++
	f.requesterID = requesterID
	f.targetID = targetUserID
	f.marker = ctx.Value(restDeletionContextKey{})
	return f.err
}

func newRESTDeletionTestService() (*RESTDeletionService, *restDeletionFake) {
	deletions := &restDeletionFake{}
	return NewRESTDeletionService(RESTDeletionServiceDependencies{
		Deletions: deletions,
	}), deletions
}

func TestRESTDeletionServicePrepareRequiresActor(t *testing.T) {
	service, deletions := newRESTDeletionTestService()

	prepared, err := service.Prepare(context.Background(), DeleteCommand{TargetUserID: 41})
	if prepared != nil || !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
	}
	if deletions.calls != 0 || len(deletions.trace) != 0 {
		t.Fatalf("missing actor reached deletion: calls=%d trace=%v", deletions.calls, deletions.trace)
	}
}

func TestRESTDeletionServiceSuccessBindsExactValuesAndContext(t *testing.T) {
	service, deletions := newRESTDeletionTestService()
	ctx := store.WithUserID(
		context.WithValue(context.Background(), restDeletionContextKey{}, "rest-delete-context"),
		17,
	)

	prepared, err := service.Prepare(ctx, DeleteCommand{TargetUserID: 41})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if deletions.calls != 0 {
		t.Fatalf("Prepare() performed deletion: calls=%d", deletions.calls)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete(): %v", err)
	}
	if !reflect.DeepEqual(deletions.trace, []string{"delete"}) || deletions.calls != 1 {
		t.Fatalf("delete trace/calls = %v/%d, want [delete]/1", deletions.trace, deletions.calls)
	}
	if deletions.requesterID != 17 || deletions.targetID != 41 || deletions.marker != "rest-delete-context" {
		t.Fatalf(
			"delete capture = requester:%d target:%d marker:%v",
			deletions.requesterID,
			deletions.targetID,
			deletions.marker,
		)
	}
}

func TestRESTDeletionServiceSelfTargetReachesDeletionPort(t *testing.T) {
	service, deletions := newRESTDeletionTestService()
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
	if deletions.calls != 1 || deletions.requesterID != 23 || deletions.targetID != 23 {
		t.Fatalf(
			"self-target delete = calls:%d requester:%d target:%d",
			deletions.calls,
			deletions.requesterID,
			deletions.targetID,
		)
	}
}

func TestRESTDeletionServicePreservesMutationErrors(t *testing.T) {
	for _, wantErr := range []error{
		store.ErrUnauthorized,
		store.ErrForbidden,
		store.ErrNotFound,
		store.ErrValidation,
		store.ErrConflict,
		errors.New("delete failed"),
	} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			service, deletions := newRESTDeletionTestService()
			deletions.err = wantErr
			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 17),
				DeleteCommand{TargetUserID: 41},
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

func TestRESTDeletionServicePreparedDeleteExecutesMutationAtMostOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		firstErr error
	}{
		{name: "successful deletion"},
		{name: "deletion failure", firstErr: errors.New("first deletion failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, deletions := newRESTDeletionTestService()
			deletions.err = tc.firstErr
			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 17),
				DeleteCommand{TargetUserID: 41},
			)
			if err != nil {
				t.Fatalf("Prepare(): %v", err)
			}

			if err := prepared.Delete(); err != tc.firstErr {
				t.Fatalf("first Delete() error = %v, want %v", err, tc.firstErr)
			}
			traceAfterFirst := append([]string(nil), deletions.trace...)
			if err := prepared.Delete(); !errors.Is(err, ErrPreparedMutationAlreadyExecuted) {
				t.Fatalf("second Delete() error = %v, want ErrPreparedMutationAlreadyExecuted", err)
			}
			if deletions.calls != 1 || !reflect.DeepEqual(deletions.trace, traceAfterFirst) {
				t.Fatalf("second Delete() reached store: calls=%d trace=%v", deletions.calls, deletions.trace)
			}
		})
	}
}

func TestRESTDeletionServicePreparedValuesAndContextAreOwned(t *testing.T) {
	for _, targetID := range []int64{52, 0, -53} {
		t.Run(strconv.FormatInt(targetID, 10), func(t *testing.T) {
			service, deletions := newRESTDeletionTestService()
			ctx := store.WithUserID(
				context.WithValue(context.Background(), restDeletionContextKey{}, targetID),
				19,
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
			if deletions.requesterID != 19 || deletions.targetID != targetID || deletions.marker != targetID {
				t.Fatalf(
					"bound delete = requester:%d target:%d marker:%v",
					deletions.requesterID,
					deletions.targetID,
					deletions.marker,
				)
			}
		})
	}
}
