package project

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type restUpdateFake struct {
	trace projectServiceTrace

	projects []store.Project
	getErrs  []error
	getCalls int
	getCtxs  []context.Context
	getIDs   []int64

	imageErr       error
	imageCalls     int
	imageProjectID int64
	imageActorID   int64
	image          *string
	dominantColor  string
	onImage        func()

	nameErr       error
	nameCalls     int
	nameProjectID int64
	nameActorID   int64
	name          string

	publishCalls     int
	publishProjectID int64
	publishCtx       context.Context
}

func (f *restUpdateFake) GetProject(ctx context.Context, projectID int64) (store.Project, error) {
	f.trace.add("get-project")
	f.getCtxs = append(f.getCtxs, ctx)
	f.getIDs = append(f.getIDs, projectID)
	index := f.getCalls
	f.getCalls++
	if index < len(f.getErrs) && f.getErrs[index] != nil {
		return store.Project{}, f.getErrs[index]
	}
	if index < len(f.projects) {
		return f.projects[index], nil
	}
	return store.Project{ID: 1}, nil
}

func (f *restUpdateFake) UpdateProjectImage(
	_ context.Context,
	projectID int64,
	actorID int64,
	image *string,
	dominantColor string,
) error {
	f.trace.add("update-image")
	f.imageCalls++
	f.imageProjectID = projectID
	f.imageActorID = actorID
	f.image = cloneString(image)
	f.dominantColor = dominantColor
	if f.onImage != nil {
		f.onImage()
	}
	if image != nil {
		*image = "mutated-by-image-store"
	}
	return f.imageErr
}

func (f *restUpdateFake) UpdateProjectName(
	_ context.Context,
	projectID int64,
	actorID int64,
	name string,
) error {
	f.trace.add("update-name")
	f.nameCalls++
	f.nameProjectID = projectID
	f.nameActorID = actorID
	f.name = name
	return f.nameErr
}

func (f *restUpdateFake) PublishProjectUpdated(ctx context.Context, projectID int64) {
	f.trace.add("publish-project-updated")
	f.publishCalls++
	f.publishProjectID = projectID
	f.publishCtx = ctx
}

func newRESTUpdateTestService(fake *restUpdateFake) *RESTUpdateService {
	return NewRESTUpdateService(RESTUpdateServiceDependencies{
		Projects:  fake,
		Images:    fake,
		Names:     fake,
		Publisher: fake,
	})
}

func TestRESTUpdatePreparationPreservesModeSpecificTargetOrdering(t *testing.T) {
	t.Run("full mode performs no preparation read", func(t *testing.T) {
		fake := &restUpdateFake{}
		prepared, err := newRESTUpdateTestService(fake).Prepare(
			store.WithUserID(context.Background(), 7),
			RESTUpdateTarget{ProjectID: 41, Mode: store.ModeFull},
		)
		if err != nil || prepared == nil {
			t.Fatalf("Prepare() = %v, %v", prepared, err)
		}
		assertProjectServiceTrace(t, &fake.trace)
		if prepared.actorID == nil || *prepared.actorID != 7 || prepared.projectID != 41 {
			t.Fatalf("prepared identity = %+v", prepared)
		}
	})

	t.Run("anonymous active creatorless board reads before binding actor", func(t *testing.T) {
		expires := time.Now().UTC().Add(24 * time.Hour)
		fake := &restUpdateFake{projects: []store.Project{{ID: 42, ExpiresAt: &expires}}}
		prepared, err := newRESTUpdateTestService(fake).Prepare(
			context.Background(),
			RESTUpdateTarget{ProjectID: 42, Mode: store.ModeAnonymous},
		)
		if err != nil || prepared == nil || prepared.actorID != nil {
			t.Fatalf("Prepare() = %+v, %v", prepared, err)
		}
		assertProjectServiceTrace(t, &fake.trace, "get-project")
		if len(fake.getIDs) != 1 || fake.getIDs[0] != 42 {
			t.Fatalf("GetProject IDs = %v, want [42]", fake.getIDs)
		}
	})

	t.Run("anonymous hidden target states return not found", func(t *testing.T) {
		now := time.Now().UTC()
		creatorID := int64(5)
		for _, tt := range []struct {
			name    string
			project store.Project
		}{
			{name: "durable", project: store.Project{ID: 1}},
			{name: "creator owned", project: store.Project{ID: 1, ExpiresAt: projectServiceTime(now.Add(time.Hour)), CreatorUserID: &creatorID}},
			{name: "expired", project: store.Project{ID: 1, ExpiresAt: projectServiceTime(now.Add(-time.Hour))}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				fake := &restUpdateFake{projects: []store.Project{tt.project}}
				prepared, err := newRESTUpdateTestService(fake).Prepare(context.Background(), RESTUpdateTarget{ProjectID: 1, Mode: store.ModeAnonymous})
				if prepared != nil || !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("Prepare() = %+v, %v, want not found", prepared, err)
				}
				assertProjectServiceTrace(t, &fake.trace, "get-project")
			})
		}
	})

	t.Run("anonymous read failure is unchanged", func(t *testing.T) {
		wantErr := errors.New("anonymous read failed")
		fake := &restUpdateFake{getErrs: []error{wantErr}}
		prepared, err := newRESTUpdateTestService(fake).Prepare(context.Background(), RESTUpdateTarget{ProjectID: 1, Mode: store.ModeAnonymous})
		if prepared != nil || err != wantErr {
			t.Fatalf("Prepare() = %+v, %v, want exact read error", prepared, err)
		}
	})
}

