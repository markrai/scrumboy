package tag

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

type mcpDeletionCall struct {
	ctx              context.Context
	projectSlug      string
	mode             store.Mode
	projectID        int64
	actorID          int64
	tagID            int64
	isAnonymousBoard bool
}

type mcpDeletionFake struct {
	trace          []string
	calls          []mcpDeletionCall
	projectContext store.ProjectContext
	mineTags       []store.TagWithColor
	accessErr      error
	mineReadErr    error
	projectReadErr error
	deleteErr      error
	contextErrors  bool
}

func (f *mcpDeletionFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.record("project-access", mcpDeletionCall{ctx: ctx, projectSlug: slug, mode: mode})
	if f.contextErrors {
		return store.ProjectContext{}, ctx.Err()
	}
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpDeletionFake) ListUserTags(
	ctx context.Context,
	userID int64,
) ([]store.TagWithColor, error) {
	f.record("mine-read", mcpDeletionCall{ctx: ctx, actorID: userID})
	if f.contextErrors {
		return nil, ctx.Err()
	}
	if f.mineReadErr != nil {
		return nil, f.mineReadErr
	}
	return f.mineTags, nil
}

func (f *mcpDeletionFake) GetProjectScopedTagByID(
	ctx context.Context,
	projectID, tagID int64,
) (store.TagWithColor, error) {
	f.record("project-tag-read", mcpDeletionCall{ctx: ctx, projectID: projectID, tagID: tagID})
	if f.contextErrors {
		return store.TagWithColor{}, ctx.Err()
	}
	if f.projectReadErr != nil {
		return store.TagWithColor{}, f.projectReadErr
	}
	return store.TagWithColor{TagID: tagID, Name: "project-row"}, nil
}

func (f *mcpDeletionFake) DeleteTag(
	ctx context.Context,
	userID, tagID int64,
	isAnonymousBoard bool,
) error {
	f.record("row-delete", mcpDeletionCall{
		ctx: ctx, actorID: userID, tagID: tagID, isAnonymousBoard: isAnonymousBoard,
	})
	if f.contextErrors {
		return ctx.Err()
	}
	return f.deleteErr
}

func (f *mcpDeletionFake) record(operation string, call mcpDeletionCall) {
	f.trace = append(f.trace, operation)
	f.calls = append(f.calls, call)
}

func newMCPDeletionTestService(fake *mcpDeletionFake) *MCPDeletionService {
	return NewMCPDeletionService(MCPDeletionServiceDependencies{
		Access:        fake,
		MineRead:      fake,
		ProjectScoped: fake,
		Rows:          fake,
	})
}

func TestMCPDeletionMineIDSequenceAndErrors(t *testing.T) {
	t.Run("success uses one pre-read and one row deletion", func(t *testing.T) {
		fake := &mcpDeletionFake{mineTags: []store.TagWithColor{
			{TagID: 7, Name: "other"},
			{TagID: 11, Name: "target"},
		}}
		ctx := store.WithUserID(context.Background(), 5)
		prepared, err := newMCPDeletionTestService(fake).PrepareMineID(ctx, MCPMineIDDeletionTarget{TagID: 11})
		if err != nil {
			t.Fatalf("PrepareMineID() error = %v", err)
		}
		if err := prepared.Delete(); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		assertMCPDeletionTrace(t, fake, "mine-read", "row-delete")
		if len(fake.calls) != 2 || fake.calls[0].ctx != ctx || fake.calls[0].actorID != 5 {
			t.Fatalf("mine read calls = %#v", fake.calls)
		}
		assertMCPDeletionRowCall(t, fake.calls[1], ctx, 5, 11, false)
	})

	t.Run("missing actor wins before mine read", func(t *testing.T) {
		fake := &mcpDeletionFake{}
		prepared, err := newMCPDeletionTestService(fake).PrepareMineID(context.Background(), MCPMineIDDeletionTarget{TagID: 13})
		if prepared != nil || !errors.Is(err, ErrActorRequired) {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertMCPDeletionTrace(t, fake)
	})

	t.Run("absent exact ID returns not found without deletion", func(t *testing.T) {
		fake := &mcpDeletionFake{mineTags: []store.TagWithColor{{TagID: 17}}}
		prepared, err := newMCPDeletionTestService(fake).PrepareMineID(
			store.WithUserID(context.Background(), 19),
			MCPMineIDDeletionTarget{TagID: 23},
		)
		if prepared != nil || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, store.ErrNotFound)", prepared, err)
		}
		assertMCPDeletionTrace(t, fake, "mine-read")
	})

	t.Run("read error preserves identity", func(t *testing.T) {
		readErr := errors.New("mine read failed")
		fake := &mcpDeletionFake{mineReadErr: readErr}
		prepared, err := newMCPDeletionTestService(fake).PrepareMineID(
			store.WithUserID(context.Background(), 29),
			MCPMineIDDeletionTarget{TagID: 31},
		)
		if prepared != nil || err != readErr {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, identical %v)", prepared, err, readErr)
		}
		assertMCPDeletionTrace(t, fake, "mine-read")
	})

	t.Run("delete error preserves identity without post-read or retry", func(t *testing.T) {
		deleteErr := errors.New("mine delete failed")
		fake := &mcpDeletionFake{
			mineTags:  []store.TagWithColor{{TagID: 37}},
			deleteErr: deleteErr,
		}
		prepared, err := newMCPDeletionTestService(fake).PrepareMineID(
			store.WithUserID(context.Background(), 41),
			MCPMineIDDeletionTarget{TagID: 37},
		)
		if err != nil {
			t.Fatalf("PrepareMineID() error = %v", err)
		}
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertMCPDeletionTrace(t, fake, "mine-read", "row-delete")
	})
}

