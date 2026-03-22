package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"scrumboy/internal/crypto"

	"golang.org/x/crypto/bcrypt"
)

const (
	login2FAPendingTTL    = 10 * time.Minute
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

// IncrementLogin2FAPendingAttempt increments attempt_count and returns ErrTooManyAttempts if over cap.
func (s *Store) IncrementLogin2FAPendingAttempt(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrNotFound
	}
	tokenHash := hashToken(token)
	nowMs := time.Now().UTC().UnixMilli()

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, `SELECT attempt_count FROM login_2fa_pending WHERE token_hash = ? AND expires_at > ?`, tokenHash, nowMs).Scan(&attemptCount); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return fmt.Errorf("get attempt count: %w", err)
	}
	attemptCount++
	if attemptCount > maxAttemptsPerToken {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM login_2fa_pending WHERE token_hash = ?`, tokenHash)
		return ErrTooManyAttempts
	}
	_, err := s.db.ExecContext(ctx, `UPDATE login_2fa_pending SET attempt_count = ?, last_attempt_at = ? WHERE token_hash = ?`, attemptCount, nowMs, tokenHash)
	if err != nil {
		return fmt.Errorf("increment attempt: %w", err)
	}
	return nil
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

// EncryptTOTPSecret encrypts plaintext using the store's encryption key.
func (s *Store) EncryptTOTPSecret(plaintext []byte) (string, error) {
	if s.encryptionKey == nil {
		return "", Err2FAEncryptionNotConfigured
	}
	return crypto.EncryptTOTPSecret(s.encryptionKey, plaintext)
}

// DecryptTOTPSecret decrypts encrypted data using the store's encryption key.
func (s *Store) DecryptTOTPSecret(encrypted string) ([]byte, error) {
	if s.encryptionKey == nil {
		return nil, Err2FAEncryptionNotConfigured
	}
	return crypto.DecryptTOTPSecret(s.encryptionKey, encrypted)
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

// AddRecoveryCodes stores bcrypt hashes of recovery codes.
func (s *Store) AddRecoveryCodes(ctx context.Context, userID int64, codes []string) error {
	if userID <= 0 {
		return fmt.Errorf("%w: invalid user id", ErrValidation)
	}
	nowMs := time.Now().UTC().UnixMilli()
	for _, code := range codes {
		code = strings.TrimSpace(strings.ToUpper(code))
		code = strings.ReplaceAll(code, "-", "")
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

// ConsumeRecoveryCode finds an unused code for the user, verifies it, marks used_at.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	code = strings.TrimSpace(strings.ToUpper(code))
	code = strings.ReplaceAll(code, "-", "")
	if len(code) != recoveryCodeLength {
		return false, nil
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, code_hash FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID)
	if err != nil {
		return false, fmt.Errorf("list recovery codes: %w", err)
	}
	defer rows.Close()

	nowMs := time.Now().UTC().UnixMilli()
	for rows.Next() {
		var id int64
		var codeHash string
		if err := rows.Scan(&id, &codeHash); err != nil {
			return false, fmt.Errorf("scan recovery code: %w", err)
		}
		if bcrypt.CompareHashAndPassword([]byte(codeHash), []byte(code)) == nil {
			if _, err := s.db.ExecContext(ctx, `UPDATE user_recovery_codes SET used_at = ? WHERE id = ?`, nowMs, id); err != nil {
				return false, fmt.Errorf("mark recovery code used: %w", err)
			}
			return true, nil
		}
	}
	return false, nil
}

// DeleteRecoveryCodesByUser removes all recovery codes for the user.
func (s *Store) DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM user_recovery_codes WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}
