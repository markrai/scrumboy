package tag

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"scrumboy/internal/store"
)

type restDeletionCall struct {
	ctx              context.Context
	projectID        int64
	actorID          int64
	tagID            int64
	name             string
	isAnonymousBoard bool
}

type restDeletionFake struct {
	trace           []string
	calls           []restDeletionCall
	publications    []restDeletionCall
	affected        []int64
	operationErrors map[string]error
	boardTagID      int64
	boardNameErr    error
	personalTagID   int64
	personalNameErr error
	contextErrors   bool
}

func (f *restDeletionFake) DeleteMyTagByID(
	ctx context.Context,
	userID, tagID int64,
) ([]int64, error) {
	f.record("mine-id-delete", restDeletionCall{ctx: ctx, actorID: userID, tagID: tagID})
	if err := f.result(ctx, "mine-id-delete"); err != nil {
		return nil, err
	}
	return f.affected, nil
}

func (f *restDeletionFake) DeleteMyTagByName(
	ctx context.Context,
	projectID, userID int64,
	name string,
) ([]int64, error) {
	f.record("mine-name-delete", restDeletionCall{
		ctx: ctx, projectID: projectID, actorID: userID, name: name,
	})
	if err := f.result(ctx, "mine-name-delete"); err != nil {
		return nil, err
	}
	return f.affected, nil
}

func (f *restDeletionFake) DeleteTagForDurableProjectByID(
	ctx context.Context,
	projectID, userID, tagID int64,
) ([]int64, error) {
	f.record("durable-id-delete", restDeletionCall{
		ctx: ctx, projectID: projectID, actorID: userID, tagID: tagID,
	})
	if err := f.result(ctx, "durable-id-delete"); err != nil {
		return nil, err
	}
	return f.affected, nil
}

func (f *restDeletionFake) DeleteTag(
	ctx context.Context,
	userID, tagID int64,
	isAnonymousBoard bool,
) error {
	f.record("row-delete", restDeletionCall{
		ctx: ctx, actorID: userID, tagID: tagID, isAnonymousBoard: isAnonymousBoard,
	})
	return f.result(ctx, "row-delete")
}

func (f *restDeletionFake) GetBoardScopedTagIDByName(
	ctx context.Context,
	projectID int64,
	name string,
) (int64, error) {
	f.record("board-name-read", restDeletionCall{ctx: ctx, projectID: projectID, name: name})
	if f.contextErrors {
		return 0, ctx.Err()
	}
	if f.boardNameErr != nil {
		return 0, f.boardNameErr
	}
	return f.boardTagID, nil
}

func (f *restDeletionFake) GetTagIDByName(
	ctx context.Context,
	userID int64,
	name string,
) (int64, error) {
	f.record("personal-name-read", restDeletionCall{ctx: ctx, actorID: userID, name: name})
	if f.contextErrors {
		return 0, ctx.Err()
	}
	if f.personalNameErr != nil {
		return 0, f.personalNameErr
	}
	return f.personalTagID, nil
}

func (f *restDeletionFake) PublishTagDeleted(ctx context.Context, projectID int64, name string) {
	f.trace = append(f.trace, "publish")
	f.publications = append(f.publications, restDeletionCall{ctx: ctx, projectID: projectID, name: name})
}

func (f *restDeletionFake) record(operation string, call restDeletionCall) {
	f.trace = append(f.trace, operation)
	f.calls = append(f.calls, call)
}

func (f *restDeletionFake) result(ctx context.Context, operation string) error {
	if f.contextErrors {
		return ctx.Err()
	}
	return f.operationErrors[operation]
}

func newRESTDeletionTestService(fake *restDeletionFake) *RESTDeletionService {
	return NewRESTDeletionService(RESTDeletionServiceDependencies{
		MineID:        fake,
		MineName:      fake,
		DurableID:     fake,
		Rows:          fake,
		BoardNames:    fake,
		PersonalNames: fake,
		Publisher:     fake,
	})
}

