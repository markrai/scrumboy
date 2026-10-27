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

	"scrumboy/internal/crypto"

	"golang.org/x/crypto/bcrypt"
)

const (
	login2FAPendingTTL   = 10 * time.Minute
	enrollmentTTL        = 10 * time.Minute
	maxAttemptsPerToken  = 5
	recoveryCodeLength   = 8 // Crockford base32, 4-4 split
	RecoveryCodeCount    = 8
	crockfordBase32Chars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ" // no I, L, O, U
)

// CreateLogin2FAPending creates a short-lived token for "password OK, pending 2FA" (latest wins).
func (s *Store) CreateLogin2FAPending(ctx context.Context, userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	if userID <= 0 {
		return "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if ttl <= 0 {
		ttl = login2FAPendingTTL
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	expiresAt = now.Add(ttl)
	expiresAtMs := expiresAt.UnixMilli()

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("rand token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	tokenHash := hashToken(token)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin create login 2fa pending tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Latest wins: delete expired, then delete by user_id, then insert
	if _, err := tx.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE expires_at < ?`, nowMs); err != nil {
		return "", time.Time{}, fmt.Errorf("delete expired login 2fa pending: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE user_id = ?`, userID); err != nil {
		return "", time.Time{}, fmt.Errorf("delete by user login 2fa pending: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO login_2fa_pending(user_id, token_hash, created_at, expires_at, attempt_count)
		VALUES (?, ?, ?, ?, 0)`, userID, tokenHash, nowMs, expiresAtMs); err != nil {
		return "", time.Time{}, fmt.Errorf("insert login 2fa pending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit create login 2fa pending tx: %w", err)
	}
	return token, expiresAt, nil
}

// GetUserByLogin2FAPendingToken returns the user and attempt_count for a valid pending token.
func (s *Store) GetUserByLogin2FAPendingToken(ctx context.Context, token string) (User, int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return User{}, 0, ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	var (
		u             User
		isBootstrap   bool
		systemRoleStr string
		createdAt     int64
		attemptCount  int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.name, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled, p.attempt_count
		FROM login_2fa_pending p
		JOIN users u ON u.id = p.user_id
		WHERE p.token_hash = ? AND p.expires_at > ?
	`, tokenHash, nowMs).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &u.TwoFactorEnabled, &attemptCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, 0, ErrNotFound
		}
		return User{}, 0, fmt.Errorf("get login 2fa pending: %w", err)
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	return u, attemptCount, nil
}

// IncrementLogin2FAPendingAttempt atomically advances attempt_count using SQLite's
// own UPDATE. Each admitted call increases the persisted count by one, at most
// maxAttemptsPerToken times. The next call invalidates the unexpired challenge.
func (s *Store) IncrementLogin2FAPendingAttempt(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	admitted, err := s.db.ExecContext(ctx, `
UPDATE login_2fa_pending
SET attempt_count = attempt_count + 1, last_attempt_at = ?
WHERE token_hash = ? AND expires_at > ? AND attempt_count < ?`,
		nowMs, tokenHash, nowMs, maxAttemptsPerToken)
	if err != nil {
		return fmt.Errorf("increment attempt: %w", err)
	}
	changed, err := admitted.RowsAffected()
	if err != nil {
		return fmt.Errorf("count incremented attempts: %w", err)
	}
	if changed == 1 {
		return nil
	}

	invalidated, err := s.db.ExecContext(ctx, `
DELETE FROM login_2fa_pending
WHERE token_hash = ? AND expires_at > ? AND attempt_count >= ?`,
		tokenHash, nowMs, maxAttemptsPerToken)
	if err != nil {
		return fmt.Errorf("invalidate exhausted login 2fa pending: %w", err)
	}
	removed, err := invalidated.RowsAffected()
	if err != nil {
		return fmt.Errorf("count invalidated login 2fa pending: %w", err)
	}
	if removed == 1 {
		return ErrTooManyAttempts
	}
	return ErrNotFound
}

// CompleteLogin2FA consumes a pending 2FA challenge and creates a session in one
// transaction. The pending-token DELETE is the first statement so one challenge
// can commit at most one session. A recovery proof is consumed only after that
// CAS succeeds. Rollback leaves the code unused and the pending row intact.
func (s *Store) CompleteLogin2FA(ctx context.Context, rawPendingToken string, userID int64, proof RecoveryCodeProof, sessionTTL time.Duration) (string, time.Time, error) {
	rawPendingToken = strings.TrimSpace(rawPendingToken)
	if rawPendingToken == "" || userID <= 0 {
		return "", time.Time{}, ErrNotFound
	}
	if proof.ID < 0 || (proof.ID == 0 && proof.Hash != "") || (proof.ID > 0 && proof.Hash == "") {
		return "", time.Time{}, ErrUnauthorized
	}
	if sessionTTL <= 0 {
		sessionTTL = 30 * 24 * time.Hour
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("rand session token: %w", err)
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(b)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin complete login 2fa: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	deleted, err := tx.ExecContext(ctx, `
DELETE FROM login_2fa_pending
WHERE token_hash = ? AND user_id = ? AND expires_at > ? AND attempt_count >= 1 AND attempt_count <= ?`,
		hashToken(rawPendingToken), userID, now.UnixMilli(), maxAttemptsPerToken)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("consume login 2fa pending: %w", err)
	}
	changed, err := deleted.RowsAffected()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("count consumed login 2fa pending: %w", err)
	}
	if changed != 1 {
		return "", time.Time{}, ErrNotFound
	}

	if proof.ID > 0 {
		if err := consumeRecoveryProofTx(ctx, tx, userID, proof); err != nil {
			return "", time.Time{}, err
		}
	}

	expiresAt := now.Add(sessionTTL)
	if err := insertSessionTx(ctx, tx, userID, hashToken(sessionToken), now, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit complete login 2fa: %w", err)
	}
	return sessionToken, expiresAt, nil
}

