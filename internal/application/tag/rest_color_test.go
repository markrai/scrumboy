package tag

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type restColorCall struct {
	ctx                context.Context
	projectID          int64
	viewerUserID       *int64
	actorUserID        int64
	tagID              int64
	name               string
	color              *string
	linkTemporaryBoard bool
}

type restColorFake struct {
	trace            []string
	mutationErr      error
	returnContextErr bool
	calls            []restColorCall
	publications     []restColorCall
}

func (f *restColorFake) UpdateMyTagColor(
	ctx context.Context,
	userID, tagID int64,
	color *string,
) error {
	f.recordMutation("mine-color", restColorCall{
		ctx: ctx, actorUserID: userID, tagID: tagID, color: cloneString(color),
	})
	return f.result(ctx)
}

func (f *restColorFake) UpdateTagColorForDurableProjectByID(
	ctx context.Context,
	projectID, viewerUserID, tagID int64,
	color *string,
) error {
	f.recordMutation("durable-id-color", restColorCall{
		ctx: ctx, projectID: projectID, viewerUserID: cloneInt64(&viewerUserID), tagID: tagID, color: cloneString(color),
	})
	return f.result(ctx)
}

func (f *restColorFake) UpdateTagColorForTemporaryBoard(
	ctx context.Context,
	projectID int64,
	viewerUserID *int64,
	tagID int64,
	color *string,
) error {
	f.recordMutation("temporary-id-color", restColorCall{
		ctx: ctx, projectID: projectID, viewerUserID: cloneInt64(viewerUserID), tagID: tagID, color: cloneString(color),
	})
	return f.result(ctx)
}

func (f *restColorFake) SetViewerTagColorByName(
	ctx context.Context,
	projectID, viewerUserID int64,
	name string,
	color *string,
) error {
	f.recordMutation("durable-name-color", restColorCall{
		ctx: ctx, projectID: projectID, viewerUserID: cloneInt64(&viewerUserID), name: name, color: cloneString(color),
	})
	return f.result(ctx)
}

func (f *restColorFake) UpdateTagColorForProject(
	ctx context.Context,
	projectID int64,
	viewerUserID *int64,
	name string,
	color *string,
	linkTemporaryBoard bool,
) error {
	f.recordMutation("temporary-name-color", restColorCall{
		ctx:                ctx,
		projectID:          projectID,
		viewerUserID:       cloneInt64(viewerUserID),
		name:               name,
		color:              cloneString(color),
		linkTemporaryBoard: linkTemporaryBoard,
	})
	return f.result(ctx)
}

func (f *restColorFake) PublishTagColorUpdated(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish")
	f.publications = append(f.publications, restColorCall{ctx: ctx, projectID: projectID, name: name})
}

func (f *restColorFake) recordMutation(name string, call restColorCall) {
	f.trace = append(f.trace, name)
	f.calls = append(f.calls, call)
}

func (f *restColorFake) result(ctx context.Context) error {
	if f.returnContextErr {
		return ctx.Err()
	}
	return f.mutationErr
}

func newRESTColorTestService(fake *restColorFake) *RESTColorService {
	return NewRESTColorService(RESTColorServiceDependencies{
		MineColor:          fake,
		DurableIDColor:     fake,
		TemporaryIDColor:   fake,
		DurableNameColor:   fake,
		TemporaryNameColor: fake,
		Publisher:          fake,
	})
}

