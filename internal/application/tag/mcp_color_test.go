package tag

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

var _ MCPProjectAccessStore = (*store.Store)(nil)

type mcpColorProjectReadStep struct {
	trace              string
	tags               []store.TagCount
	err                error
	returnContextError bool
}

type mcpColorCall struct {
	operation      string
	ctx            context.Context
	slug           string
	mode           store.Mode
	projectContext store.ProjectContext
	projectID      int64
	viewerUserID   *int64
	actorUserID    int64
	tagID          int64
	tagName        string
	color          *string
}

type mcpColorFake struct {
	trace []string
	calls []mcpColorCall

	projectContext   store.ProjectContext
	accessErr        error
	accessContextErr bool

	mineTags           []store.TagWithColor
	mineReadErr        error
	mineReadContextErr bool

	projectReadSteps []mcpColorProjectReadStep
	projectReadIndex int

	mineColorErr          error
	mineColorContextErr   bool
	mineNormalizedColor   *string
	durableIDColorErr     error
	durableIDContextErr   bool
	temporaryIDColorErr   error
	temporaryIDContextErr bool
	durableNameColorErr   error
	durableNameContextErr bool
}

func (f *mcpColorFake) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	f.trace = append(f.trace, "access")
	f.calls = append(f.calls, mcpColorCall{operation: "access", ctx: ctx, slug: slug, mode: mode})
	if f.accessContextErr {
		return store.ProjectContext{}, ctx.Err()
	}
	if f.accessErr != nil {
		return store.ProjectContext{}, f.accessErr
	}
	return f.projectContext, nil
}

func (f *mcpColorFake) ListUserTags(ctx context.Context, userID int64) ([]store.TagWithColor, error) {
	f.trace = append(f.trace, "mine-read")
	f.calls = append(f.calls, mcpColorCall{operation: "mine-read", ctx: ctx, actorUserID: userID})
	if f.mineReadContextErr {
		return nil, ctx.Err()
	}
	if f.mineReadErr != nil {
		return nil, f.mineReadErr
	}
	return f.mineTags, nil
}

func (f *mcpColorFake) ListTagCounts(
	ctx context.Context,
	projectContext *store.ProjectContext,
) ([]store.TagCount, error) {
	if f.projectReadIndex >= len(f.projectReadSteps) {
		f.trace = append(f.trace, "unexpected-project-read")
		return nil, errors.New("unexpected project tag read")
	}
	step := f.projectReadSteps[f.projectReadIndex]
	f.projectReadIndex++
	f.trace = append(f.trace, step.trace)
	f.calls = append(f.calls, mcpColorCall{
		operation: step.trace, ctx: ctx, projectContext: cloneProjectContext(*projectContext),
	})
	if step.returnContextError {
		return nil, ctx.Err()
	}
	if step.err != nil {
		return nil, step.err
	}
	return step.tags, nil
}

func (f *mcpColorFake) UpdateTagColor(
	ctx context.Context,
	viewerUserID *int64,
	tagID int64,
	color *string,
) error {
	f.trace = append(f.trace, "mine-color")
	f.calls = append(f.calls, mcpColorCall{
		operation: "mine-color", ctx: ctx, viewerUserID: cloneInt64(viewerUserID), tagID: tagID,
		color: cloneString(color),
	})
	if f.mineColorContextErr {
		return ctx.Err()
	}
	if f.mineNormalizedColor != nil && color != nil {
		*color = *f.mineNormalizedColor
	}
	return f.mineColorErr
}

func (f *mcpColorFake) UpdateTagColorForDurableProjectByID(
	ctx context.Context,
	projectID, viewerUserID, tagID int64,
	color *string,
) error {
	f.trace = append(f.trace, "durable-id-color")
	f.calls = append(f.calls, mcpColorCall{
		operation: "durable-id-color", ctx: ctx, projectID: projectID,
		viewerUserID: cloneInt64(&viewerUserID), tagID: tagID, color: cloneString(color),
	})
	if f.durableIDContextErr {
		return ctx.Err()
	}
	return f.durableIDColorErr
}

