package publicboard

import (
	"context"
	"errors"
	"testing"

	"scrumboy/internal/store"
)

type publicationMutationFake struct {
	calls   int
	ctx     context.Context
	project int64
	actor   int64
	enabled bool
	result  store.ProjectPublicationState
	err     error
}

type publicationRevokerFake struct {
	projects []int64
}

func (f *publicationRevokerFake) RevokePublicProject(projectID int64) {
	f.projects = append(f.projects, projectID)
}

func (f *publicationMutationFake) UpdateProjectPublicViewing(ctx context.Context, projectID, actorUserID int64, enabled bool) (store.ProjectPublicationState, error) {
	f.calls++
	f.ctx = ctx
	f.project = projectID
	f.actor = actorUserID
	f.enabled = enabled
	return f.result, f.err
}

func TestPublicationServiceRequiresFullEnabledCapability(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 7)
	for _, tc := range []struct {
		name    string
		mode    store.Mode
		enabled bool
	}{
		{name: "full flag off", mode: store.ModeFull},
		{name: "anonymous flag off", mode: store.ModeAnonymous},
		{name: "anonymous flag on", mode: store.ModeAnonymous, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &publicationMutationFake{}
			service := NewPublicationService(PublicationServiceOptions{Mutations: fake, Mode: tc.mode, PublicProjectsEnabled: tc.enabled})
			if _, err := service.SetPublication(ctx, PublicationCommand{ProjectID: 9, Enabled: true}); !errors.Is(err, ErrPublicationCapabilityDisabled) {
				t.Fatalf("SetPublication error = %v, want capability disabled", err)
			}
			if fake.calls != 0 {
				t.Fatalf("store calls = %d, want 0", fake.calls)
			}
		})
	}
}

func TestPublicationServiceRequiresAuthenticatedActor(t *testing.T) {
	fake := &publicationMutationFake{}
	service := NewPublicationService(PublicationServiceOptions{Mutations: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
	if _, err := service.SetPublication(context.Background(), PublicationCommand{ProjectID: 9, Enabled: true}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("SetPublication error = %v, want actor required", err)
	}
	if fake.calls != 0 {
		t.Fatalf("store calls = %d, want 0", fake.calls)
	}
}

func TestPublicationServiceDelegatesOnceAndReturnsNarrowState(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 7)
	fake := &publicationMutationFake{result: store.ProjectPublicationState{ProjectID: 9, Slug: "ignite", Enabled: true, Changed: true}}
	service := NewPublicationService(PublicationServiceOptions{Mutations: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
	result, err := service.SetPublication(ctx, PublicationCommand{ProjectID: 9, Enabled: true})
	if err != nil {
		t.Fatalf("SetPublication: %v", err)
	}
	if fake.calls != 1 || fake.ctx != ctx || fake.project != 9 || fake.actor != 7 || !fake.enabled {
		t.Fatalf("delegation = calls:%d project:%d actor:%d enabled:%v", fake.calls, fake.project, fake.actor, fake.enabled)
	}
	if result.ProjectID != 9 || result.Slug != "ignite" || !result.Enabled || !result.Changed {
		t.Fatalf("result = %+v", result)
	}
}

func TestPublicationServiceReturnsStoreErrorUnchanged(t *testing.T) {
	wantErr := errors.New("mutation failed")
	fake := &publicationMutationFake{err: wantErr}
	service := NewPublicationService(PublicationServiceOptions{Mutations: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
	_, err := service.SetPublication(store.WithUserID(context.Background(), 7), PublicationCommand{ProjectID: 9})
	if err != wantErr || fake.calls != 1 {
		t.Fatalf("SetPublication error/calls = %v/%d, want exact error/1", err, fake.calls)
	}
}

func TestPublicationServiceRevokesSynchronouslyOnlyAfterChangedDisable(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 7)
	for _, tc := range []struct {
		name       string
		state      store.ProjectPublicationState
		storeError error
		want       []int64
	}{
		{name: "changed disable", state: store.ProjectPublicationState{ProjectID: 9, Slug: "ignite", Changed: true}},
		{name: "changed enable", state: store.ProjectPublicationState{ProjectID: 9, Slug: "ignite", Enabled: true, Changed: true}},
		{name: "disable no-op", state: store.ProjectPublicationState{ProjectID: 9, Slug: "ignite"}},
		{name: "store failure", storeError: errors.New("failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revoker := &publicationRevokerFake{}
			fake := &publicationMutationFake{result: tc.state, err: tc.storeError}
			service := NewPublicationService(PublicationServiceOptions{
				Mutations: fake, Revoker: revoker, Mode: store.ModeFull, PublicProjectsEnabled: true,
			})
			_, _ = service.SetPublication(ctx, PublicationCommand{ProjectID: 9, Enabled: tc.state.Enabled})
			if tc.name == "changed disable" {
				tc.want = []int64{9}
			}
			if len(revoker.projects) != len(tc.want) {
				t.Fatalf("revocations = %v, want %v", revoker.projects, tc.want)
			}
		})
	}
}

type publicationStatusFake struct {
	calls  int
	actor  int64
	result store.ProjectPublicationStatus
}

func (f *publicationStatusFake) GetProjectPublicationStatus(ctx context.Context, projectID, actorUserID int64) (store.ProjectPublicationStatus, error) {
	f.calls++
	f.actor = actorUserID
	return f.result, nil
}

func TestPublicationServiceGetPublicationGatesAndDelegates(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 7)
	for _, tc := range []struct {
		name    string
		mode    store.Mode
		enabled bool
		ctx     context.Context
		want    error
	}{
		{name: "anonymous mode", mode: store.ModeAnonymous, enabled: true, ctx: ctx, want: ErrPublicationCapabilityDisabled},
		{name: "operator gate off", mode: store.ModeFull, enabled: false, ctx: ctx, want: ErrPublicationCapabilityDisabled},
		{name: "no actor", mode: store.ModeFull, enabled: true, ctx: context.Background(), want: ErrActorRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &publicationStatusFake{}
			service := NewPublicationService(PublicationServiceOptions{Status: fake, Mode: tc.mode, PublicProjectsEnabled: tc.enabled})
			if _, err := service.GetPublication(tc.ctx, 9); !errors.Is(err, tc.want) || fake.calls != 0 {
				t.Fatalf("err=%v calls=%d", err, fake.calls)
			}
		})
	}
	fake := &publicationStatusFake{result: store.ProjectPublicationStatus{ProjectID: 9, Slug: "ignite", Enabled: true, Publishable: true}}
	service := NewPublicationService(PublicationServiceOptions{Status: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
	got, err := service.GetPublication(ctx, 9)
	if err != nil || fake.calls != 1 || fake.actor != 7 || !got.Enabled || !got.Publishable || got.Slug != "ignite" {
		t.Fatalf("got=%+v err=%v calls=%d actor=%d", got, err, fake.calls, fake.actor)
	}
}
