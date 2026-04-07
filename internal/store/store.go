package store

import (
	"context"
	"database/sql"
)

// TodoAssignedFunc is called after a successful commit when a todo's assignee changes.
// projectSlug comes from the project row already loaded in the same write transaction (no extra slug query).
type TodoAssignedFunc func(ctx context.Context, projectID, todoID, localID int64, title, projectSlug string, from, to *int64, actorUserID int64)

type Store struct {
	db                    *sql.DB
	encryptionKey         []byte // 32-byte key for TOTP secret encryption; nil if 2FA encryption disabled
	todoAssignedPublisher TodoAssignedFunc
}

type StoreOptions struct {
	EncryptionKey []byte // Base64-decoded 32-byte key for AES-256-GCM; nil or empty to disable 2FA encryption
}

func New(db *sql.DB, opts *StoreOptions) *Store {
	s := &Store{db: db}
	if opts != nil && len(opts.EncryptionKey) == 32 {
		s.encryptionKey = opts.EncryptionKey
	}
	return s
}

func (s *Store) SetTodoAssignedPublisher(fn TodoAssignedFunc) {
	s.todoAssignedPublisher = fn
}

func (s *Store) Health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