func (f *mcpColorFake) UpdateTagColorForTemporaryBoard(
	ctx context.Context,
	projectID int64,
	viewerUserID *int64,
	tagID int64,
	color *string,
) error {
	f.trace = append(f.trace, "temporary-id-color")
	f.calls = append(f.calls, mcpColorCall{
		operation: "temporary-id-color", ctx: ctx, projectID: projectID,
		viewerUserID: cloneInt64(viewerUserID), tagID: tagID, color: cloneString(color),
	})
	if f.temporaryIDContextErr {
		return ctx.Err()
	}
	return f.temporaryIDColorErr
}

func (f *mcpColorFake) SetViewerTagColorByName(
	ctx context.Context,
	projectID, viewerUserID int64,
	name string,
	color *string,
) error {
	f.trace = append(f.trace, "durable-name-color")
	f.calls = append(f.calls, mcpColorCall{
		operation: "durable-name-color", ctx: ctx, projectID: projectID,
		viewerUserID: cloneInt64(&viewerUserID), tagName: name, color: cloneString(color),
	})
	if f.durableNameContextErr {
		return ctx.Err()
	}
	return f.durableNameColorErr
}

func newMCPColorTestService(fake *mcpColorFake) *MCPColorService {
	return NewMCPColorService(MCPColorServiceDependencies{
		Access:           fake,
		MineRead:         fake,
		ProjectRead:      fake,
		MineColor:        fake,
		DurableIDColor:   fake,
		TemporaryIDColor: fake,
		DurableNameColor: fake,
	})
}

func TestMCPColorMineIDSequenceAndSyntheticResult(t *testing.T) {
	t.Run("success uses store-normalized fresh color", func(t *testing.T) {
		originalTagColor := "#111111"
		normalized := "#abcdef"
		fake := &mcpColorFake{
			mineTags:            []store.TagWithColor{{TagID: 17, Name: "mine", Color: &originalTagColor, CanDelete: true}},
			mineNormalizedColor: &normalized,
		}
		ctx := store.WithUserID(context.Background(), 11)
		inputColor := "  #abcdef  "
		prepared, err := newMCPColorTestService(fake).PrepareMineID(ctx, MCPMineIDColorTarget{
			TagID: 17,
			Color: NewColorIntent(&inputColor),
		})
		if err != nil {
			t.Fatalf("PrepareMineID() error = %v", err)
		}
		inputColor = "changed after preparation"
		originalTagColor = "changed after preparation"

		result, err := prepared.Update()
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		assertMCPColorTrace(t, fake, "mine-read", "mine-color")
		if result.TagID != 17 || result.Name != "mine" || !result.CanDelete {
			t.Fatalf("result = %#v", result)
		}
		assertMCPColorStringPointer(t, result.Color, "#abcdef")
		call := findMCPColorCall(t, fake, "mine-color")
		if call.ctx != ctx || call.tagID != 17 || call.viewerUserID == nil || *call.viewerUserID != 11 {
			t.Fatalf("mine color call = %#v", call)
		}
		assertMCPColorStringPointer(t, call.color, "  #abcdef  ")
	})

	for _, tc := range []struct {
		name        string
		mutationErr error
		wantErr     error
	}{
		{name: "nil clear succeeds"},
		{name: "missing preference clear succeeds", mutationErr: store.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &mcpColorFake{
				mineTags:     []store.TagWithColor{{TagID: 19, Name: "clear", Color: stringPointer("#123456")}},
				mineColorErr: tc.mutationErr,
			}
			prepared, err := newMCPColorTestService(fake).PrepareMineID(
				store.WithUserID(context.Background(), 13),
				MCPMineIDColorTarget{TagID: 19, Color: NewColorIntent(nil)},
			)
			if err != nil {
				t.Fatalf("PrepareMineID() error = %v", err)
			}
			result, err := prepared.Update()
			if err != tc.wantErr {
				t.Fatalf("Update() error = %v, want %v", err, tc.wantErr)
			}
			if result.Color != nil {
				t.Fatalf("result color = %s, want nil", describeStringPointer(result.Color))
			}
			assertMCPColorTrace(t, fake, "mine-read", "mine-color")
		})
	}
}

