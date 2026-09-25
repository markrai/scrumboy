package todo

import (
	"context"
	"scrumboy/internal/store"
)

type ArchiveReadStore interface {
	ListArchivedTodos(context.Context, int64, int, *int64, *int64, store.Mode) ([]store.Todo, string, bool, error)
}

type ArchiveReadService struct{ read ArchiveReadStore }

func NewArchiveReadService(read ArchiveReadStore) *ArchiveReadService {
	return &ArchiveReadService{read: read}
}

func (s *ArchiveReadService) List(ctx context.Context, projectID int64, limit int, afterAt, afterID *int64, mode store.Mode) ([]store.Todo, string, bool, error) {
	return s.read.ListArchivedTodos(ctx, projectID, limit, afterAt, afterID, mode)
}
