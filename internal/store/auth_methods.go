package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"scrumboy/internal/auth"
)

// IsUsablePasswordHash reports whether hash is a bcrypt hash understood by the
// password verifier. Empty and malformed legacy values are not authentication methods.
func IsUsablePasswordHash(hash string) bool {
	if strings.TrimSpace(hash) == "" {
		return false
	}
	_, err := bcrypt.Cost([]byte(hash))
	return err == nil
}

func (s *Store) populateUserAuthMethods(ctx context.Context, u *User) error {
	if u == nil || u.ID <= 0 {
		return nil
	}
	var hash sql.NullString
	var anyOIDC, configuredOIDC bool
	err := s.db.QueryRowContext(ctx, `
SELECT password_hash,
       EXISTS(SELECT 1 FROM user_oidc_identities WHERE user_id = users.id),
       CASE WHEN ? = '' THEN FALSE ELSE EXISTS(
         SELECT 1 FROM user_oidc_identities WHERE user_id = users.id AND issuer = ?
       ) END
FROM users WHERE id = ?`, s.configuredOIDCIssuer, s.configuredOIDCIssuer, u.ID).
		Scan(&hash, &anyOIDC, &configuredOIDC)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get user authentication methods: %w", err)
	}
	u.HasLocalPassword = hash.Valid && IsUsablePasswordHash(hash.String)
	u.HasAnyOIDCIdentity = anyOIDC
	u.OIDCLinked = configuredOIDC
	return nil
}

func (s *Store) CountMalformedPasswordHashes(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT password_hash FROM users WHERE password_hash IS NOT NULL AND TRIM(password_hash) <> ''`)
	if err != nil {
		return 0, fmt.Errorf("list password hashes: %w", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return 0, fmt.Errorf("scan password hash: %w", err)
		}
		if !IsUsablePasswordHash(hash) {
			n++
		}
	}
	return n, rows.Err()
}

// UpdateOIDCIdentityEmailAtLogin records the latest verified IdP email without
// changing the canonical Scrumboy email or display profile.
func (s *Store) UpdateOIDCIdentityEmailAtLogin(ctx context.Context, userID int64, issuer, subject, email string) error {
	email = normalizeEmail(email)
	res, err := s.db.ExecContext(ctx, `UPDATE user_oidc_identities SET email_at_login = ? WHERE user_id = ? AND issuer = ? AND subject = ?`, email, userID, issuer, subject)
	if err != nil {
		return fmt.Errorf("update oidc identity email: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// LinkOIDCIdentityExplicit attaches an identity only after both-side proof has
// been completed by the HTTP layer. Canonical email matching is rechecked here.
func (s *Store) LinkOIDCIdentityExplicit(ctx context.Context, userID int64, issuer, subject, verifiedEmail string) error {
	verifiedEmail = normalizeEmail(verifiedEmail)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin oidc link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var canonical string
	if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, userID).Scan(&canonical); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("get link user: %w", err)
	}
	if normalizeEmail(canonical) != verifiedEmail {
		return fmt.Errorf("%w: oidc email does not match canonical email", ErrValidation)
	}
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE LOWER(TRIM(email)) = ?`, verifiedEmail).Scan(&owners); err != nil {
		return fmt.Errorf("check canonical email ownership: %w", err)
	}
	if owners != 1 {
		return ErrConflict
	}
	var linkedUser int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_oidc_identities WHERE issuer = ? AND subject = ?`, issuer, subject).Scan(&linkedUser)
	if err == nil {
		if linkedUser != userID {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE user_oidc_identities SET email_at_login = ? WHERE user_id = ? AND issuer = ? AND subject = ?`, verifiedEmail, userID, issuer, subject)
		if err != nil {
			return fmt.Errorf("refresh linked identity: %w", err)
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check linked identity: %w", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_oidc_identities(user_id, issuer, subject, email_at_login, created_at) VALUES (?, ?, ?, ?, ?)`, userID, issuer, subject, verifiedEmail, now); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return fmt.Errorf("insert oidc identity: %w", err)
	}
	return tx.Commit()
}