func TestMCPColorMineIDFailures(t *testing.T) {
	t.Run("missing actor stops before read", func(t *testing.T) {
		fake := &mcpColorFake{}
		prepared, err := newMCPColorTestService(fake).PrepareMineID(context.Background(), MCPMineIDColorTarget{TagID: 1})
		if prepared != nil || !errors.Is(err, ErrActorRequired) {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertMCPColorTrace(t, fake)
	})

	t.Run("pre-read error is unchanged", func(t *testing.T) {
		readErr := errors.New("mine read failed")
		fake := &mcpColorFake{mineReadErr: readErr}
		prepared, err := newMCPColorTestService(fake).PrepareMineID(
			store.WithUserID(context.Background(), 23), MCPMineIDColorTarget{TagID: 29},
		)
		if prepared != nil || err != readErr {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, identical error)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "mine-read")
	})

	t.Run("missing tag is store not found", func(t *testing.T) {
		fake := &mcpColorFake{mineTags: []store.TagWithColor{{TagID: 31}}}
		prepared, err := newMCPColorTestService(fake).PrepareMineID(
			store.WithUserID(context.Background(), 37), MCPMineIDColorTarget{TagID: 41},
		)
		if prepared != nil || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, store.ErrNotFound)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "mine-read")
	})

	for _, tc := range []struct {
		name  string
		color ColorIntent
		err   error
	}{
		{name: "non-clear not found remains error", color: NewColorIntent(stringPointer("#123456")), err: store.ErrNotFound},
		{name: "ordinary mutation error is unchanged", color: NewColorIntent(nil), err: errors.New("mine write failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &mcpColorFake{
				mineTags:     []store.TagWithColor{{TagID: 43, Name: "mine"}},
				mineColorErr: tc.err,
			}
			prepared, err := newMCPColorTestService(fake).PrepareMineID(
				store.WithUserID(context.Background(), 47), MCPMineIDColorTarget{TagID: 43, Color: tc.color},
			)
			if err != nil {
				t.Fatalf("PrepareMineID() error = %v", err)
			}
			result, err := prepared.Update()
			if err != tc.err || result != (store.TagWithColor{}) {
				t.Fatalf("Update() = (%#v, %v), want zero result and identical %v", result, err, tc.err)
			}
			assertMCPColorTrace(t, fake, "mine-read", "mine-color")
		})
	}
}

func TestMCPColorProjectNameSequenceAndProjection(t *testing.T) {
	ownerID := int64(53)
	fake := &mcpColorFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 59, OwnerUserID: &ownerID},
			Role:    store.RoleViewer,
		},
		projectReadSteps: []mcpColorProjectReadStep{{
			trace: "project-read-post",
			tags:  []store.TagCount{{TagID: 0, Name: "make-space", Count: 3, Color: stringPointer("#abcdef")}},
		}},
	}
	ctx := store.WithUserID(context.Background(), 61)
	prepared, err := newMCPColorTestService(fake).PrepareProjectName(ctx, MCPProjectNameColorTarget{
		ProjectSlug: "project-slug",
		Mode:        store.ModeFull,
		Name:        "Make Space",
		Color:       NewColorIntent(stringPointer("#abcdef")),
	})
	if err != nil {
		t.Fatalf("PrepareProjectName() error = %v", err)
	}
	ownerID = 999
	fake.projectContext.Project.ID = 999

	result, err := prepared.Update()
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertMCPColorTrace(t, fake, "access", "durable-name-color", "project-read-post")
	if result.Name != "make-space" || result.Count != 3 {
		t.Fatalf("result = %#v", result)
	}
	access := findMCPColorCall(t, fake, "access")
	if access.ctx != ctx || access.slug != "project-slug" || access.mode != store.ModeFull {
		t.Fatalf("access call = %#v", access)
	}
	mutation := findMCPColorCall(t, fake, "durable-name-color")
	if mutation.ctx != ctx || mutation.projectID != 59 || mutation.viewerUserID == nil ||
		*mutation.viewerUserID != 61 || mutation.tagName != "Make Space" {
		t.Fatalf("name mutation = %#v", mutation)
	}
	postRead := findMCPColorCall(t, fake, "project-read-post")
	if postRead.ctx != ctx || postRead.projectContext.Project.ID != 59 ||
		postRead.projectContext.Project.OwnerUserID == nil || *postRead.projectContext.Project.OwnerUserID != 53 {
		t.Fatalf("post-read = %#v", postRead)
	}
}