func TestRESTColorMineIDSequenceAndErrors(t *testing.T) {
	t.Run("success preserves prepared values without publication", func(t *testing.T) {
		fake := &restColorFake{}
		ctx := context.WithValue(context.Background(), restColorContextKey{}, "mine")
		color := "  #aBcDeF  "
		prepared := newRESTColorTestService(fake).PrepareMineID(ctx, MineIDColorCommand{
			ActorUserID: 17,
			TagID:       29,
			Color:       NewColorIntent(&color),
		})
		color = "changed after preparation"

		if err := prepared.Update(); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		assertRESTColorTrace(t, fake, "mine-color")
		if len(fake.calls) != 1 || len(fake.publications) != 0 {
			t.Fatalf("calls = %d, publications = %d, want 1 and 0", len(fake.calls), len(fake.publications))
		}
		call := fake.calls[0]
		if call.ctx != ctx || call.actorUserID != 17 || call.tagID != 29 {
			t.Fatalf("mine call = %#v", call)
		}
		assertRESTColorStringPointer(t, call.color, "  #aBcDeF  ")
	})

	t.Run("mutation failure is unchanged and does not publish", func(t *testing.T) {
		mutationErr := errors.New("mine mutation failed")
		fake := &restColorFake{mutationErr: mutationErr}
		prepared := newRESTColorTestService(fake).PrepareMineID(context.Background(), MineIDColorCommand{
			ActorUserID: 31,
			TagID:       37,
			Color:       NewColorIntent(nil),
		})

		if err := prepared.Update(); err != mutationErr {
			t.Fatalf("Update() error = %v, want identical %v", err, mutationErr)
		}
		assertRESTColorTrace(t, fake, "mine-color")
		if len(fake.publications) != 0 {
			t.Fatalf("publications = %d, want 0", len(fake.publications))
		}
	})
}

func TestRESTColorProjectIDDispatchAndPublication(t *testing.T) {
	tests := []struct {
		name       string
		kind       ProjectKind
		viewerID   *int64
		wantViewer *int64
		wantTrace  []string
	}{
		{
			name:       "durable",
			kind:       DurableProject,
			viewerID:   int64Pointer(41),
			wantViewer: int64Pointer(41),
			wantTrace:  []string{"durable-id-color", "publish"},
		},
		{
			name:       "creator temporary",
			kind:       CreatorOwnedTemporaryBoard,
			viewerID:   int64Pointer(43),
			wantViewer: int64Pointer(43),
			wantTrace:  []string{"temporary-id-color", "publish"},
		},
		{
			name:      "anonymous temporary",
			kind:      AnonymousTemporaryBoard,
			wantTrace: []string{"temporary-id-color", "publish"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restColorFake{}
			ctx := context.WithValue(context.Background(), restColorContextKey{}, tt.name)
			viewerSource := tt.viewerID
			prepared, err := newRESTColorTestService(fake).PrepareProjectID(ctx, ProjectIDColorCommand{
				Project:      ResolvedProject{ProjectID: 47, Kind: tt.kind},
				ViewerUserID: viewerSource,
				TagID:        53,
				Color:        NewColorIntent(stringPointer("#123456")),
			})
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}
			if viewerSource != nil {
				*viewerSource = 999
			}

			if err := prepared.Update(); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			assertRESTColorTrace(t, fake, tt.wantTrace...)
			if len(fake.calls) != 1 || len(fake.publications) != 1 {
				t.Fatalf("calls = %d, publications = %d, want 1 and 1", len(fake.calls), len(fake.publications))
			}
			call := fake.calls[0]
			if call.ctx != ctx || call.projectID != 47 || call.tagID != 53 || !equalInt64Pointers(call.viewerUserID, tt.wantViewer) {
				t.Fatalf("mutation call = %#v, want viewer %v", call, tt.wantViewer)
			}
			assertRESTColorStringPointer(t, call.color, "#123456")
			publication := fake.publications[0]
			if publication.ctx != ctx || publication.projectID != 47 || publication.name != "" {
				t.Fatalf("publication = %#v", publication)
			}
		})
	}
}