func TestRESTDeletionMineIDFanout(t *testing.T) {
	t.Run("deduplicates affected projects without inventing an origin", func(t *testing.T) {
		fake := &restDeletionFake{affected: []int64{7, 7, 3, 11, 3}}
		ctx := context.WithValue(context.Background(), restDeletionContextKey{}, "mine")
		prepared := newRESTDeletionTestService(fake).PrepareMineID(ctx, MineIDDeleteCommand{
			ActorUserID: 17,
			TagID:       19,
		})

		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "mine-id-delete", "publish", "publish", "publish")
		assertRESTDeletionMutation(t, fake.calls[0], ctx, 0, 17, 19, "", false)
		assertRESTDeletionPublications(t, fake, ctx, "", 7, 3, 11)
	})

	t.Run("zero affected projects publishes nothing", func(t *testing.T) {
		fake := &restDeletionFake{}
		prepared := newRESTDeletionTestService(fake).PrepareMineID(context.Background(), MineIDDeleteCommand{
			ActorUserID: 23,
			TagID:       29,
		})
		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "mine-id-delete")
	})

	t.Run("failure preserves identity and publishes nothing", func(t *testing.T) {
		deleteErr := errors.New("mine delete failed")
		fake := &restDeletionFake{operationErrors: map[string]error{"mine-id-delete": deleteErr}}
		prepared := newRESTDeletionTestService(fake).PrepareMineID(context.Background(), MineIDDeleteCommand{
			ActorUserID: 31,
			TagID:       37,
		})
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertRESTDeletionTrace(t, fake, "mine-id-delete")
		if len(fake.publications) != 0 {
			t.Fatalf("publications = %d, want 0", len(fake.publications))
		}
	})
}

func TestRESTDeletionProjectIDDispatchAndPublication(t *testing.T) {
	t.Run("durable uses compatibility method and origin-first affected fanout", func(t *testing.T) {
		fake := &restDeletionFake{affected: []int64{47, 53, 47, 59, 53}}
		ctx := context.WithValue(context.Background(), restDeletionContextKey{}, "durable")
		actorSource := int64Pointer(41)
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectID(ctx, ProjectIDDeleteCommand{
			Project:     ResolvedProject{ProjectID: 47, Kind: DurableProject},
			ActorUserID: actorSource,
			TagID:       43,
		})
		if err != nil {
			t.Fatalf("PrepareProjectID() error = %v", err)
		}
		*actorSource = 999

		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "durable-id-delete", "publish", "publish", "publish")
		assertRESTDeletionMutation(t, fake.calls[0], ctx, 47, 41, 43, "", false)
		assertRESTDeletionPublications(t, fake, ctx, "", 47, 53, 59)
	})

	tests := []struct {
		name          string
		kind          ProjectKind
		actorSource   *int64
		wantActor     int64
		wantAnonymous bool
	}{
		{name: "creator temporary with actor", kind: CreatorOwnedTemporaryBoard, actorSource: int64Pointer(61), wantActor: 61},
		{name: "creator temporary without actor", kind: CreatorOwnedTemporaryBoard},
		{name: "anonymous temporary with actor", kind: AnonymousTemporaryBoard, actorSource: int64Pointer(67), wantActor: 67, wantAnonymous: true},
		{name: "anonymous temporary without actor", kind: AnonymousTemporaryBoard, wantAnonymous: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restDeletionFake{}
			ctx := context.WithValue(context.Background(), restDeletionContextKey{}, tt.name)
			prepared, err := newRESTDeletionTestService(fake).PrepareProjectID(ctx, ProjectIDDeleteCommand{
				Project:     ResolvedProject{ProjectID: 71, Kind: tt.kind},
				ActorUserID: tt.actorSource,
				TagID:       73,
			})
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}
			if tt.actorSource != nil {
				*tt.actorSource = 999
			}

			if err := prepared.Delete(); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			assertRESTDeletionTrace(t, fake, "row-delete", "publish")
			assertRESTDeletionMutation(t, fake.calls[0], ctx, 0, tt.wantActor, 73, "", tt.wantAnonymous)
			assertRESTDeletionPublications(t, fake, ctx, "", 71)
		})
	}
}