func TestMCPColorProjectNameFailures(t *testing.T) {
	t.Run("access error is unchanged", func(t *testing.T) {
		accessErr := errors.New("access failed")
		fake := &mcpColorFake{accessErr: accessErr}
		prepared, err := newMCPColorTestService(fake).PrepareProjectName(
			store.WithUserID(context.Background(), 67), MCPProjectNameColorTarget{ProjectSlug: "missing"},
		)
		if prepared != nil || err != accessErr {
			t.Fatalf("PrepareProjectName() = (%v, %v), want (nil, identical error)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access")
	})

	t.Run("missing actor follows access", func(t *testing.T) {
		fake := &mcpColorFake{projectContext: store.ProjectContext{Project: store.Project{ID: 71}}}
		prepared, err := newMCPColorTestService(fake).PrepareProjectName(
			context.Background(), MCPProjectNameColorTarget{ProjectSlug: "project"},
		)
		if prepared != nil || !errors.Is(err, ErrActorRequired) {
			t.Fatalf("PrepareProjectName() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access")
	})

	for _, tc := range []struct {
		name        string
		mutationErr error
		readStep    *mcpColorProjectReadStep
		wantErr     error
		wantTrace   []string
	}{
		{
			name: "mutation error prevents post-read", mutationErr: errors.New("name write failed"),
			wantTrace: []string{"access", "durable-name-color"},
		},
		{
			name: "post-read error follows one mutation", readStep: &mcpColorProjectReadStep{
				trace: "project-read-post", err: errors.New("name post-read failed"),
			}, wantTrace: []string{"access", "durable-name-color", "project-read-post"},
		},
		{
			name: "missing grouped projection is application error", readStep: &mcpColorProjectReadStep{
				trace: "project-read-post", tags: []store.TagCount{{Name: "other"}},
			}, wantErr: ErrColorProjectionMissing,
			wantTrace: []string{"access", "durable-name-color", "project-read-post"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &mcpColorFake{
				projectContext:      store.ProjectContext{Project: store.Project{ID: 73}, Role: store.RoleViewer},
				durableNameColorErr: tc.mutationErr,
			}
			if tc.readStep != nil {
				fake.projectReadSteps = []mcpColorProjectReadStep{*tc.readStep}
			}
			prepared, err := newMCPColorTestService(fake).PrepareProjectName(
				store.WithUserID(context.Background(), 79),
				MCPProjectNameColorTarget{ProjectSlug: "project", Name: "group", Color: NewColorIntent(nil)},
			)
			if err != nil {
				t.Fatalf("PrepareProjectName() error = %v", err)
			}
			result, err := prepared.Update()
			wantErr := tc.wantErr
			if wantErr == nil {
				if tc.mutationErr != nil {
					wantErr = tc.mutationErr
				} else {
					wantErr = tc.readStep.err
				}
			}
			if err != wantErr || result != (store.TagCount{}) {
				t.Fatalf("Update() = (%#v, %v), want zero result and identical %v", result, err, wantErr)
			}
			assertMCPColorTrace(t, fake, tc.wantTrace...)
			if countMCPColorCalls(fake, "durable-name-color") != 1 {
				t.Fatalf("name mutation calls = %d, want 1", countMCPColorCalls(fake, "durable-name-color"))
			}
		})
	}

	t.Run("temporary name still reaches durable-only store capability", func(t *testing.T) {
		expiresAt := time.Now()
		fake := &mcpColorFake{
			projectContext:      store.ProjectContext{Project: store.Project{ID: 83, ExpiresAt: &expiresAt}},
			durableNameColorErr: store.ErrValidation,
		}
		prepared, err := newMCPColorTestService(fake).PrepareProjectName(
			store.WithUserID(context.Background(), 89),
			MCPProjectNameColorTarget{ProjectSlug: "temporary", Name: "name"},
		)
		if err != nil {
			t.Fatalf("PrepareProjectName() error = %v", err)
		}
		if _, err := prepared.Update(); err != store.ErrValidation {
			t.Fatalf("Update() error = %v, want store.ErrValidation", err)
		}
		assertMCPColorTrace(t, fake, "access", "durable-name-color")
	})
}

func TestMCPColorProjectIDDurableAndTemporarySequences(t *testing.T) {
	tests := []struct {
		name         string
		project      store.Project
		role         store.ProjectRole
		wantMutation string
	}{
		{name: "durable maintainer", project: store.Project{ID: 97}, role: store.RoleMaintainer, wantMutation: "durable-id-color"},
		{name: "temporary viewer skips durable role gate", project: temporaryProject(101), role: store.RoleViewer, wantMutation: "temporary-id-color"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &mcpColorFake{
				projectContext: store.ProjectContext{Project: tt.project, Role: tt.role},
				projectReadSteps: []mcpColorProjectReadStep{
					{trace: "project-read-pre", tags: []store.TagCount{{TagID: 103, Name: "target"}}},
					{trace: "project-read-post", tags: []store.TagCount{{TagID: 103, Name: "target", Color: stringPointer("#abcdef")}}},
				},
			}
			ctx := store.WithUserID(context.Background(), 107)
			prepared, err := newMCPColorTestService(fake).PrepareProjectID(ctx, MCPProjectIDColorTarget{
				ProjectSlug: "project", Mode: store.ModeFull, TagID: 103,
				Color: NewColorIntent(stringPointer("#abcdef")),
			})
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}

			result, err := prepared.Update()
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			assertMCPColorTrace(t, fake, "access", "project-read-pre", tt.wantMutation, "project-read-post")
			if result.TagID != 103 || result.Name != "target" {
				t.Fatalf("result = %#v", result)
			}
			mutation := findMCPColorCall(t, fake, tt.wantMutation)
			if mutation.ctx != ctx || mutation.projectID != tt.project.ID || mutation.tagID != 103 ||
				mutation.viewerUserID == nil || *mutation.viewerUserID != 107 {
				t.Fatalf("mutation = %#v", mutation)
			}
			if countMCPColorCalls(fake, "durable-id-color")+countMCPColorCalls(fake, "temporary-id-color") != 1 {
				t.Fatalf("ID mutation calls = %d, want 1", countMCPColorCalls(fake, "durable-id-color")+countMCPColorCalls(fake, "temporary-id-color"))
			}
		})
	}
}

