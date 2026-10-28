package customfield

import (
	"context"
	"errors"
	"strings"

	"scrumboy/internal/store"
)

var (
	ErrMaintainerRequired = errors.New("custom field mutation maintainer required")
	ErrNameRequired       = errors.New("name required")
	ErrInvalidType        = errors.New("type must be text, url or number")
	ErrKeyRequired        = errors.New("custom field key required")
)

// CreateCommand carries adapter-decoded create fields. Name and type
// normalization belong to this package; key derivation stays in the store.
type CreateCommand struct {
	Key  string
	Name string
	Type string
}

// DeleteCommand identifies the field key supplied by the adapter.
type DeleteCommand struct {
	Key string
}

// MutationStore is the persistence capability shared by REST and MCP
// definition mutations. Caps, key syntax, conflicts, audit, and cascading
// deletes remain store-owned.
type MutationStore interface {
	CreateCustomField(ctx context.Context, projectID int64, key, name string, typ store.CustomFieldType) (store.CustomField, error)
	DeleteCustomField(ctx context.Context, projectID int64, key string) error
}

func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameRequired
	}
	return name, nil
}

func normalizeType(raw string) (store.CustomFieldType, error) {
	typ := store.CustomFieldType(strings.TrimSpace(raw))
	if !store.ValidCustomFieldType(typ) {
		return "", ErrInvalidType
	}
	return typ, nil
}

func normalizeDeleteKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", ErrKeyRequired
	}
	return key, nil
}

func authorizeMaintainer(role store.ProjectRole) error {
	if !role.HasMinimumRole(store.RoleMaintainer) {
		return ErrMaintainerRequired
	}
	return nil
}