func TestRESTColorProjectNameDispatchAndPublication(t *testing.T) {
	tests := []struct {
		name       string
		kind       ProjectKind
		viewerID   *int64
		wantViewer *int64
		wantTrace  []string
		wantLink   bool
	}{
		{
			name:       "durable",
			kind:       DurableProject,
			viewerID:   int64Pointer(59),
			wantViewer: int64Pointer(59),
			wantTrace:  []string{"durable-name-color", "publish"},
		},
		{
			name:       "creator temporary",
			kind:       CreatorOwnedTemporaryBoard,
			viewerID:   int64Pointer(61),
			wantViewer: int64Pointer(61),
			wantTrace:  []string{"temporary-name-color", "publish"},
			wantLink:   true,
		},
		{
			name:      "anonymous temporary",
			kind:      AnonymousTemporaryBoard,
			wantTrace: []string{"temporary-name-color", "publish"},
			wantLink:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restColorFake{}
			ctx := context.WithValue(context.Background(), restColorContextKey{}, tt.name)
			viewerSource := tt.viewerID
			prepared, err := newRESTColorTestService(fake).PrepareProjectName(ctx, ProjectNameColorCommand{
				Project:      ResolvedProject{ProjectID: 67, Kind: tt.kind},
				ViewerUserID: viewerSource,
				Name:         " Name With Spaces ",
				Color:        NewColorIntent(stringPointer("#abcdef")),
			})
			if err != nil {
				t.Fatalf("PrepareProjectName() error = %v", err)
			}
			if viewerSource != nil {
				*viewerSource = 999
			}

			if err := prepared.Update(); err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			assertRESTColorTrace(t, fake, tt.wantTrace...)
			if len(fake.calls) != 1 || len(fake.publications) != 1 {
				t.Fatalf("calls = %d, publications = %d, want 1 and 1", len(fake.calls), len(fake.publications))
			}
			call := fake.calls[0]
			if call.ctx != ctx || call.projectID != 67 || call.name != " Name With Spaces " ||
				!equalInt64Pointers(call.viewerUserID, tt.wantViewer) || call.linkTemporaryBoard != tt.wantLink {
				t.Fatalf("mutation call = %#v", call)
			}
			assertRESTColorStringPointer(t, call.color, "#abcdef")
			publication := fake.publications[0]
			if publication.ctx != ctx || publication.projectID != 67 || publication.name != " Name With Spaces " {
				t.Fatalf("publication = %#v", publication)
			}
		})
	}
}

func TestRESTColorProjectMutationFailuresNeverPublish(t *testing.T) {
	mutationErr := errors.New("project mutation failed")
	tests := []struct {
		name      string
		kind      ProjectKind
		byName    bool
		viewerID  *int64
		wantTrace string
	}{
		{name: "durable ID", kind: DurableProject, viewerID: int64Pointer(71), wantTrace: "durable-id-color"},
		{name: "creator temporary ID", kind: CreatorOwnedTemporaryBoard, viewerID: int64Pointer(73), wantTrace: "temporary-id-color"},
		{name: "anonymous temporary ID", kind: AnonymousTemporaryBoard, wantTrace: "temporary-id-color"},
		{name: "durable name", kind: DurableProject, byName: true, viewerID: int64Pointer(79), wantTrace: "durable-name-color"},
		{name: "creator temporary name", kind: CreatorOwnedTemporaryBoard, byName: true, viewerID: int64Pointer(83), wantTrace: "temporary-name-color"},
		{name: "anonymous temporary name", kind: AnonymousTemporaryBoard, byName: true, wantTrace: "temporary-name-color"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restColorFake{mutationErr: mutationErr}
			service := newRESTColorTestService(fake)
			var err error
			if tt.byName {
				prepared, prepareErr := service.PrepareProjectName(context.Background(), ProjectNameColorCommand{
					Project:      ResolvedProject{ProjectID: 89, Kind: tt.kind},
					ViewerUserID: tt.viewerID,
					Name:         "failure-name",
					Color:        NewColorIntent(nil),
				})
				if prepareErr != nil {
					t.Fatalf("PrepareProjectName() error = %v", prepareErr)
				}
				err = prepared.Update()
			} else {
				prepared, prepareErr := service.PrepareProjectID(context.Background(), ProjectIDColorCommand{
					Project:      ResolvedProject{ProjectID: 89, Kind: tt.kind},
					ViewerUserID: tt.viewerID,
					TagID:        97,
					Color:        NewColorIntent(nil),
				})
				if prepareErr != nil {
					t.Fatalf("PrepareProjectID() error = %v", prepareErr)
				}
				err = prepared.Update()
			}

			if err != mutationErr {
				t.Fatalf("Update() error = %v, want identical %v", err, mutationErr)
			}
			assertRESTColorTrace(t, fake, tt.wantTrace)
			if len(fake.publications) != 0 {
				t.Fatalf("publications = %d, want 0", len(fake.publications))
			}
		})
	}
}