func TestMCPDeletionProjectIDSequenceAndClassification(t *testing.T) {
	expiresAt := time.Unix(1_800_000_000, 0)
	creatorID := int64(43)
	tests := []struct {
		name          string
		project       store.Project
		wantAnonymous bool
	}{
		{name: "durable", project: store.Project{ID: 47}},
		{name: "creator temporary", project: store.Project{ID: 47, ExpiresAt: &expiresAt, CreatorUserID: &creatorID}},
		{name: "anonymous temporary", project: store.Project{ID: 47, ExpiresAt: &expiresAt}, wantAnonymous: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpDeletionFake{projectContext: store.ProjectContext{
				Project: tt.project,
				Role:    store.RoleMaintainer,
			}}
			ctx := store.WithUserID(context.Background(), 53)
			prepared, err := newMCPDeletionTestService(fake).PrepareProjectID(ctx, MCPProjectIDDeletionTarget{
				ProjectSlug: "exact-slug",
				Mode:        store.ModeFull,
				TagID:       59,
			})
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}

			// A prepared deletion owns the classification used by Delete.
			fake.projectContext.Project.ExpiresAt = nil
			fake.projectContext.Project.CreatorUserID = int64Pointer(999)
			if err := prepared.Delete(); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}

			assertMCPDeletionTrace(t, fake, "project-access", "project-tag-read", "row-delete")
			access := fake.calls[0]
			if access.ctx != ctx || access.projectSlug != "exact-slug" || access.mode != store.ModeFull {
				t.Fatalf("access call = %#v", access)
			}
			projectRead := fake.calls[1]
			if projectRead.ctx != ctx || projectRead.projectID != 47 || projectRead.tagID != 59 {
				t.Fatalf("project read call = %#v", projectRead)
			}
			assertMCPDeletionRowCall(t, fake.calls[2], ctx, 53, 59, tt.wantAnonymous)
		})
	}
}

