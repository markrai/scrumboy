package todo

import (
	"context"
	"errors"
	"reflect"
	"testing"

	apprefresh "scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

type archiveStoreCall struct {
	operation string
	ctx       context.Context
	projectID int64
	localIDs  []int64
	mode      store.Mode
}

type archiveStoreFake struct {
	calls  []archiveStoreCall
	result store.TodoArchiveBatchResult
	err    error
}

func (f *archiveStoreFake) ArchiveTodosByLocalID(ctx context.Context, projectID int64, localIDs []int64, mode store.Mode) (store.TodoArchiveBatchResult, error) {
	f.calls = append(f.calls, archiveStoreCall{operation: "archive", ctx: ctx, projectID: projectID, localIDs: append([]int64(nil), localIDs...), mode: mode})
	return f.result, f.err
}

func (f *archiveStoreFake) RestoreTodosByLocalID(ctx context.Context, projectID int64, localIDs []int64, mode store.Mode) (store.TodoArchiveBatchResult, error) {
	f.calls = append(f.calls, archiveStoreCall{operation: "restore", ctx: ctx, projectID: projectID, localIDs: append([]int64(nil), localIDs...), mode: mode})
	return f.result, f.err
}

type archiveRefreshCall struct {
	projectID int64
	reason    string
	entity    apprefresh.Entity
}

type archiveRefreshFake struct{ calls []archiveRefreshCall }

func (f *archiveRefreshFake) PublishBoardRefresh(_ context.Context, projectID int64, reason string, entity apprefresh.Entity) {
	f.calls = append(f.calls, archiveRefreshCall{projectID: projectID, reason: reason, entity: entity})
}

func TestRESTArchiveServiceUsesOneBatchPrimitiveAndRefreshesOnlyTransitions(t *testing.T) {
	ctx := context.Background()
	archive := &archiveStoreFake{result: store.TodoArchiveBatchResult{TransitionedCount: 2}}
	refresh := &archiveRefreshFake{}
	prepared := NewRESTArchiveService(RESTArchiveServiceDependencies{Archive: archive, Refresh: refresh}).Prepare(ctx, ResolvedArchiveTarget{
		ProjectContext: store.ProjectContext{Project: store.Project{ID: 7}},
		Mode:           store.ModeFull,
	})
	ids := []int64{4, 2}
	if _, err := prepared.Archive(ArchiveBatchCommand{LocalIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if len(archive.calls) != 1 || archive.calls[0].operation != "archive" || archive.calls[0].projectID != 7 || archive.calls[0].mode != store.ModeFull || !reflect.DeepEqual(archive.calls[0].localIDs, ids) {
		t.Fatalf("archive calls=%+v", archive.calls)
	}
	if len(refresh.calls) != 1 || refresh.calls[0].projectID != 7 || refresh.calls[0].reason != RefreshReasonTodoArchived || refresh.calls[0].entity != (apprefresh.Entity{}) {
		t.Fatalf("archive refresh=%+v", refresh.calls)
	}

	archive.result = store.TodoArchiveBatchResult{TransitionedCount: 0, UnchangedCount: 2}
	if _, err := prepared.Restore(ArchiveBatchCommand{LocalIDs: ids}); err != nil {
		t.Fatal(err)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("no-op restore published refresh: %+v", refresh.calls)
	}
	archive.err = errors.New("failed")
	if _, err := prepared.Restore(ArchiveBatchCommand{LocalIDs: ids}); err != archive.err {
		t.Fatalf("restore error=%v want identical %v", err, archive.err)
	}
	if len(refresh.calls) != 1 {
		t.Fatalf("failed restore published refresh: %+v", refresh.calls)
	}
}

func TestMCPArchiveServiceResolvesAccessThenUsesSameBatchPrimitive(t *testing.T) {
	ctx := context.Background()
	access := &mcpDeleteAccessFake{projectContext: store.ProjectContext{Project: store.Project{ID: 9, Slug: "archive"}, Role: store.RoleViewer}}
	archive := &archiveStoreFake{result: store.TodoArchiveBatchResult{TransitionedCount: 1}}
	prepared, err := NewMCPArchiveService(MCPArchiveServiceDependencies{Access: access, Archive: archive}).Prepare(ctx, MCPArchiveTarget{ProjectSlug: "archive", Mode: store.ModeFull})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Archive([]int64{3}); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Restore([]int64{3}); err != nil {
		t.Fatal(err)
	}
	if len(access.calls) != 1 || len(archive.calls) != 2 || archive.calls[0].operation != "archive" || archive.calls[1].operation != "restore" {
		t.Fatalf("access=%+v archive=%+v", access.calls, archive.calls)
	}
	if archive.calls[0].projectID != 9 || archive.calls[0].ctx != ctx || archive.calls[0].mode != store.ModeFull {
		t.Fatalf("archive call=%+v", archive.calls[0])
	}
}