func TestRESTDeletionProjectIDFailureSilence(t *testing.T) {
	deleteErr := errors.New("project ID delete failed")
	tests := []struct {
		name      string
		kind      ProjectKind
		actor     *int64
		operation string
	}{
		{name: "durable", kind: DurableProject, actor: int64Pointer(79), operation: "durable-id-delete"},
		{name: "creator temporary", kind: CreatorOwnedTemporaryBoard, actor: int64Pointer(83), operation: "row-delete"},
		{name: "anonymous temporary", kind: AnonymousTemporaryBoard, operation: "row-delete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restDeletionFake{operationErrors: map[string]error{tt.operation: deleteErr}}
			prepared, err := newRESTDeletionTestService(fake).PrepareProjectID(context.Background(), ProjectIDDeleteCommand{
				Project:     ResolvedProject{ProjectID: 89, Kind: tt.kind},
				ActorUserID: tt.actor,
				TagID:       97,
			})
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}
			if err := prepared.Delete(); err != deleteErr {
				t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
			}
			assertRESTDeletionTrace(t, fake, tt.operation)
		})
	}
}

func TestRESTDeletionProjectNameDurableAndCreatorTemporary(t *testing.T) {
	t.Run("durable deletes personal name and fans out exact name", func(t *testing.T) {
		fake := &restDeletionFake{affected: []int64{103, 107, 103, 109}}
		ctx := context.WithValue(context.Background(), restDeletionContextKey{}, "durable-name")
		actorSource := int64Pointer(101)
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(ctx, ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 103, Kind: DurableProject},
			ActorUserID: actorSource,
			Name:        " Name With Spaces ",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		*actorSource = 999

		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "mine-name-delete", "publish", "publish", "publish")
		assertRESTDeletionMutation(t, fake.calls[0], ctx, 103, 101, 0, " Name With Spaces ", false)
		assertRESTDeletionPublications(t, fake, ctx, " Name With Spaces ", 103, 107, 109)
	})

	t.Run("durable failure preserves identity and publishes nothing", func(t *testing.T) {
		deleteErr := errors.New("durable name delete failed")
		fake := &restDeletionFake{operationErrors: map[string]error{"mine-name-delete": deleteErr}}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 109, Kind: DurableProject},
			ActorUserID: int64Pointer(107),
			Name:        "failed-name",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertRESTDeletionTrace(t, fake, "mine-name-delete")
	})

	t.Run("creator temporary is rejected before every collaborator", func(t *testing.T) {
		fake := &restDeletionFake{}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project: ResolvedProject{ProjectID: 113, Kind: CreatorOwnedTemporaryBoard},
			Name:    "blocked",
		})
		if prepared != nil || !errors.Is(err, ErrNameDeletionNotAllowed) {
			t.Fatalf("PrepareProjectName() = (%v, %v), want (nil, ErrNameDeletionNotAllowed)", prepared, err)
		}
		assertRESTDeletionTrace(t, fake)
	})

	t.Run("board-scoped row delete failure never publishes", func(t *testing.T) {
		deleteErr := errors.New("board row delete failed")
		fake := &restDeletionFake{
			boardTagID: 133,
			operationErrors: map[string]error{
				"row-delete": deleteErr,
			},
		}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project: ResolvedProject{ProjectID: 137, Kind: AnonymousTemporaryBoard},
			Name:    "board-delete-error",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read", "row-delete")
	})
}