func TestMCPColorPreparedProjectIDOwnsTemporaryClassification(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	fake := &mcpColorFake{
		projectContext: store.ProjectContext{
			Project: store.Project{ID: 109, ExpiresAt: &expiresAt},
			Role:    store.RoleViewer,
		},
		projectReadSteps: []mcpColorProjectReadStep{
			{trace: "project-read-pre", tags: []store.TagCount{{TagID: 113}}},
			{trace: "project-read-post", tags: []store.TagCount{{TagID: 113}}},
		},
	}
	prepared, err := newMCPColorTestService(fake).PrepareProjectID(
		store.WithUserID(context.Background(), 127),
		MCPProjectIDColorTarget{ProjectSlug: "temporary", TagID: 113},
	)
	if err != nil {
		t.Fatalf("PrepareProjectID() error = %v", err)
	}

	// Changing the access fake's source after preparation must not turn the
	// already-prepared temporary operation into a durable one.
	fake.projectContext.Project.ExpiresAt = nil

	if _, err := prepared.Update(); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertMCPColorTrace(t, fake, "access", "project-read-pre", "temporary-id-color", "project-read-post")
	if countMCPColorCalls(fake, "temporary-id-color") != 1 || countMCPColorCalls(fake, "durable-id-color") != 0 {
		t.Fatalf(
			"temporary calls = %d, durable calls = %d, want 1 and 0",
			countMCPColorCalls(fake, "temporary-id-color"),
			countMCPColorCalls(fake, "durable-id-color"),
		)
	}
}

