package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"scrumboy/internal/auth"
)

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *Store) authEnabled(ctx context.Context) (bool, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users LIMIT 1)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("auth enabled: %w", err)
	}
	return exists, nil
}

func authEnabledTx(ctx context.Context, tx *sql.Tx) (bool, error) {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users LIMIT 1)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("auth enabled: %w", err)
	}
	return exists, nil
}

func (s *Store) GetUser(ctx context.Context, userID int64) (User, error) {
	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
		image            sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, `SELECT id, email, name, image, is_bootstrap, system_role, created_at, two_factor_enabled FROM users WHERE id = ?`, userID).Scan(&u.ID, &u.Email, &u.Name, &image, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled); err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}
	if image.Valid {
		u.Image = &image.String
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser // Default fallback
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled
	return u, nil
}

// UpdateUserImage updates the user's avatar. Pass nil to clear.
func (s *Store) UpdateUserImage(ctx context.Context, userID int64, image *string) error {
	if image == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE users SET image = NULL WHERE id = ?`, userID)
		if err != nil {
			return fmt.Errorf("update user image: %w", err)
		}
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET image = ? WHERE id = ?`, *image, userID)
	if err != nil {
		return fmt.Errorf("update user image: %w", err)
	}
	return nil
}

// GetUserPasswordHash returns the password hash for the given user.
func (s *Store) GetUserPasswordHash(ctx context.Context, userID int64) (string, error) {
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get user password hash: %w", err)
	}
	return hash, nil
}