func TestRESTDeletionAnonymousNameResolution(t *testing.T) {
	t.Run("board-scoped row wins and deletes as user zero", func(t *testing.T) {
		fake := &restDeletionFake{boardTagID: 127}
		ctx := context.WithValue(context.Background(), restDeletionContextKey{}, "board-row")
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(ctx, ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 131, Kind: AnonymousTemporaryBoard},
			ActorUserID: int64Pointer(137),
			Name:        "board-name",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}

		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read", "row-delete", "publish")
		assertRESTDeletionMutation(t, fake.calls[1], ctx, 0, 0, 127, "", true)
		assertRESTDeletionPublications(t, fake, ctx, "board-name", 131)
	})

	t.Run("personal fallback lookup failure never deletes or publishes", func(t *testing.T) {
		lookupErr := errors.New("personal lookup failed")
		fake := &restDeletionFake{
			boardNameErr:    store.ErrNotFound,
			personalNameErr: lookupErr,
		}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 181, Kind: AnonymousTemporaryBoard},
			ActorUserID: int64Pointer(179),
			Name:        "personal-lookup-error",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); err != lookupErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, lookupErr)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read", "personal-name-read")
	})

	t.Run("not found falls back to copied actor personal row", func(t *testing.T) {
		fake := &restDeletionFake{
			boardNameErr:  store.ErrNotFound,
			personalTagID: 139,
		}
		ctx := context.WithValue(context.Background(), restDeletionContextKey{}, "personal-row")
		actorSource := int64Pointer(149)
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(ctx, ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 151, Kind: AnonymousTemporaryBoard},
			ActorUserID: actorSource,
			Name:        "personal-name",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		*actorSource = 999

		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read", "personal-name-read", "row-delete", "publish")
		assertRESTDeletionMutation(t, fake.calls[1], ctx, 0, 149, 0, "personal-name", false)
		assertRESTDeletionMutation(t, fake.calls[2], ctx, 0, 149, 139, "", true)
		assertRESTDeletionPublications(t, fake, ctx, "personal-name", 151)
	})

	t.Run("missing actor is checked only after board miss", func(t *testing.T) {
		fake := &restDeletionFake{boardNameErr: store.ErrNotFound}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project: ResolvedProject{ProjectID: 157, Kind: AnonymousTemporaryBoard},
			Name:    "missing-actor",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); !errors.Is(err, ErrActorRequired) {
			t.Fatalf("Delete() error = %v, want ErrActorRequired", err)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read")
	})

	t.Run("non-not-found board error does not fall back", func(t *testing.T) {
		lookupErr := errors.New("board lookup failed")
		fake := &restDeletionFake{boardNameErr: lookupErr, personalTagID: 163}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 167, Kind: AnonymousTemporaryBoard},
			ActorUserID: int64Pointer(173),
			Name:        "lookup-error",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); err != lookupErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, lookupErr)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read")
	})

	t.Run("selected row delete failure never publishes", func(t *testing.T) {
		deleteErr := errors.New("row delete failed")
		fake := &restDeletionFake{
			boardNameErr:  store.ErrNotFound,
			personalTagID: 179,
			operationErrors: map[string]error{
				"row-delete": deleteErr,
			},
		}
		prepared, err := newRESTDeletionTestService(fake).PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
			Project:     ResolvedProject{ProjectID: 181, Kind: AnonymousTemporaryBoard},
			ActorUserID: int64Pointer(191),
			Name:        "delete-error",
		})
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertRESTDeletionTrace(t, fake, "board-name-read", "personal-name-read", "row-delete")
	})
}

