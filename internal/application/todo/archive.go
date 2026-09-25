package todo

import (
	"context"

	"scrumboy/internal/store"
)

const (
	RefreshReasonTodoArchived = "todo_archived"
	RefreshReasonTodoRestored = "todo_restored"
)

type ArchiveBatchCommand struct{ LocalIDs []int64 }

type ArchiveStore interface {
	ArchiveTodosByLocalID(context.Context, int64, []int64, store.Mode) (store.TodoArchiveBatchResult, error)
	RestoreTodosByLocalID(context.Context, int64, []int64, store.Mode) (store.TodoArchiveBatchResult, error)
}

type ArchiveService struct{ archive ArchiveStore }

func NewArchiveService(archive ArchiveStore) *ArchiveService {
	return &ArchiveService{archive: archive}
}

func (s *ArchiveService) Archive(ctx context.Context, projectID int64, cmd ArchiveBatchCommand, mode store.Mode) (store.TodoArchiveBatchResult, error) {
	return s.archive.ArchiveTodosByLocalID(ctx, projectID, cmd.LocalIDs, mode)
}

func (s *ArchiveService) Restore(ctx context.Context, projectID int64, cmd ArchiveBatchCommand, mode store.Mode) (store.TodoArchiveBatchResult, error) {
	return s.archive.RestoreTodosByLocalID(ctx, projectID, cmd.LocalIDs, mode)
}
