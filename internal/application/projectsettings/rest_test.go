package projectsettings

import (
	"context"
	"errors"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type mutationFake struct {
	project     store.Project
	authErr     error
	updateErr   error
	updateCalls int
	lastPatch   store.ProjectBoardSettingsPatch
	result      store.ProjectBoardSettings
}

func (f *mutationFake) CheckCanManageProject(context.Context, int64, int64) error {
	return f.authErr
}

func (f *mutationFake) GetProject(context.Context, int64) (store.Project, error) {
	return f.project, nil
}

func (f *mutationFake) UpdateProjectBoardSettings(_ context.Context, _, _ int64, patch store.ProjectBoardSettingsPatch) (store.ProjectBoardSettings, error) {
	f.updateCalls++
	f.lastPatch = patch
	if f.updateErr != nil {
		return store.ProjectBoardSettings{}, f.updateErr
	}
	return f.result, nil
}

type refreshFake struct {
	calls   int
	reasons []string
}

func (f *refreshFake) PublishBoardRefresh(_ context.Context, _ int64, reason string) {
	f.calls++
	f.reasons = append(f.reasons, reason)
}

func preparedSettings(t *testing.T, deps RESTServiceDependencies) *PreparedREST {
	t.Helper()
	ctx := store.WithUserID(context.Background(), 1)
	prepared, err := NewRESTService(deps).Prepare(ctx, ResolvedRESTTarget{ProjectID: 9})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return prepared
}

func TestPrepareRequiresActorAndManageAuth(t *testing.T) {
	svc := NewRESTService(RESTServiceDependencies{Mutations: &mutationFake{}})
	if _, err := svc.Prepare(context.Background(), ResolvedRESTTarget{ProjectID: 9}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare = %v, want ErrActorRequired", err)
	}
	if _, err := NewRESTService(RESTServiceDependencies{
		Mutations: &mutationFake{authErr: store.ErrForbidden},
	}).Prepare(store.WithUserID(context.Background(), 1), ResolvedRESTTarget{ProjectID: 9}); !errors.Is(err, store.ErrForbidden) {
		t.Fatalf("Prepare = %v, want ErrForbidden", err)
	}
}

func TestPatchPublishesOnceAfterSuccessfulMutation(t *testing.T) {
	refresh := &refreshFake{}
	mutations := &mutationFake{result: store.ProjectBoardSettings{
		DefaultSprintWeeks: 1,
		SprintsEnabled:     true,
		AgendaEnabled:      true,
		AgendaTimezone:     "UTC",
		AgendaTitle:        "Agenda",
		AgendaColor:        store.DefaultAgendaColor,
	}}
	weeks := 1
	tz := "UTC"
	view, err := preparedSettings(t, RESTServiceDependencies{Mutations: mutations, Refresh: refresh}).Patch(PatchCommand{
		DefaultSprintWeeks: &weeks,
		AgendaTimezone:     &tz,
	})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if view.DefaultSprintWeeks != 1 || view.AgendaTimezone != "UTC" {
		t.Fatalf("view=%+v", view)
	}
	if mutations.updateCalls != 1 {
		t.Fatalf("update calls=%d, want 1", mutations.updateCalls)
	}
	if refresh.calls != 1 || (len(refresh.reasons) > 0 && refresh.reasons[0] != refreshReasonProjectSettingsUpdated) {
		t.Fatalf("refresh calls=%d reasons=%v, want 1 %s", refresh.calls, refresh.reasons, refreshReasonProjectSettingsUpdated)
	}
}

func TestPatchDoesNotPublishWhenStoreFails(t *testing.T) {
	refresh := &refreshFake{}
	mutations := &mutationFake{updateErr: store.ErrValidation}
	tz := "America/Chicago"
	weeks := 1
	if _, err := preparedSettings(t, RESTServiceDependencies{Mutations: mutations, Refresh: refresh}).Patch(PatchCommand{
		DefaultSprintWeeks: &weeks,
		AgendaTimezone:     &tz,
	}); !errors.Is(err, store.ErrValidation) {
		t.Fatalf("Patch = %v, want ErrValidation", err)
	}
	if refresh.calls != 0 {
		t.Fatalf("refresh calls=%d, want 0", refresh.calls)
	}
}

func TestPatchAgendaOnTemporaryBoardFailsClosed(t *testing.T) {
	refresh := &refreshFake{}
	expires := time.Now().UTC().Add(time.Hour)
	mutations := &mutationFake{project: store.Project{ID: 9, ExpiresAt: &expires}}
	tz := "UTC"
	weeks := 1
	if _, err := preparedSettings(t, RESTServiceDependencies{Mutations: mutations, Refresh: refresh}).Patch(PatchCommand{
		DefaultSprintWeeks: &weeks,
		AgendaTimezone:     &tz,
	}); !errors.Is(err, ErrDurableRequired) {
		t.Fatalf("Patch = %v, want ErrDurableRequired", err)
	}
	if mutations.updateCalls != 0 {
		t.Fatalf("update calls=%d, want 0", mutations.updateCalls)
	}
	if refresh.calls != 0 {
		t.Fatalf("refresh calls=%d, want 0", refresh.calls)
	}
}

func TestPatchSprintOnlyAllowsTemporaryBoard(t *testing.T) {
	refresh := &refreshFake{}
	expires := time.Now().UTC().Add(time.Hour)
	weeks := 1
	mutations := &mutationFake{
		project: store.Project{ID: 9, ExpiresAt: &expires},
		result:  store.ProjectBoardSettings{DefaultSprintWeeks: 1, SprintsEnabled: true},
	}
	if _, err := preparedSettings(t, RESTServiceDependencies{Mutations: mutations, Refresh: refresh}).Patch(PatchCommand{
		DefaultSprintWeeks: &weeks,
	}); err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if mutations.updateCalls != 1 {
		t.Fatalf("update calls=%d, want 1", mutations.updateCalls)
	}
	if refresh.calls != 1 {
		t.Fatalf("refresh calls=%d, want 1", refresh.calls)
	}
}
