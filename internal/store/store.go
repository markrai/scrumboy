package store

import (
	"context"
	"database/sql"
)

type Store struct {
	db            *sql.DB
	encryptionKey []byte // 32-byte key for TOTP secret encryption; nil if 2FA encryption disabled
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

func (s *Store) Health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