func randomAuthGrant() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) CreateFirstPasswordGrant(ctx context.Context, userID int64, sessionToken string, ttl time.Duration) (string, time.Time, error) {
	if userID <= 0 || sessionToken == "" || ttl <= 0 {
		return "", time.Time{}, ErrValidation
	}
	raw, err := randomAuthGrant()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate first-password grant: %w", err)
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin first-password grant: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var valid bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sessions WHERE token_hash = ? AND user_id = ? AND expires_at > ?)`, hashToken(sessionToken), userID, now.UnixMilli()).Scan(&valid); err != nil || !valid {
		if err != nil {
			return "", time.Time{}, fmt.Errorf("validate grant session: %w", err)
		}
		return "", time.Time{}, ErrUnauthorized
	}
	_, _ = tx.ExecContext(ctx, `DELETE FROM first_password_grants WHERE expires_at <= ? OR user_id = ?`, now.UnixMilli(), userID)
	if _, err := tx.ExecContext(ctx, `INSERT INTO first_password_grants(token_hash, user_id, session_token_hash, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`, hashToken(raw), userID, hashToken(sessionToken), now.UnixMilli(), expires.UnixMilli()); err != nil {
		return "", time.Time{}, fmt.Errorf("insert first-password grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit first-password grant: %w", err)
	}
	return raw, expires, nil
}

func (s *Store) FirstPasswordGrantValid(ctx context.Context, rawGrant, sessionToken string, userID int64) (bool, error) {
	if rawGrant == "" || sessionToken == "" || userID <= 0 {
		return false, nil
	}
	now := time.Now().UTC().UnixMilli()
	var valid bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM first_password_grants g
 JOIN sessions s ON s.token_hash = g.session_token_hash AND s.user_id = g.user_id
 WHERE g.token_hash = ? AND g.user_id = ? AND g.session_token_hash = ?
   AND g.expires_at > ? AND s.expires_at > ?
)`, hashToken(rawGrant), userID, hashToken(sessionToken), now, now).Scan(&valid)
	return valid, err
}

// SetFirstPassword consumes a narrowly scoped grant and uses an exact-value CAS
// so it cannot overwrite a valid password installed by a concurrent operation.
func (s *Store) SetFirstPassword(ctx context.Context, userID int64, rawGrant, sessionToken, password string) error {
	return s.setFirstPassword(ctx, userID, rawGrant, sessionToken, password, 0)
}

// SetFirstPasswordWithRecoveryCode consumes the recovery code and installs the
// first password in one transaction. A failed grant check, password CAS, or
// commit therefore leaves the recovery code usable.
func (s *Store) SetFirstPasswordWithRecoveryCode(ctx context.Context, userID int64, rawGrant, sessionToken, password string, recoveryCodeID int64) error {
	if recoveryCodeID <= 0 {
		return ErrUnauthorized
	}
	return s.setFirstPassword(ctx, userID, rawGrant, sessionToken, password, recoveryCodeID)
}

func (s *Store) setFirstPassword(ctx context.Context, userID int64, rawGrant, sessionToken, password string, recoveryCodeID int64) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash first password: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin set first password: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().UnixMilli()
	var grantUser int64
	err = tx.QueryRowContext(ctx, `SELECT g.user_id FROM first_password_grants g JOIN sessions s ON s.token_hash=g.session_token_hash AND s.user_id=g.user_id WHERE g.token_hash=? AND g.user_id=? AND g.session_token_hash=? AND g.expires_at>? AND s.expires_at>?`, hashToken(rawGrant), userID, hashToken(sessionToken), now, now).Scan(&grantUser)
	if err != nil {
		return ErrUnauthorized
	}
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&old); err != nil {
		return ErrNotFound
	}
	if old.Valid && IsUsablePasswordHash(old.String) {
		return ErrConflict
	}
	if recoveryCodeID > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE user_recovery_codes SET used_at = ? WHERE id = ? AND user_id = ? AND used_at IS NULL`, now, recoveryCodeID, userID)
		if err != nil {
			return fmt.Errorf("consume recovery code for first password: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count consumed recovery codes for first password: %w", err)
		}
		if changed != 1 {
			return ErrUnauthorized
		}
	}
	var result sql.Result
	if old.Valid {
		result, err = tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?`, string(newHash), userID, old.String)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash IS NULL`, string(newHash), userID)
	}
	if err != nil {
		return fmt.Errorf("set first password: %w", err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM first_password_grants WHERE token_hash = ?`, hashToken(rawGrant)); err != nil {
		return fmt.Errorf("consume first-password grant: %w", err)
	}
	return tx.Commit()
}

