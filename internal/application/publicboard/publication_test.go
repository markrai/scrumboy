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
