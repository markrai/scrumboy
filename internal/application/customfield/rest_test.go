package customfield

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

var _ MutationStore = (*store.Store)(nil)

type restCtxKey struct{}

func TestRESTPrepareAuthorization(t *testing.T) {
	fake := &recordingStore{}
	service := NewRESTService(fake)
	ctx := context.WithValue(context.Background(), restCtxKey{}, "rest")

	t.Run("maintainer and owner succeed", func(t *testing.T) {
		for _, role := range []store.ProjectRole{store.RoleMaintainer, store.RoleOwner} {
			prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 9, Role: role})
			if err != nil {
				t.Fatalf("%s: %v", role, err)
			}
			if prepared == nil || prepared.projectID != 9 || prepared.ctx != ctx || prepared.service != service {
				t.Fatalf("%s prepared=%+v", role, prepared)
			}
		}
		if fake.createCount()+fake.deleteCount()+fake.accessCalls != 0 {
			t.Fatalf("prepare mutated store: %+v access=%d", fake.calls, fake.accessCalls)
		}
	})

	t.Run("contributor and viewer fail without an operation", func(t *testing.T) {
		for _, role := range []store.ProjectRole{store.RoleContributor, store.RoleViewer} {
			prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 9, Role: role})
			if !errors.Is(err, ErrMaintainerRequired) {
				t.Fatalf("%s error=%v", role, err)
			}
			if prepared != nil {
				t.Fatalf("%s returned a prepared mutation", role)
			}
		}
		if fake.createCount()+fake.deleteCount() != 0 {
			t.Fatalf("failed prepare mutated store: %+v", fake.calls)
		}
	})
}

func TestRESTPreparedCreateDelegatesOnce(t *testing.T) {
	fake := &recordingStore{createResult: store.CustomField{Key: "ticket", Name: "Ticket", Type: store.CustomFieldText}}
	service := NewRESTService(fake)
	ctx := context.WithValue(context.Background(), restCtxKey{}, "create")
	prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 9, Role: store.RoleMaintainer})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	t.Run("success", func(t *testing.T) {
		fake.calls = nil
		got, err := prepared.Create(CreateCommand{Key: "ticket", Name: "  Ticket  ", Type: " text "})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if got.Key != "ticket" || fake.createCount() != 1 || fake.accessCalls != 0 {
			t.Fatalf("got=%+v calls=%+v access=%d", got, fake.calls, fake.accessCalls)
		}
		call := fake.calls[0]
		if call.ctx != ctx || call.projectID != 9 || call.name != "Ticket" || call.typ != store.CustomFieldText || call.key != "ticket" {
			t.Fatalf("call=%+v", call)
		}
	})

	t.Run("empty name", func(t *testing.T) {
		fake.calls = nil
		_, err := prepared.Create(CreateCommand{Name: "  ", Type: "text"})
		if !errors.Is(err, ErrNameRequired) || fake.createCount() != 0 {
			t.Fatalf("err=%v calls=%d", err, fake.createCount())
		}
	})

	t.Run("invalid type", func(t *testing.T) {
		fake.calls = nil
		_, err := prepared.Create(CreateCommand{Name: "X", Type: "boolean"})
		if !errors.Is(err, ErrInvalidType) || fake.createCount() != 0 {
			t.Fatalf("err=%v calls=%d", err, fake.createCount())
		}
	})

	t.Run("store error is not retried", func(t *testing.T) {
		storeErr := errors.New("create failed")
		fake.calls = nil
		fake.createErr = storeErr
		_, err := prepared.Create(CreateCommand{Name: "X", Type: "text"})
		if err != storeErr || fake.createCount() != 1 {
			t.Fatalf("err=%v calls=%d", err, fake.createCount())
		}
		fake.createErr = nil
	})
}

func TestRESTPreparedDeleteDelegatesOnce(t *testing.T) {
	fake := &recordingStore{}
	service := NewRESTService(fake)
	ctx := context.WithValue(context.Background(), restCtxKey{}, "delete")
	prepared, err := service.Prepare(ctx, ResolvedRESTMutationTarget{ProjectID: 4, Role: store.RoleOwner})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	t.Run("success", func(t *testing.T) {
		if err := prepared.Delete(DeleteCommand{Key: "  ticket  "}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.deleteCount() != 1 || fake.calls[0].key != "ticket" || fake.calls[0].projectID != 4 || fake.calls[0].ctx != ctx {
			t.Fatalf("calls=%+v", fake.calls)
		}
	})

	t.Run("empty key", func(t *testing.T) {
		fake.calls = nil
		if err := prepared.Delete(DeleteCommand{Key: "   "}); !errors.Is(err, ErrKeyRequired) || fake.deleteCount() != 0 {
			t.Fatalf("err=%v calls=%d", err, fake.deleteCount())
		}
	})

	t.Run("store error is not retried", func(t *testing.T) {
		storeErr := errors.New("delete failed")
		fake.calls = nil
		fake.deleteErr = storeErr
		if err := prepared.Delete(DeleteCommand{Key: "ticket"}); err != storeErr || fake.deleteCount() != 1 {
			t.Fatalf("err=%v calls=%d", err, fake.deleteCount())
		}
	})
}
