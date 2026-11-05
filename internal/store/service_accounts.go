package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// serviceAccountEmailDomain holds the placeholder addresses that satisfy users.email's NOT NULL
// UNIQUE contract. .invalid can never resolve (RFC 2606), and every sign-in and recovery lookup by
// email excludes service accounts, so a placeholder is never a login or recovery mechanism.
const serviceAccountEmailDomain = "service.invalid"

// isReservedServiceEmail reports whether a normalized address belongs to the domain reserved for
// service-account placeholders, which no human account may newly claim.
func isReservedServiceEmail(email string) bool {
	email = strings.TrimRight(email, ".")
	return strings.HasSuffix(email, "@"+serviceAccountEmailDomain) ||
		strings.HasSuffix(email, "."+serviceAccountEmailDomain)
}

// effectiveSystemRole resolves a stored role for authorization. A service account is always a
// plain user whatever its row says, and an unrecognized role never grants privilege.
func effectiveSystemRole(raw string, isService bool) SystemRole {
	if isService {
		return SystemRoleUser
	}
	if role, ok := ParseSystemRole(raw); ok {
		return role
	}
	return SystemRoleUser
}

// isServiceIdentityViolation reports whether err came from migration 080's database enforcement:
// a "service identity:" trigger or the users_service_identity CHECK.
func isServiceIdentityViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "service identity:") || strings.Contains(msg, "users_service_identity")
}

// wrapIdentityWriteErr gives a failed write to users or a credential table its usual context,
// unless the database rejected it for a service account.
func wrapIdentityWriteErr(op string, err error) error {
	if isServiceIdentityViolation(err) {
		return ErrServiceAccountRestricted
	}
	return fmt.Errorf("%s: %w", op, err)
}

// isServiceUser is the one place account type is resolved: from the identity the transport recorded
// for this user, or else from the users row.
func isServiceUser(ctx context.Context, q sqlRowQueryer, userID int64) (bool, error) {
	if isService, ok := ServiceIdentityFromContext(ctx, userID); ok {
		return isService, nil
	}
	var isService bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = ? AND is_service <> 0)`, userID).Scan(&isService); err != nil {
		return false, fmt.Errorf("check service identity: %w", err)
	}
	return isService, nil
}

// rejectServiceActor refuses an operation reserved for human actors.
func rejectServiceActor(ctx context.Context, q sqlRowQueryer) error {
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return nil
	}
	isService, err := isServiceUser(ctx, q, userID)
	if err != nil {
		return err
	}
	if isService {
		return ErrServiceAccountRestricted
	}
	return nil
}

// serviceLinkAccessBlocked reports whether userID is a service account with no membership in the
// project. Expiring boards normally admit anyone holding the link, but a service identity acts only
// within explicitly delegated projects.
func serviceLinkAccessBlocked(ctx context.Context, q sqlRowQueryer, projectID, userID int64) (bool, error) {
	isService, err := isServiceUser(ctx, q, userID)
	if err != nil || !isService {
		return false, err
	}
	var isMember bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = ? AND user_id = ?)`, projectID, userID).Scan(&isMember); err != nil {
		return false, fmt.Errorf("check service link access: %w", err)
	}
	return !isMember, nil
}

// rejectServiceLinkAccess is the gate in the project loaders, which every link-collaboration path
// goes through.
func rejectServiceLinkAccess(ctx context.Context, q sqlRowQueryer, p Project) error {
	if p.ExpiresAt == nil {
		return nil
	}
	userID, ok := UserIDFromContext(ctx)
	if !ok {
		return nil
	}
	blocked, err := serviceLinkAccessBlocked(ctx, q, p.ID, userID)
	if err != nil {
		return err
	}
	if blocked {
		return ErrNotFound
	}
	return nil
}

// CreateServiceAccount creates a non-interactive identity. It requires the Owner or Admin system
// role, checked on the same transaction as the insert so that a concurrent demotion of the
// requester cannot be bypassed.
//
// The account has role 'user', no password, no OIDC identity, and a permanent public_id assigned
// by the users insert trigger. It deliberately skips the seeding done for human accounts: it gets
// no project membership and no notification preference, so all project access must be granted
// explicitly.
func (s *Store) CreateServiceAccount(ctx context.Context, requesterID int64, name string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, fmt.Errorf("%w: invalid name", ErrValidation)
	}
	placeholder, err := uuid.NewRandom()
	if err != nil {
		return User{}, fmt.Errorf("generate service account email: %w", err)
	}
	email := "svc-" + placeholder.String() + "@" + serviceAccountEmailDomain
	nowMs := time.Now().UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin create service account tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := requireAdminTx(ctx, tx, requesterID); err != nil {
		return User{}, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
SELECT 1 + MAX(
  COALESCE((SELECT MAX(id) FROM users), 0),
  COALESCE((SELECT MAX(owner_user_id) FROM projects), 0),
  COALESCE((SELECT MAX(creator_user_id) FROM projects), 0),
  COALESCE((SELECT MAX(assignee_user_id) FROM todos), 0)
)`).Scan(&id); err != nil {
		return User{}, fmt.Errorf("allocate service account id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO users(id, email, name, password_hash, is_bootstrap, system_role, created_at, is_service)
VALUES (?, ?, ?, NULL, 0, 'user', ?, 1)`, id, email, name, nowMs); err != nil {
		return User{}, wrapIdentityWriteErr("insert service account", err)
	}
	var publicID string
	if err := tx.QueryRowContext(ctx, `SELECT public_id FROM users WHERE id = ?`, id).Scan(&publicID); err != nil {
		return User{}, fmt.Errorf("read service account public id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit create service account tx: %w", err)
	}
	return User{
		ID:         id,
		PublicID:   publicID,
		Email:      email,
		Name:       name,
		IsService:  true,
		SystemRole: SystemRoleUser,
		CreatedAt:  time.UnixMilli(nowMs).UTC(),
	}, nil
}