// UpdateUserPassword updates the user's password. Validates via auth.ValidatePassword first.
func (s *Store) UpdateUserPassword(ctx context.Context, userID int64, newPassword string) error {
	if err := auth.ValidatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID)
	if err != nil {
		return fmt.Errorf("update user password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BootstrapUser creates the first user for the instance.
// It hard-fails if any user already exists.
func (s *Store) BootstrapUser(ctx context.Context, email, password, name string) (User, error) {
	email = normalizeEmail(email)
	password = strings.TrimSpace(password)
	name = strings.TrimSpace(name)
	if email == "" || !strings.Contains(email, "@") {
		return User{}, fmt.Errorf("%w: invalid email", ErrValidation)
	}
	if err := auth.ValidatePassword(password); err != nil {
		return User{}, err
	}
	if name == "" {
		return User{}, fmt.Errorf("%w: invalid name", ErrValidation)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin bootstrap tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if n != 0 {
		return User{}, ErrConflict
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	nowMs := time.Now().UTC().UnixMilli()

	res, err := tx.ExecContext(ctx, `INSERT INTO users(email, name, password_hash, is_bootstrap, system_role, created_at) VALUES (?, ?, ?, ?, ?, ?)`, email, name, string(hash), true, "owner", nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("last insert id user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit bootstrap tx: %w", err)
	}
	return User{ID: id, Email: email, Name: name, IsBootstrap: true, SystemRole: SystemRoleOwner, CreatedAt: time.UnixMilli(nowMs).UTC()}, nil
}

func (s *Store) AuthenticateUser(ctx context.Context, email, password string) (User, error) {
	email = normalizeEmail(email)
	password = strings.TrimSpace(password)
	if email == "" || password == "" {
		return User{}, ErrUnauthorized
	}
	var (
		u                User
		pwHash           sql.NullString
		isBootstrap      bool
		systemRoleStr    string
		createdAtMs      int64
		twoFactorEnabled bool
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, email, name, password_hash, is_bootstrap, system_role, created_at, two_factor_enabled FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.Name, &pwHash, &isBootstrap, &systemRoleStr, &createdAtMs, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrUnauthorized
		}
		return User{}, fmt.Errorf("auth user: %w", err)
	}
	if !pwHash.Valid || bcrypt.CompareHashAndPassword([]byte(pwHash.String), []byte(password)) != nil {
		return User{}, ErrUnauthorized
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser // Default fallback
	}
	u.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	u.TwoFactorEnabled = twoFactorEnabled
	return u, nil
}

// AssignUnownedDurableProjectsToUser assigns all durable projects (expires_at IS NULL) that have no owner
// to the provided user. It is idempotent and intended to run after first successful login.
//
// IMPORTANT: Anonymous temporary boards are intentionally ownerless and must NEVER be assigned.
// This function explicitly filters by expires_at IS NULL to ensure only durable projects are assigned.
// Temporary boards (expires_at IS NOT NULL) are pastebin-style and remain unowned forever.
func (s *Store) AssignUnownedDurableProjectsToUser(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin assign projects tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	// CRITICAL: Only assign durable projects (expires_at IS NULL).
	// Temporary boards (expires_at IS NOT NULL) must NEVER be assigned, even if owner_user_id IS NULL.
	if _, err := tx.ExecContext(ctx, `
UPDATE projects
SET owner_user_id = ?
WHERE owner_user_id IS NULL
  AND expires_at IS NULL
`, userID); err != nil {
		return fmt.Errorf("assign projects: %w", err)
	}
	// Also create maintainer memberships - again, only for durable projects
	_, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
		SELECT id, ?, 'maintainer', updated_at
		FROM projects
		WHERE owner_user_id = ? AND expires_at IS NULL
	`, userID, userID)
	if err != nil {
		return fmt.Errorf("create memberships: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit assign projects tx: %w", err)
	}
	return nil
}

// ClaimTemporaryBoard converts an unowned temporary board into an owned durable project.
// This is a server-side escape hatch, not a primary UX flow.
//
// IMPORTANT: This function is ONLY called explicitly via API endpoint.
// It must NEVER be called automatically during login, bootstrap, or assignment.
// Claim converts an unowned, unexpired temporary board into an owned durable project.
//
// Behavior:
// - requires an authenticated user (enforced by caller; this validates userID)
// - only acts on projects that exist
// - if project is expired, returns ErrNotFound
// - if already owned by someone else, returns ErrNotFound (do not leak)
// - idempotent if already claimed by this user
func (s *Store) ClaimTemporaryBoard(ctx context.Context, projectID, userID int64) error {
	if projectID <= 0 || userID <= 0 {
		return fmt.Errorf("%w: invalid ids", ErrValidation)
	}

	enabled, err := s.authEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		// If there are no users, claiming doesn't make sense.
		return ErrConflict
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var owner sql.NullInt64
	var expires sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT owner_user_id, expires_at FROM projects WHERE id = ?`, projectID).Scan(&owner, &expires); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("load project for claim: %w", err)
	}

	// If already owned, do not allow claim (except idempotent success for same user if already durable).
	if owner.Valid {
		if owner.Int64 == userID && !expires.Valid {
			return tx.Commit()
		}
		return ErrNotFound
	}

	// If expired or not a temporary board, refuse.
	if expires.Valid {
		if expires.Int64 <= nowMs {
			return ErrNotFound
		}
	} else {
		// Durable but unowned: allow claim (should be rare due to first-login assignment).
		if _, err := tx.ExecContext(ctx, `
UPDATE projects
SET owner_user_id = ?, updated_at = ?
WHERE id = ? AND owner_user_id IS NULL
`, userID, nowMs, projectID); err != nil {
			return fmt.Errorf("claim durable unowned: %w", err)
		}
		// Create maintainer membership
		_, err = tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
			VALUES (?, ?, 'maintainer', ?)
		`, projectID, userID, nowMs)
		if err != nil {
			return fmt.Errorf("create membership: %w", err)
		}
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE projects
SET owner_user_id = ?, expires_at = NULL, last_activity_at = ?, updated_at = ?
WHERE id = ? AND owner_user_id IS NULL AND expires_at IS NOT NULL
`, userID, nowMs, nowMs, projectID); err != nil {
		return fmt.Errorf("claim temp board: %w", err)
	}
	// Create maintainer membership
	_, err = tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, 'maintainer', ?)
	`, projectID, userID, nowMs)
	if err != nil {
		return fmt.Errorf("create membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit claim tx: %w", err)
	}
	return nil
}

// CreateSession creates a new session for the user.
//
// Multi-session support:
// - We intentionally DO NOT revoke existing sessions for the same user here.
// - Each successful login may create an additional concurrent session (e.g., multiple devices/browsers).
//
// If you need to revoke all sessions for a user (e.g., admin action, \"logout all\"), call DeleteSessionsByUserID.
//
// Returns the raw token to be set in a cookie; DB stores only token_hash.
func (s *Store) CreateSession(ctx context.Context, userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	if userID <= 0 {
		return "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("rand token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	tokenHash := hashToken(token)

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	expiresAt = now.Add(ttl)
	expiresAtMs := expiresAt.UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin create session tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// NOTE: We no longer rotate sessions by deleting existing rows for this user.
	// This enables multiple concurrent sessions per user (one cookie token per device/browser).
	if _, err := tx.ExecContext(ctx, `
INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)`, userID, tokenHash, nowMs, expiresAtMs, nowMs); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: sessions.token_hash") {
			return "", time.Time{}, ErrConflict
		}
		return "", time.Time{}, fmt.Errorf("insert session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit create session tx: %w", err)
	}
	return token, expiresAt, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	tokenHash := hashToken(token)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteSessionsByUserID deletes all sessions for the given user.
// This is an optional \"revoke all\" mechanism (e.g., logout-all, password reset hardening, admin disable).
func (s *Store) DeleteSessionsByUserID(ctx context.Context, userID int64) error {
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete sessions by user id: %w", err)
	}
	return nil
}

// GetUserBySessionToken returns the session's user if the session exists and is not expired.
// It enforces expires_at on every lookup. last_seen_at update is best-effort.
func (s *Store) GetUserBySessionToken(ctx context.Context, token string) (User, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
	)
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.email, u.name, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = ?
  AND s.expires_at > ?
`, tokenHash, nowMs).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get session user: %w", err)
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser // Default fallback
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled

	// Best-effort: refresh last_seen_at; do not fail auth if this update fails.
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE token_hash = ?`, nowMs, tokenHash)
	return u, nil
}

// CreateUser creates a new user (admin-only operation).
// This is separate from BootstrapUser which creates the first user.
func (s *Store) CreateUser(ctx context.Context, email, password, name string) (User, error) {
	email = normalizeEmail(email)
	password = strings.TrimSpace(password)
	name = strings.TrimSpace(name)
	if email == "" || !strings.Contains(email, "@") {
		return User{}, fmt.Errorf("%w: invalid email", ErrValidation)
	}
	if err := auth.ValidatePassword(password); err != nil {
		return User{}, err
	}
	if name == "" {
		return User{}, fmt.Errorf("%w: invalid name", ErrValidation)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}

	nowMs := time.Now().UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin create user tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT INTO users(email, name, password_hash, is_bootstrap, system_role, created_at) VALUES (?, ?, ?, ?, ?, ?)`, email, name, string(hash), false, "user", nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("last insert id user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit create user tx: %w", err)
	}
	return User{ID: id, Email: email, Name: name, IsBootstrap: false, SystemRole: SystemRoleUser, CreatedAt: time.UnixMilli(nowMs).UTC()}, nil
}