// DeleteLogin2FAPendingToken removes the pending token (e.g. after successful 2FA).
func (s *Store) DeleteLogin2FAPendingToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	tokenHash := hashToken(token)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete login 2fa pending: %w", err)
	}
	return nil
}

// CreateTwoFactorEnrollment creates a pending enrollment (latest wins).
func (s *Store) CreateTwoFactorEnrollment(ctx context.Context, userID int64, secretEnc string, ttl time.Duration) (setupToken string, expiresAt time.Time, err error) {
	if userID <= 0 {
		return "", time.Time{}, fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if secretEnc == "" {
		return "", time.Time{}, fmt.Errorf("%w: secret required", ErrValidation)
	}
	if ttl <= 0 {
		ttl = enrollmentTTL
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	expiresAt = now.Add(ttl)
	expiresAtMs := expiresAt.UnixMilli()

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, fmt.Errorf("rand token: %w", err)
	}
	setupToken = base64.RawURLEncoding.EncodeToString(b)
	tokenHash := hashToken(setupToken)

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin create enrollment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM two_factor_enrollments WHERE expires_at < ?`, nowMs); err != nil {
		return "", time.Time{}, fmt.Errorf("delete expired enrollments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM two_factor_enrollments WHERE user_id = ?`, userID); err != nil {
		return "", time.Time{}, fmt.Errorf("delete by user enrollments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO two_factor_enrollments(user_id, token_hash, secret_enc, created_at, expires_at, attempt_count)
		VALUES (?, ?, ?, ?, ?, 0)`, userID, tokenHash, secretEnc, nowMs, expiresAtMs); err != nil {
		return "", time.Time{}, fmt.Errorf("insert enrollment: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf("commit create enrollment tx: %w", err)
	}
	return setupToken, expiresAt, nil
}

// GetTwoFactorEnrollmentByToken returns userID and encrypted secret for a valid enrollment.
func (s *Store) GetTwoFactorEnrollmentByToken(ctx context.Context, token string) (userID int64, secretEnc string, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	if err := s.db.QueryRowContext(ctx, `SELECT user_id, secret_enc FROM two_factor_enrollments WHERE token_hash = ? AND expires_at > ?`, tokenHash, nowMs).Scan(&userID, &secretEnc); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", ErrNotFound
		}
		return 0, "", fmt.Errorf("get enrollment: %w", err)
	}
	return userID, secretEnc, nil
}

// IncrementEnrollmentAttempt increments attempt_count and returns ErrTooManyAttempts if over cap.
func (s *Store) IncrementEnrollmentAttempt(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT attempt_count FROM two_factor_enrollments WHERE token_hash = ? AND expires_at > ?`, tokenHash, nowMs).Scan(&attemptCount); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get attempt count: %w", err)
	}
	attemptCount++
	if attemptCount > maxAttemptsPerToken {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM two_factor_enrollments WHERE token_hash = ?`, tokenHash)
		return ErrTooManyAttempts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE two_factor_enrollments SET attempt_count = ?, last_attempt_at = ? WHERE token_hash = ?`, attemptCount, nowMs, tokenHash)
	if err != nil {
		return fmt.Errorf("increment attempt: %w", err)
	}
	return nil
}