func TestMCPColorProjectIDPreparationOrdering(t *testing.T) {
	t.Run("access error stops before actor and read", func(t *testing.T) {
		accessErr := errors.New("access failed")
		fake := &mcpColorFake{accessErr: accessErr}
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(
			store.WithUserID(context.Background(), 109), MCPProjectIDColorTarget{ProjectSlug: "missing", TagID: 1},
		)
		if prepared != nil || err != accessErr {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, identical error)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access")
	})

	t.Run("missing actor follows access and precedes read", func(t *testing.T) {
		fake := &mcpColorFake{projectContext: store.ProjectContext{Project: store.Project{ID: 113}}}
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(
			context.Background(), MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 1},
		)
		if prepared != nil || !errors.Is(err, ErrActorRequired) {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access")
	})

	t.Run("pre-read error is unchanged", func(t *testing.T) {
		readErr := errors.New("pre-read failed")
		fake := &mcpColorFake{
			projectContext:   store.ProjectContext{Project: store.Project{ID: 127}, Role: store.RoleMaintainer},
			projectReadSteps: []mcpColorProjectReadStep{{trace: "project-read-pre", err: readErr}},
		}
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(
			store.WithUserID(context.Background(), 131), MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 137},
		)
		if prepared != nil || err != readErr {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, identical error)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre")
	})

	t.Run("wrong tag wins before insufficient durable role", func(t *testing.T) {
		fake := &mcpColorFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 139}, Role: store.RoleViewer},
			projectReadSteps: []mcpColorProjectReadStep{{
				trace: "project-read-pre", tags: []store.TagCount{{TagID: 149}},
			}},
		}
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(
			store.WithUserID(context.Background(), 151), MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 157},
		)
		if prepared != nil || !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, store.ErrNotFound)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre")
	})

	t.Run("durable role gate follows successful pre-read", func(t *testing.T) {
		fake := &mcpColorFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 163}, Role: store.RoleContributor},
			projectReadSteps: []mcpColorProjectReadStep{{
				trace: "project-read-pre", tags: []store.TagCount{{TagID: 167}},
			}},
		}
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(
			store.WithUserID(context.Background(), 173), MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 167},
		)
		if prepared != nil || !errors.Is(err, ErrMaintainerRequired) {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, ErrMaintainerRequired)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre")
	})
}

