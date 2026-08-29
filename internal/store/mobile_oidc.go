package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMobileOIDCFlowTTL  = 10 * time.Minute
	defaultMobileOIDCGrantTTL = 2 * time.Minute
	defaultSessionTTL         = 30 * 24 * time.Hour
)

// MobileOIDCFlow is a trusted server-side marker that distinguishes a mobile
// login callback from the existing browser login callback. Raw state and app
// verifier values are never persisted.
type MobileOIDCFlow struct {
	HandoffChallenge string
	ReturnTo         string
	ExpiresAt        time.Time
	CallbackConsumed bool
}

// MobileOIDCExchange is the result of atomically consuming a handoff grant and
// creating the normal Scrumboy browser-session record.
type MobileOIDCExchange struct {
	SessionToken     string
	SessionExpiresAt time.Time
	ReturnTo         string
}

func randomRawURLToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func mobileOIDCStateHash(raw string) string { return hashToken(raw) }

// CreateMobileOIDCFlow persists the app proof challenge and a hash of the
// provider state created by the existing OIDC validator.
func (s *Store) CreateMobileOIDCFlow(ctx context.Context, rawState, handoffChallenge, returnTo string, ttl time.Duration) error {
	rawState = strings.TrimSpace(rawState)
	handoffChallenge = strings.TrimSpace(handoffChallenge)
	if rawState == "" || handoffChallenge == "" {
		return fmt.Errorf("%w: mobile OIDC proof is required", ErrValidation)
	}
	if ttl <= 0 {
		ttl = defaultMobileOIDCFlowTTL
	}
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO mobile_oidc_flows(
  state_hash, handoff_challenge, challenge_method, return_to, created_at, expires_at
) VALUES (?, ?, 'S256', ?, ?, ?)`,
		mobileOIDCStateHash(rawState), handoffChallenge, returnTo, now.UnixMilli(), now.Add(ttl).UnixMilli())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrConflict
		}
		return fmt.Errorf("create mobile OIDC flow: %w", err)
	}
	return nil
}

// GetMobileOIDCFlow deliberately returns expired/completed markers so callback
// routing continues to fail closed as a mobile flow instead of falling back to
// the browser callback path.
func (s *Store) GetMobileOIDCFlow(ctx context.Context, rawState string) (MobileOIDCFlow, error) {
	var (
		flow       MobileOIDCFlow
		expiresMS  int64
		consumedMS sql.NullInt64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT handoff_challenge, return_to, expires_at, callback_consumed_at
FROM mobile_oidc_flows
WHERE state_hash = ?`, mobileOIDCStateHash(strings.TrimSpace(rawState))).Scan(
		&flow.HandoffChallenge, &flow.ReturnTo, &expiresMS, &consumedMS,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return MobileOIDCFlow{}, ErrNotFound
		}
		return MobileOIDCFlow{}, fmt.Errorf("get mobile OIDC flow: %w", err)
	}
	flow.ExpiresAt = time.UnixMilli(expiresMS).UTC()
	flow.CallbackConsumed = consumedMS.Valid
	return flow, nil
}