// DeleteTwoFactorEnrollmentByToken removes the enrollment (e.g. after successful enable).
func (s *Store) DeleteTwoFactorEnrollmentByToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	tokenHash := hashToken(token)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM two_factor_enrollments WHERE token_hash = ?`, tokenHash); err != nil {
		return fmt.Errorf("delete enrollment: %w", err)
	}
	return nil
}

// GetUserTwoFactorSecret decrypts and returns the TOTP secret for the user.
func (s *Store) GetUserTwoFactorSecret(ctx context.Context, userID int64) (string, error) {
	if s.encryptionKey == nil {
		return "", Err2FAEncryptionNotConfigured
	}
	var secretEnc string
	if err := s.db.QueryRowContext(ctx, `SELECT two_factor_secret_enc FROM users WHERE id = ? AND two_factor_secret_enc IS NOT NULL`, userID).Scan(&secretEnc); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get secret: %w", err)
	}
	plaintext, err := crypto.DecryptTOTPSecret(s.encryptionKey, secretEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plaintext), nil
}

// SetUserTwoFactor stores the encrypted secret and sets two_factor_enabled = 1.
func (s *Store) SetUserTwoFactor(ctx context.Context, userID int64, encryptedSecret string) error {
	if s.encryptionKey == nil {
		return Err2FAEncryptionNotConfigured
	}
	if encryptedSecret == "" {
		return fmt.Errorf("%w: secret required", ErrValidation)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET two_factor_enabled = 1, two_factor_secret_enc = ? WHERE id = ?`, encryptedSecret, userID); err != nil {
		return fmt.Errorf("set user two factor: %w", err)
	}
	return nil
}

// CompleteTwoFactorEnrollment enables 2FA, inserts the complete recovery-code
// set, and consumes the enrollment in one transaction. Recovery codes are
// hashed before the transaction begins so a hashing failure writes nothing.
func (s *Store) CompleteTwoFactorEnrollment(ctx context.Context, userID int64, setupToken string, codes []string) error {
	if s.encryptionKey == nil {
		return Err2FAEncryptionNotConfigured
	}
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	setupToken = strings.TrimSpace(setupToken)
	if setupToken == "" {
		return ErrNotFound
	}
	hashes, err := hashRecoveryCodeSet(codes)
	if err != nil {
		return err
	}

	tokenHash := hashToken(setupToken)
	nowMs := time.Now().UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin complete 2fa enrollment tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var enrollUserID int64
	var secretEnc string
	err = tx.QueryRowContext(ctx, `SELECT user_id, secret_enc FROM two_factor_enrollments WHERE token_hash = ? AND expires_at > ?`, tokenHash, nowMs).Scan(&enrollUserID, &secretEnc)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get enrollment for complete: %w", err)
	}
	if enrollUserID != userID {
		return ErrForbidden
	}
	if secretEnc == "" {
		return fmt.Errorf("%w: secret required", ErrValidation)
	}

	deleted, err := tx.ExecContext(ctx, `DELETE FROM two_factor_enrollments WHERE token_hash = ? AND user_id = ?`, tokenHash, userID)
	if err != nil {
		return fmt.Errorf("consume enrollment: %w", err)
	}
	if n, err := deleted.RowsAffected(); err != nil {
		return fmt.Errorf("count consumed enrollments: %w", err)
	} else if n != 1 {
		return ErrNotFound
	}

	enabled, err := tx.ExecContext(ctx, `UPDATE users SET two_factor_enabled = 1, two_factor_secret_enc = ? WHERE id = ? AND two_factor_enabled = 0`, secretEnc, userID)
	if err != nil {
		return fmt.Errorf("enable user two factor: %w", err)
	}
	if n, err := enabled.RowsAffected(); err != nil {
		return fmt.Errorf("count enabled two factor: %w", err)
	} else if n != 1 {
		return fmt.Errorf("%w: two-factor authentication is already enabled", ErrConflict)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear leftover recovery codes: %w", err)
	}
	if err := insertHashedRecoveryCodesTx(ctx, tx, userID, hashes); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit complete 2fa enrollment tx: %w", err)
	}
	return nil
}