func TestMCPColorProjectIDMutationAndPostReadFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		temporary    bool
		color        ColorIntent
		mutationErr  error
		postRead     *mcpColorProjectReadStep
		wantErr      error
		wantPostRead bool
	}{
		{name: "durable mutation error", mutationErr: errors.New("durable write failed"), color: NewColorIntent(stringPointer("#123456"))},
		{name: "temporary mutation error", temporary: true, mutationErr: errors.New("temporary write failed"), color: NewColorIntent(stringPointer("#123456"))},
		{name: "durable non-clear not found", mutationErr: store.ErrNotFound, color: NewColorIntent(stringPointer("#123456"))},
		{name: "temporary non-clear not found", temporary: true, mutationErr: store.ErrNotFound, color: NewColorIntent(stringPointer("#123456"))},
		{
			name: "durable harmless clear not found", mutationErr: store.ErrNotFound, color: NewColorIntent(nil), wantPostRead: true,
			postRead: &mcpColorProjectReadStep{trace: "project-read-post", tags: []store.TagCount{{TagID: 179}}},
		},
		{
			name: "temporary harmless clear not found", temporary: true, mutationErr: store.ErrNotFound, color: NewColorIntent(nil), wantPostRead: true,
			postRead: &mcpColorProjectReadStep{trace: "project-read-post", tags: []store.TagCount{{TagID: 179}}},
		},
		{
			name: "durable post-read error", color: NewColorIntent(nil), wantPostRead: true,
			postRead: &mcpColorProjectReadStep{trace: "project-read-post", err: errors.New("post-read failed")},
		},
		{
			name: "temporary post-read error", temporary: true, color: NewColorIntent(nil), wantPostRead: true,
			postRead: &mcpColorProjectReadStep{trace: "project-read-post", err: errors.New("post-read failed")},
		},
		{
			name: "post-read target missing", color: NewColorIntent(nil), wantPostRead: true, wantErr: ErrColorProjectionMissing,
			postRead: &mcpColorProjectReadStep{trace: "project-read-post", tags: []store.TagCount{{TagID: 181}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := store.Project{ID: 191}
			role := store.RoleMaintainer
			mutationName := "durable-id-color"
			if tc.temporary {
				project = temporaryProject(191)
				role = store.RoleViewer
				mutationName = "temporary-id-color"
			}
			fake := &mcpColorFake{
				projectContext: store.ProjectContext{Project: project, Role: role},
				projectReadSteps: []mcpColorProjectReadStep{{
					trace: "project-read-pre", tags: []store.TagCount{{TagID: 179}},
				}},
			}
			if tc.postRead != nil {
				fake.projectReadSteps = append(fake.projectReadSteps, *tc.postRead)
			}
			if tc.temporary {
				fake.temporaryIDColorErr = tc.mutationErr
			} else {
				fake.durableIDColorErr = tc.mutationErr
			}

			prepared, err := newMCPColorTestService(fake).PrepareProjectID(
				store.WithUserID(context.Background(), 193),
				MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 179, Color: tc.color},
			)
			if err != nil {
				t.Fatalf("PrepareProjectID() error = %v", err)
			}
			result, err := prepared.Update()
			wantErr := tc.wantErr
			if wantErr == nil {
				switch {
				case tc.mutationErr != nil && !tc.color.IsClear():
					wantErr = tc.mutationErr
				case tc.mutationErr != nil && !errors.Is(tc.mutationErr, store.ErrNotFound):
					wantErr = tc.mutationErr
				case tc.postRead != nil && tc.postRead.err != nil:
					wantErr = tc.postRead.err
				}
			}
			if err != wantErr {
				t.Fatalf("Update() error = %v, want identical %v", err, wantErr)
			}
			if wantErr != nil && result != (store.TagCount{}) {
				t.Fatalf("Update() result = %#v, want zero after error", result)
			}
			wantTrace := []string{"access", "project-read-pre", mutationName}
			if tc.wantPostRead {
				wantTrace = append(wantTrace, "project-read-post")
			}
			assertMCPColorTrace(t, fake, wantTrace...)
			if countMCPColorCalls(fake, mutationName) != 1 {
				t.Fatalf("mutation calls = %d, want 1", countMCPColorCalls(fake, mutationName))
			}
		})
	}
}