func TestRESTUpdateExecutionSequencesAndPublication(t *testing.T) {
	ctx := store.WithUserID(context.Background(), 17)

	tests := []struct {
		name            string
		command         RESTUpdateCommand
		wantTrace       []string
		wantImageCalls  int
		wantNameCalls   int
		wantPublish     int
		wantNameActorID int64
	}{
		{
			name:      "empty patch reads without publication",
			command:   RESTUpdateCommand{},
			wantTrace: []string{"get-project"},
		},
		{
			name:           "image only",
			command:        RESTUpdateCommand{Image: projectServiceString("not-a-data-url")},
			wantTrace:      []string{"update-image", "get-project", "publish-project-updated"},
			wantImageCalls: 1,
			wantPublish:    1,
		},
		{
			name:            "name only",
			command:         RESTUpdateCommand{Name: projectServiceString("  Raw Name  ")},
			wantTrace:       []string{"update-name", "get-project", "publish-project-updated"},
			wantNameCalls:   1,
			wantPublish:     1,
			wantNameActorID: 17,
		},
		{
			name:            "image before name",
			command:         RESTUpdateCommand{Name: projectServiceString("Both"), Image: projectServiceString("bad-image")},
			wantTrace:       []string{"update-image", "update-name", "get-project", "publish-project-updated"},
			wantImageCalls:  1,
			wantNameCalls:   1,
			wantPublish:     1,
			wantNameActorID: 17,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			image := "result-image"
			fake := &restUpdateFake{projects: []store.Project{{ID: 91, Name: "result", Image: &image}}}
			prepared, err := newRESTUpdateTestService(fake).Prepare(ctx, RESTUpdateTarget{ProjectID: 91, Mode: store.ModeFull})
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			result, err := prepared.Update(tt.command)
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			assertProjectServiceTrace(t, &fake.trace, tt.wantTrace...)
			if fake.imageCalls != tt.wantImageCalls || fake.nameCalls != tt.wantNameCalls || fake.publishCalls != tt.wantPublish {
				t.Fatalf("calls image/name/publish = %d/%d/%d", fake.imageCalls, fake.nameCalls, fake.publishCalls)
			}
			if fake.nameCalls > 0 && fake.nameActorID != tt.wantNameActorID {
				t.Fatalf("name actor ID = %d, want %d", fake.nameActorID, tt.wantNameActorID)
			}
			if fake.imageCalls > 0 && fake.dominantColor != "#888888" {
				t.Fatalf("dominant color = %q, want fallback", fake.dominantColor)
			}
			if result.ID != 91 || result.Name != "result" {
				t.Fatalf("result = %+v", result)
			}
			if len(fake.getIDs) != 1 || fake.getIDs[0] != 91 {
				t.Fatalf("GetProject IDs = %v, want [91]", fake.getIDs)
			}
			assertProjectServiceContext(t, fake.getCtxs[0], ctx)
			if fake.publishCalls > 0 {
				assertProjectServiceContext(t, fake.publishCtx, ctx)
				if fake.publishProjectID != 91 {
					t.Fatalf("published project ID = %d", fake.publishProjectID)
				}
			}
		})
	}
}