// RecoveryCodeProof is a matched unused recovery-code identity. Hash is the
// stored bcrypt value at match time. ReplaceRecoveryCodes compare-and-swaps
// both the row ID and this hash so a later row that reuses the SQLite
// INTEGER PRIMARY KEY cannot satisfy a stale proof.
type RecoveryCodeProof struct {
	ID   int64
	Hash string
}

// ReplaceRecoveryCodes replaces the user's recovery-code set in one
// transaction. When proof.ID is set, that unused code is consumed in the
// same transaction as the replay guard, identified by both row ID and the
// bcrypt hash captured at match time. Codes are hashed before the
// transaction begins so a hashing failure leaves the prior set intact.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, codes []string, proof RecoveryCodeProof) error {
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	if proof.ID < 0 || (proof.ID == 0 && proof.Hash != "") || (proof.ID > 0 && proof.Hash == "") {
		return ErrUnauthorized
	}
	hashes, err := hashRecoveryCodeSet(codes)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin replace recovery codes tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT two_factor_enabled FROM users WHERE id = ?`, userID).Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get two factor for replace recovery codes: %w", err)
	}
	if enabled != 1 {
		return fmt.Errorf("%w: two-factor authentication is not enabled", ErrValidation)
	}

	if proof.ID > 0 {
		if err := consumeRecoveryProofTx(ctx, tx, userID, proof); err != nil {
			return err
		}
		// Keep the proof row until after insert so this transaction cannot
		// recycle its own INTEGER PRIMARY KEY onto a replacement row.
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ? AND id != ?`, userID, proof.ID); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		if err := insertHashedRecoveryCodesTx(ctx, tx, userID, hashes); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE id = ? AND user_id = ?`, proof.ID, userID); err != nil {
			return fmt.Errorf("delete consumed recovery proof: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("delete recovery codes: %w", err)
		}
		if err := insertHashedRecoveryCodesTx(ctx, tx, userID, hashes); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace recovery codes tx: %w", err)
	}
	return nil
}

// ClearUserTwoFactor clears both two_factor_enabled and two_factor_secret_enc.
func (s *Store) ClearUserTwoFactor(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin clear 2fa tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET two_factor_enabled = 0, two_factor_secret_enc = NULL WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("clear user 2fa: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit clear 2fa tx: %w", err)
	}
	return nil
}

// EncryptSecret encrypts plaintext using the store's encryption key.
func (s *Store) EncryptSecret(plaintext []byte) (string, error) {
	if s.encryptionKey == nil {
		return "", ErrEncryptionNotConfigured
	}
	return crypto.EncryptSecret(s.encryptionKey, plaintext)
}

// DecryptSecret decrypts encrypted data using the store's encryption key.
func (s *Store) DecryptSecret(encrypted string) ([]byte, error) {
	if s.encryptionKey == nil {
		return nil, ErrEncryptionNotConfigured
	}
	return crypto.DecryptSecret(s.encryptionKey, encrypted)
}

// EncryptTOTPSecret encrypts plaintext using the store's encryption key.
func (s *Store) EncryptTOTPSecret(plaintext []byte) (string, error) {
	if s.encryptionKey == nil {
		return "", Err2FAEncryptionNotConfigured
	}
	return crypto.EncryptSecret(s.encryptionKey, plaintext)
}

// DecryptTOTPSecret decrypts encrypted data using the store's encryption key.
func (s *Store) DecryptTOTPSecret(encrypted string) ([]byte, error) {
	if s.encryptionKey == nil {
		return nil, Err2FAEncryptionNotConfigured
	}
	return crypto.DecryptSecret(s.encryptionKey, encrypted)
}

// GenerateRecoveryCodes generates N recovery codes in Crockford base32 (8 chars, 4-4 split).
func GenerateRecoveryCodes(n int) []string {
	codes := make([]string, n)
	for i := 0; i < n; i++ {
		var s string
		for j := 0; j < 8; j++ {
			b := make([]byte, 1)
			_, _ = rand.Read(b)
			s += string(crockfordBase32Chars[b[0]%32])
		}
		codes[i] = s[:4] + "-" + s[4:]
	}
	return codes
}

func normalizeRecoveryCode(code string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(code)), "-", "")
}

func hashRecoveryCodeSet(codes []string) ([]string, error) {
	if len(codes) != RecoveryCodeCount {
		return nil, fmt.Errorf("%w: expected %d recovery codes", ErrValidation, RecoveryCodeCount)
	}
	hashes := make([]string, 0, len(codes))
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		normalized := normalizeRecoveryCode(code)
		if len(normalized) != recoveryCodeLength {
			return nil, fmt.Errorf("%w: invalid recovery code", ErrValidation)
		}
		if _, dup := seen[normalized]; dup {
			return nil, fmt.Errorf("%w: duplicate recovery code", ErrValidation)
		}
		seen[normalized] = struct{}{}
		hash, err := bcrypt.GenerateFromPassword([]byte(normalized), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("hash recovery code: %w", err)
		}
		hashes = append(hashes, string(hash))
	}
	return hashes, nil
}

func consumeRecoveryProofTx(ctx context.Context, tx *sql.Tx, userID int64, proof RecoveryCodeProof) error {
	if proof.ID <= 0 || proof.Hash == "" {
		return ErrUnauthorized
	}
	nowMs := time.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, `UPDATE user_recovery_codes SET used_at = ? WHERE id = ? AND user_id = ? AND used_at IS NULL AND code_hash = ?`, nowMs, proof.ID, userID, proof.Hash)
	if err != nil {
		return fmt.Errorf("consume recovery proof: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count consumed recovery proofs: %w", err)
	}
	if changed != 1 {
		return ErrUnauthorized
	}
	return nil
}

func insertHashedRecoveryCodesTx(ctx context.Context, tx *sql.Tx, userID int64, hashes []string) error {
	nowMs := time.Now().UTC().UnixMilli()
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_recovery_codes(user_id, code_hash, created_at) VALUES (?, ?, ?)`, userID, hash, nowMs); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	return nil
}