// CreateMobileOIDCHandoffGrant marks the trusted mobile callback consumed and
// creates a short-lived one-time grant in the same transaction.
func (s *Store) CreateMobileOIDCHandoffGrant(ctx context.Context, rawState string, userID int64, ttl time.Duration) (string, time.Time, string, error) {
	if userID <= 0 || strings.TrimSpace(rawState) == "" {
		return "", time.Time{}, "", fmt.Errorf("%w: invalid mobile OIDC grant", ErrValidation)
	}
	if ttl <= 0 {
		ttl = defaultMobileOIDCGrantTTL
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("begin mobile OIDC grant tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stateHash := mobileOIDCStateHash(strings.TrimSpace(rawState))
	var challenge, returnTo string
	var expiresMS int64
	var consumedMS sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT handoff_challenge, return_to, expires_at, callback_consumed_at
FROM mobile_oidc_flows
WHERE state_hash = ?`, stateHash).Scan(&challenge, &returnTo, &expiresMS, &consumedMS)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", time.Time{}, "", ErrNotFound
		}
		return "", time.Time{}, "", fmt.Errorf("load mobile OIDC flow for grant: %w", err)
	}
	now := time.Now().UTC()
	if consumedMS.Valid || now.UnixMilli() >= expiresMS {
		return "", time.Time{}, "", ErrNotFound
	}
	updated, err := tx.ExecContext(ctx, `
UPDATE mobile_oidc_flows
SET callback_consumed_at = ?
WHERE state_hash = ? AND callback_consumed_at IS NULL AND expires_at > ?`, now.UnixMilli(), stateHash, now.UnixMilli())
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("consume mobile OIDC callback: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return "", time.Time{}, "", ErrNotFound
	}

	code, err := randomRawURLToken(32)
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("generate mobile OIDC grant: %w", err)
	}
	grantExpires := now.Add(ttl)
	_, err = tx.ExecContext(ctx, `
INSERT INTO mobile_oidc_handoff_grants(
  code_hash, user_id, flow_state_hash, handoff_challenge, return_to, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(code), userID, stateHash, challenge, returnTo, now.UnixMilli(), grantExpires.UnixMilli())
	if err != nil {
		return "", time.Time{}, "", fmt.Errorf("insert mobile OIDC grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, "", fmt.Errorf("commit mobile OIDC grant: %w", err)
	}
	return code, grantExpires, returnTo, nil
}

func s256Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func constantTimeTextEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ExchangeMobileOIDCHandoff atomically consumes a code and creates the normal
// Scrumboy session. Concurrent exchanges can produce at most one session.
func (s *Store) ExchangeMobileOIDCHandoff(ctx context.Context, rawCode, rawState, verifier string, sessionTTL time.Duration) (MobileOIDCExchange, error) {
	rawCode = strings.TrimSpace(rawCode)
	rawState = strings.TrimSpace(rawState)
	verifier = strings.TrimSpace(verifier)
	if rawCode == "" || rawState == "" || verifier == "" {
		return MobileOIDCExchange{}, ErrNotFound
	}
	if sessionTTL <= 0 {
		sessionTTL = defaultSessionTTL
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return MobileOIDCExchange{}, fmt.Errorf("begin mobile OIDC exchange tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID, expiresMS int64
	var expectedStateHash, challenge, returnTo string
	var consumedMS sql.NullInt64
	err = tx.QueryRowContext(ctx, `
SELECT user_id, flow_state_hash, handoff_challenge, return_to, expires_at, consumed_at
FROM mobile_oidc_handoff_grants
WHERE code_hash = ?`, hashToken(rawCode)).Scan(
		&userID, &expectedStateHash, &challenge, &returnTo, &expiresMS, &consumedMS,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return MobileOIDCExchange{}, ErrNotFound
		}
		return MobileOIDCExchange{}, fmt.Errorf("load mobile OIDC grant: %w", err)
	}
	now := time.Now().UTC()
	if consumedMS.Valid || now.UnixMilli() >= expiresMS ||
		!constantTimeTextEqual(expectedStateHash, mobileOIDCStateHash(rawState)) ||
		!constantTimeTextEqual(challenge, s256Challenge(verifier)) {
		return MobileOIDCExchange{}, ErrNotFound
	}

	updated, err := tx.ExecContext(ctx, `
UPDATE mobile_oidc_handoff_grants
SET consumed_at = ?
WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?`, now.UnixMilli(), hashToken(rawCode), now.UnixMilli())
	if err != nil {
		return MobileOIDCExchange{}, fmt.Errorf("consume mobile OIDC grant: %w", err)
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return MobileOIDCExchange{}, ErrNotFound
	}

	sessionToken, err := randomRawURLToken(32)
	if err != nil {
		return MobileOIDCExchange{}, fmt.Errorf("generate mobile session: %w", err)
	}
	sessionExpires := now.Add(sessionTTL)
	_, err = tx.ExecContext(ctx, `
INSERT INTO sessions(user_id, token_hash, created_at, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)`, userID, hashToken(sessionToken), now.UnixMilli(), sessionExpires.UnixMilli(), now.UnixMilli())
	if err != nil {
		return MobileOIDCExchange{}, fmt.Errorf("insert mobile session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MobileOIDCExchange{}, fmt.Errorf("commit mobile OIDC exchange: %w", err)
	}
	return MobileOIDCExchange{SessionToken: sessionToken, SessionExpiresAt: sessionExpires, ReturnTo: returnTo}, nil
}