func TestRESTDeletionRejectsInvalidPreparedCombinations(t *testing.T) {
	tests := []struct {
		name    string
		byName  bool
		kind    ProjectKind
		actorID *int64
		wantErr error
	}{
		{name: "durable ID missing actor", kind: DurableProject, wantErr: ErrActorRequired},
		{name: "durable name missing actor", byName: true, kind: DurableProject, wantErr: ErrActorRequired},
		{name: "unknown ID kind", kind: ProjectKind(99), wantErr: ErrInvalidDeletionProjectKind},
		{name: "unknown name kind", byName: true, kind: ProjectKind(99), wantErr: ErrInvalidDeletionProjectKind},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &restDeletionFake{}
			service := newRESTDeletionTestService(fake)
			if tt.byName {
				prepared, err := service.PrepareProjectName(context.Background(), ProjectNameDeleteCommand{
					Project: ResolvedProject{ProjectID: 193, Kind: tt.kind}, ActorUserID: tt.actorID,
				})
				if prepared != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("PrepareProjectName() = (%v, %v), want (nil, %v)", prepared, err, tt.wantErr)
				}
			} else {
				prepared, err := service.PrepareProjectID(context.Background(), ProjectIDDeleteCommand{
					Project: ResolvedProject{ProjectID: 193, Kind: tt.kind}, ActorUserID: tt.actorID,
				})
				if prepared != nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, %v)", prepared, err, tt.wantErr)
				}
			}
			assertRESTDeletionTrace(t, fake)
		})
	}
}

func TestRESTDeletionUsesCancelledBoundContext(t *testing.T) {
	fake := &restDeletionFake{contextErrors: true}
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := newRESTDeletionTestService(fake).PrepareProjectID(ctx, ProjectIDDeleteCommand{
		Project:     ResolvedProject{ProjectID: 197, Kind: DurableProject},
		ActorUserID: int64Pointer(199),
		TagID:       211,
	})
	if err != nil {
		t.Fatalf("PrepareProjectID() error = %v", err)
	}
	cancel()

	if err := prepared.Delete(); err != context.Canceled {
		t.Fatalf("Delete() error = %v, want context.Canceled", err)
	}
	assertRESTDeletionTrace(t, fake, "durable-id-delete")
	if fake.calls[0].ctx != ctx {
		t.Fatal("deletion did not use the bound context")
	}
}

func TestRESTDeletionNilPublisherIsNoOp(t *testing.T) {
	fake := &restDeletionFake{}
	service := NewRESTDeletionService(RESTDeletionServiceDependencies{DurableID: fake})
	prepared, err := service.PrepareProjectID(context.Background(), ProjectIDDeleteCommand{
		Project:     ResolvedProject{ProjectID: 223, Kind: DurableProject},
		ActorUserID: int64Pointer(227),
		TagID:       229,
	})
	if err != nil {
		t.Fatalf("PrepareProjectID() error = %v", err)
	}
	if err := prepared.Delete(); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

type restDeletionContextKey struct{}

func assertRESTDeletionTrace(t *testing.T, fake *restDeletionFake, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(fake.trace, want) {
		t.Fatalf("trace = %v, want %v", fake.trace, want)
	}
}

func assertRESTDeletionMutation(
	t *testing.T,
	got restDeletionCall,
	wantCtx context.Context,
	wantProjectID, wantActorID, wantTagID int64,
	wantName string,
	wantAnonymous bool,
) {
	t.Helper()
	if got.ctx != wantCtx || got.projectID != wantProjectID || got.actorID != wantActorID ||
		got.tagID != wantTagID || got.name != wantName || got.isAnonymousBoard != wantAnonymous {
		t.Fatalf("call = %#v, want context/project/actor/tag/name/anonymous = %v/%d/%d/%d/%q/%v",
			got, wantCtx, wantProjectID, wantActorID, wantTagID, wantName, wantAnonymous)
	}
}

func assertRESTDeletionPublications(
	t *testing.T,
	fake *restDeletionFake,
	wantCtx context.Context,
	wantName string,
	wantProjectIDs ...int64,
) {
	t.Helper()
	if len(fake.publications) != len(wantProjectIDs) {
		t.Fatalf("publications = %#v, want project IDs %v", fake.publications, wantProjectIDs)
	}
	for i, wantProjectID := range wantProjectIDs {
		got := fake.publications[i]
		if got.ctx != wantCtx || got.projectID != wantProjectID || got.name != wantName {
			t.Fatalf("publication[%d] = %#v, want context/project/name = %v/%d/%q", i, got, wantCtx, wantProjectID, wantName)
		}
	}
}