func TestMCPColorCancellationUsesExactBoundContext(t *testing.T) {
	t.Run("access", func(t *testing.T) {
		fake := &mcpColorFake{accessContextErr: true}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 197))
		cancel()
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(ctx, MCPProjectIDColorTarget{ProjectSlug: "project"})
		if prepared != nil || err != context.Canceled {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, context.Canceled)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access")
		if findMCPColorCall(t, fake, "access").ctx != ctx {
			t.Fatal("access did not receive exact bound context")
		}
	})

	t.Run("mine pre-read", func(t *testing.T) {
		fake := &mcpColorFake{mineReadContextErr: true}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 199))
		cancel()
		prepared, err := newMCPColorTestService(fake).PrepareMineID(ctx, MCPMineIDColorTarget{TagID: 1})
		if prepared != nil || err != context.Canceled {
			t.Fatalf("PrepareMineID() = (%v, %v), want (nil, context.Canceled)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "mine-read")
		if findMCPColorCall(t, fake, "mine-read").ctx != ctx {
			t.Fatal("mine read did not receive exact bound context")
		}
	})

	t.Run("project pre-read", func(t *testing.T) {
		fake := &mcpColorFake{
			projectContext:   store.ProjectContext{Project: store.Project{ID: 211}, Role: store.RoleMaintainer},
			projectReadSteps: []mcpColorProjectReadStep{{trace: "project-read-pre", returnContextError: true}},
		}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 223))
		cancel()
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(ctx, MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 227})
		if prepared != nil || err != context.Canceled {
			t.Fatalf("PrepareProjectID() = (%v, %v), want (nil, context.Canceled)", prepared, err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre")
		if findMCPColorCall(t, fake, "project-read-pre").ctx != ctx {
			t.Fatal("project pre-read did not receive exact bound context")
		}
	})

	t.Run("mutation after preparation", func(t *testing.T) {
		fake := &mcpColorFake{
			projectContext:      store.ProjectContext{Project: store.Project{ID: 229}, Role: store.RoleMaintainer},
			projectReadSteps:    []mcpColorProjectReadStep{{trace: "project-read-pre", tags: []store.TagCount{{TagID: 233}}}},
			durableIDContextErr: true,
		}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 239))
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(ctx, MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 233})
		if err != nil {
			t.Fatalf("PrepareProjectID() error = %v", err)
		}
		cancel()
		if _, err := prepared.Update(); err != context.Canceled {
			t.Fatalf("Update() error = %v, want context.Canceled", err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre", "durable-id-color")
		if findMCPColorCall(t, fake, "durable-id-color").ctx != ctx {
			t.Fatal("mutation did not receive exact bound context")
		}
	})

	t.Run("post-read after committed mutation", func(t *testing.T) {
		fake := &mcpColorFake{
			projectContext: store.ProjectContext{Project: store.Project{ID: 241}, Role: store.RoleMaintainer},
			projectReadSteps: []mcpColorProjectReadStep{
				{trace: "project-read-pre", tags: []store.TagCount{{TagID: 251}}},
				{trace: "project-read-post", returnContextError: true},
			},
		}
		ctx, cancel := context.WithCancel(store.WithUserID(context.Background(), 257))
		prepared, err := newMCPColorTestService(fake).PrepareProjectID(ctx, MCPProjectIDColorTarget{ProjectSlug: "project", TagID: 251})
		if err != nil {
			t.Fatalf("PrepareProjectID() error = %v", err)
		}
		cancel()
		if _, err := prepared.Update(); err != context.Canceled {
			t.Fatalf("Update() error = %v, want context.Canceled", err)
		}
		assertMCPColorTrace(t, fake, "access", "project-read-pre", "durable-id-color", "project-read-post")
		if countMCPColorCalls(fake, "durable-id-color") != 1 {
			t.Fatalf("mutation calls = %d, want 1", countMCPColorCalls(fake, "durable-id-color"))
		}
		if findMCPColorCall(t, fake, "project-read-post").ctx != ctx {
			t.Fatal("post-read did not receive exact bound context")
		}
	})
}

func temporaryProject(id int64) store.Project {
	expiresAt := time.Now().Add(time.Hour)
	return store.Project{ID: id, ExpiresAt: &expiresAt}
}

func assertMCPColorTrace(t *testing.T, fake *mcpColorFake, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(fake.trace, want) {
		t.Fatalf("trace = %v, want %v", fake.trace, want)
	}
}

func findMCPColorCall(t *testing.T, fake *mcpColorFake, operation string) mcpColorCall {
	t.Helper()
	for _, call := range fake.calls {
		if call.operation == operation {
			return call
		}
	}
	t.Fatalf("missing %q call in %#v", operation, fake.calls)
	return mcpColorCall{}
}

func countMCPColorCalls(fake *mcpColorFake, operation string) int {
	count := 0
	for _, call := range fake.calls {
		if call.operation == operation {
			count++
		}
	}
	return count
}

func assertMCPColorStringPointer(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("color = %s, want %q", describeStringPointer(got), want)
	}
}