func TestRESTUpdatePreservesMissingActorAndPartialFailureContracts(t *testing.T) {
	t.Run("image requires actor before any collaborator", func(t *testing.T) {
		fake := &restUpdateFake{}
		prepared, err := newRESTUpdateTestService(fake).Prepare(context.Background(), RESTUpdateTarget{ProjectID: 1, Mode: store.ModeFull})
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		_, err = prepared.Update(RESTUpdateCommand{Image: projectServiceString("image")})
		if !errors.Is(err, ErrActorRequired) {
			t.Fatalf("Update() error = %v, want actor required", err)
		}
		assertProjectServiceTrace(t, &fake.trace)
	})

	t.Run("name without actor passes user zero", func(t *testing.T) {
		fake := &restUpdateFake{projects: []store.Project{{ID: 2}}}
		prepared, _ := newRESTUpdateTestService(fake).Prepare(context.Background(), RESTUpdateTarget{ProjectID: 2, Mode: store.ModeFull})
		if _, err := prepared.Update(RESTUpdateCommand{Name: projectServiceString("name")}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if fake.nameActorID != 0 {
			t.Fatalf("name actor ID = %d, want 0", fake.nameActorID)
		}
	})

	t.Run("image failure stops name read and publication", func(t *testing.T) {
		wantErr := errors.New("image failed")
		fake := &restUpdateFake{imageErr: wantErr}
		prepared, _ := newRESTUpdateTestService(fake).Prepare(store.WithUserID(context.Background(), 7), RESTUpdateTarget{ProjectID: 3, Mode: store.ModeFull})
		_, err := prepared.Update(RESTUpdateCommand{Name: projectServiceString("name"), Image: projectServiceString("image")})
		if err != wantErr {
			t.Fatalf("Update() error = %v, want exact image error", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "update-image")
	})

	t.Run("image success then name failure stops read and publication", func(t *testing.T) {
		wantErr := errors.New("name failed")
		fake := &restUpdateFake{nameErr: wantErr}
		prepared, _ := newRESTUpdateTestService(fake).Prepare(store.WithUserID(context.Background(), 7), RESTUpdateTarget{ProjectID: 4, Mode: store.ModeFull})
		_, err := prepared.Update(RESTUpdateCommand{Name: projectServiceString("name"), Image: projectServiceString("image")})
		if err != wantErr {
			t.Fatalf("Update() error = %v, want exact name error", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "update-image", "update-name")
		if fake.imageCalls != 1 || fake.nameCalls != 1 {
			t.Fatalf("partial mutation calls = %d/%d", fake.imageCalls, fake.nameCalls)
		}
	})

	t.Run("post-read failure follows both writes without publication", func(t *testing.T) {
		wantErr := errors.New("post-read failed")
		fake := &restUpdateFake{getErrs: []error{wantErr}}
		prepared, _ := newRESTUpdateTestService(fake).Prepare(store.WithUserID(context.Background(), 7), RESTUpdateTarget{ProjectID: 5, Mode: store.ModeFull})
		_, err := prepared.Update(RESTUpdateCommand{Name: projectServiceString("name"), Image: projectServiceString("image")})
		if err != wantErr {
			t.Fatalf("Update() error = %v, want exact post-read error", err)
		}
		assertProjectServiceTrace(t, &fake.trace, "update-image", "update-name", "get-project")
		if fake.imageCalls != 1 || fake.nameCalls != 1 || fake.getCalls != 1 || fake.publishCalls != 0 {
			t.Fatalf("calls image/name/get/publish = %d/%d/%d/%d", fake.imageCalls, fake.nameCalls, fake.getCalls, fake.publishCalls)
		}
	})

	t.Run("optional values are copied before first mutation", func(t *testing.T) {
		name := "original name"
		image := "original image"
		fake := &restUpdateFake{projects: []store.Project{{ID: 6}}}
		fake.onImage = func() {
			name = "changed during image mutation"
			image = "changed during image mutation"
		}
		prepared, _ := newRESTUpdateTestService(fake).Prepare(store.WithUserID(context.Background(), 7), RESTUpdateTarget{ProjectID: 6, Mode: store.ModeFull})
		if _, err := prepared.Update(RESTUpdateCommand{Name: &name, Image: &image}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if fake.name != "original name" || fake.image == nil || *fake.image != "original image" {
			t.Fatalf("captured name/image = %q/%v", fake.name, fake.image)
		}
	})

	t.Run("nil publisher is a safe no-op", func(t *testing.T) {
		fake := &restUpdateFake{projects: []store.Project{{ID: 7}}}
		service := NewRESTUpdateService(RESTUpdateServiceDependencies{Projects: fake, Images: fake, Names: fake})
		prepared, _ := service.Prepare(store.WithUserID(context.Background(), 7), RESTUpdateTarget{ProjectID: 7, Mode: store.ModeFull})
		if _, err := prepared.Update(RESTUpdateCommand{Name: projectServiceString("same")}); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if !reflect.DeepEqual(fake.trace.steps, []string{"update-name", "get-project"}) {
			t.Fatalf("trace = %v", fake.trace.steps)
		}
	})
}