// ResetLocalPassword replaces an existing valid local password and revokes all
// sessions and pending login challenges in the same transaction.
func (s *Store) ResetLocalPassword(ctx context.Context, userID int64, expectedHash, password string) error {
	if !IsUsablePasswordHash(expectedHash) {
		return ErrNotFound
	}
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash reset password: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?`, string(hash), userID, expectedHash)
	if err != nil {
		return fmt.Errorf("update reset password: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke reset sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke reset challenges: %w", err)
	}
	return tx.Commit()
}

type OwnerRecoveryPosture struct {
	OwnerCount           int
	EffectiveOwnerCount  int
	EffectiveLocalOwners int
	EffectiveSSOOwners   int
	ProviderOnlyOwners   int
}

func (s *Store) OwnerRecoveryPosture(ctx context.Context, localAuthEnabled, oidcEnabled bool) (OwnerRecoveryPosture, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT password_hash,
       CASE WHEN ? = '' THEN FALSE ELSE EXISTS(
         SELECT 1 FROM user_oidc_identities WHERE user_id = users.id AND issuer = ?
       ) END
FROM users WHERE system_role = 'owner'`, s.configuredOIDCIssuer, s.configuredOIDCIssuer)
	if err != nil {
		return OwnerRecoveryPosture{}, err
	}
	defer rows.Close()
	var p OwnerRecoveryPosture
	for rows.Next() {
		var hash sql.NullString
		var configuredOIDC bool
		if err := rows.Scan(&hash, &configuredOIDC); err != nil {
			return p, err
		}
		p.OwnerCount++
		local := localAuthEnabled && hash.Valid && IsUsablePasswordHash(hash.String)
		sso := oidcEnabled && configuredOIDC
		if local {
			p.EffectiveLocalOwners++
		}
		if sso {
			p.EffectiveSSOOwners++
		}
		if local || sso {
			p.EffectiveOwnerCount++
		}
		if sso && !local {
			p.ProviderOnlyOwners++
		}
	}
	return p, rows.Err()
}

// RecoverOwnerPassword is the host-side break-glass mutation. It deliberately
// leaves OIDC links and the user's 2FA configuration untouched.
func (s *Store) RecoverOwnerPassword(ctx context.Context, email, password string) error {
	email = normalizeEmail(email)
	if email == "" {
		return ErrValidation
	}
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash recovery password: %w", err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire recovery connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("acquire database recovery lock (stop the active Scrumboy service and retry): %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()
	rows, err := conn.QueryContext(ctx, `SELECT id, system_role FROM users WHERE LOWER(TRIM(email)) = ?`, email)
	if err != nil {
		return fmt.Errorf("find recovery owner: %w", err)
	}
	var userID int64
	var role string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&userID, &role); err != nil {
			_ = rows.Close()
			return err
		}
		count++
	}
	_ = rows.Close()
	if count == 0 {
		return ErrNotFound
	}
	if count != 1 {
		return fmt.Errorf("%w: normalized email is ambiguous", ErrConflict)
	}
	if role != SystemRoleOwner.String() {
		return fmt.Errorf("%w: target is not an owner", ErrValidation)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID); err != nil {
		return fmt.Errorf("recover owner password: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke owner sessions: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("revoke owner login challenges: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit owner recovery: %w", err)
	}
	committed = true
	return nil
}