// AddRecoveryCodes stores bcrypt hashes of recovery codes.
func (s *Store) AddRecoveryCodes(ctx context.Context, userID int64, codes []string) error {
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	nowMs := time.Now().UTC().UnixMilli()
	for _, code := range codes {
		code = normalizeRecoveryCode(code)
		if len(code) != recoveryCodeLength {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash recovery code: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO user_recovery_codes(user_id, code_hash, created_at) VALUES (?, ?, ?)`, userID, string(hash), nowMs); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}
	return nil
}

// MatchRecoveryCodeProof finds an unused recovery code and returns both its
// row ID and stored bcrypt hash. The hash is the durable identity used by
// recovery-code consumption; the ID alone is not.
func (s *Store) MatchRecoveryCodeProof(ctx context.Context, userID int64, code string) (int64, string, error) {
	code = normalizeRecoveryCode(code)
	if len(code) != recoveryCodeLength {
		return 0, "", nil
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, code_hash FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID)
	if err != nil {
		return 0, "", fmt.Errorf("list recovery codes: %w", err)
	}

	var matchingID int64
	var matchingHash string
	var scanErr error
	for rows.Next() {
		var id int64
		var codeHash string
		if err := rows.Scan(&id, &codeHash); err != nil {
			scanErr = err
			break
		}
		if bcrypt.CompareHashAndPassword([]byte(codeHash), []byte(code)) == nil {
			matchingID = id
			matchingHash = codeHash
			break
		}
	}
	if err := rows.Close(); err != nil {
		return 0, "", fmt.Errorf("close recovery code rows: %w", err)
	}
	if scanErr != nil {
		return 0, "", fmt.Errorf("scan recovery code: %w", scanErr)
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("iterate recovery codes: %w", err)
	}
	return matchingID, matchingHash, nil
}

// ConsumeRecoveryCodeProof consumes a previously matched recovery-code identity.
// Only ErrUnauthorized (incomplete or stale proof) is reported as not consumed;
// begin, SQL, and commit failures propagate.
func (s *Store) ConsumeRecoveryCodeProof(ctx context.Context, userID int64, proof RecoveryCodeProof) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin consume recovery proof: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := consumeRecoveryProofTx(ctx, tx, userID, proof); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return false, nil
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit consume recovery proof: %w", err)
	}
	return true, nil
}

// ConsumeRecoveryCode finds an unused code, verifies it, and atomically marks
// it used using the matched ID-plus-hash identity. Exactly one concurrent
// caller can consume a matching row.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	id, hash, err := s.MatchRecoveryCodeProof(ctx, userID, code)
	if err != nil || id == 0 || hash == "" {
		return false, err
	}
	return s.ConsumeRecoveryCodeProof(ctx, userID, RecoveryCodeProof{ID: id, Hash: hash})
}

// DeleteRecoveryCodesByUser removes all recovery codes for the user.
func (s *Store) DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}
