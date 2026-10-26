package customfield

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

var _ MCPAccessStore = (*store.Store)(nil)

func TestMCPCreateOrderAndDelegation(t *testing.T) {
	accessFailure := errors.New("slug lookup failed")
	maintainer := store.ProjectContext{Project: store.Project{ID: 7}, Role: store.RoleMaintainer}
	contributor := store.ProjectContext{Project: store.Project{ID: 7}, Role: store.RoleContributor}
	ctx := context.Background()
	target := MCPTarget{ProjectSlug: "board", Mode: store.ModeFull}

	t.Run("validation precedes lookup", func(t *testing.T) {
		fake := &recordingStore{accessErr: accessFailure, projectContext: maintainer}
		service := NewMCPService(fake, fake)
		_, err := service.Create(ctx, target, CreateCommand{Name: "  ", Type: "text"})
		if !errors.Is(err, ErrNameRequired) || fake.accessCalls != 0 || fake.createCount() != 0 {
			t.Fatalf("err=%v access=%d creates=%d", err, fake.accessCalls, fake.createCount())
		}
		_, err = service.Create(ctx, target, CreateCommand{Name: "X", Type: "boolean"})
		if !errors.Is(err, ErrInvalidType) || fake.accessCalls != 0 || fake.createCount() != 0 {
			t.Fatalf("type err=%v access=%d creates=%d", err, fake.accessCalls, fake.createCount())
		}
	})

	t.Run("access error after validation", func(t *testing.T) {
		fake := &recordingStore{accessErr: accessFailure}
		service := NewMCPService(fake, fake)
		_, err := service.Create(ctx, target, CreateCommand{Name: "Ticket", Type: "text"})
		if err != accessFailure || fake.accessCalls != 1 || fake.createCount() != 0 {
			t.Fatalf("err=%v access=%d creates=%d", err, fake.accessCalls, fake.createCount())
		}
	})

	t.Run("non-maintainer", func(t *testing.T) {
		fake := &recordingStore{projectContext: contributor}
		service := NewMCPService(fake, fake)
		_, err := service.Create(ctx, target, CreateCommand{Name: "Ticket", Type: "text"})
		if !errors.Is(err, ErrMaintainerRequired) || fake.accessCalls != 1 || fake.createCount() != 0 {
			t.Fatalf("err=%v access=%d creates=%d", err, fake.accessCalls, fake.createCount())
		}
	})

	t.Run("success once", func(t *testing.T) {
		fake := &recordingStore{
			projectContext: maintainer,
			createResult:   store.CustomField{Key: "ticket"},
		}
		service := NewMCPService(fake, fake)
		got, err := service.Create(ctx, target, CreateCommand{Key: "ticket", Name: " Ticket ", Type: " text "})
		if err != nil || got.Key != "ticket" || fake.createCount() != 1 || fake.accessCalls != 1 {
			t.Fatalf("got=%+v err=%v calls=%+v access=%d", got, err, fake.calls, fake.accessCalls)
		}
		if fake.calls[0].projectID != 7 || fake.calls[0].name != "Ticket" {
			t.Fatalf("call=%+v", fake.calls[0])
		}
	})

	t.Run("store error is not retried", func(t *testing.T) {
		storeErr := errors.New("create failed")
		fake := &recordingStore{projectContext: maintainer, createErr: storeErr}
		service := NewMCPService(fake, fake)
		_, err := service.Create(ctx, target, CreateCommand{Name: "Ticket", Type: "text"})
		if err != storeErr || fake.createCount() != 1 {
			t.Fatalf("err=%v creates=%d", err, fake.createCount())
		}
	})
}

func TestMCPDeleteOrderAndDelegation(t *testing.T) {
	accessFailure := errors.New("slug lookup failed")
	maintainer := store.ProjectContext{Project: store.Project{ID: 7}, Role: store.RoleMaintainer}
	contributor := store.ProjectContext{Project: store.Project{ID: 7}, Role: store.RoleContributor}
	ctx := context.Background()
	target := MCPTarget{ProjectSlug: "board", Mode: store.ModeFull}

	t.Run("empty key precedes lookup", func(t *testing.T) {
		fake := &recordingStore{accessErr: accessFailure}
		service := NewMCPService(fake, fake)
		err := service.Delete(ctx, target, DeleteCommand{Key: "  "})
		if !errors.Is(err, ErrKeyRequired) || fake.accessCalls != 0 || fake.deleteCount() != 0 {
			t.Fatalf("err=%v access=%d deletes=%d", err, fake.accessCalls, fake.deleteCount())
		}
	})

	t.Run("non-maintainer", func(t *testing.T) {
		fake := &recordingStore{projectContext: contributor}
		service := NewMCPService(fake, fake)
		err := service.Delete(ctx, target, DeleteCommand{Key: "ticket"})
		if !errors.Is(err, ErrMaintainerRequired) || fake.deleteCount() != 0 {
			t.Fatalf("err=%v deletes=%d", err, fake.deleteCount())
		}
	})

	t.Run("success once", func(t *testing.T) {
		fake := &recordingStore{projectContext: maintainer}
		service := NewMCPService(fake, fake)
		if err := service.Delete(ctx, target, DeleteCommand{Key: "  Ticket  "}); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if fake.deleteCount() != 1 || fake.calls[0].key != "Ticket" || fake.calls[0].projectID != 7 {
			t.Fatalf("calls=%+v", fake.calls)
		}
	})

	t.Run("store error is not retried", func(t *testing.T) {
		storeErr := errors.New("delete failed")
		fake := &recordingStore{projectContext: maintainer, deleteErr: storeErr}
		service := NewMCPService(fake, fake)
		if err := service.Delete(ctx, target, DeleteCommand{Key: "ticket"}); err != storeErr || fake.deleteCount() != 1 {
			t.Fatalf("err=%v deletes=%d", err, fake.deleteCount())
		}
	})
}