// ListUsers returns all users. Requires admin or owner role.
func (s *Store) ListUsers(ctx context.Context, requesterID int64) ([]User, error) {
	if err := s.requireAdmin(ctx, requesterID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, email, name, is_bootstrap, system_role, created_at, two_factor_enabled FROM users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var isBootstrap bool
		var systemRoleStr string
		var createdAt int64
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &u.TwoFactorEnabled); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.IsBootstrap = isBootstrap
		if role, ok := ParseSystemRole(systemRoleStr); ok {
			u.SystemRole = role
		} else {
			u.SystemRole = SystemRoleUser
		}
		u.CreatedAt = time.UnixMilli(createdAt).UTC()
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}
	return users, nil
}

// DeleteUser deletes a user. Requires owner role, and prevents deletion of the last owner.
func (s *Store) DeleteUser(ctx context.Context, requesterID, targetUserID int64) error {
	if requesterID == targetUserID {
		return fmt.Errorf("%w: cannot delete yourself", ErrValidation)
	}

	// Require owner role
	if err := s.requireOwner(ctx, requesterID); err != nil {
		return err
	}

	// Check if target is an owner
	target, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}

	// Prevent deletion of the last owner
	if target.SystemRole == SystemRoleOwner {
		count, err := s.countOwners(ctx)
		if err != nil {
			return err
		}
		if count <= 1 {
			return fmt.Errorf("%w: cannot delete the last owner", ErrValidation)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delete user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete user (cascade will handle sessions)
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, targetUserID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user tx: %w", err)
	}
	return nil
}