func TestMCPDeletionProjectIDPreparationPrecedence(t *testing.T) {
	accessErr := errors.New("project access failed")
	projectReadErr := errors.New("project tag missing")
	tests := []struct {
		name           string
		withActor      bool
		role           store.ProjectRole
		accessErr      error
		projectReadErr error
		wantErr        error
		wantTrace      []string
	}{
		{
			name: "access failure wins", withActor: true, role: store.RoleMaintainer,
			accessErr: accessErr, wantErr: accessErr, wantTrace: []string{"project-access"},
		},
		{
			name: "missing actor follows access", role: store.RoleMaintainer,
			wantErr: ErrActorRequired, wantTrace: []string{"project-access"},
		},
		{
			name: "viewer fails before target read", withActor: true, role: store.RoleViewer,
			wantErr: ErrMaintainerRequired, wantTrace: []string{"project-access"},
		},
		{
			name: "creator temporary empty role fails before target read", withActor: true,
			wantErr: ErrMaintainerRequired, wantTrace: []string{"project-access"},
		},
		{
			name: "project-scoped read failure follows role", withActor: true, role: store.RoleMaintainer,
			projectReadErr: projectReadErr, wantErr: projectReadErr,
			wantTrace: []string{"project-access", "project-tag-read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expiresAt := time.Unix(1_800_000_000, 0)
			creatorID := int64(61)
			fake := &mcpDeletionFake{
				projectContext: store.ProjectContext{
					Project: store.Project{ID: 67, ExpiresAt: &expiresAt, CreatorUserID: &creatorID},
					Role:    tt.role,
				},
				accessErr:      tt.accessErr,
				projectReadErr: tt.projectReadErr,
			}
			ctx := context.Background()
			if tt.withActor {
				ctx = store.WithUserID(ctx, 71)
			}
			prepared, err := newMCPDeletionTestService(fake).PrepareProjectID(ctx, MCPProjectIDDeletionTarget{
				ProjectSlug: "ordered",
				Mode:        store.ModeFull,
				TagID:       73,
			})
			if prepared != nil || !errors.Is(err, tt.wantErr) {
				t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, %v)", prepared, err, tt.wantErr)
			}
			assertMCPDeletionTrace(t, fake, tt.wantTrace...)
		})
	}
}

func TestMCPDeletionProjectIDDeleteErrorAndCancellation(t *testing.T) {
	t.Run("delete error is returned once without post-read", func(t *testing.T) {
		deleteErr := errors.New("project delete failed")
		fake := &mcpDeletionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 79}, Role: store.RoleMaintainer},
			deleteErr:      deleteErr,
		}
		prepared, err := newMCPDeletionTestService(fake).PrepareProjectID(
			store.WithUserID(context.Background(), 83),
			MCPProjectIDDeletionTarget{ProjectSlug: "failure", Mode: store.ModeFull, TagID: 89},
		)
		if err != nil {
			t.Fatalf("PrepareProjectID() error = %v", err)
		}
		if err := prepared.Delete(); err != deleteErr {
			t.Fatalf("Delete() error = %v, want identical %v", err, deleteErr)
		}
		assertMCPDeletionTrace(t, fake, "project-access", "project-tag-read", "row-delete")
	})

	t.Run("cancelled bound context reaches deletion", func(t *testing.T) {
		fake := &mcpDeletionFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 97}, Role: store.RoleMaintainer},
			contextErrors:  true,
		}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 101))
		// Preparation collaborators must succeed before cancellation.
		fake.contextErrors = false
		prepared, err := newMCPDeletionTestService(fake).PrepareProjectID(ctx, MCPProjectIDDeletionTarget{
			ProjectSlug: "cancel", Mode: store.ModeFull, TagID: 103,
		})
		if err != nil {
			t.Fatalf("PrepareProjectID() error = %v", err)
		}
		fake.contextErrors = true
		cancel()
		if err := prepared.Delete(); err != context.Canceled {
			t.Fatalf("Delete() error = %v, want context.Canceled", err)
		}
		assertMCPDeletionTrace(t, fake, "project-access", "project-tag-read", "row-delete")
		if fake.calls[2].ctx != ctx {
			t.Fatal("deletion did not use the bound context")
		}
	})
}

func assertMCPDeletionTrace(t *testing.T, fake *mcpDeletionFake, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(fake.trace, want) {
		t.Fatalf("trace = %v, want %v", fake.trace, want)
	}
}

func assertMCPDeletionRowCall(
	t *testing.T,
	got mcpDeletionCall,
	wantCtx context.Context,
	wantActorID, wantTagID int64,
	wantAnonymous bool,
) {
	t.Helper()
	if got.ctx != wantCtx || got.actorID != wantActorID || got.tagID != wantTagID || got.isAnonymousBoard != wantAnonymous {
		t.Fatalf("row delete call = %#v, want context/actor/tag/anonymous = %v/%d/%d/%v",
			got, wantCtx, wantActorID, wantTagID, wantAnonymous)
	}
}