func TestRESTColorRejectsInvalidPreparedProjectCombinations(t *testing.T) {
	tests := []struct {
		name    string
		byName  bool
		kind    ProjectKind
		viewer  *int64
		wantErr error
	}{
		{name: "ID unknown kind", kind: ProjectKind(99), wantErr: ErrInvalidProjectKind},
		{name: "name zero kind", byName: true, wantErr: ErrInvalidProjectKind},
		{name: "durable ID missing actor", kind: DurableProject, wantErr: ErrActorRequired},
		{name: "durable name missing actor", byName: true, kind: DurableProject, wantErr: ErrActorRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restColorFake{}
			service := newRESTColorTestService(fake)
			if tt.byName {
				prepared, err := service.PrepareProjectName(context.Background(), ProjectNameColorCommand{
					Project: ResolvedProject{ProjectID: 101, Kind: tt.kind}, ViewerUserID: tt.viewer,
				})
				if prepared != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("PrepareProjectName() = (%v, %v), want (nil, %v)", prepared, err, tt.wantErr)
				}
			} else {
				prepared, err := service.PrepareProjectID(context.Background(), ProjectIDColorCommand{
					Project: ResolvedProject{ProjectID: 101, Kind: tt.kind}, ViewerUserID: tt.viewer,
				})
				if prepared != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, %v)", prepared, err, tt.wantErr)
				}
			}
			assertRESTColorTrace(t, fake)
		})
	}
}

func TestRESTColorPreparedProjectUsesCancelledBoundContext(t *testing.T) {
	fake := &restColorFake{returnContextErr: true}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := newRESTColorTestService(fake).PrepareProjectID(ctx, ProjectIDColorCommand{
		Project:      ResolvedProject{ProjectID: 103, Kind: DurableProject},
		ViewerUserID: int64Pointer(107),
		TagID:        109,
		Color:        NewColorIntent(nil),
	})
	if err != nil {
		t.Fatalf("PrepareProjectID() error = %v", err)
	}
	cancel()

	if err := prepared.Update(); err != context.Canceled {
		t.Fatalf("Update() error = %v, want context.Canceled", err)
	}
	assertRESTColorTrace(t, fake, "durable-id-color")
	if len(fake.calls) != 1 || fake.calls[0].ctx != ctx {
		t.Fatalf("calls = %#v, want exact bound context", fake.calls)
	}
	if len(fake.publications) != 0 {
		t.Fatalf("publications = %d, want 0", len(fake.publications))
	}
}

func TestRESTColorNilPublisherIsNoOp(t *testing.T) {
	fake := &restColorFake{}
	service := NewRESTColorService(RESTColorServiceDependencies{
		DurableIDColor: fake,
	})
	prepared, err := service.PrepareProjectID(context.Background(), ProjectIDColorCommand{
		Project:      ResolvedProject{ProjectID: 113, Kind: DurableProject},
		ViewerUserID: int64Pointer(127),
		TagID:        131,
	})
	if err != nil {
		t.Fatalf("PrepareProjectID() error = %v", err)
	}
	if err := prepared.Update(); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
}

type restColorContextKey struct{}

func assertRESTColorTrace(t *testing.T, fake *restColorFake, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(fake.trace, want) {
		t.Fatalf("trace = %v, want %v", fake.trace, want)
	}
}

func assertRESTColorStringPointer(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("color = %s, want %q", describeStringPointer(got), want)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func equalInt64Pointers(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