// UpdateUserRole updates a user's system role. Requires owner role.
// Prevents demotion of the last owner.
func (s *Store) UpdateUserRole(ctx context.Context, requesterID, targetUserID int64, newRole SystemRole) error {
	// Validate role
	if newRole != SystemRoleOwner && newRole != SystemRoleAdmin && newRole != SystemRoleUser {
		return fmt.Errorf("%w: invalid system role", ErrValidation)
	}

	// Require owner role
	if err := s.requireOwner(ctx, requesterID); err != nil {
		return err
	}

	// Get target user
	target, err := s.GetUser(ctx, targetUserID)
	if err != nil {
		return err
	}

	// Prevent demotion of the last owner
	if target.SystemRole == SystemRoleOwner && newRole != SystemRoleOwner {
		count, err := s.countOwners(ctx)
		if err != nil {
			return err
		}
		if count <= 1 {
			return fmt.Errorf("%w: cannot demote the last owner", ErrValidation)
		}
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin update role tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `UPDATE users SET system_role = ? WHERE id = ?`, newRole.String(), targetUserID); err != nil {
		return fmt.Errorf("update role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit update role tx: %w", err)
	}
	return nil
}

// GetUserByOIDCIdentity returns the user linked to the given (issuer, subject) pair.
// Returns ErrNotFound if no such identity exists.
func (s *Store) GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (User, error) {
	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
		image            sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.email, u.name, u.image, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled
FROM user_oidc_identities oi
JOIN users u ON u.id = oi.user_id
WHERE oi.issuer = ? AND oi.subject = ?
`, issuer, subject).Scan(&u.ID, &u.Email, &u.Name, &image, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user by oidc identity: %w", err)
	}
	if image.Valid {
		u.Image = &image.String
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled
	return u, nil
}

// CreateUserOIDC creates a new user from an OIDC login and links the identity.
// If CountUsers==0 AND issuer==configuredIssuer, the user becomes owner (plan section I).
// configuredIssuer is the canonical issuer from config; ownership is only granted
// when the identity's issuer matches it, preventing a misconfigured or rogue issuer
// from claiming the first-owner slot.
// Returns ErrConflict if the email is already taken by another user.
func (s *Store) CreateUserOIDC(ctx context.Context, configuredIssuer, issuer, subject, email, name string) (User, error) {
	email = normalizeEmail(email)
	if email == "" || !strings.Contains(email, "@") {
		return User{}, fmt.Errorf("%w: invalid email", ErrValidation)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = email
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin create oidc user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}

	role := SystemRoleUser
	isBootstrap := false
	if n == 0 && issuer == configuredIssuer {
		role = SystemRoleOwner
		isBootstrap = true
	}

	nowMs := time.Now().UTC().UnixMilli()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users(email, name, password_hash, is_bootstrap, system_role, created_at) VALUES (?, ?, NULL, ?, ?, ?)`,
		email, name, isBootstrap, role.String(), nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert oidc user: %w", err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("last insert id oidc user: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_oidc_identities(user_id, issuer, subject, email_at_login, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, issuer, subject, email, nowMs); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return User{}, ErrConflict
		}
		return User{}, fmt.Errorf("insert oidc identity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit create oidc user tx: %w", err)
	}

	return User{
		ID:          userID,
		Email:       email,
		Name:        name,
		IsBootstrap: isBootstrap,
		SystemRole:  role,
		CreatedAt:   time.UnixMilli(nowMs).UTC(),
	}, nil
}

// GetUserByEmail returns the user with the given email (normalized to lowercase).
// Returns ErrNotFound if no such user exists.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (User, error) {
	email = normalizeEmail(email)
	if email == "" {
		return User{}, ErrNotFound
	}
	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
		image            sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, email, name, image, is_bootstrap, system_role, created_at, two_factor_enabled FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.Name, &image, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user by email: %w", err)
	}
	if image.Valid {
		u.Image = &image.String
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled
	return u, nil
}

// LinkOIDCIdentity links an existing user to an OIDC identity by inserting a
// row in user_oidc_identities. This enables pre-existing local-password users
// to log in via SSO without creating a duplicate account.
// Returns ErrConflict if the (issuer, subject) pair is already linked.
func (s *Store) LinkOIDCIdentity(ctx context.Context, userID int64, issuer, subject, email string) error {
	email = normalizeEmail(email)
	nowMs := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_oidc_identities(user_id, issuer, subject, email_at_login, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, issuer, subject, email, nowMs)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return fmt.Errorf("link oidc identity: %w", err)
	}
	return nil
}

// UpdateUserOIDCProfile updates email and name for an existing user on OIDC login.
func (s *Store) UpdateUserOIDCProfile(ctx context.Context, userID int64, email, name string) error {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	if email == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET email = ?, name = ? WHERE id = ?`, email, name, userID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: users.email") {
			return ErrConflict
		}
		return fmt.Errorf("update oidc profile: %w", err)
	}
	return nil
}
